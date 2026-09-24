# GlyphPrototype (throwaway)

A throwaway SwiftUI prototype of the Companion's glyph-native UI, built to test the risky
bets in motion at real size: monospace prose, the Monaspace voices, the follow-agent
cursor, boot reveal / power-down, ANSI themes, hint bar + Cmd-K, take control, verdict.
All data is synthetic and in memory. It lives on `prototype/glyph-ui` and is never merged.

Run from `apps/companion`: `swift run --package-path Prototype`
(or from the repo root: `swift run --package-path apps/companion/Prototype`).
Press `space` to play the run, `⌘K` for every action, `?` for help; the striped strip on
top is prototype-only scene control.
