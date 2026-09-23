# 0003. What a person can do with each verdict, and one set of words for its state

Date: 2026-09-22
Status: accepted. Builds on ADR 0002.

## Context

The round-2 reviewers found five problems:

- A verdict the coding agent accepted said "No human has reviewed this verdict" but
  offered nothing to do about it.
- The screen sat on the last frame while the verdict cited an earlier step.
- The verdict's numbers were cut off mid-value.
- The sidebar, header and card each described one verdict in different words.
- Live and recording could both be claimed at once.

## Decision

1. **Actions follow the daemon's rules** (`session.checkReplyLocked` and `verdictLocked`).
   A person gets exactly the actions the daemon accepts, and the card says what each one
   does.
   - **Proposed or contested:** "Accept Pass/Fail" and "Reject...". Reject sends a
     human `dispute`, which the daemon records as rejected. After it, only a person can
     close later verdicts.
   - **Accepted by the coding agent:** the daemon refuses any accept or dispute on it. The
     card says so and offers "Ask for a Re-check...", which sends a human `task` to the
     verifier with the person's reason.
   - Button labels name the outcome and carry no icon, so "Accept Fail" never shows a
     check mark.
2. **One vocabulary**, shared by `RunFacts.rowStatus` (sidebar) and `VerdictReview`
   (card headline):

   | Situation | Words |
   | --- | --- |
   | Waiting for review | "Needs review" |
   | Contested | "Contested" |
   | Accepted by the coding agent | "Pass, unreviewed" |
   | Accepted by you | "Pass, you accepted" |
   | Rejected by you | "Fail, you rejected" |
   | Run state | "Live", "Idle 7m", "Machine lost" |
   | Run ended | "Ended 20:35[, machine lost]" |
   | Section headers | sentence case ("Needs you", "Running") |

   An unreviewed outcome keeps its word but not its colour.
3. **The screen opens on the verdict's evidence.** A finished run seeks to the first
   cited step and says "Step N, cited by the verdict", with a link to the step's record.
   Evidence opened from the card offers Back in both the Screen and Steps stages. Steps
   cited by a superseded verdict stay on the track as hollow diamonds.
4. **Readable claims.**
   - Short verdict text shows in full; long text shows three lines until expanded.
   - The expanded card shows each cited step's picture at 240 pt, beside the values the
     verdict claims ("It claims $24.00 $48.00").
   - A summary whose words lean clearly the other way from its outcome is flagged. The
     test needs two more hits on one side than the other, so a mixed account is not
     flagged.
   - Each dispute is listed with the verifier's next answer, so a dispute count can be
     checked.
5. **One source chip on the player:** Live, Connecting..., "Recording 3:05 of 11:34",
   or driving. The chip replaces the Following pill and the connecting line.
6. **One control:** Take Control / Give Back lives in the toolbar only. The driving
   colour is the system accent, which leaves four state hues: green, red, orange, teal.
7. **Recent steps** sit under the player (the last four, or the four up to the playhead),
   so the Screen answers "what did it just do".

## Consequences

- **Daemon changes this design wants.** Nothing here needs them to ship, but each
  closes a gap the UI can only describe:
  1. Let a person reject a verdict the coding agent accepted (today: "verdict is already
     accepted").
  2. Verdicts carry the task's acceptance criteria with a result and evidence per
     criterion, so the card can show a checklist with "no evidence" per item.
  3. A message addressed to the coding agent only, without starting a verifier turn, for
     nudging a stuck agent. Today every human note starts a turn. Nothing can wake an
     agent that is not calling `agent_wait`.
  4. Report idleness from steps and messages (PR #27 does most of this through
     `lastActivity`), so the 5-minute threshold is not the app's guess.
  5. Expose `-max-disputes` in `VerdictState`, so the card can say "1 of 2".
  6. The verifier's answer to a dispute carries `replyTo`, so the card does not have to
     assume the next verifier message is the answer.
  7. Set the guest's time zone to the host's at boot. Screenshots show the guest's UTC
     clock beside the app's local times.
- Idle runs offer "Write a Message", which focuses the composer. The composer says the
  coding agent reads messages on its next check.
- The person's "Show scroll bars" setting is respected. Rows keep clear of a legacy
  scroller instead of the app forcing overlay scrollers.
