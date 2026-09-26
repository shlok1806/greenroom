# 0014. Evidence marks: the picture shows where unseen text sits

Date: 2026-09-26
Status: accepted. Amends 0006 decision 5 ("click marks are the only thing drawn over the
picture") and builds on 0011 and root ADR 0027.

## Context

Root ADR 0027 marks text a UI read reports but a person cannot see: `blank` (its frame
holds no ink), `offscreen` or `covered`. The ledger (0011) says so in words under the check
and in the bar over the picture. A reviewer then compares those words with a screenshot of
the whole desktop, at the stage's size, and hunts for the place the text should be. Xcode's
test report draws each element's bounding box over the frame of the recording
(`design-research-2.md`); Playwright's trace viewer highlights the element an action
targets. The frame of each element is already in the UI read (`x`, `y`, `w`, `h`, as
fractions of the screen), in the same space as the recording's frames.

## Decision

1. **While the picture is paused on a check's evidence**, each unseen text that check's
   words are about (`UnseenText.concerns`) is outlined where the read put it: a dashed
   rectangle in the attention role on a dark rim, and a label saying what is wrong ("not
   drawn", "off screen", "covered by another window"). Nothing is drawn while playing,
   following live or driving.
2. **An outline carries forward only while the screen is untouched.** It comes from a UI
   read cited by the check, taken at or before the shown step with no input step (click,
   type, key, scroll, input) between them. So the screenshot right after a read shows the
   empty place, and nothing is outlined after the screen may have changed.
3. **Frames become points only through `ScreenGeometry`** (`rect(atFraction:image:view:)`),
   as click marks do.
4. The layer takes no clicks and is hidden from VoiceOver: the same fact is in words in the
   card and the bar.

Click marks and evidence marks are now the two things drawn over the picture. A third
needs its own ADR.

## Consequences

- `UnseenText` carries its `frame`; `AcceptanceCheck.unseenMarks(atStep:in:)` is pure and
  tested.
- A reviewer sees, on the evidence itself, the place a claim rests on and that it is
  empty.
