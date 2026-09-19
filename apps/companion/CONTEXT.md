# Context: companion

The macOS app for watching a run and speaking into it. Shared terms (Machine, Snapshot,
Run, Evidence) are defined in the root `CONTEXT-MAP.md` and mean the same thing here.

## Glossary

- **Run**: one verification session, seen from outside as a row in the sidebar. In the app
  a run is a `RunSummary` in the list and a `RunDetail` once it is opened. A run outlives
  its machine: a finished run has no machine and is read-only.
- **Message**: one line of the run's conversation (ADR 0006), carrying who said it
  (`coder`, `human`, `verifier`, `system`) and what it is for (`task`, `note`, `reply`,
  `question`, `answer`, `progress`, `verdict`, `accept`, `dispute`, `event`). The app is
  the `human` seat, and the only kinds it sends are `note`, `task`, `answer`, `accept` and
  `dispute`.
- **Reply**: the verifier answering in plain words, with no verdict attached. It ends the
  verifier's turn, and it is what a human note usually comes back as. The app renders it
  as the verifier's own bubble, not as a system line.
- **Verdict state**: where the latest verdict stands, as the daemon derives it from the
  transcript: `none`, `proposed`, `accepted`, `contested` or `rejected`. Only `proposed`
  and `contested` are still open, and only then does the app offer Accept and Dispute.
- **Live screen**: the Screen tab with its Live toggle on. The app does not poll for
  this; the daemon captures frames on its own schedule and pushes each new one down the
  event stream (`event: frame`), and the app just follows the newest one.
- **Frame**: one captured image of a run's screen (ADR 0008), described by
  `{at, file, step, bytes}`. The app never generates a frame; it only asks the daemon for
  the list (`GET /api/runs/{id}/frames`) and for one image
  (`GET /api/runs/{id}/frames/{file}`).
- **Recording**: a run's frames assembled by the daemon into a single video
  (`GET /api/runs/{id}/recording.mp4`), which the app can save to disk from the Screen
  tab's toolbar. Building it needs `ffmpeg` on the daemon host; a daemon without it
  answers with an error the app shows rather than a generic failure.
- **Artifact**: a file in the run directory, reached only by name through
  `GET /api/runs/{id}/artifacts/{name}`. A screenshot step's `output.path` gives the name.

## Invariants

1. **Every read and every write is an HTTP call to the daemon.** No tart, no ssh, no run
   directory, no second source of truth.
2. **The app proposes, it does not operate.** It cannot create a machine, sync code or run
   a command. It can take a screenshot and destroy a machine, and both are recorded in the
   conversation.
3. **Nothing the app does is hidden from the coding agent.** Every control writes into the
   transcript on the daemon side.
4. **Sequence numbers come from the daemon.** The app appends a message to its local
   transcript only when the arriving `seq` is greater than the last one held, so a
   reconnect that replays events cannot duplicate a message.
5. **An unknown enum value is data, not a crash.** The daemon is allowed to grow a kind,
   a status or an event name without the app being rebuilt.
6. **Every human message is answered by the verifier.** A `note`, a `task`, an `answer`
   and a `dispute` from the `human` seat each start a verifier turn, and the turn ends in
   the transcript with a `reply`, a `question` or a `verdict`. The app shows the wait
   rather than hiding it: while the last word is the human's or the coder's, the
   transcript carries a "verifier is working" row (`RunStore.awaitingVerifier`).
7. **The event stream may drop at any time.** Correctness never depends on it: after every
   drop, and every time the app comes to the foreground, it re-reads the run list and the
   selected run's detail, transcript, steps and frames from the API
   (`RunStore.resyncPlan`).
8. **The app never polls for screenshots.** It shows the frames the daemon already
   captured and pushed down the event stream; the only capture it triggers itself is an
   explicit "Screenshot" from the toolbar, and that too is a single request, not a loop.
