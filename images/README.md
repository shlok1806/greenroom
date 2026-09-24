# images

How greenroom's VM images are made. There is one recipe (ADR 0016):
`apps/daemon/scripts/build-image.sh`, which runs `greenroom prepare-image`
(`machine.PrepareGuest`, guest scripts in `apps/daemon/internal/machine/guest/`) and then the
dialog gate, `greenroom check-image`. Why the image is built this way:
[`docs/09-image-strategy.md`](../docs/09-image-strategy.md).

```
ghcr.io/cirruslabs/macos-tahoe-base   upstream, SIP off, TCC seeded
  └── greenroom-base                  build-image.sh              BASE: everything that needs no click
        (greenroom-lean-a)            build-image.sh -lean        LEAN: base plus hiding (the daemon's default)
```

## Layers

| Layer | Script | What it adds |
| --- | --- | --- |
| base | `input.go` (helper), `capturealert.go`, `desktopprefs.go`, `guest/base.sh`, `guest/toolchain.sh`, `base.go` (Software Update) | Input helper, ssh key, replayd screen-capture approvals, desktop preferences, Apple Events rows, Safari JavaScript from Apple Events, crash dialogs off, loginwindow relaunch list cut to Finder, toolchain manifest, Software Update disabled |
| lean | `guest/lean.sh` | Only hiding: Dock trimmed to the core apps, other apps' user agents disabled, widgets and banners (`notificationcenterui`), Siri, indexing, setup and Time Machine prompts off |

Base is a complete image on its own; lean adds nothing a base machine needs to work.

## Build

Prerequisites: Apple silicon, Tart 2.37.0 from the signed release (install steps in
`apps/daemon/CLAUDE.md`), Go, `jq`, the Cirrus base pulled (`tart pull
ghcr.io/cirruslabs/macos-tahoe-base:latest`, ~27 GB), at least 10 GB free, and two free VM
slots over the build (the build VM, then the gate's clone, one at a time).

```sh
cd apps/daemon
scripts/build-image.sh                                  # greenroom-base
scripts/build-image.sh -lean -name greenroom-lean-a     # the daemon's default
scripts/build-image.sh -name greenroom-base -force      # rebuild in place
```

The image is built as `<name>-building` and renamed only when the gate passes; a failed
build or gate deletes it, exits 1, and leaves an existing `<name>` untouched. It takes
about 8 minutes: ~2 to prepare, ~5 for the gate.

## The dialog gate

`greenroom check-image -image <name> [-out <dir>]` (run by the build, also usable alone on
any local image, which it never boots or changes itself):

1. Clones the image, clones the clone, boots the second headless.
2. Waits for login (Finder), then 20 s for what login starts.
3. Uses it as clients do: `screencapture` through `tart exec`, a posted event through the
   input helper, an Apple Event to System Events, Safari's `get bounds` and `do JavaScript`
   (each under a 30 s watchdog, so a prompt fails the call instead of hanging it).
4. 5 s later, fails on any on-screen window outside the allowlist in
   `internal/machine/desktopcheck.go`, any running regular app but Finder, a flat
   screenshot, or a softwareupdated that is enabled or running.
5. Reboots from inside the guest (`sudo reboot`) and does 2 to 4 again.

It writes `first-boot.png`, `after-reboot.png` and `report.json` to `-out`
(`GREENROOM_CHECK_OUT` for the build) either way. It does not write the screen-capture
approvals or desktop preferences a daemon's boot writes: the image must pass alone.

At runtime the daemon runs the same window check once before ready and returns it as
`desktop` in `machine_wait`. It reports; it never closes a window or quits an app.

## Toolchain manifest

`/usr/local/greenroom/toolchain.json` in the guest, written at build time by running a tiny
XCTest package and a tiny swift-testing package with `swift test` (ADR 0017). The daemon
returns it as `toolchain` in `machine_wait`, as the image wrote it; an image without it
reports `{"known": false}`. The current images have the Command Line Tools only: no Xcode,
no XCTest, and swift-testing does not build without extra search paths, which we do not add.

## Gotchas

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
