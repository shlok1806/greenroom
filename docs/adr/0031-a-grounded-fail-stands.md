# 0031. A grounded fail stands over answers that do not hold

Date: 2026-09-26
Status: accepted. Amends ADR 0024, point 3.

(0030 is taken by the describer switch, in progress.)

## Context

ADR 0024 point 3 checks a verdict before posting it: "A verdict that breaks a rule is not
posted." At a turn's limit (issue #127) the closing verdict gets the same checks, and the
daemon went further than the ADR: it downgraded a fail whose evidence broke any rule to
inconclusive, not only a pass (recorded in `apps/daemon/CLAUDE.md` as a known divergence).

The self-improvement demo hit it (issue #153, run `20260926-231011-600cf88cfbdbcba8`, verdict
seq 33). The verifier reported fail with eight checks. Four failing checks (`run-b-header`,
`run-b-card`, `run-b-hint`, `run-b-row-after`) cited its own fresh observations. Two other
answers broke rules: `run-b-row` cited step 5, a screenshot the coder took, and
`run-b-continue` was a timing check with no action to time. The daemon posted the whole
verdict as inconclusive. The fail was right: Run B's conversation did not load. Posting it as
inconclusive hid a well-grounded failure behind two bad answers, and outside a limit the same
verdict would have been refused and cost the verifier more steps.

ADR 0024's reason for the checks is asymmetric: a false pass is the costly error. A fail
already needs at least one failing check with valid evidence; an answer that does not hold
does not make that check's evidence any weaker.

## Decision

1. **A fail with at least one failing check whose evidence holds is posted as a fail**, at a
   limit and outside one. Each other answer that breaks a rule is posted `unchecked`, its
   `observed` saying which rule it broke ("Not verified: ..."), exactly as an inconclusive's
   answers are settled today. A declared check that was not answered is posted `unchecked`
   ("Not answered."). The summary gains one line: "[greenroom] Posted as fail on its evidenced
   failing checks. N answers did not hold and are shown as unchecked: <each rule>."
2. **Problems with the verdict as a whole still refuse it** (or, at a limit, downgrade it):
   arguments that do not parse, a check with no id, an unknown or repeated id, a step number
   in the free `evidence` list, no checks declared. These are not one check's answer, so there
   is nothing to mark unchecked.
3. **A fail with no failing check whose evidence holds is refused or downgraded as before.**
4. **A pass is unchanged:** every check must pass and meet every rule, or it is refused (and
   downgraded to inconclusive at a limit).

## Consequences

- The checklist stays the badge's scope (ADR 0024 point 6): a fail's unchecked answers are
  listed, so a reader sees which failures were shown and which were not.
- The model is not asked to repair answers that do not change the verdict, which saves steps
  on a correct fail. It also gets no chance to fix them: a check it answered badly stays
  unchecked on that verdict. A coder who wants those checks answered sends another task.
- The known divergence "at a limit it also downgrades a fail whose evidence breaks the rules"
  in `apps/daemon/CLAUDE.md` no longer applies and is removed.
- `report_verdict`'s description and the closing prompt say so. Bench runs from before this
  change counted such fails as refused or inconclusive; compare runs across it with care.
