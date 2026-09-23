# Context: companion

The macOS app for watching a run, speaking into it, and taking its screen. Shared terms
are in the root `CONTEXT-MAP.md`.

## Glossary

- **Run**: a row in the sidebar (`RunSummary`), a `RunDetail` once opened. A finished run
  has no machine and is read-only.
- **Message**: one line of the conversation (ADR 0006). Sender: `coder`, `human`,
  `verifier`, `system`. Kind: `task`, `note`, `reply`, `question`, `answer`, `progress`,
  `verdict`, `accept`, `dispute`, `event`. The app is the `human` seat and sends only
  `note`, `task`, `answer`, `accept`, `dispute`.
- **Reply**: the verifier answering in plain words, no verdict. Ends its turn. Rendered as
  a verifier bubble.
- **Verdict state**: `none`, `proposed`, `accepted`, `contested` or `rejected`, derived by
  the daemon. Accept and Dispute show only for `proposed` and `contested`.
- **Frame**: one captured screen image (ADR 0008), `{at, file, step, bytes}`, listed at
  `GET /api/runs/{id}/frames` and fetched at `.../frames/{file}`.
- **Live screen**: the Screen tab following live: the machine's H.264 stream
  (`GET /api/runs/{id}/screen/live`, ADR 0011) while it plays, else the newest frame the
  daemon pushes (`event: frame`).
- **Recording**: a run's frames as one mp4 (`GET /api/runs/{id}/recording.mp4`). Needs
  `ffmpeg` on the daemon host.
- **Artifact**: a run-directory file fetched by name
  (`GET /api/runs/{id}/artifacts/{name}`); a screenshot step's `output.path` is the name.
- **Control lease**: one seat's right to a machine's mouse and keys, expiring after 60 s
  of silence (ADR 0009). Taken with "Take control", renewed while driving.
- **Driving**: the Screen tab while the app holds the lease: pinned to the newest frame,
  scrubber off, red badge, all input goes to the machine.
- **Action**: `move`, `click`, `down`, `up`, `scroll`, `type`, `key`, `sleep`.
  Coordinates are fractions 0 to 1, never pixels.
- **Batch**: actions sent in one request, one step in the evidence. The queue is trimmed
  while a request is in flight (`InputBatch.coalesced`).

## Invariants

1. Every read and write is an HTTP call to the daemon. No second source of truth.
2. The app does not operate a run: no creating machines, syncing or running commands. It
   may screenshot, destroy, and take the screen (ADR 0009).
3. Everything the app does is written into the transcript by the daemon.
4. Sequence numbers come from the daemon. A message is appended only if its `seq` is
   newer than the last held, so replays never duplicate.
5. An unknown enum value is data, not a crash.
6. Every human `note`, `task`, `answer` and `dispute` starts a verifier turn. While the
   last word is human or coder, the transcript shows "verifier is working"
   (`RunStore.awaitingVerifier`).
7. The event stream may drop. After a drop, and on every return to the foreground, the
   app re-reads the run list and the open run (`RunStore.resyncPlan`).
8. Input is sent only under a held lease, and the lease is given back on every way out of
   the Screen tab, including quit. While driving, the player is live.
9. The app never polls for screenshots. The toolbar "Screenshot" is one request.
