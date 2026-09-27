# 0016. Twins say what tells them apart

Date: 2026-09-27
Status: accepted. Amends 0012 decisions 2 and 4 (the row carries the verdict's scope; titles
are not made distinct with the time).

## Context

Issue #157. Runs that share a task are common: the verifier bench runs each case several
times, and an agent re-runs a check. Their rows share a title, and 0012 said the row's time
(or its tally) tells them apart. In the list they often did not:

- With a tally, a narrow row drops the time first (`2/4 failed` outlasts `04:00`), so two
  trials with the same tally read the same.
- Two trials started in the same minute show the same `04:00`.
- Copies of one run (as the self-improvement demo served) share even the second, and their
  ids shared the six hex digits `Chrome.runHash` shows (`b48b96`), so a person, and
  Greenroom's verifier reading the accessibility tree, could not pick one.

## Decision

1. **A twin is a run whose short title another listed run also has.** Each twin gets a
   mark (`RunTitle.twinMarks`, over every run held, so a search does not change it): when
   it started to the minute, unless another twin started in the same minute; then to the
   second; failing both, its id tag, `#` and the id's leading hex digits, as many as tell
   the twins apart and at least the six `runHash` shows (`RunTitle.idTags`).
2. **A twin's mark leads its row's second line and never gives way** (`RowMeta.lines`): the
   tally, the running time and the size give way first. An id tag keeps the start time
   after it while there is room. A run with its own title reads as 0012 says.
3. `RunTitle.distinct` (the strip's tooltips and accessibility labels) appends the same
   mark, so a mark says the same thing everywhere.
4. `Chrome.runHash` stays the first six hex digits: "Run acbc2b" is how people already name
   runs. The tag only grows past six digits when twins share them.

## Consequences

- A twin in a narrow column may show its mark where a lone run shows its tally; its state
  word (`Fail, needs review`) still says the outcome.
- Harness scenario 47 serves five bench trials of one WordCount task and three copies of
  one run in one second, two of them sharing `b48b96`.
