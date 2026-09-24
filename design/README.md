# design

The design data for greenroom's interfaces. Decisions: companion ADR 0004
(`apps/companion/docs/adr/`); how it is used: `apps/companion/docs/design-spec.md`.

- `themes/*.json`: the first-party themes, dark and light, each with a high-contrast
  variant. Each is an ANSI palette in Ghostty's keys (`background`, `foreground`,
  `cursor-color`, `cursor-text`, `selection-background`, `selection-foreground`, and
  `palette`, 16 colours), plus a `name`. The same shape is how a user's Ghostty or iTerm
  theme will be imported.
- `tokens.json`: cell metrics (base face, size, line height), the face for each voice,
  the palette slot for each semantic role, motion timings and layout breakpoints in
  columns.

Who reads them: the Companion (SwiftUI) today; the web dashboard later, through CSS
generated from these files when the web starts. Nothing is generated yet.

`apps/companion/Tests/CompanionTests/DesignDataTests.swift` checks every file: all keys,
16 valid colours, every role on a slot 0 to 15, and contrast against the background
(4.5:1 for text and roles, 3:1 for dim, 7:1 for everything in a `-hc` theme). A theme
that fails does not ship. Change a colour here, then run `swift test` in
`apps/companion`.
