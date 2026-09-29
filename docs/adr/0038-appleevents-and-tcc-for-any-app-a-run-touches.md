# 0038. AppleEvents and TCC for any app a run touches, and a watch for the prompts that slip through

Date: 2026-09-28
Status: accepted

Extends ADR 0018 (the base profile, `base.sh`, the dialog gate) and ADR 0013 (TCC.db writes
are safe under root with SIP off; `machine_approve_capture` is the precedent for approving an
app under test by call). Fixes issue #252.

## Context

`base.sh` grants `kTCCServiceAppleEvents` from tart-guest-agent and sshd-keygen-wrapper to a
fixed list of nine targets: Finder, Terminal, System Settings, Safari, TextEdit, Preview,
Activity Monitor, Console, System Events. Reproduced live (run `20260928-205525-f6042c37845c1c9e`,
step 7, and again in this run's own repro below): `osascript -e 'tell application "Calculator"
to get name of every window'` through `machine_exec` raises `"tart-guest-agent" wants access to
control "Calculator"` (Allow / Don't Allow). Nothing answers it, so `osascript` blocks until a
person clicks the dialog in the Companion; the run stalls silently. The same happens for every
app outside the nine, which includes every app under test, since a user's app is built at run
time with a bundle id the image cannot know ahead of it.

Two other gaps, named in the issue:

- `machine_ui` and the boot-time desktop check are not built to watch continuously during a
  run, so a prompt that appears between boot and the next `machine_wait` is invisible until
  something notices the command never returned.
- Only AppleEvents is covered. An app under test that reads Accessibility, captures its own
  screen (distinct from `machine_approve_capture`'s replayd bypass, which is a different
  alert), reads Desktop/Documents/Downloads, or opens the camera or microphone hits the same
  class of stall on a TCC service `base.sh` never touches.

## Decision

### 1. Build time: every installed app, not a pinned list

`base.sh` resolves `kTCCServiceAppleEvents` targets by walking `/Applications`,
`/System/Applications` and `/System/Applications/Utilities` (each app's bundle id read with
`plutil -extract CFBundleIdentifier raw -o -` on its `Info.plist`, never pinned) instead of a
fixed list. Finder and System Events live in `/System/Library/CoreServices`, outside those three
directories, and stay as an explicit addition, same as System Events already was. The set is
deduplicated and sorted for a stable read-back. The list is overridable with
`GREENROOM_TCC_APP_DIRS` (space-separated directories), which only the test harness uses, so the
real recipe always resolves the guest's actual `/Applications` at build time, as ADR 0018 already
resolves tart-guest-agent's own path at build time rather than pinning a version. Every row is
read back; a missing one fails the build by the target's bundle id, as every other `base.sh`
check does.

This covers every app the base and lean images ship or Xcode installs. It does not cover an app
built at run time, whose bundle id does not exist yet when the image is built.

### 2. Run time: `machine_approve_control`, by call, mirroring `machine_approve_capture`

ADR 0013 already answered this shape of problem for the screen-capture bypass alert: an app
under test is not known at build time, so it is approved by an explicit call once it exists
(`machine_approve_capture`, point 5). The alternatives considered for TCC:

- **A boot-time or periodic in-guest watcher that grants any new `.app` under `~/work` on
  sight.** Rejected for now: the AppleEvents dialog for `tell application "X" to activate` can
  fire on the very command that both launches and controls X, so a watcher reacting to the
  launch notification has no guaranteed window before the first control attempt when build and
  control are chained in one shell command. It also adds a persistent guest process and a new
  daemon-guest protocol for a problem the existing call-based pattern already solves for the
  sibling alert. If a later measurement shows callers routinely skip approving, this is the
  next thing to add, behind its own experiment.
- **A PPPC configuration profile.** Rejected: profile installation needs either MDM enrollment
  or manual approval in System Settings, both of which are themselves a click a fresh guest has
  nobody to make. TCC.db is a plain SQLite file writable by root with SIP off (ADR 0013), which
  is exactly the guest's situation, and it is what `base.sh` and `machine_approve_capture`
  already write; profiles would be a second mechanism for the same fact.
- **Writing the TCC.db row directly, keyed to the app's own bundle id and resolved executable
  path.** Chosen. Measured live in this run (see below): `sudo -n sqlite3` writes both databases
  in under a second with no guest reboot or process restart needed (unlike the screen-capture
  bypass alert's replayd, TCC's `tccd` only needs `killall tccd` to drop its cache, which does
  not interrupt a live screen stream), and the row takes effect on the next AppleEvent with no
  further wait.

`machine_approve_control(runId, app)`: resolves the app's bundle id and its main executable's
realpath, then writes, to both the system and user TCC.db, INSERT OR REPLACE rows for:

- `kTCCServiceAppleEvents`: tart-guest-agent -> the app's bundle id, and sshd-keygen-wrapper ->
  the app's bundle id (so a `machine_exec`/`osascript` or an ssh session can drive it).
- `kTCCServiceAppleEvents`: the app's own resolved executable -> `com.apple.systemevents`, and
  the app's own executable -> its own bundle id (so an app that scripts System Events, or
  scripts itself, does not prompt either).
- `kTCCServiceAccessibility`, `kTCCServiceScreenCapture`, `kTCCServiceSystemPolicyDesktopFolder`,
  `kTCCServiceSystemPolicyDocumentsFolder`, `kTCCServiceSystemPolicyDownloadsFolder`,
  `kTCCServiceCamera`, `kTCCServiceMicrophone`: the app's own executable as client, no indirect
  object (point 4 below).

It records a `machine_approve_control` step and returns the resolved bundle id, executable path
and the services granted, the same shape `machine_approve_capture` returns its bundle URL in.
Calling it again for the same app is a cheap no-op (INSERT OR REPLACE). It is documented next to
`machine_approve_capture` as the call to make once a build under test produces an `.app`, before
the first thing that might control, script or otherwise touch it.

### 3. Never stall silently: a desktop look when a command is still running

`machine_ui`'s frontmost-application read cannot see a system prompt (issue #223, not fixed
here): the process that owns a TCC or notification dialog is not a regular Dock app, and #223 is
its own change. What already can see it is the window-level check ADR 0018 built for the dialog
gate: `readDesktop`/`DesktopReport` reads every on-screen window through `CGWindowListCopyWindowInfo`
regardless of which process owns it or whether it is a regular app, and `Report()` already
flags anything outside the clean-desktop allowlist. Today that only runs once, at boot.

`ExecWait` (and so `machine_exec`, which starts a command and immediately waits for it) now
takes the same look whenever it is about to answer `running: true`: the command has already sat
for a full `waitSeconds` without returning, which is the stall signature from the Calculator
repro. A dialog found this way is attached to the exec result as `desktop`, written to the
machine's own `desktop` field (which `machine_wait` already reports, so a person or the
Companion polling either one sees the same finding), and logged. Nothing here clicks Allow or
closes anything, as ADR 0018 point 4 requires; it only says what is on screen so the caller can
decide (approve the app and retry, or hand it to a person). A command that returns before its
wait elapses is not checked again: it was not stalled, so there is nothing new to find, and the
extra look is skipped for the common fast path.

### 4. The rest of the family, for apps under test

Point 2's grant list adds `kTCCServiceAccessibility`, `kTCCServiceScreenCapture`,
`kTCCServiceSystemPolicyDesktopFolder`/`DocumentsFolder`/`DownloadsFolder`, `kTCCServiceCamera`
and `kTCCServiceMicrophone` for the app's own executable. These are only granted to an approved
app under test, not to the base image's fixed and enumerated targets in point 1: a pre-installed
system app being scripted by AppleEvents does not also need its own camera and microphone
grants, and the base build should not carry TCC rows for every one of them speculatively.
`kTCCServiceScreenCapture` here is the ordinary "X would like to record this screen" TCC row,
distinct from `machine_approve_capture`'s replayd bypass-approval file (ADR 0013); an app that
captures the screen itself still needs both.

### 5. The dialog gate scripts an app outside the old list, and a fresh build

`check-image`'s exercises gain two cases: `tell application "Calculator" to get name`, quitting
Calculator after, proving point 1's enumeration covers a built-in app that was never in the old
fixed list (the query needs the Apple Events grant but no window; the issue's "name of every
window" fails with -1728 whenever Calculator has no window open, which is not a permission
answer); and a minimal `.app` built fresh in the clone with `swiftc` (an `Info.plist` with its
own bundle id, no Xcode project needed), whose `main.swift` runs `NSAppleScript` for
`tell application id "<its own id>" to activate`, after the check calls
`machine_approve_control`'s script directly on it. This is the two cases issue #252 names: a built-in app outside the old list, and a freshly
built test app scripting itself.

`imageRecipeVersion` moves from 3 to 4: an image built before this ADR has the fixed nine-target
list and none of the extra TCC family, so it fails a `check-image` run against this recipe and
boot's stale-recipe warning names it, as every prior bump does.

## Consequences

- Every pre-installed app on the base and lean images, plus every app the build enumerates from
  `/Applications` and the two system directories, never raises the AppleEvents prompt. An app
  built at run time needs one `machine_approve_control` call before it is first driven; skipping
  it is a stall, but point 3 means the stall is reported with what is on screen, never silent.
- `machine_approve_control` is a second call next to `machine_approve_capture` for the verifier
  and the Companion to remember. Both approve an app under test against a different alert; a
  build that captures its own screen and also needs AppleEvents or another TCC service calls
  both.
- A rebuild is required: the enumerated list, the extra TCC family and the two new gate
  exercises are recipe changes an existing `greenroom-lean-a` or `greenroom-base` lacks.
- The desktop look added to `ExecWait` costs one extra guest round trip only when a command did
  not return within its wait, which was already the slow, unusual path.
- This still depends on SIP being off and TCC.db being a writable SQLite file in the guest, as
  ADR 0013 already depends on for the screen-capture bypass alert. A base image that changes
  either assumption needs a different mechanism for both ADRs, not just this one.
