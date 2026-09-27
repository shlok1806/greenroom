# Context: companion

The macOS app for watching a run, speaking into it, and taking its screen. Shared terms
are in the root `CONTEXT-MAP.md`.

## Glossary

- **Run**: a row in the sidebar (`RunSummary`), a `RunDetail` once opened. A finished run
  has no machine and is read-only. Named by its **task**, the first `task` message.
- **Stage**: the run's main column: the Screen, with the Steps as a timeline under it on a
  wide window (tabs in the round-2 window). The conversation column sits beside it.
- **Verdict card**: the current verdict pinned above the conversation, headed by its state
  and who decided, with its evidence and the only Accept and Dispute buttons.
- **Run facts**: the one derived state of a run the whole window shows (`RunFacts`).
- **Phase**: booting, live, idle, ended or failed. **Idle** is a ready machine with no step
  or message for 5 minutes. **Ended** says how: destroyed by you, destroyed by the coding
  agent, or machine lost (its VM stopped under the run).
- **Done**: a run the coding agent finished with `run_finish` (root ADR 0031), with its
  **outcome**: `Verified` (an accepted pass on this run), `Unverified` or `Abandoned`, a
  summary and a ref (branch, commit, PR). Done outranks the phase; the machine may still
  be up. A run with no finish, every run from before it included, is not finished.
- **Needs you**: a question from the verifier, a proposed verdict to review, a contested
  one, or a verifier stopped at its limit on a live machine. Pinned at the top of the sidebar.
- **Failure**: a step whose tool call failed (its output is not a result) or whose command
  exited non-zero.
- **Human review**: a verdict a person accepted or rejected. One the coding agent accepted
  has none and reads "unreviewed".
- **Re-check**: a human `task` asking the verifier to look again at a verdict the daemon
  will no longer let anyone close (accepted by the coding agent).
- **Superseded evidence**: steps an earlier, replaced verdict cited; still marked on the
  track.
- **Message**: one line of the conversation (ADR 0006). Sender: `coder`, `human`,
  `verifier`, `system`. Kind: `task`, `note`, `reply`, `question`, `answer`, `progress`,
  `verdict`, `accept`, `dispute`, `event`. The app is the `human` seat and sends only
  `note`, `task`, `answer`, `accept`, `dispute`.
- **Reply**: the verifier answering in plain words, no verdict. Ends its turn. Rendered as
  a verifier bubble.
- **Stopped at its limit**: a verifier reply carrying `stop` (`steps`: it used all its tool
  calls; `time`: it ran out of time), posted when a turn reaches its cap with no verdict. The
  verifier waits until a message starts a new turn; Continue sends the note "Continue."
  (companion ADR 0015).
- **Verdict state**: `none`, `proposed`, `accepted`, `contested` or `rejected`, derived by
  the daemon. Accept and Dispute show only for `proposed` and `contested`.
- **Frame**: one captured screen image (ADR 0008), `{at, file, step, bytes}`, listed at
  `GET /api/runs/{id}/frames` and fetched at `.../frames/{file}`. The run list names each
  run's newest as `lastFrame`.
- **Live screen**: the Screen stage following live: the machine's H.264 stream
  (`GET /api/runs/{id}/screen/live`, ADR 0011) while it plays, else the newest frame the
  daemon pushes (`event: frame`).
- **Recording**: a run's frames as one mp4 (`GET /api/runs/{id}/recording.mp4`). Needs
  `ffmpeg` on the daemon host.
- **Artifact**: a run-directory file fetched by name
  (`GET /api/runs/{id}/artifacts/{name}`); a screenshot step's `output.path` is the name.
- **Control lease**: one seat's right to a machine's mouse and keys, expiring after 60 s
  of silence (ADR 0009). Taken with "Take control", renewed while driving.
- **Driving**: the Screen stage while the app holds the lease: pinned to the newest frame,
  scrubber off, driving role (magenta, ADR 0004), all input goes to the machine.
- **Action**: `move`, `click`, `down`, `up`, `scroll`, `type`, `key`, `sleep`.
  Coordinates are fractions 0 to 1, never pixels.
- **Batch**: actions sent in one request, one step in the evidence. The queue is trimmed
  while a request is in flight (`InputBatch.coalesced`).
- **Cell**: one character position on the mono grid, sized from the base face
  (`design/tokens.json` `cell`). Chrome and data (step and command rows, code, output,
  ids, times, key hints, section labels) size to whole cells; the rest of the window sizes
  from the spacing scale, not cells (ADR 0004, narrowed by ADR 0008).
- **Voice**: who is speaking (chrome, coder, verifier, human, machine). Since ADR 0008 a
  voice is shown by a sender label in words and a thin coloured left edge on the message
  group, not by a typeface: all reading text sets in one face (Mona Sans), all chrome and
  data in one mono face (Monaspace Neon).
- **Theme**: an ANSI palette (background, foreground, 16 slots, cursor, selection) in
  Ghostty's keys, `design/themes/*.json`, plus three Greenroom extensions beyond that
  shape (`greenroom-brand`, `greenroom-brand-text`, `greenroom-chrome-tint`, ADR 0008). A
  **slot** is one of the 16 ANSI colours (0 to 15). A **role** is a meaning mapped to a
  slot: pass, failure, attention, live, driving, dim. **Brand** and **chromeTint** are
  Greenroom's own roles, held as those three extension keys rather than a slot.
- **Cursor**: the block `█` in inverse video, filled with the brand colour since ADR
  0008. Marks keyboard focus, and the agent's current step or message (blinks while it
  thinks).
- **Hint bar**: the bottom row of the grid listing the keys that work in the current
  context; `?` expands it into full help.
- **Action registry**: the one list of actions (name, key, context, enabled) that the hint
  bar, help, Cmd-K palette and menu bar read (ADR 0005).
- **Zoom**: one pane filling the window (`z`), restored by `z` again.
- **Click mark**: a cell-shaped mark over the screen where the agent clicked or typed:
  about 800 ms while playing, static on a paused step, toggled by `m` (ADR 0006).

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
   (`RunStore.awaitingVerifier`), unless a later daemon event says nobody or nothing will
   answer (no verifier configured, or the machine destroyed with the task open).
7. The event stream may drop. After a drop, and on every return to the foreground, the
   app re-reads the run list and the open run (`RunStore.resyncPlan`).
8. Input is sent only under a held lease, and the lease is given back on every way out of
   the Screen stage (Steps, another run, a stopped machine, quit). While driving, the player is live.
9. The app never polls for screenshots. The toolbar "Screenshot" is one request.
