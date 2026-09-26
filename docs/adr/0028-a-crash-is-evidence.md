# 0028. A crash after an action is evidence, and the daemon reports it

Date: 2026-09-26
Status: accepted. Extends ADR 0024 and ADR 0027.

## Context

The first simple-tier run on the ADR 0027 verifier (30 dev cases, 3 trials) had no false
passes, but 3 of its 4 misses were edge-crash mutants ended `inconclusive`:
`tipsplit-crash-empty-bill` (twice) and `wordcount-clear-crash`. In each, the verifier did
what the task said (clear the Bill field, press Clear), saw the app disappear, relaunched it
again and again (up to 16 steps), and finally marked every check after the crash
`unchecked` because it "could not verify" the values. The task claimed "nothing breaks";
the crash is the answer.

Two things were missing: the daemon did not say that the app had quit, so the model had to
infer it from Finder coming to the front; and nothing told the model, or let the review
accept, that an observed crash is evidence against a check.

## Decision

1. **The effect read reports a quit or crash.** After each verifier input, if the app that
   was frontmost before the input is no longer running, the effect says so:
   `effect: <App> is no longer running (it quit or crashed)`, plus the newest crash report
   for it written since the input (`~/Library/Logs/DiagnosticReports/<App>*.ips`), by path
   and its first exception line, when there is one. The step record stores it as
   `effect.kind: "quit"`.
2. **A quit is evidence for `fail`.** A check whose actions include the input that made the
   app quit may be answered `fail` citing that effect read as its evidence. The review
   accepts it. It never accepts a quit as evidence for `pass`.
3. **The prompt says it once:** if the app crashes or quits while you do what the task
   describes, that is a `fail` of the checks that depend on it, citing the step that shows
   it; relaunch once only if a later check does not depend on the crashing action.

## Consequences

- Edge-crash mutants end in a grounded `fail` in a few steps instead of a relaunch loop.
- An app that quits on purpose (a "Quit" button under test) reads as `quit` too; the check
  for that button can pass on a later observation that the app is gone, since only a pass
  resting on the quit effect itself is refused. That case is rare in verification and is
  left to the verifier's judgement.
- Crash reports are read from the guest's user log directory only; a crash that writes no
  report still shows as `quit`.
