# 0004. A glyph-native interface on a character grid

Date: 2026-09-23
Update 2026-09-27: Superseded by 0019 (the native SwiftUI redesign).
Status: accepted, amended by 0008. Supersedes 0001. Amends 0002 (colour table) and 0003
(the driving colour, where Take Control lives). 0008 replaces this ADR's type and colour
decisions with a readable, mostly-proportional revision.

## Context

The round-2 companion (ADR 0001 to 0003) is a standard Mac app: a three-column
`NavigationSplitView`, system text styles, system colours, no custom glass. It works, and
it looks like every other SwiftUI app. Three things pushed past it:

- **It has no identity.** greenroom's users live in terminals and agent CLIs. A window of
  stock controls says nothing about what the product is.
- **The wide window is empty** (issue #23). The transcript keeps an 820 pt measure and
  the rest of a large display is blank. Screen and Steps are tabs, so the evidence behind
  a verdict is never beside the screen.
- **The stock layout fights the tasks.** `NavigationSplitView` and `.inspector` add
  hundreds of points to the minimum width even while hidden (#64), so the conversation
  split was already hand-made (`ColumnDivider`), against ADR 0001.

The person decided the direction in a design session on 2026-09-23. This ADR records it.

## Decision

1. **Surfaces.** The native SwiftUI companion now. A web dashboard later reuses the same
   language and the same design data.
2. **Posture: fully custom chrome.** Own window chrome, own components, own type. No
   system toolbar or sidebar. We own accessibility ourselves (spec, Accessibility).
3. **Concept: glyph-native.** The Charm school (Bubble Tea, Lip Gloss, Glamour), built as
   a native app: real springs, blur where it helps, real video, a real mouse. There is no
   "character voice" copy layer; words stay plain.
4. **A true character-cell grid.** Every size, gutter and pane edge is whole cells. All
   text is monospace, prose included, at one size. Borders are box-drawing glyphs on the
   grid. Panes resize in cell steps. The one exception is the live machine screen, which
   breaks the grid and is framed as a window cut into it.
5. **Type: Monaspace, one face per voice.** Bundled under the OFL. Neon for chrome and the
   coder, Xenon (slab) for the verifier, Radon (handwriting) for the human with Argon as
   the fallback, Krypton for machine and tool output. The faces share metrics, so the
   grid holds across voices. A person may override the chrome and coder font; the voice
   faces stay fixed.
6. **Colour is an ANSI theme.** A theme is a background, a foreground, 16 colours, a
   cursor and a selection, stored in Ghostty's keys (`design/themes/*.json`). Semantic
   roles map to slots (`design/tokens.json`): pass green (2), failure red (1), needs you
   yellow (3), live cyan (6), driving magenta (5), dim text bright black (8). Roles take
   the normal slots because they hold 4.5:1 on paper and on near-black alike; bright
   slots never carry meaning. Every state is also a word. First-party themes: dark and
   light, each with a high-contrast variant. Importing a Ghostty or iTerm theme comes
   later and passes the same contrast check.
7. **The brand is a cursor.** Monochrome; hue is reserved for meaning. The signature is
   a block cursor in inverse video (`█`). It marks keyboard focus and the agent's current
   attention: it glides to the step being run or the message being written, and blinks
   while the agent thinks. The logo is `greenroom█`; the icon is `█`.
8. **Layout adapts by cell columns.** Wide (200 columns and more): runs, then the screen
   with the steps as a timeline under it, then the transcript. Screen and Steps stop
   being tabs. Medium (140 to 199): runs collapse to a strip of status glyphs and the
   steps to a one-row track under the screen. Narrow: one pane, `tab` cycles. `z` zooms
   any pane to the window and restores it. This closes issue #23: the wide window gives
   its width to the screen and the steps, and the transcript keeps a 56 to 80 column
   measure. Compare mode is later.
9. **Rendering: approach C.** Real SwiftUI views snapped to the grid through a
   `GridMetrics` environment value (the cell size from Monaspace's metrics, rounded to
   backing pixels), plus small Canvas or Metal effect layers per component (decode, draw,
   spinner, dither, cursor). Effects never own content: the text exists as real text
   first. Accessibility always sees the final state. Live video stays on
   `AVSampleBufferDisplayLayer` (ADR 0011).
10. **Design data at the repo root.** `design/themes/*.json` and `design/tokens.json` are
    the source for colours, faces, cell metrics, motion timings and layout thresholds.
    `DesignDataTests` decodes them and checks the contrast of every theme; a failing
    theme does not ship. CSS is generated only when the web starts. The action registry
    stays in Swift (ADR 0005).
11. **Beautiful UI is the component inventory, not the look.** Its agent-interface
    primitives (thinking trace, tool chips, approval card, agent screen, task rows,
    loading state, search, streaming text, code block) are the reference for what
    components we need and how they behave. We port that behaviour into SwiftUI in this
    language. Its visual style (Inter, rounded cards, shadows, a blue accent) is not
    adopted in the Mac app. The mapping is in the spec (Components); the credit and the
    web plan are in ADR 0007.

The first-party themes, measured (WCAG 2 contrast against the background):

| Theme | Background | Foreground | failure 1 | pass 2 | attention 3 | driving 5 | live 6 | dim 8 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| dark | `#101010` | `#d6d6d3` 13.06 | `#ec6a64` 6.17 | `#72c07a` 8.64 | `#d9b554` 9.68 | `#cb8ade` 7.46 | `#5cc2c4` 9.03 | `#6b6b6b` 3.57 |
| light | `#f4f1e8` | `#23211d` 14.23 | `#b4302a` 5.47 | `#2d7a3a` 4.70 | `#8a6408` 4.76 | `#8d3aa0` 5.73 | `#1b7575` 4.83 | `#8b867b` 3.21 |
| dark-hc | `#000000` | `#ffffff` 21.00 | `#ff8f87` 9.53 | `#7fe08a` 12.93 | `#ffd866` 15.27 | `#f0a8ff` 11.75 | `#6fe6e8` 14.16 | `#a8a8a8` 8.83 |
| light-hc | `#fffdf7` | `#000000` 20.65 | `#9a1111` 8.38 | `#0b5d1e` 7.94 | `#6a4a00` 7.96 | `#6e1780` 9.84 | `#00585a` 8.12 | `#555555` 7.33 |

Thresholds: normal themes 4.5:1 for the foreground and every role, 3:1 for dim; the
high-contrast themes 7:1 for all of them.

## What this changes in earlier ADRs

- **0001** is superseded. Its decisions 1 (three columns, `NavigationSplitView`,
  `.inspector`, the Screen and Steps segmented control with Cmd-1 and Cmd-2) and 7
  (system text styles, a 4 pt grid, system colours, "no custom colours, fonts or
  dependencies") are replaced. Its decisions 2 (the pinned verdict card), 3 (a run is
  named by its task), 4 (one window) and 6 (the connection state) stand.
- **0002, decision 2** (the colour table) changes hue, not structure: orange becomes
  yellow (slot 3), teal becomes cyan (slot 6), purple becomes magenta (slot 5). Each
  colour still means one thing and every state is still a word. Its decision 6 moves Next
  and Previous Failure off Command-' (ADR 0005 re-keys them through the registry). The
  rest stands, including the snapshot harness.
- **0003, decision 6** said the driving colour is the system accent and Take Control
  lives only in the toolbar. There is no system toolbar and no system accent: driving is
  magenta (slot 5), and take control is one registry action (`t`) with one clickable
  switch on the screen's border. There is still exactly one control. The rest of 0003
  stands: the verdict actions, the one vocabulary, the source label.
- **0002, consequence** "Esc does not give back control" still holds and is now the
  driving hard mode of ADR 0005.

## Consequences

- `Views/Theme.swift` (`Space`, `Radius`, `Palette`) is replaced by grid tokens read
  from `design/`. No point, radius or colour literal in a view; a view asks for cells and
  roles.
- Every view is rebuilt. The build goes by layer, one PR each: tokens, themes and grid;
  action registry, hint bar and Cmd-K; runs; stage (screen and steps timeline);
  transcript (markdown renderer and voices); signature moments. Each ends with an E2E
  screenshot review against a live daemon.
- The hand-made `ColumnDivider`, `RunLayout` point widths and the sidebar fold rules go;
  layout is by columns from `tokens.json`.
- Fonts are bundled (five Monaspace faces, OFL). The app's size grows by the fonts.
- We own accessibility for every control: labels, focus, traits, Reduce Motion, Increase
  Contrast. The system does none of it for custom chrome.
- A throwaway prototype on branch `prototype/glyph-ui` tests the risky bets first
  (monospace prose at length, Radon or Argon, cursor-follow, the boot reveal). It is
  never merged. The spec says what changes if each bet fails.
- The web dashboard, when it comes, reads the same `design/` files; a CSS generator is
  added then, not before.
