# 0023. The scrub bar's preview floats above it and never sticks

Date: 2026-09-28
Status: accepted. Amends redesign 7's timeline bar (0019) and applies 0021 to playback.

## Context

The person watching a finished run found the scrubber gone: under the picture there was only
the hover preview (a frame and "1:17 Step 5 · Ran cd ...") with the transport controls
showing through it, and it stayed. Reproduced in a Greenroom VM on a copy of that run:

- The preview was a child of the track's `ZStack`, lifted with `.alignmentGuide(.top)`. A
  stack grows to hold what its alignment guides move, so while the preview showed, the
  track's stack was about 230 pt tall inside its 32 pt frame: the preview sat where the bar
  was, the bar was pushed down out of sight, and the controls drew over the preview. The
  `contentShape` on the grown stack kept the hover alive over the preview's area too.
- The preview was hover state set by `onContinuousHover` and by the drag. A drag that ended
  away from the bar never got a hover end, so the preview stayed, and with it the missing
  bar. Nothing cleared it when the window stopped being key, the app went to the back or
  the window went off screen.
- Checks proven close together drew their numbers on top of each other ("2" and "3" as one
  smudge), and playback kept moving the playhead ten times a second while nobody could see
  the window (against 0021).

## Decision

1. **The preview is an overlay** standing on a line of no height along the track's top
   edge: it floats above the bar (over the caption and picture), never changes the bar's
   layout, never covers the controls, and is outside the track's accessibility element.
   Only the 32 pt track answers the pointer. Its shadow is `Elevation.float`, tight enough
   not to shade the bar under it.
2. **One small state machine, `ScrubPreview`** (pure, held by `ShellModel`), decides whether
   it shows: a pointer over the track or a held press shows it; it hides when the pointer
   leaves (unless a press is held), when a press ends away from the track, and on dismissal:
   the window resigns key or is minimised, the app resigns active or hides
   (`ScrubPreviewDismisser`), the window goes off screen (`OnScreen`), a scroll (the one
   event monitor in `Keys`) or a run change. It shows again only on the next pointer move
   over the track.
3. **Close checks share a label**: numbers whose labels would touch are merged into one
   ("2,3", or "4-9" for a run) at the middle of their marks, until no two touch
   (`RecordingTimeline.checkLabels`); labels stay inside the bar's ends.
4. **Playback holds while the window is off screen**: the playhead does not move while
   nobody can see it and picks up where it was.
5. `ScrubPreviewTests` hold every transition, the labels, the hold, and the rendered bar:
   identical under a preview, identical after hover then exit, and drawn again after the
   window was hidden and shown, for a finished run and a live one.

## Consequences

- The preview can overlap the bottom of the picture while it shows, as a video player's does.
- The inspector's divider uses the system's `pointerStyle(.columnResize)` rather than an
  `NSCursor` push and pop, for the same reason: a hover end that never comes must not leave
  anything behind.
