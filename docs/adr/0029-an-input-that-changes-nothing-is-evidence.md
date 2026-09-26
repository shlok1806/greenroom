# 0029. An input that changes nothing is evidence

Date: 2026-09-26
Status: accepted. Extends ADR 0024 and ADR 0028.

## Context

The second simple-tier run (30 dev cases, 3 trials, ADR 0028 in place) got 89 of 90 right
with no false pass, but cost more than its targets: p95 4 min 55 s and 206k tokens per verdict,
against 3 min and 150k. The slowest trials were dead-control mutants.
`todolist-clear-done-no-action` took 26 to 40 steps and up to 424k tokens: the verifier
clicked a Clear done button that does nothing 3 to 12 times, by id, by position and by
keyboard, and took screenshots in between, each read reporting `effect: no change detected`.
A click on a dead control succeeds, so the repeat guard (#125), which counts failing calls,
never fired. In one trial the fail it finally reported, citing those effect reads, was refused
because its checks were visual and it cited no screenshot. The verifier then took one only to
satisfy the rule. That screenshot's description alone took 89 s.

The effect read after an input (ADR 0024) already says the input changed nothing. When the
task says the input should change something, that read shows the failure, the same way the
quit read in ADR 0028 shows a crash.

## Decision

1. **A repeated click that changes nothing is named.** Per turn, the verifier tracks clicks
   (`machine_click`, or a `machine_input` batch that is one click) whose effect read found no
   change, by the control they aimed at: the element of the verifier's read before the click,
   by identity (ids change between reads), whether it was clicked by id or by a position inside
   it, or else the point. From the second such click on the same control, the result adds:
   "This is the second time this control (<its name>) changed nothing. If the task says it
   should change something, that is evidence for fail: cite the effect reads (steps X and Y).
   Otherwise, try something different or ask." (third, 4th, ... with every read listed after
   that). Any input whose effect was a change, or a quit, clears the
   tally, since the screen is then not what those reads saw.
2. **Nothing ends the turn for it.** Unlike #125, a third click does not end the turn with a
   question: that would turn a correct fail into a question.
3. **A no-change read is evidence for `fail`.** A fail answer whose last action is an input
   whose effect read found no change, and which cites that read as evidence, holds on it
   alone, whatever the check's kinds (no screenshot needed). The input must be the check's
   last action: after a later input changed the screen, the old read shows nothing about the
   result. A pass never rests on a no-change read (ADR 0024's effect rule still asks for a
   later observation).
4. **The prompt says it once:** a control the task says changes something and that changes
   nothing gets one more click; if that changes nothing either, it is a fail citing the click
   and its effect read. Do not keep clicking it.

## Consequences

- Dead-control mutants end in a grounded fail after two clicks instead of a click loop and
  a screenshot taken only to satisfy the visual rule.
- A control whose effect the accessibility tree cannot see (a canvas, a change drawn only in
  pixels, a result that arrives after the read) reads as "no change". The review then accepts
  a fail that may be wrong. This can cause a false fail, never a false pass. The hint says
  "if the task says it should change something", and the bench's correct cases measure the
  risk.
- A click that is expected to change nothing (clicking a field that already has focus) never
  gets the hint unless it is repeated with no change in between.
