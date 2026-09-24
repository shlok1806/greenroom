# 0006. A motion vocabulary, signature moments, and click marks on the screen

Date: 2026-09-23
Status: accepted. Builds on ADR 0004. Amends the companion rule "nothing is drawn over the
Screen stage's picture".

## Context

A glyph-native window (ADR 0004) with no motion reads as a static terminal dump; one with
motion everywhere reads as a toy and slows the person down. The round-2 window had
almost none: a pulsing dot and system transitions.

Three things need motion to be understood: state that changes while nobody is looking
(a verdict lands, a status flips), work in progress (the agent thinking, a machine
booting), and handoffs (the person taking the screen and giving it back). The screen also
has a gap motion can fill: in a recording nobody can see where the agent clicked, so a
reviewer cannot tell what a step did.

The old rule kept the picture clean: nothing is drawn over it, and everything about it
sits under the track. Showing where the agent clicked needs an exception.

## Decision

1. **Text moves in steps, space moves in springs.** A fixed vocabulary, timings in
   `design/tokens.json` `motion`:

   | Motion | What it does | Budget | Used for |
   | --- | --- | --- | --- |
   | decode | characters scramble, then settle | 300 ms at most | new status text, titles, the verdict outcome |
   | draw | a box border traces itself | 250 ms at most | a card or pane appears |
   | type | text arrives at the stream's real rate | the stream's rate | streaming messages |
   | tick | braille spinner `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` | while it lasts | working, thinking |
   | load | 3 x 3 block-glyph wavefront, shimmer label, elapsed timer | while it lasts | boot, verifier working, connecting |
   | glide | spring, about 250 ms | about 250 ms | the cursor |
   | settle | spring, about 300 ms | about 300 ms | resize, zoom, expand |

   Nothing else moves. A new motion needs a row here first.
2. **Motion never blocks.** Navigation is instant and the animation plays over the new
   state. Input during an animation acts on the final state.
3. **Reduce Motion** makes every change instant, the spinner a static `…`, and freezes the
   loader's grid (its timer still ticks). Signature moments show their end state.
4. **Signature moments** may exceed the budget, and each plays once per event:
   - **Boot.** The screen window shows the daemon's real boot events as terminal lines
     (clone, boot, ssh, ready). The first frame resolves from an ASCII and dither
     rendering into real pixels over about 600 ms.
   - **Take control.** Everything but the screen dims ("house lights"), the cursor slides
     into the screen and becomes the pointer, the hint bar turns magenta. Give back
     reverses it.
   - **Verdict lands.** The outcome decodes in as big block letters (PASS or FAIL,
     figlet style on the grid) and the card border draws around it. On FAIL the cursor
     glides to the first failing step.
   - **Click marks.** See 5.
   - **Power-down.** On destroy the video dissolves into glyphs and freezes as a dithered
     still, kept as the run's thumbnail.
   - **Welcome.** The empty state types the `greenroom█` wordmark and the one command that
     starts the daemon.
5. **Click marks amend "nothing is drawn over the screen".** Where the agent clicked or
   typed, a cell-shaped ripple shows over the picture for about 800 ms. `m` turns them
   off and on. When the recording is paused on a step, that step's click shows as a
   static mark. Click marks are the only thing drawn over the picture; position, the
   step, live state and stream errors stay under the track.
6. **Effects never own content.** Each motion is a Canvas or Metal layer over real text
   or real video. Accessibility sees the final state.

## Consequences

- The decode, draw, loader, dither and cursor effects are small components with their
  own tests of the final state: a decoding label's accessibility value is the final text
  from the first frame.
- Click marks need where each step clicked. Input steps (`machine_click`,
  `machine_input`) carry coordinates as screen fractions, so a mark is placed through `ScreenGeometry`, the only place a fraction
  becomes a point. Typing has no coordinate; its mark sits at the last click.
- The boot moment needs the daemon's boot events as they happen. If the event stream does
  not carry them, that is a daemon change, and until then the boot lines show what the
  run facts know (booting, ready).
- The power-down still is a new artifact. Where it is kept (the app's cache or a daemon
  route) is decided when it is built; the app keeps no run on disk (companion rules), so a
  daemon route is the likely answer.
- **Pending the prototype** (`prototype/glyph-ui`): the boot reveal. If it reads slow or
  cheap, the first frame appears directly and the boot lines stay. Cursor-follow is
  pending too (ADR 0004, spec).
