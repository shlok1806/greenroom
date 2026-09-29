# 0021. Clocks stop while the window is off screen

Date: 2026-09-28
Status: accepted. Amends 0019's motion (`Clocked`, the `TimelineView` clocks) and 0011's
live screen for the redesigned window.

## Context

Issue #219: the Companion used 58 to 72% of a core for hours with its window not on screen.
Measured again on the redesigned window (main at bb37f2c, release build, a Greenroom Mac, two
runs starting, one of them open): 13.2% with the window in front, 9.3% with another window
over it, 13.3% with the app hidden. Nothing slows down when nobody can see the window:

- every turning glyph (`StatusGlyph` through `Clocked`) and the loader redraw every display
  frame through `TimelineView(.animation)`, and each redraw lays text out again
  (`LineBox`, which built a new `NSFont` on every measurement);
- the header's clock, the timeline bar, each running row of the runs list and the undo toast
  tick on their own `TimelineView(.periodic)` (the toast every 250 ms even with nothing
  held);
- the live screen keeps streaming and decoding while hidden.

## Decision

1. **One fact, `onScreen`**, read from the run window's `NSWindow.occlusionState` (false when
   the window is minimised, the app hidden, the window on another Space or wholly covered),
   set by `OnScreenReader` at the shell's root and passed in the environment (and to the runs
   table's hosted cells by hand, as `frozenNow` is).
2. **Every clock stops while `onScreen` is false**: `Clocked` draws its moment once, and the
   periodic clocks (header, timeline bar, running rows) draw once with the
   current time. When the window shows again they pick up from the clock, since every loop
   works out its phase from the time (0019), so nothing jumps or restarts.
3. **The undo toast ticks only while a choice is held.**
4. **The live screen stops while the window is off screen** and starts again when it shows,
   as it already does when its view goes away.
5. **Type metrics are worked out once per style**, not per layout (`TypeStyle.naturalLineHeight`).
6. `OnScreenTests` holds the rule: a view in a window that is not visible has no running clock.

## Consequences

- A window that is partly covered is still visible to AppKit and keeps ticking; only a
  window nobody can see stops.
- The live picture takes a moment to reconnect after the window comes back.
