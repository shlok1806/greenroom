# images

How greenroom's VM images are made. There is one recipe (ADR 0018):
`apps/daemon/scripts/build-image.sh`, which runs `greenroom prepare-image`
(`machine.PrepareGuest`, guest scripts in `apps/daemon/internal/machine/guest/`) and then the
dialog gate, `greenroom check-image`. Why the image is built this way:
[`docs/09-image-strategy.md`](../docs/09-image-strategy.md).

```
ghcr.io/cirruslabs/macos-tahoe-base   upstream, SIP off, TCC seeded
  └── greenroom-base                  build-image.sh              BASE: the host's Xcode and everything that needs no click
        (greenroom-lean-a)            build-image.sh -lean        LEAN: base plus hiding (the daemon's default)
```

Every image carries Xcode, copied from the host that builds it (ADR 0026). There is no
Xcode-less variant.

## Layers

| Layer | Script | What it adds |
| --- | --- | --- |
| base | `xcode.go` (`guest/disk.sh`, `guest/xcode.sh`), `input.go` (helper), `capturealert.go`, `desktopprefs.go`, `guest/base.sh`, `guest/toolchain.sh`, `base.go` (Software Update) | Grown disk, the host's Xcode (selected, license accepted, first launch run, developer mode on, its Login Items & Extensions alert closed), input helper, ssh key, replayd screen-capture approvals, desktop preferences, Apple Events rows, Safari JavaScript from Apple Events, crash dialogs off, loginwindow relaunch list cut to Finder, toolchain manifest, Software Update disabled |
| lean | `guest/lean.sh` | Only hiding: Dock trimmed to the core apps, other apps' user agents disabled, widgets and banners (`notificationcenterui`), Siri, indexing, setup and Time Machine prompts off |

Base is a complete image on its own; lean adds nothing a base machine needs to work.

## Build

