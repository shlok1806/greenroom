# 0012. A run's row says its verdict, not its picture

Date: 2026-09-26
Update 2026-09-27: The row layout is superseded by 0019 (glyph, name, one meta).
Status: accepted. Supersedes 0010 (run thumbnails from the last frame). Amends 0003
decision 2 (the row's words for a verdict waiting on a person).

## Context

The runs list is how a person finds the run that needs them (job 3 in
`design-research-2.md`). The audit (`ux-audit.md`, R1 to R5) read it with the real list
of 179 runs, 136 of them waiting for review:

- Every waiting row said `! Needs review`, and none said whether the verifier proposes a
  pass or a fail, the one fact that sets how carefully to review.
- The daemon sends each verdict's checks in `/api/runs`, and no row used them.
- Each row's thumbnail (ADR 0010), a braille dither of the last frame at 56 x 44 pt, drew
  the same grey tile on every row: a desktop with one small window. It took a fifth of the
  column and pushed titles into two truncated lines.
- Runs with the same title got ", 02:29" appended, above a line starting "02:29".
- With 136 runs waiting, "Needs you" filled the column; Running and the days were out of
  reach without scrolling past all of it.

## Decision

1. **The row's state leads with the proposed outcome.** A verdict waiting on a person
   reads `Fail, needs review`, `Pass, needs review`, `Inconclusive, contested`, in the
   attention role with its `!`. The card's headline says the same two words (its state,
   and the outcome under it), so the one vocabulary of 0003 holds.
2. **The row carries the verdict's scope.** A verdict with checks puts a short tally before
   the time: `2/4 failed`, else `1/2 unchecked`, else `8/8 passed`
   (`Checklist.rowTally`). In a narrow column the time gives way first; the tooltip keeps
   it. Rows without checks keep the step count.
3. **No thumbnail in the row.** The title takes the row's width. `RunThumbnails` and its
   cache are removed; the power-down still stays a moment on the screen (ADR 0006), not a
   list decoration. If a picture ever earns a place in the list, it is a real one at a
   readable size, shown on demand.
4. **Titles are not made distinct with the time.** The row's time (or its tally) tells
   twins apart; the title stays the task's own words.
5. **Pinned sections show their newest five**, then "Show all N" (and "Show fewer"); the
   open run always shows, and a search shows every match. `j` and `k` move through what
   shows. The section label keeps the full count.

## Consequences

- `VerdictState` decodes `checks` (leniently: a malformed list is dropped, never the
  verdict).
- `lastFrame` stays in `RunSummary` as the wire has it; nothing reads it now.
- ADR 0010's cache, fetches and live refresh go with the thumbnails: less work per row
  and per frame event.
