# design

The design data for greenroom's interfaces. Decisions: companion ADR 0004 and 0008
(`apps/companion/docs/adr/`); how it is used: `apps/companion/docs/design-spec.md`.

- `themes/*.json`: the first-party themes, dark and light, each with a high-contrast
  variant. Each is an ANSI palette in Ghostty's keys (`background`, `foreground`,
  `cursor-color`, `cursor-text`, `selection-background`, `selection-foreground`, and
  `palette`, 16 colours), plus a `name`. The same shape is how a user's Ghostty or iTerm
  theme will be imported.
  - Each theme file also carries three keys beyond Ghostty's shape: `greenroom-brand`,
    `greenroom-brand-text` and `greenroom-chrome-tint` (named in `tokens.json`
    `themeKeys`). These are Greenroom's own extensions, not ANSI slots: `brand` is the
    olive used on large calm surfaces and actions (buttons, the selected run, the active
    tab, the cursor, the wordmark), `brandText` is the label colour that sits on top of
    it, and `chromeTint` is the faint brand-tinted background for the sidebar and top
    bar. A theme without them (an imported Ghostty or iTerm theme, which has no concept
    of a brand colour) is not to be refused: the importer, when it is built, fills these
    three from the first-party theme of the same appearance and contrast level (`dark`,
    `dark-hc`, `light` or `light-hc`) rather than failing the import.
  - `cursor-color`, `cursor-text` and `selection-*` stay in each file for Ghostty
    compatibility and apply only inside the terminal-screen well. The app's own focus
    cursor, selected run and active tab use `brand` and `brandText`, never these keys.
- `tokens.json`: cell metrics for the mono face, the two type faces (`mono` for chrome
  and data, `reading` and `readingHeading` for prose), the reading size and line height,
  the palette slot for each ANSI-slot semantic role, the `themeKeys` names above, the
  spacing scale and radii, motion timings and layout breakpoints in columns.

Who reads them: the Companion (SwiftUI) today; the web dashboard later, through CSS
generated from these files when the web starts. Nothing is generated yet.

`apps/companion/Tests/CompanionTests/DesignDataTests.swift` checks every file, parsing
each into typed models rather than grepping: all keys, 16 valid colours plus the three
`greenroom-*` keys, every role on a slot 0 to 15, and contrast against the background
(4.5:1 for the foreground and every role including dim, 7:1 for everything in a `-hc`
theme). It also checks `brand` against the background (3:1, a large-surface use),
`brandText` against `brand` (4.5:1 normal, 7:1 `-hc`), the foreground against
`chromeTint` (4.5:1 normal, 7:1 `-hc`), and that `brand` and the `pass` role are
distinguishable hues (at least 25 degrees apart in HSL). A theme that fails does not
ship. Change a colour here, then run `swift test` in `apps/companion`.
