# 0008. Every run records its screen as a timelapse, kept with the evidence

Date: 2026-09-19
Status: accepted (implemented)

## Context

`docs/00-idea.md` names the recording as part of a run's evidence (manifest, screenshots,
recording) and nothing produces one. A reviewer reading a verdict today has the
transcript, the step log and whatever screenshots the verifier chose to take. What they
cannot do is watch the run: see what the screen showed while a build ran, when a dialog
appeared, what the agent was looking at when it decided. The companion (ADR 0007) shows
the latest screenshot and polls for new ones while a person watches; a person who was
not watching has nothing.

Two ways to record were weighed.

**Video inside the guest** (`screencapture -v` over `tart exec`) gives smooth video, but
the file lives in the guest until it is copied out. A machine that crashes, hangs or is
destroyed before the copy loses the whole recording, which is exactly when the recording
matters most. It also needs a stop-and-copy step in the destroy path and a running
process in the guest that the daemon cannot see.

**Frames captured by the daemon** cost one `screencapture` in the guest every few seconds,
the same call `machine_screenshot` already makes, and each frame is on the host the
moment it is taken. Nothing is lost on a crash, nothing runs in the guest that the daemon
does not start, and every frame carries the step number that was current when it was
taken, which is what makes "jump to step 14" possible. The result is a timelapse, not
video. Agent work happens in steps seconds apart, so a frame every two seconds shows
everything a reviewer needs.

## Decision

**The daemon records every machine from `ready` to `destroyed`**, one JPEG frame every
`-frame-interval` (default 2 s), into `runs/<runId>/frames/`. Each frame is
`<unix-ms>.jpg`, and `frames.jsonl` beside them holds one line per frame:
`{ at, file, step, bytes }` where `step` is the latest step number in `steps.jsonl` at the
time of capture. The recorder is part of `internal/machine`, beside the step recorder,
because it is evidence and the run directory is where evidence lives.

**Frames are the screen the companion shows.** The Screen tab's own polling (ADR 0007)
goes away: the app shows the newest frame of a live run and scrubs the frames of any run,
live or finished. `machine_screenshot` stays as the on-demand, lossless PNG the verifier
and the coder use for looking closely; it is a step, and the timelapse is context around
the steps.

**Capture pauses when it cannot help.** No frames while the machine is booting or after
it fails. If a capture errors, the recorder logs once, waits one interval, and tries
again; it never fails the run. A frame identical in size to the previous one is still
kept, because "the screen did not change for a minute" is itself evidence.

**A video is an export, not the record.** `GET /api/runs/{id}/recording.mp4` builds an
H.264 file from the frames with `ffmpeg` if it is on the host's PATH, caches it in the
run directory, and answers 404 with a clear message when `ffmpeg` is absent. The frames
are canonical; the mp4 is for sharing.

### API

- `GET /api/runs/{id}/frames` returns `frames.jsonl` as JSON.
- `GET /api/runs/{id}/frames/{file}` serves one frame.
- `GET /api/runs/{id}/recording.mp4` as above.
- `/api/events` gains `frame` events: `{ runId, at, file, step }`.

### Companion

The Screen tab becomes a player: the frame strip as a scrubber, play and pause at 1x and
4x, the current step and time shown over the frame, Live follows the newest frame on a
live run. Clicking a `progress` message or a step row jumps the scrubber to the first
frame at or after that step. The capture button stays and takes a lossless
`machine_screenshot` step.

## Consequences

**The guest does more work.** One `screencapture` every two seconds is a real cost on a
VM that is also building. Measured on the base image, a capture takes about 0.2 s and a
frame is about 100 KB, so a ten minute run is 300 frames and 30 MB. `-frame-interval 0`
turns recording off for hosts that cannot afford it.

**Run directories grow.** 30 MB for ten minutes is tolerable on a developer Mac and not
for a fleet. Retention (delete frames older than N days, keep the mp4) is deferred until a
disk fills.

**Issue #7 stays outside this.** The timelapse comes from `screencapture` in the guest,
which works headless; it does not need the graphics-enabled mode that trips the GPU
crash dialog. Watch mode (a live window on the host) and the recording are independent.

**Deferred.** Per-frame diffs to skip unchanged screens, input overlays (where the agent
clicked, once M2 gives it a mouse), and audio, which no one has asked for.
