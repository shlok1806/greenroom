# 0016. Retire watch mode: every machine boots headless, a person watches in the companion

Date: 2026-09-23
Status: accepted. Supersedes the last sentence of ADR 0011 decision 5 ("It stays available
for a person via `-watch`") and ADR 0007's expectation that watch mode over VNC is the fix
once issue #7 is closed.

## Context

`machine_create` took `watch: true`. The daemon then booted the VM with
`tart run --vnc-experimental` instead of `--no-graphics`, read the `vnc://` address tart
printed, returned it as `vncUrl` and, with `serve -open-viewer` (on by default), opened it
on the host. The smoke client's `-watch` asked for it.

Issue #7: a machine booted with graphics restarts its guest GPU continuously (20
`.gpuRestart` reports in 8 minutes, a kernel panic file, load average 36.9) and covers the
screen with "Your computer was restarted because of a problem." The dialog cannot be
cleared while the reports keep coming. Screenshots are the evidence greenroom returns, and
this dialog sits on top of the app under test. Headless machines are not affected.

Since watch mode was added, the companion gained a live screen (ADR 0011): H.264 captured
inside the guest with ScreenCaptureKit, streamed over one `tart exec -i` pipe, working on
a headless machine. It shows the same picture without graphics mode, a private API, a
network port, or a second channel into the machine that the daemon cannot see or record
(ADR 0009's objection to VNC). Watch mode was the one remaining reason to boot with
graphics.

## Decision

1. Retire the graphics path instead of fixing it. Every machine boots with
   `tart run --no-graphics`. `tart.Client.Start` has no mode argument.
2. Remove watch mode end to end: `machine_create`'s `watch` argument, `vncUrl` on the
   machine (MCP results, `state.json`, `/api/runs/{id}`), `Process.VNCURL`,
   `machine.WithWatchHandler`, `serve -open-viewer`, and the smoke client's `-watch`.
3. The companion's live screen is the only way a person sees a machine. For now it is
   watch-only unless the person takes the lease (ADR 0009); nothing else opens a window
   on the host.
4. Issue #7 closes as won't fix for the graphics path. Its headless acceptance criteria
   (no crash dialog, no screen-recording reminder in screenshots) remain covered by the
   image work and ADR 0013.

## Consequences

- An MCP client that still sends `watch` is refused by the tool's input schema, rather than
  silently getting a headless machine. It drops the argument and opens the companion.
- A `serve` command line or launchd plist that passes `-open-viewer` no longer starts;
  remove the flag.
- A `state.json` written by an older daemon may hold `vncUrl`; it is ignored on load.
- Watching needs the companion and a ready machine. There is no view of a booting
  machine's screen, as there was none headless before.
- Should VNC or graphics mode ever come back, it needs a new ADR that answers issue #7 and
  ADR 0009's objection that a VNC session leaves nothing in the run's evidence.