Prerequisites: Apple silicon, Tart 2.37.0 from the signed release (install steps in
`apps/daemon/CLAUDE.md`), Go, `jq`, the Cirrus base pulled (`tart pull
ghcr.io/cirruslabs/macos-tahoe-base:latest`, ~27 GB), Xcode on the host (selected with
`xcode-select`, or named with `-xcode`; it must run on the guest's macOS, 26.6.2 today, so
Xcode 27 is the newest that fits), at least 20 GB free, and two free VM slots over the build
(the build VM, then the gate's clone, one at a time).

```sh
cd apps/daemon
scripts/build-image.sh                                  # greenroom-base
scripts/build-image.sh -lean -name greenroom-lean-a     # the daemon's default
scripts/build-image.sh -name greenroom-base -force      # rebuild in place
scripts/build-image.sh -xcode /Applications/Xcode-27.0.app -disk-size 120   # another Xcode, a bigger disk
```

The image is built as `<name>-building` and renamed only when the gate passes; a failed
build or gate deletes it, exits 1, and leaves an existing `<name>` untouched. It takes
about 7 minutes (a lean build measured 406 s on 2026-09-25): ~4 to prepare, of which Xcode
is ~3, and ~2 for the gate. The image costs about 4.4 GB of host disk (`df` delta).

## The dialog gate

`greenroom check-image -image <name> [-out <dir>]` (run by the build, also usable alone on
any local image, which it never boots or changes itself):

1. Clones the image, clones the clone, boots the second headless.
2. Waits for login (Finder), then 20 s for what login starts.
3. Uses it as clients do: `screencapture` through `tart exec`, a posted event through the
   input helper, an Apple Event to System Events, Safari's `get bounds` and `do JavaScript`
   (each under a 30 s watchdog, so a prompt fails the call instead of hanging it), and
   `xcodebuild -checkFirstLaunchStatus` plus an `xcodebuild` of a fresh one-file package
   (240 s watchdog), so a license, first-launch, component, privacy or developer tools
   prompt fails the image.
4. 5 s later, fails on any on-screen window outside the allowlist in
   `internal/machine/desktopcheck.go`, any running regular app but Finder, a flat
   screenshot, or a softwareupdated that is enabled or running.
5. Reboots from inside the guest (`sudo reboot`) and does 2 to 4 again.

It writes `first-boot.png`, `after-reboot.png` and `report.json` to `-out`
(`GREENROOM_CHECK_OUT` for the build) either way. It does not write the screen-capture
approvals or desktop preferences a daemon's boot writes: the image must pass alone.

At runtime the daemon runs the same window check once before ready and returns it as
`desktop` in `machine_wait`. It reports; it never closes a window or quits an app.

## Xcode (ADR 0026)

`prepare-image` copies the host's Xcode before anything else (`machine.InstallXcode`):

1. `build-image.sh` grows the clone's disk (`tart set --disk-size`, default 90 GB). The
   Cirrus base's LaunchDaemon runs `tart-guest-agent --run-daemon`, which resizes the APFS
   container at boot (about 35 s after the agent answers); `guest/disk.sh` waits for it,
   runs `diskutil apfs resizeContainer <store> 0` itself if it never happens, and reads the
   size back. Layout on 26.6.2: `disk0s1` ISC, `disk0s2` the system container, nothing after.
2. The app is streamed over ssh: `ditto -c` on the host into `sudo ditto -x --hfsCompression`
   in the guest, at `/Applications/Xcode.app` whatever the host's copy is called.
3. `guest/xcode.sh`: `lsregister -f` (see the gotcha on Login Items below), `xcode-select -s`,
   `xcodebuild -license accept`, `xcodebuild -runFirstLaunch`, `DevToolsSecurity -enable`
   and the user in `_developer`, a wait until Login Items has posted about Xcode, then reads
   back each, plus `codesign --verify --deep --strict` and `spctl --assess` of the copy and
   no quarantine attribute. `guest/base.sh` later closes that Login Items alert.

Measured 2026-09-25 (host Xcode 27.0 27A266a, guest 26.6.2): 100 s to copy, 80 s to set up
and verify, 4.0 GB in the guest, 4.4 GB of host disk for the whole image. The gate's
`xcodebuild` took 8 s per pass.

The image follows the host: rebuild after updating the host's Xcode. A host Xcode newer than
the guest's macOS allows fails in `xcode.sh` by name (Xcode 27 needs 26.6). No simulator
runtime is installed (ADR 0026, point 7).

## Toolchain manifest

`/usr/local/greenroom/toolchain.json` in the guest, written at build time by running a tiny
XCTest package and a tiny swift-testing package with `swift test`, and a one-file package
with `xcodebuild -scheme ... build` (ADR 0019, ADR 0026). It records `xcode`, `xcodePath`,
`xcodeVersion`, `xcodeFirstLaunch`, `xctest`, `swiftTesting`, `xcodebuild` (each with an
`...Error` line when false), `developerDir`, `swiftVersion`, `commandLineTools`, `macos` and
`imageRecipe`. With Xcode present, a false probe fails the build by name: an image never
claims a toolchain it does not have. The daemon returns the file as `toolchain` in
`machine_wait`, as the image wrote it; an image without it reports `{"known": false}`.

## Recipe version

`imageRecipeVersion` (`apps/daemon/internal/machine/image.go`) names what the recipe puts in
an image; bump it with every recipe change an existing image lacks. It is baked into the
manifest as `imageRecipe`. At boot the daemon logs a warning with the rebuild command when
an image's manifest names another recipe (or none: images from before ADR 0026), and records
`imageRecipeStale` and `imageRecipeFound` in the boot step. The VM suite (`vm-suite.yml`)
names its image `greenroom-base-v<inputHelperVersion>-r<imageRecipeVersion>` and rebuilds when
either changes; it keeps the newest older image and deletes the rest. Local names
(`greenroom-lean-a`, `greenroom-base`) do not change: rebuild them with `-force`.

## Gotchas

- **rsync expands Xcode.** Xcode's files are APFS-compressed; rsync (openrsync) and a plain
  `ditto` write them expanded, 10.2 GB instead of 4.0 GB for Xcode 27, and the host's sparse
  disk image pays for every byte written. Copy with `ditto -x --hfsCompression`. Blocks the
  guest frees are returned to the host (TRIM works), but only after the delete.
- **Opening Xcode.app still shows onboarding.** `xcodebuild` raises nothing, but launching
  the app shows "External Agent Access" (Always / While Xcode is Open / Never) and then
  "What's New in Xcode". Clicking through wrote `IDEAllowUnauthenticatedAgents = 1`,
  `IDELastShownWhatsNewContentRevision = 6` and `IDEMostRecentPostFLEToolsVersion = 27.0` to
  `com.apple.dt.Xcode`. Not baked: which agent access to grant is a decision, and the gate
  does not open Xcode.app.
- **`-runFirstLaunch` before any xcodebuild.** Without it, `xcodebuild
  -checkFirstLaunchStatus` exits 69 and Xcode.app offers "Install additional components".
- **Installing Xcode raises a Login Items alert that survives into every clone.** Xcode
  carries a Quick Look previewer and a Spotlight importer. When LaunchServices registers
  Xcode, `backgroundtaskmanagementd` (BTM) records them under Login Items & Extensions, and
  about 12 s later `BackgroundTaskManagementAgent` posts "Multiple Extensions Added" ("Xcode"
  added multiple extensions) from `com.apple.BTMNotificationAgent`. It is an alert, not a
  banner: it stays on screen until someone closes it, and usernoted keeps it, so every clone
  showed it at every login (`window owned by "Notification Center" ... at layer 21`, the
  gate's finding for PR #141). BTM itself marks the items `notified` when it posts and never
  posts again (`sudo sfltool dumpbtm`); what came back was the stored alert
  (NotificationCenter logs "Re-add ... displaying as alert"). Lean passed only because it
  disables `com.apple.notificationcenterui.agent`, which hides every banner and alert.
  The fix closes it at build time, as a person would: `xcode.sh` runs `lsregister -f` on the
  copy and waits (up to 90 s) until BTM reads every Xcode item notified; `base.sh`, once
  tart-guest-agent may script System Events, performs the alert's own Close action through
  System Events and reads back that no alert is on screen. Notification Center then deletes
  it, and it stays gone in clones and after a reboot. `base.sh` closes only the alerts the
  recipe is known to raise (`build_alerts`); any other alert fails the build by its text,
  rather than being swept away. Do not widen the gate's allowlist for Notification Center.

- **`sync` before `tart stop`.** Otherwise files written just before are gone on the next
  boot. `PrepareGuest` and `DisableSoftwareUpdate` end with it.
- **loginwindow relaunches whatever is in its list**, from
  `~/Library/Group Containers/group.com.apple.loginwindow.persistent-apps/persistantApps`,
  whatever `TALLogoutSavesState` says (measured on 26.6.2). The list follows the apps that
  run, so an app left running while an image is built comes back on every machine. `base.sh`
  stops the apps the list names, cuts it to Finder and reads it back.
- **Software Update is two launchd jobs.** Disabling `com.apple.softwareupdated` alone
  leaves `com.apple.mobile.softwareupdated` to start the same daemon after a reboot.
- **SIP is off** in the Cirrus base. That is what lets root write TCC.db. Say so when
  claiming a result was verified.
- **No secrets in images.** Anything per run goes in through `machine_sync` or
  `machine_exec`.
- **Screen work runs in the user's session**, through `tart exec` (tart-guest-agent is a
  LaunchAgent). A LaunchDaemon has no WindowServer.
- **Two VMs per host**, Apple's limit. The build and its gate each take a slot, one after
  the other.
- To keep a new guest binary's Apple Events or screen capture from prompting, run it through
  `tart exec`: TCC and replayd both key on the responsible process, tart-guest-agent.
