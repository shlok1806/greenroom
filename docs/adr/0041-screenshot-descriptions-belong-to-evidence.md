# 0041. Screenshot descriptions belong to their evidence step

Date: 2026-09-30
Status: accepted. Extends ADR 0024 and 0027; issue #193.

## Context

A screenshot citation currently proves only that a capture exists. Its description lives in
progress text, outside the review's authoritative step ledger. A later capture of another run
can support a visual pass despite not showing the text asserted by its observation.

## Decision

- Persist the verifier's accepted, position-sanitized description, or its description error,
  against the original screenshot step. A write-once JSON sidecar named for that step is
  joined onto `Step.screenshotDescription` by `ReadSteps`. Captures retain their original
  timestamps and numbering; description latency does not become observation latency.
- Both whole-screen and cropped verifier captures use the same recording path. Recording
  failure is reported to the model. A missing, failed or corrupt description cannot support
  a newly reviewed visual pass. Historical captures and verdicts still load unchanged.
- For visual pass answers, check explicitly quoted text and distinctive decimal/comma numbers
  in the criterion and observation against the cited screenshot descriptions together. Conjunctions split clauses outside quoted strings. A clause containing a
  negation is excluded: an assertion that text is absent does not require that text to appear.
  A mismatch names the cited steps and missing or negated text, and asks for the capture of the claimed
  state or an unchecked answer. Numeric tokens retain signs and specified currency, ignore spaces
  inside the token ("$ 49.56" is "$49.56"), and substring matches cannot support them. An
  unquoted duration ("within 1.5 s") is a timing claim the step records measure, not screen
  text, so it is not required in a description.
  Earlier described captures can support earlier states of a multi-action check; the existing
  visual rule still requires a capture after the last action, and captures before a handover
  are excluded from textual support. No new check is inferred from progress wording.
- Keep ADR 0027's blank, offscreen and covered safeguards. A text match in a description
  cannot establish foreground, location, colour or absence. In particular it cannot override
  contradictory rendered metadata. This deliberately does not implement P4's lexical escape.
- Keep grounded fail answers, crash/no-change shortcuts and settlement under ADR 0031.
  The new support requirement applies to passes, never to evidence demonstrating absence.

## Limits

This detects missing named text and undescribed evidence, not arbitrary semantic mismatch.
A vision model can omit or hallucinate text, and matching text may belong to another window.
Unquoted appearance claims and negated clauses still need human or independent image review.
Rejecting all captures after unrelated actions would also reject legitimate multi-step checks;
therefore we retain per-check action freshness and explicitly ask for capture before leaving a
transient state. Record and bench richer location-aware visual support before relaxing any
rendered safeguard.
