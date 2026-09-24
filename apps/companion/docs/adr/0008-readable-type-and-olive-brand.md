# 0008. Readable type and an olive brand

Date: 2026-09-24
Status: accepted. Amends 0004 (decisions 5 and 6, the type and colour tables) and its
spec. Builds on 0004 to 0007; nothing else in them changes.

## Context

The person tried the all-monospace glyph direction (ADR 0004) end to end and found it
hard to read: prose set in Monaspace, five faces standing in for who is speaking, and
borders and a strict cell grid everywhere made the window feel like a terminal, not a
product. They want the terminal kept as an accent, not the whole surface, closer to how
Browserbase's session viewer reads: a calm, mostly proportional product with grid chrome
around it, less developer-tool, more approachable to a reviewer who is not an engineer.

This is a revision, not a reversal. The cursor brand, the hint bar with Cmd-K and single
keys and one action registry (ADR 0005), ANSI-slot themes with a contrast test, the
adaptive column layout and `z` zoom, the motion vocabulary and signature moments (ADR
0006), and Beautiful UI as the component reference (ADR 0007) all stand as decided. What
changes is which text is monospace, how strict the grid is, the two type faces, and the
brand colour.

## Decision

18. **Type follows what is being read, not who is speaking.** Chrome and data stay
    monospace: step and command rows, code, output, ids, times, key hints, small
    uppercase section labels. Anything read as sentences is proportional: verifier
    replies, human notes, the task, the verdict reason, questions, empty-state copy. The
    old rule ("all text is monospace, prose included") is gone.
    - **No strict cell grid, no borders everywhere.** Panes and rows are no longer forced
      to whole character cells, and box-drawing borders are no longer the default frame
      for every pane and card. In their place: a 4/8 pt spacing grid, generous
      whitespace, thin 1 px hairlines, and quiet panels with small radii (4, 6, 8 pt).
      The terminal look becomes an accent, carried by the block cursor, mono labels, key
      hints, the glyph loader and spinner, the decoded verdict, and the boot reveal, not
      by every pane's frame.
    - **Plain words first.** A step reads as a sentence a person would say ("Clicked Bill
      field", "Typed 120", "Took a screenshot", "Ran swift test"); the raw tool name,
      JSON or command is one expand (`⏎`) away, not shown by default. Rows carry at most
      two secondary facts, so a row is scannable rather than a data dump.
19. **Both colour schemes are designed, not one default and one afterthought.** The app
    follows the system's light or dark appearance, and Increase Contrast switches to the
    `-hc` variant of whichever is active, exactly as before. The machine screen well
    stays dark in both, because it is a picture of a monitor, not a page.
20. **Two faces, not five.** Mona Sans (GitHub, OFL, variable, github.com/github/mona-sans
    v2.0.27) is the face for reading text and headings; headings may use a heavier,
    slightly wider cut of it for emphasis without changing size. Monaspace Neon is the one
    mono face, for chrome and data both. The four voice faces (Xenon for the verifier,
    Radon and its Argon fallback for the human, Krypton for machine output) are dropped.
    A speaker is now shown by a small sender label and a thin coloured left edge on their
    message group, never by a typeface. Reading text sets at 14 to 15 pt with a line
    height around 1.5, looser than the mono grid's 1.35.
21. **The brand is olive, not monochrome.** The cursor stays the signature mark, but the
    brand gains a colour: a deep olive green, about `#4B5320` in the light theme, lifted
    in dark so it reads on a near-black background, and deeper again in light-hc for the
    button-label contrast that variant needs. Olive is used only on large, calm surfaces
    and on actions: primary buttons, the selected run, the active tab, the block cursor,
    the wordmark `greenroom█`, and a faint olive tint on the sidebar and top-bar
    backgrounds. Content areas that must read true to a screenshot (the screen, steps,
    transcript) stay neutral. A `brand` token and a `chromeTint` token are added per
    theme; they are Greenroom's own keys, not ANSI slots, so a theme file gains three
    extra keys alongside the 16-colour palette (design/README.md documents this for
    anyone importing a Ghostty or iTerm theme, which will not carry them).
22. **Pass moves to a bluer, brighter emerald, and it must not read as the same colour as
    the brand.** The green ANSI slot (2) keeps the `pass` role, but its hue shifts from a
    plain leaf green toward cyan (hue about 158 to 160, versus olive's 69 to 72) and gets
    brighter and more saturated. It is used only on small status marks, always paired
    with `✓` and the word "Pass", never on a large surface. Olive and pass emerald must be
    visibly different hues, checked by a hue-distance test, not by eye.
23. **The contrast test grows three new checks**, alongside the existing foreground,
    role and dim checks: brand against the background (3:1, a large-surface use), the
    brand's paired text against the brand (4.5:1 normal, 7:1 in `-hc`), and the
    foreground against `chromeTint` (4.5:1 normal, 7:1 in `-hc`, since real chrome text
    sits on that tint). The dim role's own threshold rises to match every other role:
    4.5:1 in the normal themes, 7:1 in `-hc` (it was 3:1 normal before; secondary text
    was reading too faint).

Measured (WCAG 2 contrast against the background, this revision):

| Theme | pass (slot 2) | dim (slot 8) | brand vs bg | brand-text on brand | fg on chromeTint |
| --- | --- | --- | --- | --- | --- |
| dark | `#47e1a8` 11.42 | `#7d7d7d` 4.62 | `#8da041` 6.56 | `#101010` 6.56 | `#1a1c14` 11.82 |
| light | `#127b58` 4.65 | `#6c6c6c` 4.65 | `#4B5320` 7.27 | `#f4f1e8` 7.27 | `#e8e6da` 12.83 |
| dark-hc | `#65ecba` 14.26 | `#9b9b9b` 7.56 | `#92a73e` 7.82 | `#000000` 7.82 | `#0d0f06` 19.30 |
| light-hc | `#0b6145` 7.35 | `#535353` 7.56 | `#363d14` 11.25 | `#fffdf7` 11.25 | `#f1f0e7` 18.36 |

Hue distance between brand and pass: about 86 degrees in the dark themes, about 90 in
the light themes (both comfortably past the 25-degree minimum). Full values, including
the unchanged failure, attention, driving and live roles, are in `design/themes/*.json`.

## Consequences

- `design/tokens.json`: the `type` map drops `verifier`, `human`, `humanFallback` and
  `machine`, and gains `mono` (Monaspace Neon), `reading` and `readingHeading` (Mona
  Sans). A new `reading` section holds the prose size and line height. New `spacing` and
  `radii` sections replace the point literals that used to live in `Views/Theme.swift`.
  A new `themeKeys` section names the three theme-file keys (`greenroom-brand`,
  `greenroom-brand-text`, `greenroom-chrome-tint`) so Swift and the tests read the name
  from one place rather than a string literal each.
- `design/themes/*.json`: the green palette slot and the dim (bright black) slot change
  in all four files; each file gains the three `greenroom-*` keys above.
- `DesignDataTests` decodes the three new theme keys and the new token sections as typed
  models (never grep), and gates: brand-on-background, brand-text-on-brand,
  foreground-on-chromeTint, dim at its raised threshold, and the brand/pass hue
  distance. A theme or token file that fails does not ship, as before.
- Mona Sans joins Monaspace as a bundled font (OFL); the app's acknowledgements list
  gains it. Bundle size grows by one variable font.
- Every view that rendered prose in a Monaspace face now renders it in Mona Sans at the
  reading size; every view that drew a full box-drawing frame is re-checked against the
  hairline-and-radius treatment. This is spread across the same build layers ADR 0004
  named (tokens; chrome; stage; transcript), not a new one.
- A sender is told apart by its label and left-edge colour, not its face; `MessageGroup`
  loses its per-voice font lookup and gains a left-edge colour lookup instead.
- The prototype question "monospace prose at length" (ADR 0004, spec) is answered: it
  read worse, and the fallback in the spec (a proportional face for reading text) is now
  the decision, not a fallback.
