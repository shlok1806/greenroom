# 0040. Plan evidence for the claim before changing its state

Date: 2026-09-30
Status: accepted. Clarifies ADR 0027 and the verifier planning guidance.

## Context

Issue #263's Board run marked four correct intermediate states unchecked. Its UI reads
recorded the column counts, but it declared every check visual and took screenshots only
before and after those states. The visual evidence rule correctly refused tree-only proof
for the declared visual checks. The mistake was the evidence plan, not freshness.

## Decision

- Choose evidence kinds by the acceptance criterion. Text, counts, selection and membership
  are value checks when a UI read can establish them. For example, column headers reading
  To Do (1), Doing (1), Done (1) are values, and a card belonging to Doing is membership.
  Layout, color, clipping, readability and actual visibility need visual evidence.
- Explain this distinction in the system prompt, declaration tool and declaration result.
  Use value wording such as reads or contains instead of turning a value request into an
  appearance claim. Preserve any appearance requirement the task actually makes.
- Plan observations before the first input. Cite the recorded UI read or effect read for
  each intermediate value state. For a visual state, capture a screenshot before moving,
  clearing, dismissing or otherwise changing it. A final screenshot cannot prove an earlier
  state. Review overly broad visual declarations before acting, while redeclaration is free.
- Keep the daemon's rules intact. Never downgrade an explicitly declared visual check or
  remove a visual or timing requirement inferred from the criterion. After acting, the
  checklist remains locked. Freshness, handover, timing, lost-input and not-drawn checks
  still apply. Evidence planning must not trade away false-pass protection.

## Consequences

A value check may cite its earlier intermediate-state observation even after the workflow
moves on, provided that observation follows the actions it proves and satisfies the existing
rules. Genuine visual claims still require screenshots of their own state. This is guidance
for the model, not semantic classification by another model or a weakening of review. Scripted
regressions establish the contract; live model compliance still needs a Board replay.
