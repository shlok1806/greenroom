# 22. Open-source SwiftUI sources and a pixel-perfect clone plan

The Companion is being rebuilt in native SwiftUI (no web UI) from the Figma design in
[docs/20 section 12](https://github.com/shlok1806/greenroom/blob/design/ux-research/docs/20-companion-ux-research.md#12-wireframes-and-mockups-figma)
(file <https://www.figma.com/design/041UmqtdMYVCufxO8g9Ius>). The design borrows its agent
states and motion from Beautiful UI and shadcn/ui, which are React. This document does three
things:

1. Lists the open-source SwiftUI and AppKit code worth copying, with licences read from each
   repo's LICENSE file, and what to take for which screen element (section 2).
2. Gives every component in the design a clone spec: the source to copy (repo, file, commit),
   the measured numbers, how SwiftUI reproduces them, and the known gaps (sections 3 and 5).
3. Defines how "cloned perfectly" is proved: ground truth rendered from the originals in
   Chromium and WebKit with a controlled clock, the SwiftUI port rendered by the snapshot
   harness with a controlled clock, and a comparison with numeric thresholds that runs in CI
   (sections 4, 6 and 7).

The first 12 ground-truth specs are already extracted and checked in under
[`22-swiftui-clone-plan/specs/`](22-swiftui-clone-plan/specs/), with the tooling that made
them in [`22-swiftui-clone-plan/tools/`](22-swiftui-clone-plan/tools/).

Everything here targets the package as it is: `apps/companion/Package.swift` says
`swift-tools-version: 6.0` and `platforms: [.macOS(.v15)]`, and the dependency allowlist
(companion ADR 0007) holds one package, swift-markdown 0.9.0. Every API named below exists on
macOS 15 unless marked otherwise.

## Contents

1. [The rule for "perfect"](#1-the-rule-for-perfect)
2. [Open-source sources](#2-open-source-sources)
3. [Porting rules: CSS and Motion to SwiftUI](#3-porting-rules-css-and-motion-to-swiftui)
4. [Ground truth: extracting the originals](#4-ground-truth-extracting-the-originals)
5. [Component inventory and clone specs](#5-component-inventory-and-clone-specs)
6. [Proving the clone](#6-proving-the-clone)
7. [Pixel-perfect against Figma](#7-pixel-perfect-against-figma)
8. [Order of work and the stacked PRs](#8-order-of-work-and-the-stacked-prs)
9. [Ideas worth adopting (proposals)](#9-ideas-worth-adopting-proposals)
10. [Sources](#10-sources)

## 1. The rule for "perfect"

Three sources of truth overlap, so the plan fixes which one wins for each property:

| Property | Wins | Why |
| --- | --- | --- |
| Colour, type, spacing, radius, elevation, layout of every screen | **Figma** (tokens page, components page, mockups) | It is the design the person approved. |
| Motion (durations, curves, delays, stagger, keyframes) the Figma file names | **Figma tokens page, Motion section** | It overrides the originals on purpose (for example the checking ring turns in 800 ms, not Beautiful UI's 1.1 s). |
| Motion and state behaviour Figma does not specify (how a row expands, how a trace reveals, how a toast stacks) | **The original's source**, measured by the extractor | Figma is static; the design says "Task Rows, Thinking, Tool Chips from Beautiful UI", so their behaviour is the spec. |
| Structure and keyboard behaviour (palette filtering, list selection, split views) | **The original's source** (cmdk, shadcn, NetNewsWire, CodeEdit) | Behaviour is not drawn. |

A clone spec is therefore the extracted JSON of the original plus a short list of Figma
overrides. "Perfect" means the SwiftUI port matches the merged spec within the thresholds in
section 6, in light and dark, at 1x and 2x.

The font is the one forced difference. Beautiful UI draws in Inter (with `cv11`, `ss01`) and
JetBrains Mono; Figma draws in Inter because its renderer lacks SF Pro; the app ships SF Pro
and SF Mono (docs/20 section 12, "Font"). The extractor therefore renders every original twice:
as published (`font: "inter"`) and with the faces swapped for SF Pro and SF Mono
(`font: "sf"`). The port is compared glyph for glyph against the `sf` runs and only for layout
and colour against the `inter` runs and Figma (section 7).

## 2. Open-source sources

Licences below were read from each repository's LICENSE file on 2026-09-27; stars and last
push are from the GitHub API that day; the commit is the default branch head that day and is
the one to copy from. **Copy** means code may be copied with its notice kept (MIT, Apache-2.0,
BSD, ISC). **Study** means read for ideas only, never copy (GPL, AGPL, no licence, or extra
conditions).

### 2.1 The top 15 to copy from

| # | Repository | Licence | Stars, last push | Commit | Take exactly this | For |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | [slev12397/beautiful-ui](https://github.com/slev12397/beautiful-ui) | MIT (Shane Levine, 2026) | 280, 2026-09-22 | `44a274e598395ab61e7c96c26fda2758780253b7` | `components/primitives/TaskRows.tsx`, `ThinkingState.tsx`, `ToolChips.tsx`, `LoadingState.tsx`, `ChatComposer.tsx`, `StreamingText.tsx`, `AgentScreen.tsx`; `components/atoms/Shimmer.tsx`, `StatusPill.tsx`, `Button.tsx`, `ProgressRing.tsx`, `StreamText.tsx`, `SegmentedControl.tsx`, `Chip.tsx`; tokens and keyframes in `app/globals.css` | Task rows, Thinking, Tool chips, boot loader, composer, shimmer, spinners, streaming text: all motion and state behaviour (specs already extracted) |
| 2 | [shadcn-ui/ui](https://github.com/shadcn-ui/ui) | MIT | 124,705, 2026-09-24 | `98a1fe67b439324ddc857f47fbdce056600a4329` | `apps/v4/registry/new-york-v4/ui/command.tsx`, `kbd.tsx`, `sidebar.tsx`, `sonner.tsx`, `skeleton.tsx`, `spinner.tsx`, `empty.tsx`, `tooltip.tsx`, `sheet.tsx`, `resizable.tsx`, `button.tsx` | Palette, keycaps, sidebar, toast wrapper, skeletons, empty states, tooltips, sheet |
| 3 | [jasonkneen/ShadKit](https://github.com/jasonkneen/ShadKit) | MIT (Jason Kneen, 2026) | 45, 2026-09-15 | `6dbefdeb72a276708b7ca748c71ac166c0f4f17d` | `Sources/ShadcnUI/Theme/OKLCH.swift` (OKLCH to sRGB), `Primitives/Command.swift` (grouped palette with an external keyboard-selection model), `Primitives/ResizeSplit.swift`, `Primitives/OverlayHost.swift`, `Primitives/Feedback.swift` (`ShadcnSkeleton`), `AIElementsUI/Reasoning.swift` (`AIChainOfThought`), `AIElementsUI/Tool.swift`, `AIElementsUI/Task.swift`, `AIElementsUI/PromptInput.swift` | The only SwiftUI port of shadcn/ui and Vercel AI Elements that ports from the registry source rather than screenshots; macOS 14+, no dependencies. Use it for **structure**; its motion is approximate (`.easeOut(duration: 0.12)` everywhere), so motion comes from our specs. Young repo: review every copied file. |
| 4 | [emilkowalski/sonner](https://github.com/emilkowalski/sonner) | MIT | 13,016, 2026-08-10 | `8e4662b39255120b62138312058f5d77c0139a5e` | `src/index.tsx` constants and `src/styles.css` (stacking, lift, swipe) | The "verdict ready" toast |
| 5 | [dip/cmdk](https://github.com/dip/cmdk) (was pacocoursey/cmdk) | MIT | 12,991, 2025-10-29 | `dd2250ed608443e8f32bafc5fa2d1d07a3746aa3` | `cmdk/src/command-score.ts` (the ranking function), selection and loop semantics in `cmdk/src/index.tsx` | Cmd-K palette filtering and ranking, ported to a pure Swift function |
| 6 | [barvian/number-flow](https://github.com/barvian/number-flow) | MIT | 7,717, 2026-09-20 | `c5906cf35c86583e756a6b91e043291c10b416e0` | `packages/number-flow/src/lite.ts` (default timings, the `linear()` spring table), `src/styles.ts` (the mask) | Checks tally ("2 of 4") and elapsed time rolling digits |
| 7 | [motiondivision/motion](https://github.com/motiondivision/motion) | MIT | 33,758, 2026-09-27 | `636e725fc71315ca91ff196eb09685017e2fe0c8` | `packages/motion-dom/src/animation/generators/spring.ts` (`findSpring`, `visualDuration`), `animation/utils/default-transitions.ts` | Exact spring conversion (section 3.3) |
| 8 | [CodeEditApp/CodeEdit](https://github.com/CodeEditApp/CodeEdit) | MIT | 23,046, 2026-08-18 | `fa2aebd86373211c78626074b53ab75010767575` | `CodeEdit/Features/Documents/Controllers/CodeEditSplitViewController.swift`, `Features/InspectorArea/Views/InspectorAreaView.swift`, `Features/ActivityViewer/Notifications/CECircularProgressView.swift`, `TaskNotificationView.swift`, `Features/NavigatorArea/OutlineView/*` | Split view and collapsing panes, the inspector column (Activity), a native circular progress, toolbar activity pill |
| 9 | [Ranchero-Software/NetNewsWire](https://github.com/Ranchero-Software/NetNewsWire) | MIT | 10,422, 2026-09-23 | `b4361413fc1850110f9f42652f0f84e7a51e9d64` | `Mac/MainWindow/Sidebar/SidebarViewController.swift`, `SidebarOutlineView.swift`, `Cell/SidebarCellLayout.swift`, `Cell/SidebarCellAppearance.swift`, `Keyboard/SidebarKeyboardDelegate.swift`, `UnreadCountView.swift`; `Mac/MainWindow/Timeline/TimelineTableView.swift` | The runs sidebar: source-list cell layout maths, keyboard handling, trailing count, and the NSTableView fallback for a very long list |
| 10 | [ghostty-org/ghostty](https://github.com/ghostty-org/ghostty) | MIT | 61,614, 2026-09-27 | `b40acce58dcf77df52231c3798ea58e924647c89` | `macos/Sources/Features/Command Palette/CommandPalette.swift` | A shipping SwiftUI command palette on macOS: focus handling, arrow keys, material background, shortcut symbols |
| 11 | [p0deje/Maccy](https://github.com/p0deje/Maccy) | MIT | 21,735, 2026-09-04 | `c376789c5d377b7c520b6f6e91f3f3a1aa28640b` | `Maccy/Views/HistoryListView.swift`, `HoverSelectionModifier.swift`, `MouseMovedViewModifer.swift`, `KeyHandlingView.swift`, `SearchFieldView.swift`, `KeyboardShortcutView.swift`, `VisualEffectView.swift` | Fast keyboard-first list with hover that does not steal selection, type-to-filter, shortcut glyph strings |
| 12 | [siteline/swiftui-introspect](https://github.com/siteline/swiftui-introspect) | MIT | 6,560, 2026-09-15 | `2fd59a7ae9f7bd6adf110a7342032bed21db38df` | `Sources/ViewTypes/List.swift`, `ListWithSidebarStyle.swift`, `ScrollView.swift`, `Window.swift`, `TextField.swift`, `NavigationSplitView.swift` | AppKit hooks where SwiftUI has no modifier: `NSTableView` row height and intercell spacing, scroll elasticity, titlebar separator, focus ring on a field. Copy the technique into our code; do not add the package without amending ADR 0007. |
| 13 | [pointfreeco/swift-snapshot-testing](https://github.com/pointfreeco/swift-snapshot-testing) | MIT | 4,347, 2026-09-21 | `28e5de025e3fd98991791bfbf83023ab3708da03` | `Sources/SnapshotTesting/Snapshotting/UIImage.swift` (`perceptuallyCompare`, CoreImage `CILabDeltaE`), `NSImage.swift` | The per-pixel delta E comparison in the clone checker (section 6). Copy the algorithm, not the package. |
| 14 | [EmergeTools/Pow](https://github.com/EmergeTools/Pow) | MIT | 4,405, 2026-04-13 | `1b4b1dda28c50b95f0872927ee2226fe8b58950e` | `Sources/Pow/Infrastructure/Spring.swift`, `SecondOrderDynamics.swift`, `ProgressableAnimation.swift`, `Extensions/Animation+TimingCurves.swift`, `Transitions/Blur.swift`, `Transitions/Move.swift` | Progress-driven effects that the harness can render at a given progress; blur-and-fade content swap (Figma: "2 px blur to hide a content swap") |
| 15 | [twostraws/Inferno](https://github.com/twostraws/Inferno) | MIT (Paul Hudson, 2023) | 2,924, 2026-05-17 | `a40c7a0bdec03aae1bd0b96c41d6a75451bafd2c` | `Sources/Inferno/Shaders/Transformation/Shimmer.metal`, `AnimatedGradientFill.metal` | A GPU shimmer via `colorEffect` if the text shimmer (section 5, C08) costs too much on long lists. GitHub reports NOASSERTION; the LICENSE file is plain MIT. |

### 2.2 Also worth copying (permissive)

| Repository | Licence | Stars | Commit | What for |
| --- | --- | --- | --- | --- |
| [markiv/SwiftUI-Shimmer](https://github.com/markiv/SwiftUI-Shimmer) | MIT | 1,698 | `0226e21f9bf355d40e07e5f5e1c33679d50e167f` | `Sources/Shimmer/Shimmer.swift`: a gradient mask shimmer with `bandSize`; the structure for C08, retimed to our spec. Last push 2024-08. |
| [jtrivedi/Wave](https://github.com/jtrivedi/Wave) | MIT | 2,396 | `27b7e1d2d42b8a4f56d9e84213910b0441c8b810` | `Sources/Wave/Spring.swift`: the `response`/`dampingRatio` to stiffness/damping maths, a second check on section 3.3; interruptible springs for the split-view divider. |
| [b3ll/Motion](https://github.com/b3ll/Motion) | BSD-2-Clause | 1,491 | `c9a57f9d2b9d0a1e1b905753175e75a422d381d5` | Analytic spring solver as a cross-check; last push 2024-10. |
| [sindresorhus/KeyboardShortcuts](https://github.com/sindresorhus/KeyboardShortcuts) | MIT | 2,716 | `772133d9dbe800fdac0473226822994c5c162c58` | `Sources/KeyboardShortcuts/Shortcut.swift` and `Key.swift`: correct glyph strings for every key and modifier (the keycap text). |
| [sindresorhus/Settings](https://github.com/sindresorhus/Settings) | MIT | 1,552 | `f41475771f65379ca10852c95119a7f53f0de5a5` | Settings window structure if screen 14 grows panes. |
| [MrKai77/Luminare](https://github.com/MrKai77/Luminare) | BSD-3-Clause | 167 | `c6b60e3b24dac0f51c25d0dcda27a45cc37e1e93` | Settings-sheet sections and toggles drawn in SwiftUI (from the Loop app, whose own code is GPL; Luminare itself is BSD-3). |
| [XcodesOrg/XcodesApp](https://github.com/XcodesOrg/XcodesApp) | MIT | 8,576 | `f9d50b93c7c4fde19ec3033e553471adee762a82` | `Xcodes/Frontend/Common/NavigationSplitViewWrapper.swift`, `ProgressButton.swift`, `ObservingProgressIndicator.swift`, `XcodeList/BottomStatusBar.swift` (the "2 of 3 Macs free" footer), `InfoPane/*` (a detail pane with installation steps: screen 06 phases). |
| [gonzalezreal/textual](https://github.com/gonzalezreal/textual) | MIT | 889 | `01b51875a5406eefc95f52a058cb059e7bc94dc4` | Successor to MarkdownUI (now maintenance mode): selectable rich text with attachments; only if the Activity transcript needs text selection across blocks. Needs an ADR 0007 amendment. |
| [gonzalezreal/swift-markdown-ui](https://github.com/gonzalezreal/swift-markdown-ui) | MIT | 3,932 | `8371aeb32f35795a9e559029fb08613355733626` | Maintenance mode; read its `Theme` model for block spacing numbers, do not adopt. |
| [swiftlang/swift-markdown](https://github.com/swiftlang/swift-markdown) | Apache-2.0 | 3,427 | pinned 0.9.0 | Already the one allowed dependency; keep parsing with it. |
| [appstefan/HighlightSwift](https://github.com/appstefan/HighlightSwift) | MIT (bundles highlight.js, BSD-3) | 211 | `99c431b38a1444a5fd6a4978307fbbefe3a7af53` | Syntax colours through JavaScriptCore if Activity ever shows code blocks with languages. Not needed for v1 (section 9). |
| [JohnSundell/Splash](https://github.com/JohnSundell/Splash) | MIT | 1,871 | `2e3f17c2d09689c8bf175c4a84ff7f2ad3353301` | Pure-Swift highlighter, Swift grammar only; last push 2024-05. |
| [raspu/Highlightr](https://github.com/raspu/Highlightr) | MIT | 1,869 | `da9bb2e4604da001f1d1a1adaa55f6a38509b405` | Same idea as HighlightSwift, NSAttributedString output. |
| [exyte/Chat](https://github.com/exyte/Chat) | MIT | 1,876 | `dcb3e5a3eb8210cf03c73e70551e635806925196` | Growing input field and message list mechanics for the composer. |
| [AttilaTheFun/agent_ui](https://github.com/AttilaTheFun/agent_ui) | Apache-2.0 | 1 | `bc851ba40d05c238cedaf43444da12b443a95b90` | An agent transcript, tool-activity rows and composer as SwiftUI packages (macOS 14). Brand new; read before copying. |
| [EnesKaraosman/SwiftyChat](https://github.com/EnesKaraosman/SwiftyChat) | Apache-2.0 | 352 | `d27dd3d238629a730f2f6a62df5fcc97efe37b40` | Loading and typing states in a chat list (macOS 14). |
| [gluonfield/enchanted](https://github.com/gluonfield/enchanted) | Apache-2.0 | 6,006 | `dc9bff882411a1317ff0cc5869de7e0515897df0` | A shipping native LLM chat app: streaming text, composer, conversation sidebar. |
| [adamtheturtle/CommandPaletteKit](https://github.com/adamtheturtle/CommandPaletteKit) | MIT | 0 | `eedf11935d4e3ea264921544526cf1bfe98b8045` | A dependency-free SwiftUI Cmd-K with fuzzy matching; second reference after Ghostty. |
| [sanzaru/SimpleToast](https://github.com/sanzaru/SimpleToast) | Apache-2.0 | 486 | `df5a3af345b162af9f37e6588fc23212a885033d` | Toast presentation over a SwiftUI view (macOS supported); the Sonner stack is ours. |
| [elai950/AlertToast](https://github.com/elai950/AlertToast) | MIT | 2,447 | `76a1c1793cd3443001c20c6033beb84453b25d9f` | Apple-style HUD toast; last push 2024-11. |
| [sunghyun-k/swiftui-toasts](https://github.com/sunghyun-k/swiftui-toasts) | MIT | 225 | `2b3b062f4d485de6ddc03797e5dbb81c261bf78b` | Archived; read its interactive dismiss. |
| [exyte/ActivityIndicatorView](https://github.com/exyte/ActivityIndicatorView) | MIT | 1,669 | `41f89372c018decaf965e8dddeb6e5347741beb4` | Spinner shapes (arcs, gradient ring) as reference for the checking ring. |
| [orchetect/MacControlCenterUI](https://github.com/orchetect/MacControlCenterUI) | MIT | 174 | `086922750e4286477431be75032218350441db3c` | Menu bar extra controls (proposal 9.7). |
| [steipete/CodexBar](https://github.com/steipete/CodexBar) | MIT | 21,981 | `3fb7b1d01c57a6495028f13fefafc14a0fb755d8` | A menu bar app for coding agents: status item, popover layout (proposal 9.7). |
| [exelban/stats](https://github.com/exelban/stats) | MIT | 42,151 | `3e57aafba5219767d1926a2d9492c3ee2cf41549` | Compact resource readouts (the resource warning, screen 08). |
| [SwiftUIX/SwiftUIX](https://github.com/SwiftUIX/SwiftUIX) | MIT | 8,168 | `3a99044b7045fb5907a7df18c7fe323e5656c6ab` | `NSVisualEffectView` and window wrappers to read; too large to adopt. |
| [aheze/Popovers](https://github.com/aheze/Popovers) | MIT | 2,213 | `576b4d3eacd9c6e4101519d803726e44bb2a3c45` | Anchored popovers (the tool chip diff preview); iOS-first. |
| [krzysztofzablocki/Inject](https://github.com/krzysztofzablocki/Inject) | MIT | 3,484 | `67e3ee9a2b7e40d6af72d07cf1b0d5c04399e809` | Hot reload while tuning a clone; dev only. |
| [lucide-icons/lucide](https://github.com/lucide-icons/lucide) | ISC | 24,747 | `66d8f9fc394b8530377e5f6112f0b8908ba01280` | The icons the Figma file draws (docs/20: "Lucide: every icon"). ISC is MIT-equivalent; keep the notice. |
| [Mobilecn-UI/swiftcn-ui](https://github.com/Mobilecn-UI/swiftcn-ui) | MIT | 146 | `515a26e0861f28fdaa1808e22789573d46f8c60e` | Older shadcn-inspired SwiftUI kit (iOS-first, 2024); ShadKit supersedes it. |
| [Dicky019/swiftcn](https://github.com/Dicky019/swiftcn), [Vignesh-Thangamariappan/shadcn-swift](https://github.com/Vignesh-Thangamariappan/shadcn-swift) | MIT | 4, 0 | `efc12cb`, `c29d2ab` | Other shadcn copy-paste SwiftUI attempts; thin, read only if ShadKit lacks a piece. |
| [GRimAce11/FlowMotion](https://github.com/GRimAce11/FlowMotion) | MIT | 0 | `5a5dc2d831357d31ff5090f05c76e72519623320` | Analytic spring solver and a timeline DSL (macOS 14); brand new. |

### 2.3 Study only, never copy

| Repository | Licence | Why it is here |
| --- | --- | --- |
| [Dimillian/IceCubesApp](https://github.com/Dimillian/IceCubesApp) | AGPL-3.0 | The best SwiftUI example of a sidebar with timelines, toolbar and settings; read for patterns. |
| [MrKai77/Loop](https://github.com/MrKai77/Loop) | GPL-3.0 | Radial menu and window chrome; study only (its UI kit Luminare is BSD-3 and listed above). |
| [GetStream/swiftui-spring-animations](https://github.com/GetStream/swiftui-spring-animations) | GPL-3.0 | A catalogue of SwiftUI spring parameters; read the numbers, copy nothing. |
| [kevinhermawan/Ollamac](https://github.com/kevinhermawan/Ollamac) | Apache-2.0 **with extra commercial conditions** | Not plain Apache; treat as study only. |
| [coteditor/CotEditor](https://github.com/coteditor/CotEditor) | Apache-2.0 code, **CC BY-NC-ND images** | Code may be copied, images never. |
| [gillesdm/SwiftCN](https://github.com/gillesdm/SwiftCN), [easy-utils/agent-swiftui](https://github.com/easy-utils/agent-swiftui) | No licence | Unlicensed means all rights reserved. |
| Beautiful UI `SidebarNav.tsx` | MIT file, but imports the paid `@central-icons-react` package (install fails without `CENTRAL_LICENSE_KEY`) | Use shadcn's sidebar and NetNewsWire instead. |

### 2.4 What was searched and what was not found

- **SwiftUI ports of shadcn/ui:** ShadKit (best, covers AI Elements too), swiftcn-ui, swiftcn,
  shadcn-swift, SwiftCN (unlicensed). **No SwiftUI port of Beautiful UI exists** (searched
  GitHub, Swift Package Index and the web); ours in section 5 is the first.
- **Sonner-like SwiftUI toast:** none clones Sonner's stack; SimpleToast, AlertToast,
  swiftui-toasts and Toasty present a single toast. The stack is small enough to port from
  Sonner's CSS (C21).
- **NumberFlow-like:** SwiftUI's `.contentTransition(.numericText(value:))` (macOS 14) rolls
  digits but with Apple's own curve and blur; open-source ports (AnimateNumberText,
  RollingCounter, AnimatedNumber) are approximations. C10 ports NumberFlow's own timing table.
- **Command palette:** Ghostty (shipping, MIT), CodeEdit's Open Quickly, ShadKit `Command`,
  CommandPaletteKit. cmdk's scorer is the part worth porting exactly.
- **Fast large lists on macOS:** SwiftUI `List` on macOS is backed by `NSTableView` and reuses
  rows; `LazyVStack` creates rows lazily but keeps them, which is what makes it slow on long
  content (measurements in the STRV and fatbobman articles in section 10). The runs list and a
  check list are small; the Activity timeline can reach thousands of rows, so it uses `List`
  (or NetNewsWire's `TimelineTableView` pattern through `NSViewRepresentable` if profiling says
  so), never `LazyVStack` inside `ScrollView`.
- **X/Twitter, Hacker News, designeer.xyz:** HN's "Ask HN: Open-Source SwiftUI Apps?" names only
  IceCubes; designeer.xyz has no SwiftUI or AppKit entries in any category (it is web-first), so
  its value was already used in docs/20. Awesome lists read: `donarb/swiftui-macos`,
  `stakes/swiftui-macos-resources`, `jaywcjlove/awesome-swift-macos-apps`,
  `onmyway133/awesome-swiftui`, `stefanjudis/awesome-command-palette`.

## 3. Porting rules: CSS and Motion to SwiftUI

These rules apply to every component. Each is either exact (the same maths) or a known gap with
a stated workaround.

### 3.1 Colour

- CSS colours are sRGB. Build every token as `Color(.sRGB, red:green:blue:opacity:)`, never
  `Color(red:green:blue:)` in a generic space and never an asset catalog colour in Display P3.
- `oklch()` tokens are converted once, offline, to 8-bit sRGB hex with the standard OKLab
  matrices and per-channel clipping (`tools/tokens.py`; ShadKit's `OKLCH.swift` does the same at
  runtime). The browser's own conversion was cross-checked: the extractor reads colours back
  through a canvas and gets the same hex (for example `--ink-2` light `#62656B`).
- `color-mix(in srgb, A p%, B)` is a straight sRGB mix: `A*p + B*(1-p)` per channel, done
  offline into a token.
- The harness must render comparison images into an sRGB bitmap
  (`NSBitmapImageRep` with `.sRGB` colour space, window `colorSpace = .sRGB`); Playwright
  screenshots are sRGB.

### 3.2 Curves (cubic-bezier, keyframes, `linear()`)

| CSS | SwiftUI (exact) |
| --- | --- |
| `cubic-bezier(x1, y1, x2, y2) d ms` | `.timingCurve(x1, y1, x2, y2, duration: d/1000)` (same unit Bezier; for a reusable curve `UnitCurve.bezier(startControlPoint:endControlPoint:)`) |
| `ease` | `.timingCurve(0.25, 0.1, 0.25, 1, ...)` (not SwiftUI's `.easeInOut`) |
| `ease-in` / `ease-out` / `ease-in-out` | `(0.42, 0, 1, 1)` / `(0, 0, 0.58, 1)` / `(0.42, 0, 0.58, 1)` as `timingCurve`. SwiftUI's `.easeOut` is **not** CSS `ease-out`. |
| Tailwind default transition (`transition-*` without an ease class) | `(0.4, 0, 0.2, 1)`, 150 ms unless a `duration-*` class is present. The extractor caught this on TaskRows: the border radius and chevron rotate with `cubic-bezier(0.4, 0, 0.2, 1)` 300 ms while the detail reveal uses `(0.23, 1, 0.32, 1)`. |
| `@keyframes` with `animation-timing-function` | **The curve applies per segment between keyframes**, not to the whole animation. Port as a pure function `value(t)` that finds the segment and applies the curve to local progress, driven by `TimelineView` or `KeyframeAnimator` with one `CubicKeyframe`/`LinearKeyframe` per segment and the curve inside. |
| `animation-delay` with `fill-mode: both` | The 0% value is shown during the delay: set the start state before the view appears, then `withAnimation(curve.delay(delay))`. |
| `step-end` keyframes (the caret blink) | `KeyframeAnimator` with `MoveKeyframe` jumps, or a `TimelineView(.periodic(from:by: 0.5))`. |
| `linear(p0, p1, ...)` (NumberFlow's spring table) | A `CustomAnimation` whose `animate(value:time:context:)` returns `value.scaled(by: table(t/d))`, where `table` interpolates the listed points evenly spaced (the CSS `linear()` rule when no percentages are given). |
| `infinite` loops (spin, shimmer, pixel grid) | Never `repeatForever`. Drive from a `MotionClock` date through `TimelineView(.animation)` and compute the phase `((now - start) mod period) / period`; the harness injects a fixed date. This makes every loop renderable at any time, which the proof needs. |

### 3.3 Springs (Motion to SwiftUI)

Motion (`packages/motion-dom/src/animation/generators/spring.ts` at `636e725`) and SwiftUI both
evaluate the closed-form damped harmonic oscillator, so a conversion is exact when the three
physical parameters match:

- **Physics springs** (`stiffness k`, `damping c`, `mass m`; Motion defaults 100, 10, 1):
  `Animation.spring(Spring(mass: m, stiffness: k, damping: c))` (macOS 14), or equivalently
  `.spring(response: 2π·√(m/k), dampingFraction: c / (2·√(k·m)))`.
- **`visualDuration v` and `bounce b`** (Motion 11+): Motion sets `k = (2π / (1.2·v))²`,
  `c = 2·clamp(0.05, 1, 1 − b)·√k`, `m = 1`. That is exactly
  `.spring(response: 1.2·v, dampingFraction: clamp(0.05, 1, 1 − b))`, which is also
  `.spring(duration: 1.2·v, bounce: b)` for `0 ≤ b ≤ 0.95`.
- **`duration d` and `bounce b` without `visualDuration`:** Motion solves for the undamped
  frequency with 12 Newton iterations (`findSpring`, initial guess `5/d`, `safeMin = 0.001`,
  ζ = `clamp(0.05, 1, 1 − b)`), then `k = ω²`, `c = 2ζ√k`. Port `findSpring` verbatim to Swift
  (about 40 lines) and feed `Spring(mass: 1, stiffness: k, damping: c)`; do not use SwiftUI's
  `.spring(duration:bounce:)` here, it means something else.
- **Motion's implicit defaults** when a transition is omitted (`default-transitions.ts`):
  `x`, `y`, `rotate` and other transforms use `stiffness 500, damping 25` (ζ 0.559, response
  0.281 s); `scale` uses `stiffness 550, damping 30`, or critical damping `2·√550 ≈ 46.9` when
  the target is 0; other values use `cubic-bezier(0.25, 0.1, 0.35, 1)` 300 ms; three or more
  keyframes use 800 ms.
- **Initial velocity:** Motion's velocity is in units per second; SwiftUI's
  `initialVelocity` in `interpolatingSpring` is relative to the distance. Divide by
  `(to − from)`.
- **Gap:** Motion stops at `restDelta 0.5`/`restSpeed 2` (0.005/0.01 for small ranges), SwiftUI
  at its own threshold. The curves are identical until then, so the comparison window for a
  spring ends when Motion reports done.

None of the Beautiful UI pieces the design uses animate through Motion at runtime (they are
CSS keyframes and transitions; `grep` finds no `motion/react` import in `components/`), so the
spring rules matter for Motion Primitives, Kibo and any future piece.

### 3.4 Boxes, borders, rings and shadows

- **Radius:** CSS `border-radius` is circular. Always write
  `RoundedRectangle(cornerRadius: r, style: .circular)`; Figma corners with 0 smoothing are
  circular too. `rounded-full` (a 9999 px radius) is `Capsule(style: .circular)`.
- **Border** (`border 1px`): inside the box, `.overlay(shape.strokeBorder(color, lineWidth: 1))`.
- **Ring** (`box-shadow: 0 0 0 1px C`, Beautiful UI's `shadow-hairline`, `shadow-card`,
  `shadow-btn`): drawn *outside* the box, with the radius grown by the spread. Port:
  `.background(RoundedRectangle(cornerRadius: r + 1, style: .circular).fill(C).padding(-1))`
  under the fill, not a stroke on top.
- **Shadow:** a CSS shadow `x y B s C` is a Gaussian of standard deviation `B/2`. SwiftUI's
  `.shadow(color:radius:x:y:)` and `CALayer.shadowRadius` take the same `B/2` (Sketch and Figma
  use this 2:1 rule too; confirm it once in the harness with a single-layer shadow before
porting the stacks). Spread `s` has no SwiftUI parameter: draw the shadow from the shape
  expanded by `s` in a background layer. Layered stacks (shadow-plugin's `--shadow-sm` has six
  layers) become six stacked `.shadow` modifiers on a background shape, in CSS order, and only
  the background casts (so the content does not get six shadows).
- **Inset shadow** (`--shadow-inset-field: inset 0 1px 2px`): `shape.fill(C.shadow(.inner(color:radius:x:y:)))` (macOS 13+), radius `B/2`.
- **`overflow: hidden` with a radius:** `.clipShape(RoundedRectangle(..., style: .circular))`.
- **Hairlines:** 1 CSS px is 1 pt; at 2x it is two device pixels, same as SwiftUI. Do not use
  `1 / displayScale` hairlines where the source says 1px.

### 3.5 Type (WebKit and CoreText)

- **Face:** port in SF Pro via `Font.system(size:weight:)`, which switches between SF Pro Text
  and Display at 20 pt the same way `-apple-system` does in WebKit and Chromium on macOS. Mono is
  `Font.system(size:weight:design: .monospaced)` (SF Mono).
- **Weight:** 400 `.regular`, 500 `.medium`, 600 `.semibold`, 700 `.bold`.
- **Letter spacing:** CSS `letter-spacing: -0.01em` at 13 px is `-0.13` pt of tracking:
  `.tracking(em × size)`. Figma percentages are the same: Display 22 pt at −1.8% is
  `.tracking(-0.396)`. Use `tracking`, not `kerning` (kerning disables ligature-aware spacing).
- **Line height:** a CSS line box is `line-height` tall with the half-leading split above and
  below the glyphs; SwiftUI's `Text` is as tall as the font's own line (SF Pro 13 pt is about
  15.5 pt) and `.lineSpacing` only adds space *between* lines. For a single line of CSS
  `line-height L`: `.frame(height: L)` (text is vertically centred, which matches half-leading
  for SF). For wrapped text: `.lineSpacing(L − font.lineHeight)` plus
  `.padding(.vertical, (L − font.lineHeight)/2)`, where `font.lineHeight` comes from
  `NSFont.systemFont(ofSize:weight:)` (`ascender − descender + leading`).
  `leading-none` (line-height 1) makes the box shorter than the glyphs: frame height = size.
- **Tabular figures:** `tabular-nums` is `.monospacedDigit()`.
- **Truncation:** `truncate` is `.lineLimit(1).truncationMode(.tail)`; `text-wrap: balance`
  has no SwiftUI equivalent (known gap, only on multi-line headings, which the design avoids).
- **Rasterisation (known gap):** the sources set `-webkit-font-smoothing: antialiased`, which
  renders glyphs with grayscale antialiasing and no CoreText stem dilation; AppKit text keeps
  CoreText's default smoothing, so SwiftUI glyphs are a fraction heavier at the same weight.
  The proof therefore compares glyph pixels with a looser threshold than everything else and
  compares text by its measured frame and baseline (section 6.3). An experiment worth one
  afternoon: render the port with `CGContext.setShouldSmoothFonts(false)` in a custom
  `NSView` capture and see if the glyph delta collapses.

### 3.6 Layout primitives

- `grid-template-rows: 0fr → 1fr` (the expand-in-place reveal used by TaskRows, ThinkingState
  and ToolChips) animates the row's height from 0 to its content height with the content
  clipped. Port: measure the content with `.onGeometryChange(for: CGFloat.self)` (macOS 15),
  then `.frame(height: open ? measured : 0, alignment: .top).clipped()` animated with the
  source curve, and animate opacity alongside. Do not use `if open { ... }` with a transition:
  that inserts and removes the view and uses a different curve.
- Flex `gap` is `HStack(spacing:)`/`VStack(spacing:)`; `min-w-0 flex-1 truncate` is
  `.frame(maxWidth: .infinity, alignment: .leading)` with `.layoutPriority(-1)` on the
  truncating label so the trailing meta keeps its width.
- `transform-origin` is `.scaleEffect(s, anchor:)`.
- `translate: 0 -0.5px` and other sub-point nudges are kept as `.offset(y: -0.5)`.

## 4. Ground truth: extracting the originals

### 4.1 What was run for the first specs

1. `git clone https://github.com/slev12397/beautiful-ui` at `44a274e`.
2. `npm install` fails on `@central-icons-react/round-outlined-radius-2-stroke-2` (its postinstall
   demands `CENTRAL_LICENSE_KEY`, a paid licence). It is only imported by `SidebarNav.tsx`,
   `components/site/UseThisHarness.tsx` and `lib/meta.ts`, none of which the design uses, so
   the scratch clone drops it from `package.json` and installs the rest (the lockfile is
   regenerated, so transitive versions are the day's latest within the declared ranges; the
   pieces extracted depend only on React, Next and Tailwind v4 at runtime).
3. A scratch route [`tools/spec-page.tsx`](22-swiftui-clone-plan/tools/spec-page.tsx) (copy to
   `app/spec/page.tsx`) renders exactly one piece at `/spec?c=<piece>&v=<variant>` on a flat
   `--page` background, with the site's toolbars and sound effects removed from
   `app/layout.tsx`.
4. `next dev -p 3917`, then [`tools/extract.mjs`](22-swiftui-clone-plan/tools/extract.mjs)
   with Playwright 1.61 (Chromium 149, WebKit 26.5).

### 4.2 How the extractor controls time

- **Viewport and scale:** 640 x 480 CSS px at `deviceScaleFactor: 2`, `reducedMotion:
  no-preference`.
- **JS clock:** `page.clock.install()` then `page.clock.pauseAt(T0)` *before* navigation, so
  every `setTimeout`/`setInterval`/`requestAnimationFrame`/`Date` in the page is frozen at the
  component's mount (t = 0). Time only moves through `page.clock.runFor()`.
- **CSS clock:** CSS animations and transitions run on the document timeline, which the fake
  clock does not touch. After each step the extractor calls a page function that pauses every
  `document.getAnimations()` entry and sets `currentTime = now − birth`, where `birth` is the
  virtual time the animation first appeared. Transitions (`CSSTransition`) are handled the same
  way, so a React state change at t = 1500 ms starts its transitions at exactly 1500.
- **Steps:** whole milliseconds on the 60 Hz grid (0, 17, 33, 50, ...), reading virtual time
  back from the page (`runFor` rounds fractional milliseconds, which first put every event one
  or three frames early). React commits between steps, so scripted sequences arm their next
  timer correctly; the first attempt with one long `runFor` fired only the first timer, which
  the checked-in screenshots would have shown as a stuck "Failed" row.
- **Settle:** at the piece's settle time every finite animation is finished; infinite loops
  are left paused at their phase.

### 4.3 What a spec file holds

`specs/<piece>.json`:

```text
piece, source, sourceSha, licence, viewport, deviceScaleFactor, frameMs, settleMs, variant
runs[8]: one per engine (chromium, webkit) x theme (light, dark) x font (inter, sf)
  engine, version, theme, font
  settled[]: every element under the piece, depth first
    path            "div/div[1]/button[0]/span[0]", the stable address used for mapping
    tag, cls        element and its Tailwind classes (the source of truth for intent)
    text            direct text, if any
    frame           [x, y, width, height] in CSS px relative to the piece's origin
    style           color, background, backgroundImage, fill, stroke, strokeWidth,
                    fontFamily, fontSize, fontWeight, lineHeight, letterSpacing,
                    fontVariantNumeric, padding, gap, radius, border, shadow, opacity,
                    transform, filter; every colour resolved to sRGB hex (#RRGGBB or
                    #RRGGBB@alpha) through a canvas, including colours inside shadows
                    and gradients
  animations[] (chromium, light, sf run only): every CSS animation and transition seen
    kind (CSSAnimation or CSSTransition), name, target path, bornAtMs, durationMs,
    delayMs, easing, iterations, fill, keyframes[{offset, easing, ...properties}]
  curves[] (same run): per 16.67 ms frame up to the piece's sampling window, for every
    animated element whose values changed since its last row:
    tMs, target, frame, opacity, transform (matrix), backgroundPosition
specs/<piece>/<engine>-<theme>-<font>.png   the settled piece at 2x
```

The two engines are both kept on purpose: where Chromium and WebKit disagree on a value (the
`Chip` height is 20 px in Chromium and 19.84 px in WebKit with Inter; its width is 82.63 and
84.78 px with SF Mono), that difference is the floor for "perfect" on that element, because no
single browser answer is more correct than the other. WebKit is the primary reference (it is
Apple's text stack); Chromium bounds the tolerance.

### 4.4 Extracting the rest

| Source | How |
| --- | --- |
| Beautiful UI pieces not yet extracted (`AgentScreen`, `ApprovalCard`, `StreamingText`, `CodeBlock`, `DiffTable`, `PromptBar`, `SearchList`) | Add a `case` to `tools/spec-page.tsx` and an entry to `PIECES` in `tools/extract.mjs`. |
| shadcn/ui (`command`, `kbd`, `sidebar`, `sonner`, `skeleton`, `tooltip`, `sheet`, `empty`, `resizable`) | `pnpm --filter=v4 dev` in a clone of `shadcn-ui/ui` at `98a1fe6` and point the extractor at `/docs/components/<name>` with a `#spec-root` wrapper injected around the first preview, or at a scratch page like Beautiful UI's. The live `ui.shadcn.com/docs/components/*` pages work too but move with every deploy; always pin the clone. |
| Sonner | Its own `website` app in the repo at `8e4662b`; extract a stack of three toasts, the enter, the hover expand and the dismiss. |
| cmdk | Behaviour only: export `command-score.ts` results for a fixed query list to JSON and use it as the golden file for the Swift port of the scorer (C19). |
| NumberFlow | Its `site` app at `c5906cf`; sample digit transforms and mask opacity over 900 ms for 1 to 2 and 9 to 10. |
| Motion springs | A 30-line node script that runs `spring({keyframes:[0,100], ...})` from `motion-dom` and prints `next(t)` every 16.67 ms for each spring the design uses; the JSON is the golden file for C06's Swift unit test. |

### 4.5 Local only, never in CI

Extraction needs node, npm, network and a browser build; it runs on a developer Mac when a
pinned source sha is bumped. The JSON and PNGs are checked in, so CI never runs a browser.
`node tools/extract.mjs --check` (to add) re-extracts into a temp directory and fails on any
difference from the checked-in spec, which is how an upstream change is noticed and reviewed.

## 5. Component inventory and clone specs

42 components, C01 to C42: 15 atoms, 4 rows and 23 containers and screen pieces. They cover
every piece on the Figma Components page (status glyph set, icons, buttons, toolbar button,
icon button, keycap, tool chip, run row, check row, palette row, Thinking, task row, filmstrip
thumb, evidence mark, evidence frame, and the states board), every further piece the 15 screens
use, and every Beautiful UI and shadcn piece the design names (Task Rows, Thinking, Tool Chips,
Loading, Shimmer, Stream Text, Chat Composer, Approval Card, Agent Screen, Command, Kbd,
Sidebar, Sonner, Skeleton, Tooltip, Empty, Resizable). Figma numbers come from the Tokens page
(exported in docs/20 as `tokens.png`; colour swatches sampled from it match the values docs/20
quotes, for example accent `#2156D9`, pass `#157034`) and the component states board
(`component-states.png`); every source number is from the extracted JSON or the file and
commit named. "Spec" names the checked-in file when one exists.

### 5.1 Foundation tokens (Figma, the values every component reads)

Colour (light / dark / wireframe), sampled from the Figma Tokens page and cross-checked with
the Figma variables the Principles frame binds (`text #18181B`, `text-secondary #5F5F68`,
`accent #2156D9`, `border #E4E4E8`, `bg #FFFFFF`):

| Token | Light | Dark | Wireframe | Used for |
| --- | --- | --- | --- | --- |
| bg | `#FFFFFF` | `#1B1B1E` | `#FFFFFF` | Window, run pane |
| bg-sidebar | `#F7F7F8` | `#151517` | `#F4F4F4` | Runs sidebar |
| bg-stage | `#F2F2F4` | `#0F0F11` | `#E9E9E9` | Picture area |
| bg-selected | `#EAEAEE` | `#2C2C31` | `#DCDCDC` | Selected row, placeholders |
| bg-hover | `#F0F0F2` | `#242428` | `#EFEFEF` | Hover, keycaps, chips |
| bg-raised | `#FFFFFF` | `#232327` | `#FFFFFF` | Palette, sheet, secondary button |
| border | `#E4E4E8` | `#2E2E33` | `#CFCFCF` | 1 px hairlines between regions |
| text | `#18181B` | `#EDEDF0` | `#1F1F1F` | Primary text |
| text-secondary | `#5F5F68` | `#A0A0AA` | `#6B6B6B` | Meta, captions, placeholders |
| text-tertiary | `#8E8E96` | `#6E6E78` | `#9A9A9A` | Disabled text only |
| accent | `#2156D9` | `#6B9BF2` | `#4A4A4A` | Live, checking, focus, links |
| pass | `#157034` | `#4CC47A` | `#4A4A4A` | Passed glyph, pass meta |
| fail | `#C21F1F` | `#F26B6B` | `#1F1F1F` | Failed glyph, saw value, mark |
| wait | `#A34B05` | `#E0A43A` | `#6B6B6B` | Paused, warnings |
| primary | `#18181B` | `#EDEDF0` | `#1F1F1F` | Primary button fill |
| focus-ring | `#2156D9` | `#6B9BF2` | `#4A4A4A` | 2 px ring, 2 px offset |
| scrim | `#000000` | `#000000` | `#000000` | Letterbox, overlays at 18% |

Type (Figma text styles, Inter in the file, SF Pro in the app): Display 22/28 Semibold −1.8%;
Title 15/20 Semibold −1%; Body 13/18 Regular −0.5%; Body Emphasis 13/18 Medium −0.5%; Caption
11/14 Regular; Caption Emphasis 11/14 Semibold 0. Tabular figures on every time and count.

Spacing: 4, 8, 12, 16, 24, 32, 48 (8 pt grid, 4 for hairline gaps). Layout: toolbar 52, run
header 80, run row 32, check row 40 and up, sidebar 248 (208 compact, 272 wide), checks column
400 (320 compact, 440 wide), stage padding 32 (24 compact). Radius: 4 keycap and thumb; 6
buttons, run rows, marks; 8 check rows, frames, inputs; 10 window; 12 palette and sheet.
Elevation: flat inside the window (regions split by 1 px borders); raised (palette, sheet,
restart progress) `0 24 64` at 22%; window `0 16 48` at 16% plus `0 2 6` at 8%. Focus: 2 px
ring outside a 2 px gap, keyboard only. Hit areas at least 28 tall inside a 44 target.

Motion (Figma): only state changes move; 120 ms press and hover, 180 ms row settle and panel
slide, 240 ms verdict landing, all `cubic-bezier(0.23, 1, 0.32, 1)`; press scale 0.97; enter
from scale 0.96 and opacity 0; 2 px blur over a content swap; checking ring 800 ms linear;
Thinking shimmer 1.6 s; numbers roll with NumberFlow; everything stops under reduce motion.

SwiftUI: one `Tokens.swift` generated from `design/tokens.json` (rewritten to these values,
docs/20 phase 8) with `Color(.sRGB, ...)`, one `TextStyle` enum carrying size, weight,
tracking and line height (section 3.5), one `Motion` enum carrying the curves as
`Animation` values and as `UnitCurve`s for the harness.

### 5.2 Beautiful UI tokens (the originals, for reading the specs)

From `app/globals.css` at `44a274e`, converted with `tools/tokens.py` (identical to the
browser's own conversion):

| Token | Light | Dark | | Token | Light | Dark |
| --- | --- | --- | --- | --- | --- | --- |
| page | `#FAFAFB` | `#17181A` | | accent | `#0285FF` | `#3D9AFF` |
| canvas | `#F1F2F3` | `#1C1D1F` | | accent-ink | `#0070DD` | `#7EC0FF` |
| surface | `#FFFFFF` | `#232427` | | accent-tint | `#E9F3FF` | `#3D9AFF` at 16% |
| inset | `#F7F8F9` | `#1F2022` | | green | `#199A4D` | `#3CBB72` |
| hover | `#F4F5F6` | `#2A2B2E` | | green-tint | `#E8F5ED` | `#3CBB72` at 14% |
| hover-2 | `#E7E9EB` | `#313236` | | orange | `#EF720D` | `#F68F3C` |
| ink | `#1F2124` | `#F2F3F4` | | orange-tint | `#FDF1E5` | `#F68F3C` at 14% |
| ink-2 | `#62656B` | `#A5A8AD` | | red | `#E3474C` | `#EE5C61` |
| ink-3 | `#9A9DA3` | `#6C6F75` | | red-tint | `#FCECEC` | `#EE5C61` at 14% |
| line | `#ECEDEF` | `#2E3033` | | field | `#F2F2F3` | `#2B2C2F` |
| line-strong | `#E0E2E5` | `#3A3C40` | | line-soft | `#F3F4F5` | `#27282B` |

Radii: chip 6, control 8, card 10, window 14, pill 999. Curves: `--ease-out-strong
(0.23, 1, 0.32, 1)`, `--ease-in-out-strong (0.77, 0, 0.175, 1)`, `--ease-link (0.16, 1, 0.3,
1)`. Body 14 px, line height 1.5, letter spacing −0.01em, features `cv11`, `ss01`.
Shadows (light): `shadow-card = 0 0 0 1px line` + shadow-plugin `--shadow-sm` (six layers:
`0 18 47 /3%, 0 7.5 19 /2%, 0 4 10.5 /2%, 0 2.3 5.8 /1%, 0 1.2 3.1 /1%, 0 0.5 1.3 /1%`);
`shadow-btn = 0 0 0 1px line-strong` + `0 0 4px /4%`; `shadow-overlay = ring` + `--shadow-lg`
(`0 25 50 /5%, 0 12 24 /4%, 0 6 12 /3%, 0 3 6 /2%, 0 1.5 3 /2%`). Dark: rings become white at
10 to 15% and shadows single black layers (`shadow-card`: ring 11%, `0 1 2 /20%`,
`0 2 6 /20%`). Shared keyframes: `fade-up` (opacity 0 to 1, translateY 8 to 0), `pop-in`
(opacity 0 to 1, scale 0.95 to 1), `fade-in`, `spin` (0 to 360 deg), `shimmer-text`
(background-position 150% to −50%), `pixel-on` (see C09), `caret-blink` (step-end).

**Mapping onto our tokens:** a Beautiful UI piece keeps its geometry and motion but takes
Greenroom's colours: `ink → text`, `ink-2 → text-secondary`, `ink-3 → text-tertiary` (only
where Figma also uses tertiary; captions use secondary), `surface → bg-raised`,
`inset/hover → bg-hover`, `hover-2 → bg-selected`, `line → border`, `accent → accent`,
`green → pass`, `red → fail`, `orange → wait`. Tints are the status colour at 10% light and
14% dark on `bg` (Figma draws the `2 failed` meta as text, not a pill, so tints are rare).

### 5.3 The components

Legend for "Port": the SwiftUI and AppKit APIs that reproduce it. "Gap": what is known not to
match and the plan for it. A Figma number marked **(m)** was read off the mockup and states
board PNG exports, not from node metadata (the MCP could not open those pages in this session,
section 7); it is the working value and must be confirmed from the node's metadata in PR 2b
before that component is signed off. Unmarked Figma numbers come from the Tokens page. All motion stops under `accessibilityReduceMotion`, leaving the end
state (Beautiful UI's rule: `animation-duration: 0.01ms`).

#### Atoms

**C01 Status glyph set** (Figma Components: Status glyph set; used by run row, check row,
task row, header). Glyphs: pending (empty circle), checking (turning ring), passed (filled
circle, white check), failed (filled circle, white cross), paused, warning (triangle, `wait`),
stopped/error (filled circle, square), not answering. Figma sizes: 16 pt in rows, 20 pt in the
header next to Display **(m)**. Source for the filled badge: Beautiful UI `TaskRows.tsx` `Badge`
(`size-5.5` = 22 px circle, white 12 to 13 px Lucide `x`/`check` at stroke 3.5, enters with
`pop-in 300ms cubic-bezier(0.23,1,0.32,1)`: opacity 0 to 1, scale 0.95 to 1; spec
`task-rows.json`, animation target `.../button[0]/span[0]/span[0]` born at 0 and at 3900 ms).
Port: `Circle().fill(token)` with a Lucide path (C05) in white at stroke `3.5 × size/24`;
enter as `.scaleEffect(0.95→1).opacity(0→1)` with `.timingCurve(0.23, 1, 0.32, 1, duration:
0.3)`; Figma override: enter from 0.96 over 180 ms (row settle). Gap: none.

**C02 Checking ring** (glyph "checking"; the header's "Checking" state; running task row).
Source: Beautiful UI `SpinnerRing` in `TaskRows.tsx`: 24 px, stroke 2, track `line`, arc
`ink-3` covering 28% of the circumference (`strokeDasharray c*0.28 c*0.72`), round caps,
`spin 1.1s linear infinite`, optional centred number 10.5 px semibold tabular. Figma override:
16 pt **(m)**, arc in `accent`, 800 ms per turn. Port: `Circle().trim(from: 0, to: 0.28)
.stroke(style: StrokeStyle(lineWidth: 2 × 16/24, lineCap: .round))` over a track circle,
rotated by `MotionClock` phase `(t mod 0.8)/0.8 × 360°` (section 3.2). Note SVG circles start
at 3 o'clock like SwiftUI's `trim`, so no rotation offset is needed; the static
`-rotate-90` on `ProgressRing` (C03) is the one exception. Gap: none.

**C03 Progress ring** (restart progress, 07b; optional tally ring). Source: `ProgressRing.tsx`
(28 px default, stroke 2, track `line`, tone arc from 12 o'clock, `stroke-dashoffset` transition
400 ms `(0.23, 1, 0.32, 1)`, centred 12 px semibold tabular); spec `progress-ring.json`. Port:
`Circle().trim(from: 0, to: progress)` rotated −90°, animated with the same curve. Gap: none.

**C04 Keycap** (Figma: Keycap; palette rows, tooltips, settings). Source: shadcn `kbd.tsx`:
`h-5 min-w-5 px-1 gap-1 rounded-sm bg-muted text-xs font-medium text-muted-foreground`, i.e.
20 pt tall, at least 20 wide, 4 pt side padding, 12 pt medium. Figma override: radius 4,
`bg-hover` fill, `text-secondary`, Caption size 11 **(m)**. Glyph text from KeyboardShortcuts
`Shortcut.swift` (`⌘`, `⇧`, `⌥`, `⌃`, `↩`, `⌫`, `⎋`). Port: `Text` with `.monospacedDigit()`,
`.frame(minWidth: 20, minHeight: 20)`, `RoundedRectangle(cornerRadius: 4, style: .circular)`.
Gap: none.

**C05 Icons** (Figma: Icons; Lucide everywhere in the file). Source: `lucide-icons/lucide` at
`66d8f9f`, ISC. Port: a build-time script converts the ~20 SVGs the design uses (list, message
square, more horizontal, search, sliders, monitor, video, eye, mouse pointer, camera, pointer,
chevron down/right, check, x, rotate ccw, play, pause, terminal, file) into SwiftUI `Shape`s
(SVG path to `Path` on a 24-unit viewBox), stroked at `2 × size/24` with round caps and joins.
SF Symbols were the alternative; they are the right native choice but they are not what Figma
draws, so they would fail the Figma comparison. Gap: none once converted.

**C06 Motion primitives** (not drawn; used by everything). `Motion.swift` holds: `outStrong =
UnitCurve.bezier((0.23, 1), (0.32, 1))`, `inOutStrong (0.77, 0, 0.175, 1)`, `link (0.16, 1,
0.3, 1)`, `tailwindDefault (0.4, 0, 0.2, 1)`, `cssEase (0.25, 0.1, 0.25, 1)`, `cssEaseOut (0,
0, 0.58, 1)`, `cssEaseInOut (0.42, 0, 0.58, 1)`; durations `press 0.12`, `settle 0.18`,
`verdict 0.24`; `MotionClock` (an environment value with `now()` and a `TimelineView` schedule
the harness can freeze); `CSSKeyframes<Value>` (offsets, values, per-segment curve) with a pure
`value(at:)`; `SpringFromMotion` (section 3.3, including a Swift `findSpring`); `TableCurve:
CustomAnimation` for `linear()`. Unit tests compare `value(at:)` for each against the golden
JSON from section 4.4 within 0.001.

**C07 Buttons** (Figma: 3 kinds x 6 states: primary, secondary, plain; default, hover, pressed,
focused, disabled, loading). Source: Beautiful UI `Button.tsx`, spec `button.json`: sizes `xs`
28 tall, px 10, 12 px regular; `sm` 27 tall, px 12, 13 px; `md` padding 9 x 16, 14 px; all
pill (`rounded-full`); press `scale 0.96`; transition `transform, background-color, opacity`
150 ms ease-out; disabled 50% opacity; primary fill `ink` with `inset 0 1px 0 white/14%`;
secondary `surface` with `shadow-btn`. Figma overrides (win): radius 6 (not pill), height 28
(toolbar) and 32 (header actions, "Accept fail") **(m)**, Body Emphasis 13/18, press scale 0.97 over
120 ms `(0.23, 1, 0.32, 1)`, primary `primary` fill with `bg` text, secondary `bg-raised` with a
1 px `border`, focus ring 2 px `focus-ring` at 2 px offset. Loading: the checking ring (C02)
replaces the leading icon, label unchanged ("Sending" in the composer). Port: a `ButtonStyle`
per kind reading `configuration.isPressed`, `@Environment(\.isEnabled)`, `@FocusState`
(`.focusEffectDisabled()` then our own ring), hover through `.onHover`. The window-not-key
case (harness) draws the same, because our style does not use system prominence. Gap: none.

**C08 Shimmer text** (Figma: Thinking; the header "Checking" word while working; the Now line;
the boot label). Source: `Shimmer.tsx` and the inline shimmer in `ThinkingState.tsx` and
`LoadingState.tsx`: `background-clip: text`, `linear-gradient(90deg, ink-3 35%, ink 50%, ink-3
65%)`, `background-size: 200% 100%`, `shimmer-text` 1.4 s (1.8 s in `Shimmer.tsx`) linear
infinite, `background-position` 150% to −50%; spec `shimmer.json`. Maths: with text width W the
gradient image is 2W wide; `background-position-x p` puts its left edge at `(W − 2W)·p = −W·p`,
so the left edge travels from −1.5W to +0.5W and the bright centre from −0.5W to 1.5W, linearly.
Figma override: period 1.6 s, colours `text-secondary` to `text`. Port: `Text(...)
.foregroundStyle(.clear)` masked: `LinearGradient(stops: [(ts, 0), (ts, 0.35), (t, 0.5), (ts,
0.65), (ts, 1)])` framed `2W` wide and offset by `x = −W·p(t)`, `p(t) = 1.5 − 2·phase`, used as
`.foregroundStyle` via `.overlay { gradient.mask(Text(...)) }`. W from `onGeometryChange`.
Inferno's `Shimmer.metal` is the GPU fallback. Gap: SwiftUI gradient interpolation between two
greys matches sRGB interpolation to within 1 level (measure in the harness).

**C09 Pixel-grid loader** (boot, screen 06; "Loading" state). Source: `LoadingState.tsx`
"Drive": a 3 x 3 grid of 4 px cells, 1.5 px gap, radius 1, `ink`; each cell runs `pixel-on
650ms ease-in-out <delay> infinite` with keyframes opacity `0%: 0.15, 18%: 1, 42%: 1, 62%: 0.15,
100%: 0.15` (ease-in-out **per segment**), delays `(column + |row − 1|) × 90 ms`, a chevron
wave driving right (rows top to bottom: 90, 180, 270 / 0, 90, 180 / 90, 180, 270 ms; the
extractor found exactly one cell at 0, three at 90, three at 180, two at 270); label
shimmer (C08) 13 px medium and elapsed time 12 px mono `ink-3` tabular (`12.3s`, then `1m
4.0s`), gap 10; spec `loading-state.json`. Port: `Grid` of `RoundedRectangle(cornerRadius: 1)`
whose opacity is `CSSKeyframes.value(at: (t − delay) mod 0.65)`. Figma override: the boot
screen shows phases as text with times; use this loader only as the inline working mark. Gap:
none.

**C10 Rolling number** (header tally "2 of 4", elapsed "4:18", Figma Motion). Source:
NumberFlow `lite.ts` at `c5906cf`: digit transform 900 ms with the `linear()` spring table (91
points, listed in the file), opacity 450 ms `ease-out`, `trend = sign(new − old)` decides roll
direction, mask 0.25em tall fade top and bottom and 0.5em wide at the ends. Port: each digit a
`VStack` of 0 to 9 clipped to one line and offset by `−digit × lineHeight`, animated with
`TableCurve(NumberFlowTable)` over 0.9 s; entering and leaving digits fade with
`cssEaseOut 0.45 s`; mask with a vertical `LinearGradient` of height `0.25em`. Fallback while
building: `.contentTransition(.numericText(value:))` (not a clone: Apple's curve and blur).
Gap: NumberFlow also animates width changes of the whole number with the same curve; do it
with `.animation(TableCurve, value: text)` on the frame.

**C11 Tool chip** (Figma: Tool chip; running, done, error; used in task rows and Activity).
Source: `ToolChips.tsx`, spec `tool-chips.json`: chip `h-5.5` (22 px), `px-1.5`, radius 6
(`rounded-chip`), fill `field`, 11.5 px `ink-2` (mono for file and command chips),
`shadow-hairline` ring; row `h-7` (28 px), gap 8, icon slot 16 with a 13 px Lucide icon in
`ink-3` that swaps to a chevron on hover (opacity 100 ms, chevron rotate −90° to 0 over 150
ms), label 12.5 px medium `ink`; rows appear every 700 ms with `fade-up 300ms (0.23, 1, 0.32,
1)`; expanded detail: left border 1 px `line`, `ml-2 pl-3.5`, 11.5/1.6 lines, reveal 300 ms
grid-rows plus opacity. Diff chips `h-7` px 8 mono 11.5 `shadow-btn`, `pop-in 250ms` staggered
80 ms; hover preview 288 px wide, radius 10, `shadow-overlay`, `pop-in 160ms`, 11/1.8 mono lines
with `green-tint`/`red-tint` rows. Figma override **(m)**: the chip carries a tool glyph, a label and a
time (`Click 25% 0.4s`), Caption 11, `bg-hover` fill, radius 6, running state shows C02 at 12
pt, error state `fail` text and a `fail` 1 px border on a 10% `fail` tint. Port: `HStack`
chips in a `FlowLayout` (a `Layout` that wraps, as ShadKit's `WrapLayout.swift`). Gap: none.

**C12 Stream text and caret** (Activity: the verifier's words arriving). Source:
`StreamText.tsx`, spec `stream-text.json`: 2 characters every 9 ms, last 6 characters blurred
`1.6px` under a left-to-right mask `black 20% → black/20%`, caret 2 x 1.05em, radius 1, `ink`,
1.5 margin, nudged −0.5 px, solid while streaming then `caret-blink 1s step-end infinite`.
Port: `Text` concatenation of the settled prefix and a tail `Text` with `.blur(radius: 1.6)`
masked by a gradient; caret a `RoundedRectangle` with `KeyframeAnimator` jumps. Gap: SwiftUI
`blur(radius:)` is not guaranteed to be the same Gaussian as CSS `blur(σ)`; calibrate the
radius once against the spec frames (expected within ±0.3).

**C13 Status pill** (not used on screens by the Figma "badge-free status" rule; kept for the
resource banner and Settings). Source: `StatusPill.tsx`, spec `status-pill.json`: 24 tall,
px 10, gap 6, 13 px medium `leading-none`, capsule, tint fill and tone text, 6 px dot. Port:
`HStack(spacing: 6)` in a `Capsule`. Gap: none.

**C14 Segmented control** (Settings, 14; the Activity filter if added). Source:
`SegmentedControl.tsx`, spec `segmented-control.json`: 32 tall, 2 px inset track `line` at
60%, thumb `surface` with hairline ring, slides with `translateX` 200 ms `(0.23, 1, 0.32,
1)`, labels 13 medium `ink` / `ink-3`. Port: `matchedGeometryEffect` thumb or an offset by
index; not the system `Picker(.segmented)` (it cannot match). Gap: none.

**C15 Mono chip** (inline code in Activity prose). Source: `Chip.tsx`, spec `chip.json`: 12
px mono, `rounded-md` (6), px 6 py 2, `inset` fill, `ink-2`. Port: inline `Text` with
background is not possible in SwiftUI `Text` concatenation; draw in the Markdown renderer as
an attributed run with `backgroundColor` (no padding) or as a separate view in a flow layout.
Gap: padding around an inline run inside wrapped text is not available in SwiftUI `Text`;
accept a background without horizontal padding, or render code spans as views when the
paragraph has no wrap.

#### Rows

**C16 Run row** (Figma: Run row; default, hover, selected, focused, loading, empty group,
error "could not start"). Figma: 32 tall, radius 6, padding 8 horizontal **(m)**, glyph 16 (C01),
gap 8 **(m)**, name Body 13 (Body Emphasis when selected), trailing meta Caption 11
`text-secondary` right-aligned tabular (`2h`, `1:12`) or `fail` text (`2 failed`, `no Mac`);
hover `bg-hover`, selected `bg-selected`, focused selected plus the 2 px focus ring; loading a
16 pt placeholder circle and a 10 pt tall capsule bar in `bg-selected` **(m)**. Group heading
Caption Emphasis 11 `text-secondary`, sentence case, 24 above **(m)**. Source for structure: shadcn
`sidebar.tsx` (`SidebarMenuButton`: `h-8`, `rounded-md`, `px-2`, `gap-2`, `text-sm`) and
NetNewsWire `SidebarCellLayout.swift` (how a trailing count keeps its width while the name
truncates). Port: SwiftUI `List(selection:)` with `.listStyle(.sidebar)` for keyboard and
accessibility, `.listRowInsets(EdgeInsets())`, custom row background via
`.listRowBackground` with our selected colour, NSTableView row height and the system
selection highlight turned off through an introspect-style hook
(`selectionHighlightStyle = .none`, `rowHeight = 32`, `intercellSpacing = .zero`). Motion:
row settles (status change) crossfade 180 ms; keyboard moves never animate. Gap: the sidebar
List's built-in 10 pt leading inset must be removed by the hook; check the snapshot at 1x.

**C17 Check row** (Figma: Check row; pending, checking, passed, failed, selected with the
repro line, focused, error "couldn't check"). Figma: min 40 tall, radius 8, padding 12 x 12,
glyph 16, gap 12 **(m)**, check text Body 13 up to two lines, trailing value right-aligned Body 13:
`$24.00` in `text-secondary`, `saw $10.00` in `fail`, `checking` in `accent`, `couldn't check`
in `wait`; selected: `bg-selected` and a second line (the repro, Body 13 `text-secondary`);
column 400 wide with 12 inset **(m)**. Source for the state change motion: Beautiful UI `TaskRows`
(badge `pop-in`, meta `fade-in 200ms ease-out`), with Figma's 180 ms settle. Port: `VStack`
rows in a `ScrollView` (a check list is under 20 rows), selection by `@FocusState` and J/K.
Gap: none.

**C18 Task row** (Figma: Task row; collapsed, running, failed opened by itself). Source:
`TaskRows.tsx`, spec `task-rows.json`: capsule rows 44 tall (`h-11`), px 10, gap 10, badge
slot 24, label 13 medium, amount 12.5 `ink-2` tabular, status pill 22 tall 11.5 medium,
chevron 15 px in a 28 slot rotating 0 to 180° (300 ms `(0.4, 0, 0.2, 1)`); the row's radius
morphs 22 to 14 when open (300 ms, same curve, extracted); rows enter `fade-up 450ms` staggered
80 ms; the detail reveal is grid-rows 0fr to 1fr plus opacity over 300 ms `(0.23, 1, 0.32, 1)`
with a 1 px `line` rail in a 24 px column and detail lines `fade-up 300ms` delayed `120 + j ×
100` ms; the scripted demo as extracted: t 0 the three rows enter (delays 0, 80, 160) with
the done badge's `pop-in` and the running ring's 1.1 s spin; 1500 row 2 opens (radius, chevron,
reveal, detail lines at +120 and +220); 3900 row 2 closes and row 3 fails (badge `pop-in`, pill
`fade-in 200ms ease-out` with a retry icon spinning 1.2 s); 5300 row 3 turns Completed **with no
animation at all** (React reuses the badge and pill elements, so their CSS animations do not
restart: the colour and icon swap in one frame). A clone must reproduce that instant swap, or
the difference must be a written Figma override (Figma's 180 ms settle applies, so here the
override wins and the spec records it). Figma override **(m)**: rows are flat list rows (no capsule, no shadow) in the
Activity timeline: 36 tall, chevron 12 leading, glyph 16, Body 13, trailing time Caption
tabular; the opened detail holds tool chips (C11) indented 32. The Beautiful UI "List"
variant is the right base (`rounded-card` container, rows split by 1 px `line`). Port: as C17
plus the section 3.6 reveal. Gap: none.

**C19 Palette row and palette** (Figma: Palette row; default, selected, disabled with reason,
empty; screen 12). Figma: palette 560 wide **(m)**, radius 12, `bg-raised`, raised elevation (`0 24 64`
22%), search field 48 tall Body 15 (Title size, regular) **(m)**, rows 36 tall **(m)** radius 6, icon 16, Body
13, keycaps (C04) trailing; selected `bg-selected`; disabled `text-tertiary` with the reason in
a tooltip; empty: centred two lines. Sources: shadcn `command.tsx` (input 36 tall (`h-9`)
bordered bottom, list max 300, group heading 12 px medium muted `px-2 py-1.5`, item `px-2 py-1.5
gap-2 rounded-sm text-sm`, selected `bg-accent`, shortcut `ml-auto text-xs
tracking-widest`), cmdk `command-score.ts` for ranking (port verbatim, golden file from 4.4),
Ghostty `CommandPalette.swift` for focus and key handling on macOS, ShadKit
`ShadcnCommandKeyboardSelection` for driving the highlight from the field. Motion: opens with
scale 0.96 to 1 and opacity 0 to 1 over 180 ms `(0.23, 1, 0.32, 1)` (Figma enter rule),
closes 120 ms; the list does not animate while typing (Figma rule 9). Port: an overlay panel
in the window (not a sheet, not a new `NSPanel`) so the harness captures it; `TextField` with
`.onKeyPress(.upArrow/.downArrow/.return/.escape)`. Gap: none.

#### Containers and screens

**C20 Window chrome and toolbar** (all screens). Figma: window radius 10, window elevation;
unified toolbar 52 tall with the run title Body Emphasis and the source Body
`text-secondary`, trailing toolbar buttons (C07 plain with icon) `Activity`, `Message`, `...`;
the sidebar's top holds the traffic lights and a search icon. Port: `.windowStyle(.hiddenTitleBar)`
or `.windowToolbarStyle(.unified(showsTitle: false))`, `.toolbar { ToolbarItem(...) }` with
our button style, `.toolbarBackgroundVisibility(.hidden, for: .windowToolbar)` (macOS 15) and our
own 1 px `border` under the toolbar, `.windowBackgroundDragBehavior(.enabled)` (macOS 15),
`NavigationSplitView` for the sidebar split so the traffic lights sit over the sidebar.
The Figma sidebar is opaque `bg-sidebar`, so no `NSVisualEffectView` vibrancy (proposal 9.6
keeps vibrancy as an option). Gap: the system decides the traffic light inset (20 from the
left, centred in 52); Figma matches it. The window corner radius is the system's (10 on macOS
15, larger on macOS 26), not settable; Figma draws 10, so compare window contents only.

**C21 Toast** ("verdict ready" in another run). Source: Sonner at `8e4662b`: width 356,
gap 14 between stacked toasts, 3 visible, lifetime 4000 ms, viewport offset 24, radius 8,
padding 16, 13 px, `box-shadow 0 4px 12px rgba(0,0,0,.1)`; enter from `translateY(100%)`
(bottom) and opacity 0 over 400 ms (`ease`), stacked toasts behind the front scale by
`1 − 0.05 × index` and lift by `gap × index`, the stack expands on hover, height changes 400 ms,
swipe to dismiss past 45 px or velocity 0.11, unmount 200 ms after close. Figma override:
colours `bg-raised`, `border` 1 px, radius 12 (raised), Body 13, raised elevation. Port: an
overlay `ZStack` bottom-trailing in the window, each toast offset and scaled from its index,
`.timingCurve(0.25, 0.1, 0.25, 1, duration: 0.4)` (CSS `ease`), `DragGesture` for swipe. Gap:
none.

**C22 Run header** (screens 02 to 09). Figma: 80 tall; status glyph 20 **(m)** (C01/C02) and status word
Display 22/28 (`Failed`, `Checking`); tally Title 15/20 `text-secondary` rolling (C10); second
line Body 13 `text-secondary` ("Proposed by the verifier after 3:26", or the Now line with the
shimmer, C08); one primary button top right (C07), an optional secondary to its left; 1 px
`border` below. Motion: verdict landing 240 ms (status word and glyph crossfade with a 2 px blur
on the outgoing word, Pow `Blur.swift` technique, then glyph `pop-in`). Port: `HStack` with
`.contentTransition(.opacity)` on the word plus our blur transition. Gap: none.

**C23 Checks column** (screens 02 to 04). Figma: 400 wide (320 compact, 440 wide), "Checks"
Caption Emphasis heading, rows C17, first failure selected on open. Port: fixed-width column
in an `HStack` (not a user-resizable split; Figma fixes it). Gap: none.

**C24 Evidence frame and mark** (Figma: evidence frame loaded, loading, error; evidence mark).
Figma: frame radius 8 on `bg-stage`, stage padding 32, the guest image aspect-fit; the mark a
2 px **(m)** `fail` (or `pass`) rounded rectangle, radius 6, around the region; caption under the frame
"Expected $50.00, saw $10.00" Title 15 **(m)** with the values Semibold and `fail`; loading: C02 and
"Loading frame" Caption; error: "This frame is missing" Body, "The recording still has it"
Caption, a secondary button. Port: the existing `EvidenceMarks.swift` geometry (mark rects in
image coordinates mapped through the aspect-fit transform), restyled. Gap: none.

**C25 Filmstrip** (Figma: filmstrip thumb). Figma **(m)**: thumbs 66 x 48, radius 4, 8 apart, the
selected thumb with a 2 px `accent` ring and the failing frame with a 2 px `fail` underline 4
below. Port: `ScrollView(.horizontal)` of `Image` thumbs; arrows move frames. Gap: none.

**C26 Live screen** (screens 02 and 09). Figma: the guest screen in the stage, click ring
(Screen Studio style) on each click. Source: Beautiful UI `AgentScreen.tsx` for the cursor
overlay (`drop-shadow(0 1px 1.5px rgba(0,0,0,.35))`) and the existing `LiveScreenView.swift`
(frames into a `CALayer`). Port: keep the layer; for H.264 frames an
`AVSampleBufferDisplayLayer` via `NSViewRepresentable`; click ring a `Circle` stroke that
scales 0.6 to 1.4 and fades over 400 ms (spec to extract from Screen Studio-like reference is
not available as code; Figma frame is the spec). Gap: the click ring has no open-source origin;
Figma is the only truth.

**C27 Take control bar** (screen 09). Figma: full-bleed screen, a bar with "You have control"
and "Give control back" (primary). Port: overlay bar on the live screen. Gap: none.

**C28 Header states: Paused, Not answering, Restarting** (05, 07a, 07b). Figma: header state
words with one sentence and at most one primary; 07b shows phases over the dimmed last picture
(scrim 18%) with the progress ring (C03). Port: C22 variants; the dim is a `Color.black
.opacity(0.18)` overlay. Gap: none.

**C29 Resource banner** (08). Figma: one amber line under the header, dismissible: `wait`
glyph, Body 13, a plain "Restart the Mac" button, a close icon. Source for the pattern: Kibo UI
Banner (MIT, `shadcnblocks/kibo`). Port: `HStack` with `wait` at 10% fill, 1 px `border`
bottom; enter by height reveal (section 3.6) 180 ms. Gap: none.

**C30 Activity timeline** (10). Figma: steps grouped by check (task rows C18 with tool chips
C11), Thinking (C31) at the head while working, "Raw logs" link. Source for the grouping:
Beautiful UI `ToolChips` header ("4 tool calls, 2 messages" 12.5 `ink-2` with a 12 px
chevron rotating −90° to 0 over 200 ms). Port: `List` (not `LazyVStack`, section 2.4) in the
inspector column (proposal 9.3) or a sheet as drawn. Gap: none.

**C31 Thinking** (Figma: Thinking). Source: `ThinkingState.tsx` "Steps", spec
`thinking-state.json`: header button `px-1.5 py-1` radius 8, hover `hover-2` over 100 ms,
16 px sparkle glyph `ink-2` while working and `ink-3` after, the shimmer label (C08, 13 medium,
1.4 s) that becomes "Thought for 4 seconds" in `ink-2` with `fade-in 350ms ease-out`, a 14 px
chevron rotating 180° over 300 ms; the trace: `ml-[5px] pl-4` with a 1 px `line` rail whose
height follows the content (500 ms `(0.23, 1, 0.32, 1)`), rows `min-h-7` px 6 radius 6, a 12
px spinner (1.5 px border, `line-strong` with an `ink-2` top, 700 ms linear) for the running
step and a 14 px check in `ink-3` for done ones, rows `fade-up 320ms` staggered 120 ms; the
whole block reserves `min-height 176` while working (400 ms). Script as extracted: t 0 shimmer
starts and the rail's height transition (500 ms) is armed; 800 auto-expand (grid-rows and
opacity 400 ms `(0.23, 1, 0.32, 1)`, chevron 300 ms `(0.4, 0, 0.2, 1)`); 1400 two rows (`fade-up
320ms`, delays 0 and 120) and the 700 ms spinner; 3200 settled: the label swaps to "Thought for
4 seconds" (`fade-in 350ms ease-out`), rows 3 and 4 enter (delays 240, 360); 5800 collapse (400
ms). Figma override: 1.6 s shimmer,
Greenroom tokens, the header word is the Now text ("Reading Each pays"). Port: C08 + C18's
reveal; the rail height from `onGeometryChange`. Gap: none.

**C32 Composer** (Figma: Composer; empty with Send disabled, focused typing, sending, disabled
with reason). Figma: field 36 tall **(m)** radius 8, 1 px `border`, Body 13, placeholder
`text-secondary`, Send (C07 primary, small) inside at the trailing edge, focused ring 2 px
`focus-ring`, sending shows C02 and "Sending", disabled text explains why. Source: Beautiful UI
`ChatComposer.tsx`, spec `chat-composer.json` (field `rounded-control` 8, `border line`, `bg
field`, `p-2.5`, 13/1.4, shadow `0 1px 2px rgba(0,0,0,.035)`, focus `border line-strong` 150
ms, send button 28 square radius 8); exyte Chat for the growing field. Port: `TextField(axis:
.vertical)` with `.lineLimit(1...6)`, `.textFieldStyle(.plain)`, our background and ring. Gap:
the insertion caret is the system's (colour via `.tint`), not drawable.

**C33 Evidence viewer** (11). Figma: large marked frame (C24), captioned key frames, "Play
recording". Port: a window-level overlay or sheet with `AVPlayerView` (`controlsStyle =
.inline`) through `NSViewRepresentable` for the recording (Kibo Video Player is the web
reference). Gap: `AVPlayerView` controls are the system's; Figma draws a plain "Play
recording" button that opens it, which matches.

**C34 Empty and offline states** (13a, 13b). Figma: centred, one title Title 15, one line Body
`text-secondary`, a copyable command in a mono field with a copy button (Kibo Snippet). Source:
shadcn `empty.tsx` (`gap-6`, header `max-w-sm gap-2`, title `text-lg font-medium
tracking-tight`, description `text-sm/relaxed muted`). Port: custom view. `ContentUnavailableView`
(macOS 14) is structurally right but draws `.title2` bold and SF Symbols at its own size, so it
fails the Figma comparison; not used. Gap: none.

**C35 Settings sheet** (14). Figma: sheet radius 12, raised, sections with Caption Emphasis
headings, rows 36 tall **(m)**, switches, keycaps. Sources: Luminare (BSD-3) section layout, Beautiful UI
`Switch.tsx` for the toggle motion (to extract). Port: `.sheet` with our background; the
system `Toggle(.switch)` differs from Figma's switch in size, so a custom `ToggleStyle`. Gap:
sheet corner radius and shadow on macOS 15 are the system's; Figma's 12 is close; accept the
system chrome and compare contents only.

**C36 Sidebar footer** ("2 of 3 Macs free", settings icon). Figma **(m)**: 40 tall, 1 px top
border, monitor icon 14, Caption 11 `text-secondary`. Source: XcodesApp `BottomStatusBar.swift`
pattern. Port: `.safeAreaInset(edge: .bottom)` on the sidebar list. Gap: none.

**C37 Tooltip** (disabled palette rows, toolbar buttons). Source: shadcn `tooltip.tsx`
(`bg-foreground text-background text-xs rounded-md px-3 py-1.5`, fade and zoom 95 on open).
Port: the system `.help()` tooltip cannot be styled; Figma draws a custom one, so an overlay
popover with a 500 ms hover delay. Gap: none.

**C38 Focus ring** (every control). Figma: 2 px `focus-ring` outside a 2 px gap, keyboard only.
Source: Beautiful UI `:focus-visible { outline: 2px solid accent; outline-offset: 2px }`. Port:
`.focusEffectDisabled()` on every control, then `.overlay(RoundedRectangle(cornerRadius: r + 2,
style: .circular).stroke(focusRing, lineWidth: 2).padding(-4))` when `@FocusState` is set and the
last input was a key (track through `NSEvent` local monitor, like `:focus-visible`). Gap: none.

**C39 Skeleton / loading placeholders** (run row loading, frame loading). Source: shadcn
`skeleton.tsx` (`animate-pulse rounded-md bg-accent`: Tailwind `pulse` is opacity 1 to 0.5 to 1
over 2 s `cubic-bezier(0.4, 0, 0.6, 1)` infinite); ShadKit `ShadcnSkeleton`. Figma override:
static placeholders (Figma rule 9: motion only on state change); keep the pulse off by default.
Gap: none.

**C40 Split view divider** (sidebar and run pane). Source: CodeEdit
`CodeEditSplitViewController.swift` (collapse behaviour), shadcn `resizable.tsx`. Figma: 1 px
`border`, no visible handle; the sidebar collapses at compact width. Port: `NavigationSplitView`
with `.navigationSplitViewColumnWidth(min: 208, ideal: 248, max: 272)`. Gap: none.

**C41 Approval pair (Accept, Reject)** (03, 04). Source: Beautiful UI `ApprovalCard.tsx` for the
decision motion (to extract); Figma draws two header buttons (C07), not a card, so only the
press, the undo window (5 s, existing) and the success transition are taken. Gap: none.

**C42 Markdown in Activity** (messages). Source: swift-markdown (the allowed dependency) and the
existing `MarkdownView.swift`; block spacing from MarkdownUI's `Theme` numbers (read only).
Port: unchanged renderer, restyled to Body 13/18 with code spans per C15. Gap: C15's inline
padding.

## 6. Proving the clone

### 6.1 Rendering the port

The Companion already has the machinery: `CompanionSnapshots` hosts real views in an
off-screen `NSWindow` that is never key and captures with `cacheDisplay` (`SnapshotHarness.swift`
`snapshot(window:to:)`). The clone proof adds a second mode to it, `GREENROOM_CLONE=<dir>`:

- **Gallery scenes:** one scene per spec, rendering the SwiftUI component with the same content
  as the original's demo (the same strings, amounts and variant) inside a view the same size as
  the spec's root frame, at 1x and 2x (captured into an `NSBitmapImageRep` whose
  `pixelsWide` and `pixelsHigh` are the point size times the scale and whose `size` is the
  point size, so the capture does not depend on the display the harness window sits on), in
  light and dark, into an sRGB bitmap (section 3.1).
- **Parts:** every clone marks its parts with `.clonePart("badge")`, a modifier that reports
  its frame in the scene's named coordinate space through a `PreferenceKey`. The harness writes
  `<piece>.parts.json`. Each spec gets a small hand-written `mapping` (part id to web `path`, for
  example `"row1.badge": "div/div[1]/button[0]/span[0]/span[0]"`), checked in next to the spec.
- **Time:** every loop reads `MotionClock` (section 3.2), so the harness sets it to `t`.
  Every state transition is written as `phase(at: t)` over the component's script (the same
  timeline the original's `TICKS` and `STAGES` arrays define), and each animated property is
  computed from the curve at `t − birth` rather than left to the SwiftUI animation system. In
  the running app the same function drives a `TimelineView`, or the component uses the
  `Animation` values from C06 on real state changes; both paths share `Motion.swift`, and a
  unit test checks that the `Animation` and the `phase(at:)` path give the same value at 10
  sample times. For captures the harness wraps the scene in `.transaction {
  $0.disablesAnimations = true }` so only the explicit `t` moves anything.
- **Frame sequences:** the harness renders `t = 0, 17, 33, 50, ...` up to the spec's sampling
  window and records, per part, frame, opacity, and the transform it applied (from the same
  `phase(at:)` values), giving the SwiftUI side of each curve in the spec's shape.

### 6.2 The comparisons

A Swift command-line target, `CloneCheck` (in `apps/companion`, never bundled, no new
dependencies: CoreImage, Accelerate and Foundation), reads a spec, the port's parts and images,
and the mapping, and runs four comparisons:

| Check | Method | What it catches |
| --- | --- | --- |
| **Layout** | For every mapped part: `|x − x'|`, `|y − y'|`, `|w − w'|`, `|h − h'|` against the WebKit `sf` run of the same theme. | Wrong padding, gap, size, alignment. |
| **Colour** | For every mapped part with a solid fill or text colour: CIEDE2000 between the spec's resolved hex and the port's token (static, from code), and between image pixels sampled at the part's centre inset by 2 px on both renders. | Wrong token, wrong colour space, wrong opacity. |
| **Pixels** | SSIM on luminance (8 x 8 Gaussian window, computed with vImage) over the whole piece, and a per-pixel CIE Lab delta E map through CoreImage `CILabDeltaE` (the swift-snapshot-testing technique), reported as the share of pixels above ΔE 2.3 (one just-noticeable difference) and the max per-channel delta, both excluding glyph pixels (pixels inside a text part's frame whose value differs from the part's background). | Shadows, rings, radii, anti-aliasing of shapes, anything the parts miss. |
| **Motion** | For every animated part and every frame time: opacity, translation (from frame origin), scale (from frame size) and rotation (from the matrix) against the spec's curve, plus the onset time of each change. | Wrong curve, duration, delay, stagger, or per-segment easing. |

### 6.3 Thresholds

Default thresholds (per component overrides live in the mapping file, each with a reason):

| Check | Threshold | Reasoning |
| --- | --- | --- |
| Layout, non-text parts | within 0.5 pt | Sub-pixel at 1x, one device pixel at 2x. |
| Layout, text parts | origin within 0.5 pt; width within `max(0.5 pt, |chromium − webkit|)`; baseline within 0.5 pt | Engines already differ on text width (section 4.3). |
| Colour, static tokens | ΔE00 ≤ 0.5 (in practice exact hex) | Tokens are copied, so any miss is a bug. |
| Colour, sampled pixels | ΔE00 ≤ 1.0 mean, ≤ 2.0 max | Compositing and gradients round differently. |
| Pixels (sf run) | SSIM ≥ 0.985; ≤ 0.5% of non-glyph pixels above ΔE 2.3; max channel delta ≤ 8/255 on non-glyph pixels | Shape anti-aliasing differs slightly between Skia/WebKit and CoreGraphics. |
| Pixels, glyphs | SSIM on glyph masks ≥ 0.92 | Section 3.5 rasterisation gap. |
| Motion, values | opacity ± 0.02; translation ± 0.5 pt; scale ± 0.005; rotation ± 1° | Below what the eye resolves at 60 Hz. |
| Motion, timing | every onset within one frame (17 ms); end of motion within one frame | Stagger and delays must be exact. |
| Springs | as motion values, up to Motion's rest time | Section 3.3. |

### 6.4 Where it runs

| Where | What | Why there |
| --- | --- | --- |
| `swift test` (hosted `macos-15` CI and self-hosted, every PR) | `Motion.swift` against the golden curve files; `Tokens.swift` against `specs/tokens.json`; cmdk scorer against its golden file; `phase(at:)` against every spec's `animations` list (onset, duration, delay, curve) | Pure data: deterministic on any Mac, no rendering. |
| Self-hosted Mac (`vm.yml` job, `[self-hosted, macOS, ARM64]`) on PRs touching `apps/companion` | `CompanionSnapshots` in `GREENROOM_CLONE` mode, then `CloneCheck` over every spec; fails the job on any threshold; uploads the diff images | Rendering depends on the OS version and fonts, so images are compared only on one fixed machine; the checked-in spec is the reference, not a previous screenshot. |
| Locally | `tools/extract.mjs` (and `--check`) when bumping a source sha; `CloneCheck --report` writes an HTML page with side-by-side, diff heat map and curve plots per component | Needs node and browsers; human review of new ground truth. |

The job must fail on regressions only: a new component without a spec is reported, not failed,
until its spec lands.

## 7. Pixel-perfect against Figma

The same `CloneCheck` runs against Figma frames:

- **Exports:** `get_screenshot` (Figma MCP) for every component node and every screen frame at
  scale 2, and `get_metadata` for every node's frame, into `docs/22-swiftui-clone-plan/figma/`
  (the docs/20 exports cover the starred screens and boards already). In this session the MCP
  exposed only the Principles page of the file (the Components, Tokens and Mockups pages were
  not listed), so the first task of the foundation PR is to export those nodes from the desktop
  app with each page open.
- **Layout from metadata:** Figma node frames are exact numbers, so layout is compared on
  frames (0.5 pt), not pixels, with the parts mapping keyed by Figma layer names.
- **Colour from variables:** `get_variable_defs` per component gives the bound variables; the
  check is token equality, not pixel sampling.
- **Pixels without glyphs:** Figma renders Inter, the app SF Pro, so glyph pixels are masked out
  (text part frames from metadata) and SSIM and ΔE run on the rest with the section 6.3
  thresholds. Text is compared by frame: the Figma text box width for the same string in Inter
  and SF differs by a few percent, so only the text box's origin and height are checked, and the
  trailing elements' positions (which Figma lays out with auto layout) are checked against the
  port.
- **Better, if possible:** install SF Pro (Apple's download, licensed for designing Apple
  platform UI) on the design Mac and swap the file's text styles to SF Pro in one place (docs/20
  notes the styles are centralised); the Figma desktop app renders local fonts, so frames
  exported from the desktop app would carry SF glyphs and the glyph mask could be dropped.
  Figma's web renderer showed SF Pro text blank, which is why the file uses Inter; the desktop
  app with local fonts is the untested path.

## 8. Order of work and the stacked PRs

The builder is stacking PRs from `redesign/1-run-summary` (daemon summary, merged into
`redesign/2-native-foundation` locally). This plan slots in without reordering it:

| PR | Branch | Content | Clone work in it |
| --- | --- | --- | --- |
| 2 | `redesign/2-native-foundation` | Tokens, type, spacing, radius, elevation, the window shell | C06 `Motion.swift` with the curve and spring conversions and their golden tests; `Tokens.swift` equal to section 5.1 (and a `specs/tokens.json` test); `MotionClock`; the ring, shadow and focus helpers (section 3.4, C38); C05 Lucide shapes |
| 2b | `redesign/2b-clone-harness` | The proof tooling | `GREENROOM_CLONE` mode in `CompanionSnapshots`, `.clonePart`, `CloneCheck`, the self-hosted CI step; the Figma exports of section 7 |
| 3 | `redesign/3-atoms` | C01 to C04, C07 to C15 | Each atom lands with its mapping file and passes `CloneCheck` against its spec (the Beautiful UI specs already exist for C02, C03, C08, C09, C11, C12, C13, C14, C15) |
| 4 | `redesign/4-runs-list` | C16, C36, C40, C20 | Run row against Figma; sidebar against NetNewsWire behaviour (keyboard) |
| 5 | `redesign/5-run-pane` | C17, C22, C23, C24, C25, C26, C28, C29 | Header motion (verdict landing) as a frame-sequence check |
| 6 | `redesign/6-activity` | C18, C30, C31, C32, C42 | Task rows and Thinking against `task-rows.json` and `thinking-state.json` curves |
| 7 | `redesign/7-palette-and-overlays` | C19, C21, C33, C34, C35, C37, C41 | Palette against shadcn Command spec and cmdk golden ranking; toast against Sonner |

Rules for the builder, per component: copy the named source file with its licence notice in a
header comment (`// Ported from <repo>@<sha> <path>, MIT, (c) <holder>`), add the notice to the
app's acknowledgements, write the mapping file, run `CloneCheck` locally until it passes, and
include the side-by-side from `CloneCheck --report` in the PR.

## 9. Ideas worth adopting (proposals)

These are **proposals**, not part of the approved design. Each serves the brief (minimal,
everything important visible, fast, easy navigation) and each would need the person's yes and a
Figma update before it is built.

1. **Space to peek evidence** (Finder Quick Look pattern): Space on a selected check opens the
   evidence viewer (11) and Space closes it; one key instead of a click, no new chrome.
2. **Type-to-filter in the runs sidebar** (Maccy): typing letters with the sidebar focused
   filters runs without first focusing a search field; Escape clears.
3. **Activity as an inspector column** (`.inspector` on macOS 14, CodeEdit's inspector area)
   instead of a sheet: the run stays visible while Activity is open, and the column remembers its
   width.
4. **Hover a check to preview its frame** (Beautiful UI ToolChips' diff preview): the picture
   swaps to that check's marked frame while hovering, returns on exit; selection unchanged.
5. **Hold Command to reveal keycaps** (a cheat-sheet pattern): the brief removed the always-on
   hint bar; holding ⌘ for 600 ms shows keycaps (C04) beside every action and disappears on
   release.
6. **Vibrant sidebar** (`NSVisualEffectView` `.sidebar` material, NetNewsWire and most Mac apps):
   more native; conflicts with the flat Figma `bg-sidebar`, so only with a Figma change.
7. **Menu bar extra with the Needs you count** (CodexBar, Stats, MacControlCenterUI): a glance
   without the window; click opens the run that needs you.
8. **System notification on verdict ready** when the app is in the background
   (`UNUserNotificationCenter`), with the toast (C21) used only when the window is key.
9. **Drag a frame out as PNG** (`.draggable` with a file representation) so a marked frame goes
   straight into a PR comment.
10. **Recent actions first in the palette** (Ghostty, Raycast): the last three actions sit above
    the ranked list with an empty query.

Rejected on the way: syntax highlighting in Activity (commands and short diffs only; +/- line
colours from ToolChips are enough); `ContentUnavailableView` (does not match Figma, C34); Pow
change effects beyond the blur swap (motion only on state changes, under 300 ms).

## 10. Sources

- Figma file: <https://www.figma.com/design/041UmqtdMYVCufxO8g9Ius> (variables read with
  `get_variable_defs` on node `21:2`); docs/20 section 12 and its exports on branch
  `design/ux-research` (`docs/20-companion-ux-research/figma/tokens.png`,
  `component-states.png`, `m03-failed-light.png`).
- Beautiful UI: <https://github.com/slev12397/beautiful-ui> at `44a274e`, LICENSE (MIT, Shane
  Levine 2026) read; <https://www.beautifului.dev>.
- shadcn/ui: <https://github.com/shadcn-ui/ui> at `98a1fe6`, `apps/v4/registry/new-york-v4/ui/`.
- ShadKit: <https://github.com/jasonkneen/ShadKit> at `6dbefde`, README and sources read.
- Sonner <https://github.com/emilkowalski/sonner> `8e4662b`; cmdk <https://github.com/dip/cmdk>
  `dd2250e`; NumberFlow <https://github.com/barvian/number-flow> `c5906cf`; Motion
  <https://github.com/motiondivision/motion> `636e725`; shadow-plugin 2.1.1 (MIT, Florian Kiem,
  from npm) for the shadow stacks.
- Every repository in section 2 at the commit listed; licences from each LICENSE file through
  the GitHub API on 2026-09-27.
- Lists: <https://github.com/donarb/swiftui-macos>,
  <https://github.com/stakes/swiftui-macos-resources>,
  <https://github.com/jaywcjlove/awesome-swift-macos-apps>,
  <https://github.com/onmyway133/awesome-swiftui>,
  <https://github.com/stefanjudis/awesome-command-palette>, <https://swiftpackageindex.com>,
  <https://designeer.xyz>, <https://news.ycombinator.com/item?id=42321433>.
- Lists on macOS: <https://fatbobman.com/en/posts/list-or-lazyvstack/>,
  <https://www.strv.com/blog/swiftui-list-vs-lazyvstack>,
  <https://developer.apple.com/forums/thread/704778>.
- MarkdownUI's maintenance-mode notice and Textual:
  <https://github.com/gonzalezreal/swift-markdown-ui/discussions/437>.
