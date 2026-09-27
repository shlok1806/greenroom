# 0015. A verifier stopped at its limit is a card with Continue

Date: 2026-09-26
Status: accepted. Builds on 0003 (one status vocabulary), 0005 (keys), 0009 (transcript
cards) and 0012 (the row says its state), and on the daemon's issue #127.

## Context

A verifier turn has a cap on tool calls (`-verifier-max-steps`, 40) and a time budget
(`-verifier-budget`, 10 minutes). When a turn reaches one with no verdict to give, the
daemon posts a verifier reply marked `stop: "steps"` or `stop: "time"` ("I ran out of time
after 10m0s. Send a message and I will continue.") and the verifier then does nothing until
someone sends a message. `agent_wait` tells the coding agent what that means; the app drew
it as a plain reply. A person watching saw the words, with no sign that the run had stalled
on them, which limit it was, or what to do, and the run's row said nothing about it.

## Decision

1. **The stopped reply is a card** in the transcript, where the reply was (`LimitStop`,
   `LimitStopCard`): `! Stopped, out of time` (or `out of tool calls`), a sentence on what
   that limit means ("The verifier ran out of time for this turn before it finished."),
   then the verifier's own words, which carry the numbers (10m0s, 40 tool calls). The
   sender line names the kind `stopped`.
2. **Continue is the card's action while the verifier waits**: it posts your note
   "Continue.", which starts a new turn like any human note (ADR 0006). It is also a
   registry action, `continueVerifier` on `C` in the run context, hint first, in the Run
   menu and Cmd-K, offered only while it applies. No undo: a note costs one turn and
   decides nothing.
3. **What followed replaces the button.** A message that starts a turn (yours, or the
   coding agent's task, answer or dispute; its note does not) or the verifier at work again
   (a Give Back resuming the turn) makes the card say who went on and when ("You continued
   it at 18:07, below"). With the machine destroyed the card says nothing will continue it,
   as the composer does.
4. **The run waits on you.** While the newest verifier word is such a stop and nothing
   followed it, on a live machine, the run's turn is yours: it pins under "Needs you", its
   row reads `! Stopped, out of time` in the attention role, and the header's status line
   says "The verifier stopped, out of time". A question or a verdict waiting on you still
   leads. On a finished run the row reads `No verdict, out of time`: nothing will continue
   it, but it says why the task has no verdict.

## Consequences

- `Message.stop` decodes leniently (`StopReason`, an open enum): an unknown limit is data
  and reads "Stopped, at a limit" with its word.
- The row knows about a stop only for runs whose transcript is held, as with questions; the
  run list carries no last message.
- Harness scenarios 46 to 46d: the recorded bench run as it ended, the same run made live
  (Continue offered), after a Continue, and a stop at the step cap.
