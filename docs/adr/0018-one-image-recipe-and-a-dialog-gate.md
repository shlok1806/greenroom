# 0018. One image recipe, base and lean layers, and a dialog gate on every build

Date: 2026-09-24
Status: accepted

Supersedes decisions 2 to 4 of `docs/09-image-strategy.md` (the Packer `greenroom-base`
layer, its first-boot LaunchDaemon and the seed disk) and the part of ADR 0013 that has
`images/scripts/greenroom-tcc.sh` repeat the capture approvals for a Packer image.

## Context

Two recipes made an image called `greenroom-base`: `images/greenroom-base.pkr.hcl`
(Packer, TCC rows, a first-boot daemon, a smoke test) and
`apps/daemon/scripts/build-image.sh` (`prepare-image`, `machine.PrepareGuest`). The
daemon, CI and `install.sh` only ever used the second, and nobody could build the first:
Packer is installed by nothing (issue #22). Fixes landed in one and not the other, so the
image the daemon ran had none of the Packer layer's work (issue #25).

A fresh machine still showed things nobody asked for:

- Safari AppleEvents raise `"tart-guest-agent" wants access to control "Safari"`, and
  osascript blocks on it for 90 s (issue #25). `do JavaScript` needs "Allow JavaScript
  from Apple Events".
- Terminal is running at login on every image, with a restored window on lean-a, and
  lean-a relaunches apps from a stale list after a reboot (issue #60).
- Software Update keeps checking: on macOS 26 softwareupdated ignores `--schedule off`
  and deletes `AutomaticCheckEnabled` (image-experiment decision 14).
- Crash dialogs can cover the app under test.

Every one of these is invisible to a build that does not look at the screen.

## Decision

1. **One recipe.** `build-image.sh` -> `greenroom prepare-image` (`machine.PrepareGuest`,
   scripts in `internal/machine/guest/`) is the only way to make an image. The Packer
   files, `images/scripts/`, `images/data/` and `make-seed.sh` are deleted. What was
   useful moves into the canonical path: `automationmodetool` without authentication, the
   smoke test's non-uniform screenshot check and the posted event. `greenroom-tcc.sh`'s TCC
   rows for `/usr/local/greenroom/firstboot.sh` go with the first-boot daemon. The seed disk and first-boot daemon go
   without a replacement: the daemon installs its key and syncs projects over `tart exec`
   and ssh, and nothing read the seed.
2. **Two layers.** BASE (`build-image.sh`, no `-lean`) gets every fix that needs no
   click: `guest/base.sh`. LEAN (`-lean`) adds only hiding, `guest/lean.sh` as before.
   Base stays buildable on its own.
3. **Base fixes**, each read back, a failed read-back fails the build by name (as
   `lean.sh` does):
   - replayd screen-capture approvals, keyed by the resolved paths of tart-guest-agent
     (found at build time, never a pinned version) and sshd-keygen-wrapper: the existing
     `captureApprovalsScript` (ADR 0013), unchanged, so build time and boot time write the
     same records. The input helper needs no record of its own: it always runs under
     tart-guest-agent, which is its responsible process.
   - TCC AppleEvents rows for senders tart-guest-agent and sshd-keygen-wrapper, targeting
     Finder, Terminal, System Settings, Safari, TextEdit, Preview, Activity Monitor,
     Console and System Events.
   - Safari `AllowJavaScriptFromAppleEvents` on.
   - Crash dialogs off: `com.apple.DiagnosticsReporter` disabled in `gui/501`,
     CrashReporter `DialogType none`, diagnostic and panic report directories emptied.
   - automation mode without authentication.
   - What starts apps at login is removed at its cause (issue #60). Measured on 26.6.2:
     not Background Task Management (`sudo sfltool dumpbtm` lists only the Cirrus daemons)
     and not saved state, but loginwindow's persistent-apps list,
     `~/Library/Group Containers/group.com.apple.loginwindow.persistent-apps/persistantApps`,
     which the Cirrus base saved with Finder and Terminal. loginwindow relaunches that list
     at every login whatever `TALLogoutSavesState` or `LoginwindowLaunchesRelaunchApps` say,
     and rewrites it from the apps running at logout, which is how lean-a's list went stale.
     The build stops the apps the list names, cuts it to Finder, clears saved state, and
     reads the list back.
   - Software Update disabled as the last build step: `launchctl disable` of both
     `system/com.apple.softwareupdated` and `system/com.apple.mobile.softwareupdated`. The
     first alone is not enough: the second starts the same daemon after a reboot. The gate
     checks both are disabled and softwareupdated is not running, after a reboot.
4. **A boot-time kill is not a fix.** No `pkill Terminal` or window sweep at boot, even as
   a fallback. If a cause cannot be found, the gate keeps failing on it. The boot and
   prepare-image Terminal quit from PR #91 (`terminal.go`) is removed for this reason: the
   build fixes the cause, and a stray app on an older image shows up in `desktop` instead.
5. **The dialog gate.** `build-image.sh` ends with `greenroom check-image`: a clone of a
   clone of the new image, booted `--no-graphics`, exercised the way clients use a
   machine (screencapture through `tart exec`, a posted event through the input helper,
   an AppleEvent to System Events and one to Safari with osascript), then 5 s later its
   on-screen windows (`CGWindowListCopyWindowInfo`, through the input helper) and running
   regular apps (NSWorkspace) are compared with an allowlist. It runs again after an
   in-guest `sudo reboot`. Any other window, any running app but Finder, a uniform
   screenshot or a running softwareupdated fails the build, and the image is deleted. A
   screenshot of each pass is kept either way. The check is Go in `internal/machine`
   (`desktopcheck.go`) so the runtime uses the same allowlist.
6. **The runtime surfaces, never sweeps.** At boot, before ready, the daemon runs the same
   window check once and puts what it found in the machine's `desktop` field, which
   `machine_wait` returns. It never closes a window or kills an app.

## Consequences

- An image build takes one more boot and reboot of a clone (about 2 minutes) and needs a
  free VM slot after the build VM stops.
- A dialog that appears only later, or only after some app runs, is not caught by the
  gate. The runtime report is a snapshot at ready.
- An image built before this ADR fails the gate. That is the point: CI rebuilds images
  when `inputHelperVersion` changes, which this change bumps.
- Software Update never runs in a greenroom machine. The OS version is the base image's
  until the base is rebuilt from a newer Cirrus image.
- The input helper gained a `--desktop` mode, so `inputHelperVersion` is 6.
