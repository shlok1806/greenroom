# 0003. Screen looks are bounded, run one capture at a time, and the recorder backs off

Date: 2026-09-27
Status: accepted. Amends root ADR 0008 (the recorder's retry) and extends root ADR 0014's guest
watchdog to every look at the screen.

## Context

Issue #187. In run `20260927-000233-acbc2b008dfc6a8f` the guest's WindowServer wedged
("coreanimation timed out fence"). `machine_exec` kept answering in about a second; everything
that needs the screen did not:

- `machine_ui` took 249 s and `machine_screenshot` 252 s and 171 s. The MCP client stops
  waiting at 60 s, but the request's context lives on, and nothing else bounded the calls:
  `captureScreen` and the input helper's reads ran `tart exec` on the caller's context with no
  deadline. The 50 s cap (`maxWait`) covers only the waits.
- About 60 guest `screencapture` processes piled up. The recorder gave each frame a 10 s
  context and started another 2 s after it expired, but killing the host's `tart exec` never
  reaches the guest (ADR 0014): each timed-out frame left its `screencapture` waiting on
  WindowServer.
- A reinstall of the input helper blocked for its whole 3 minute deadline, because the
  install check runs `greenroom-input --version`, and the helper's top-level code asked
  WindowServer for the main display before it read its arguments. The install held the
  helper's mutex the whole time, so every UI read queued behind it. The capture-approval check
  also held its mutex across an unbounded exec, in front of every capture.

The guest was recoverable (exec worked); the daemon made it worse and gave the agent no clear
signal.

## Decision

1. **A look has a cap and one clear error.** A look is a call that needs WindowServer: a
   screenshot, a frame, a UI tree read, a desktop or screen-size read. Every screenshot and
   `machine_ui` call has a 45 s cap of its own, whatever its caller's context allows (under the
   MCP client's 60 s first-byte timer, with the same headroom as `maxWait`). A look that runs
   out of time returns `machine.ErrScreenNotAnswering` (`*ScreenNotAnsweringError`), whose text
   says the guest screen is not answering, that `machine_exec` may still work, and to call
   `machine_reboot` to recover. It is recorded on the step like any error.
2. **The guest ends its own looks.** Each look runs under `lookWatchdogScript`, ADR 0014's
   watchdog cut down to one command: `set -m` gives the command a process group, a watchdog
   sends it TERM at the limit and KILL 2 s later, the script exits 124 with a note, and the
   watchdog's own group is killed as soon as the command ends, so neither outlives the script.
   Output goes through files in a private temp dir (the screenshot is written there too) that
   is removed on every path. Limits: 15 s for a capture, a desktop read and a screen-size
   read, 25 s for a UI tree read (a large tree walks for a while). The host waits 5 s longer,
   so the guest's watchdog, not the host, normally ends the command. macOS has no `timeout`
   binary, and this is the pattern already proven in the guest.
3. **One guest capture at a time per machine** (`captureGate`). A screenshot or render check
   that finds a capture outstanding waits for it within its own cap, then captures itself. It
   never shares the other capture's picture: that picture may predate the input the caller
   just made, and a look must show the screen after it (the #124 stale-look rule reads it
   that way). If the capture it waited for timed out, it fails at once with
   `ErrScreenNotAnswering` rather than starting another on a screen that just proved hung.
   The capture runs detached from its caller: a caller that gives up returns at once, and the
   slot stays held until the guest command is over, so an abandoned capture still blocks the
   next one. On a healthy guest a capture takes about a second, so waiting costs little.
4. **The recorder skips and backs off.** A frame is skipped (not queued, not logged) while
   another capture is outstanding. After consecutive capture timeouts, from any caller, the
   recorder waits 2 s, doubling up to 1 minute, and logs once when the screen stops answering
   and once when a capture works again, which returns it to its interval. Other capture
   failures are logged as ADR 0008 says. This amends ADR 0008's "waits one interval, and tries
   again".
5. **Nothing waits behind a lock without its own context.** The helper install runs once per
   machine, detached from its callers; each caller waits for it only as long as its context
   allows, and a caller that gives up says the install is still running. Asking whether the
   helper is current is a bounded check under the watchdog (20 s); only a missing or stale
   helper pays the compile's 3 minute deadline. The capture-approval check (15 s) and write
   (30 s) are bounded, and their lock is taken with the caller's context.
6. **`--version` never touches WindowServer.** `input.swift` answers `--version` before any
   other top-level code runs. The helper version is bumped to 8, so an image with helper 7
   compiles it at boot until the image is rebuilt (helperboot.go says so).

## Consequences

- A wedged screen answers in at most 45 s with an error an agent can act on, and the guest has
  at most one `screencapture` per machine waiting on WindowServer, killed after 17 s at most,
  unless the guest agent itself is stuck (then the host gives up at 20 s and cannot reach the
  guest; the backoff keeps that to one process a minute).
- Screenshots, frames and the render check no longer capture concurrently. A burst of
  screenshots is served one after another.
- A screenshot that waits behind a hung frame capture fails after that capture's 20 s, not its
  own; it never runs a second capture into the same hang.
- `machine_input` is not bounded here: a batch may legitimately sleep or type for a long time,
  and the live screen's ACK deadline already bounds it on the streaming path.
- Recovery is `machine_reboot` (a separate change). Nothing here restarts WindowServer: a
  process in `U` state takes no signal, and restarting the GUI session logs the user out,
  which ends the guest agent and the app under test.
- Build-time paths (`prepare-image`, `check-image`) keep their own watchdogs and are unchanged.
