# 0011. The verdict is a ledger: checks lead, evidence is named, the claim sits over its proof

Date: 2026-09-26
Update 2026-09-27: Amended by 0019: the ledger becomes the page (the checks column).
Status: accepted. Builds on 0002, 0003 and 0009 and on root ADRs 0024 and 0027. Replaces
the verdict card's reading order from 0009 and the checklist layout of the ADR 0024 layer
(`CLAUDE.md`, "A verdict is a checklist"). Amends 0003 decision 5's evidence bar.

## Context

Deciding whether to trust a verdict is the product's core job. The audit (`ux-audit.md`,
section 1) read every checklist state on real bench verdicts and found the checklist below
the fold of its own card (one check visible at 1024 x 660), no glanceable count, evidence
named only by a step number, check kinds and "not drawn" text (root ADR 0027) invisible,
and the claim 600 pt away from the picture it rests on. The research
(`design-research-2.md`) points the same way from several directions: Xcode's test report
and Playwright's trace viewer put the step, the picture and the scrubber in one selection;
Chromatic reviews one change at a time with keys; NN/g finds people rarely open citations
and that cards blur a ranked list; automation-bias research says the dangerous error is
missing what was not checked.

## Decision

1. **Checks lead the card.** Under the headline (state and who the verdict is from), the
   outcome and a **tally** (`✗ 2 failed  ○ 1 not checked  ✓ 5 passed`, failed first, zero
   counts left out), then the checks, then the verifier's summary folded to two lines
   under "The verifier's summary". A verdict without checks reads as before: its reasons
   are its whole account. The scope sentence ("Verified: 3 of 4 checks; ...") is removed;
   the tally and the rows say it.
2. **A check is a ledger row**, not a box: its mark, its criterion (with "failed" or "not
   checked" in words, and its kind when it is not a plain value: `visual`, `timing, within
   2 s`), what was observed, a warning for each text its UI reads report that a person
   cannot see and that the check is about (`! UI read 6: "Each pays: $49.56" is not
   drawn`), and its evidence **named by what it is** (`Screenshot 7`, `UI read 6`,
   `Command 4`; `Step 9` for anything else) as mono text links, then `after steps 31 and
   34` for its actions. No chips, no nested frames.
3. **A row selects its check** (a click anywhere on it, or `]` and `[` in the card's order,
   wrapping) and shows its first evidence on the screen; its evidence links show that
   piece. The selected check carries a 2 pt brand edge and a highlight, as the selected
   run does. The selection lives in the verdict's draft (`VerdictDraft.selectedCheck`),
   so it ends with the verdict like the rest of the draft. A finished run opening on its
   evidence selects the check that evidence answers.
4. **The claim sits over its proof.** When the stage shows a step the verdict cites, the
   bar above the picture is the check it answers (the selected one when it cites the step,
   else the first in the card's order): its mark, criterion, kind, observation and
   warnings, and which piece of evidence this is. A step no check cites still says "Step N,
   cited by the verdict". "Back to Verdict" becomes "Back": the verdict never left.
5. **Room for a decision.** While a verdict with checks is open for review, its card may
   take 0.8 of its column (it was 0.6 in every state) and leaves the header, one line of
   transcript and the composer. Closed verdicts go back to 0.6.
6. **What the buttons do is said with them**, whole, above Reject and Accept, never inside
   the capped body where a short column cut it mid-sentence. For a proposed verdict it adds
   that the coding agent can accept it too; the headline says "proposed by the verifier".
7. **The review is visible when it refuses.** A `report_verdict` the daemon refused reads
   "Verdict refused by greenroom: <its first reason>", with the raw call behind the caret.
8. **An answered plan folds.** Once the current verdict answers the verifier's declared
   plan, the plan in the transcript is one line ("Plan: 4 checks, answered in the verdict
   above") until opened.
9. **Cited steps on the scrubber are drawn in the foreground**, filled for this verdict and
   hollow for an earlier one, never in the outcome's colour: red already means a step that
   errored on the same track.

## Consequences

- `AcceptanceCheck` decodes `kinds` (and the single `kind` an early ADR 0027 daemon wrote)
  and `within`, leniently. `EvidenceStep` and `UnseenText` (`Model/Checklist.swift`) are
  pure and tested; which unseen texts concern a check is decided by its words (the whole
  text, or its label before a colon, in the criterion or observation), since a read marks
  every unseen text on the screen.
- Two registry actions, `nextCheck` (`]`) and `previousCheck` (`[`), in the run context,
  offered while the verdict has checks; the hint bar shows `[ ] checks`.
- The card's scroll view reaches into its padding by one step so the selected row's edge
  is never clipped.
- Not done here, and worth doing next: outlining the element a UI read reports on the
  picture (Xcode's bounding boxes), dashed where it is not drawn. It needs the step's
  element frames mapped through `ScreenGeometry` and an exception to "only click marks are
  drawn over the picture" (ADR 0006), so it gets its own ADR.
