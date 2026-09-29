# 0010. A screenshot description gives fractions of the image or no position

Date: 2026-09-28
Status: accepted.

## Context

Issue #248. The verifier reads screenshots as the describer's words (ADR 0030, 0032). The
describer prompt asks for each control's "approximate center as fractions of the image width
and height", and the tool result tells the verifier to click fractions of the picture. In run
`20260928-144042-10fc05f5e0a43287` (1024x768 at scale 1) the description listed
`settings/filter icon at (180, 872)`, `dock icon 1 at (240, 948)`, `thumbnail 1 at (570, 810)`:
not fractions, and not points either, since the screen is 768 points tall. `machine_ui` put the
Settings button at (0.181, 0.871), so the describer had used a 0 to 1000 grid.

It is not one bad answer. Every screenshot description the verifier was handed on this host
(`~/.greenroom/runs/*/conversation.jsonl`, 1114 coordinate pairs, measured 2026-09-28): 966
were fractions, 129 were numbers that fit 1024x768 points (menu bar items at `(22, 19)`,
`(89, 19)`), 19 fit only a 0 to 1000 grid, and some mixed the two (`(0.471, 811)`). Which space a
pair above 1 is in cannot be told from the pair: `(500, 700)` is inside both. Converting a guess
invites a click in the wrong place, which is what the issue is about.

## Decision

1. **Every coordinate pair in a description is checked in code before the verifier reads it**
   (`verifier.checkPositions`, in `describeWith`, so plain, cropped and question screenshots all
   get it). A parenthesised or bracketed pair of numbers with both inside 0 to 1 stays. Any other
   pair (a number above 1, a negative one) is replaced with `(position unknown)`, never rescaled.
2. **The verifier is told.** A description that lost positions ends with one line: how many were
   removed, that they were not fractions of the image, and to take positions from `machine_ui`.
3. **The prompt says it once more.** Part 4 of `visionPrompt` adds that both numbers are between
   0 and 1 and that a control the describer cannot place gets no position. The prompt's other
   parts are unchanged, so the measurements behind ADR 0030 and 0032 still hold for them.
4. Positions inside 0 to 1 are not checked against `machine_ui`: a description stays
   approximate by design, and the system prompt already sends every click through `machine_ui`
   when it lists the element. A pair that is in range but wrong (the issue's `(0.25, 0.74)` for
   a close button at (0.026, 0.073)) is not caught here.

## Consequences

- No description the verifier reads places a control outside the image or in another space.
- A describer that answers in points or on a 0 to 1000 grid loses those positions, not its text,
  and the verifier is pointed at `machine_ui`, which it should use for clicks anyway.
- A pair inside quotes on its line is window text the describer copied (part 3 quotes it), such
  as a label reading "(3, 4)", and is left as it is. An unquoted pair of numbers that is not a
  position reads as `(position unknown)` when either number is above 1; the count line says so.
