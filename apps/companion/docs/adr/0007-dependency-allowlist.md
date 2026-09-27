# 0007. A dependency allowlist instead of no dependencies

Date: 2026-09-23
Update 2026-09-27: Item 6 is superseded by 0019: code is copied with its notice (ACKNOWLEDGEMENTS.md).
Status: accepted. Replaces the companion rule "no third-party dependencies" (ADR 0001,
decision 7, and `CLAUDE.md`).

## Context

The companion has had no dependencies at all. That kept the build simple and the Swift 6
strict-concurrency mode clean, and it was right for a window of stock SwiftUI controls.

The glyph-native interface (ADR 0004) needs things the standard library does not have:

- **Markdown parsing.** Verifier replies and tasks are markdown. `AttributedString`'s
  markdown support is inline-only and gives no block structure to lay out on a grid.
- **Shell output.** Tool output carries ANSI escapes (colours, cursor moves, progress
  bars). Rendering it right in our font and theme is a terminal emulator's job.
- **Ordered and deque collections and async stream operators** for the event stream,
  the frame cache and the send queue, which the code now hand-rolls.

A blanket ban pushes each of these into hand-written code. A blanket allowance invites
packages that break strict concurrency, pull in large trees or go unmaintained. What is
needed is a short list and a rule for adding to it.

The survey behind the list (2026-09-23):

- SwiftTerm 1.19: MIT, declares `swiftLanguageModes: [.v6]`.
- swift-markdown: swiftlang, actively maintained, a parser built on cmark-gfm.
- swift-collections 1.7 and swift-async-algorithms: Apple.
- KeyboardShortcuts 3.1 and Sparkle 2.10: maintained, not needed yet.
- No maintained native scrubber, JSON viewer, command palette or diff view exists.
  We build those.

## Decision

1. **Apple and swiftlang packages are allowed** without an ADR of their own, as long as
   they build in Swift 6 language mode with strict concurrency and no opt-outs.
2. **Any other dependency needs its own companion ADR** that states why it is needed,
   its licence, that it builds in Swift 6 mode with strict concurrency, and the exit plan
   (what we do if it is abandoned).
3. **The starting set:**

   | Package | Source | Licence | Used for |
   | --- | --- | --- | --- |
   | swift-markdown | swiftlang | Apache 2.0 | parsing only; we render onto the grid ourselves, Glamour style: headings bold or coloured, never larger |
   | swift-collections | Apple | Apache 2.0 | ordered sets and dictionaries, deques |
   | swift-async-algorithms | Apple | Apache 2.0 | debounce, merge, chunking on async sequences |
   | SwiftTerm | Miguel de Icaza | MIT | ANSI shell output in our font and theme (Swift 6 mode) |

   This ADR is SwiftTerm's ADR. Exit plan: it is used only for read-only output, behind
   one `ShellOutput` view; if it goes, that view falls back to stripping escapes and
   showing plain text in the machine voice.
4. **Later, by their own ADR when their feature arrives:** KeyboardShortcuts (user-set
   global shortcuts) and Sparkle (updates outside the App Store).
5. **Rejected:**

   | Candidate | Why not |
   | --- | --- |
   | Textual, MarkdownUI | proportional and variable-size text; it breaks the grid |
   | Pow, Vortex, Rive, Lottie | animation libraries; SwiftUI's springs, `Canvas`, `TimelineView` and one Metal shader cover the motion vocabulary (ADR 0006) |
   | Highlightr | runs highlight.js in a `JSContext`, which fights strict concurrency |

6. **Beautiful UI is credited, not depended on.** The Mac app ports the behaviour of
   Beautiful UI's components (github.com/slev12397/beautiful-ui, MIT, copyright (c) 2026
   Shane Levine) into SwiftUI (ADR 0004, spec). No code is copied and there is no package
   dependency. Because the behaviour is derived from it, the MIT notice is kept in the
   app's acknowledgements. The later web dashboard uses its React components directly
   through its registry (MIT), themed from `design/`, and swaps `SidebarNav`'s paid
   `@central-icons-react` icons for a free set (Lucide or Iconoir). That dashboard's
   dependencies get their own ADR when it starts.

## Consequences

- `Package.swift` gains `dependencies`. `Package.resolved` is checked in so builds are
  reproducible.
- `swift build -Xswiftc -warnings-as-errors` must stay clean with the dependencies in.
  A package that emits warnings under it is either fixed upstream or not added.
- The first build needs the network to resolve packages. CI caches `.build`.
- The app's acknowledgements list every dependency's licence and the Beautiful UI
  credit. The fonts (Monaspace, OFL) are listed there too.
- `CLAUDE.md` states the allowlist and the rule; a review that sees a new dependency
  without an ADR sends it back.
