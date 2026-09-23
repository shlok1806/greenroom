# Design research: Greenroom Companion

Research for the macOS companion (watch a run live, read the coder/verifier/human
conversation, judge a verdict with evidence, take the screen). Gathered 2026-09-22.
Every point is a takeaway plus its source. Glossary terms (Run, Frame, Verdict state,
Control lease, Driving) are in `../CONTEXT.md`.

## 1. Apple HIG for macOS

HIG pages load via JS; the text below was read from the same content at
`developer.apple.com/tutorials/data/design/human-interface-guidelines/<page>.json`.

### Designing for macOS
- Use the big display to show more content in fewer nested levels with less modality;
  let people resize/hide/show windows and support full screen; put every command in the
  menu bar; support keyboard-only work. [designing-for-macos](https://developer.apple.com/design/human-interface-guidelines/designing-for-macos)

### Sidebars
- Sidebar row height, text and glyph size follow the user's Sidebar icon size setting
  (small/medium/large). Do not hard-code row metrics. [sidebars](https://developer.apple.com/design/human-interface-guidelines/sidebars)
- At most two levels of hierarchy; use short group labels (fits "Today", "Yesterday").
- Sidebar icons follow the system accent color; a fixed color is fine only when it carries
  meaning (Mail's yellow VIP). Status color on a run row is that kind of meaning.
- Do not put critical info or actions at the bottom of the sidebar; windows get dragged
  so the bottom edge is hidden.
- Offer show/hide via toolbar button and View menu; do not hide it by default. Consider
  auto-collapsing when the window gets narrow.
- On macOS 26 the sidebar is Liquid Glass floating over content; extend rich content under
  it with `backgroundExtensionEffect()`.

### Split views and inspectors
- Persistently highlight the current selection in each pane leading to the detail.
  [split-views](https://developer.apple.com/design/human-interface-guidelines/split-views)
- Set sane min/max pane sizes so the divider never disappears; prefer the 1 pt thin divider.
- Let people hide panes, and offer more than one way back (toolbar button plus menu
  command with a shortcut).
- Buttons that open inspectors belong on the trailing edge of the toolbar.
  [toolbars](https://developer.apple.com/design/human-interface-guidelines/toolbars)
  SwiftUI: [`inspector(isPresented:content:)`](https://developer.apple.com/documentation/swiftui/view/inspector(ispresented:content:)).

### Toolbars
- Three zones: leading (sidebar toggle, back, title; not customizable), center
  (customizable, overflows first), trailing (always visible: inspector toggles, search,
  one primary action). Aim for at most three groups. [toolbars](https://developer.apple.com/design/human-interface-guidelines/toolbars)
- Only one `.prominent` primary action, on the trailing side.
- Prefer borderless SF Symbols over text; keep text-labeled buttons separated by fixed
  space so labels do not run together.
- Title: short (under 15 characters), never the app name.
- Every toolbar item must also exist as a menu bar command.
- On macOS 26 drop custom toolbar backgrounds and tints; let scroll edge effects separate
  bar and content.

### Materials and Liquid Glass
- Liquid Glass is only for the functional layer (toolbars, sidebars, floating controls),
  never the content layer. Use it sparingly on custom controls. [materials](https://developer.apple.com/design/human-interface-guidelines/materials)
- Two variants: `regular` (blurs, for text-heavy things like sidebars and popovers) and
  `clear` (for controls over media such as video). Over bright media, add a ~35% dark
  dimming layer behind clear glass. Directly relevant to controls floating on the live screen.
- Pick materials by semantic purpose, not by the color they happen to produce; put vibrant
  (system label) colors on top.

### Typography (verified against HIG table "macOS built-in text styles")
- Default 13 pt, minimum 10 pt. macOS has no Dynamic Type. [typography](https://developer.apple.com/design/human-interface-guidelines/typography)

| Style | Weight | Size | Line height | Emphasized |
|---|---|---|---|---|
| Large Title | Regular | 26 | 32 | Bold |
| Title 1 | Regular | 22 | 26 | Bold |
| Title 2 | Regular | 17 | 22 | Bold |
| Title 3 | Regular | 15 | 20 | Semibold |
| Headline | Bold | 13 | 16 | Heavy |
| Body | Regular | 13 | 16 | Semibold |
| Callout | Regular | 12 | 15 | Semibold |
| Subheadline | Regular | 11 | 14 | Semibold |
| Footnote | Regular | 10 | 13 | Semibold |
| Caption 1 | Regular | 10 | 13 | Medium |
| Caption 2 | Medium | 10 | 13 | Semibold |

The sizes in the brief are all correct. Note Caption 2 is Medium weight, not Regular.

### Color and dark mode
- Never reuse one color for two meanings, especially status. [color](https://developer.apple.com/design/human-interface-guidelines/color)
- Use dynamic system colors for their stated purpose (`labelColor`, `separatorColor`,
  `controlAccentColor`); do not hard-code values. Custom colors need light, dark and
  Increase Contrast variants.
- On glass, color is for status indicators and the primary action only.
- Contrast at least 4.5:1, aim for 7:1 for small custom text; test Dark Mode with Increase
  Contrast and Reduce Transparency, alone and together. [dark-mode](https://developer.apple.com/design/human-interface-guidelines/dark-mode)
- Media-focused apps may justify a permanently dark area (the live screen well), not the
  whole app.

### Accessibility
- Never convey state by color alone; pair with a distinct shape or symbol (red/green is
  the classic failure). [accessibility](https://developer.apple.com/design/human-interface-guidelines/accessibility)
- macOS hit targets: 28x28 pt default, 20x20 pt minimum. Pad about 12 pt around bezeled
  controls, 24 pt around bezel-less ones.
- Full Keyboard Access must work; do not override system shortcuts.
- Reduce Motion: cut automatic and repeating animation (pulses, zoom, scale).

### Motion and SF Symbols
- Motion must be purposeful, brief, cancelable, and never the only signal. Avoid motion on
  frequent interactions. [motion](https://developer.apple.com/design/human-interface-guidelines/motion)
- SF Symbols: 4 rendering modes (monochrome, hierarchical, palette, multicolor); nine
  weights matching SF Pro; variable color for changing values (progress), hierarchical
  for depth; symbol animations from SF Symbols 5. [sf-symbols](https://developer.apple.com/design/human-interface-guidelines/sf-symbols)

## 2. What makes Mac apps feel great

- Liquid Glass (macOS 26): standard `NavigationSplitView`, toolbars, sheets and search get
  the new look for free when built with Xcode 26. Group toolbar items with
  `ToolbarSpacer(.fixed)`, hide a shared glass pill with `.sharedBackgroundVisibility(.hidden)`,
  add counts with `.badge()`, use `.glassEffect()`/`GlassEffectContainer` only for custom
  floating controls, `.buttonStyle(.glassProminent)` for the one hero action,
  `.searchable` on the split view lands top trailing. [WWDC25 323](https://developer.apple.com/videos/play/wwdc2025/323/), [notes](https://wwdcnotes.com/documentation/wwdc25-323-build-a-swiftui-app-with-the-new-design/)
- Window tailoring: `.toolbar(removing: .title)`, `.toolbarBackgroundVisibility(.hidden, for: .windowToolbar)`,
  `.containerBackground(.thickMaterial, for: .window)`, `.defaultWindowPlacement`,
  `.windowIdealPlacement` (size to content within `visibleRect`), `.restorationBehavior(.disabled)`
  for transient windows. [WWDC24 10148](https://developer.apple.com/videos/play/wwdc2024/10148/)
- "Mac-assed Mac app" (Brent Simmons, via Collin Donnell): unapologetically platform
  native, standard controls, accessible, not a custom web-like UI. [Daring Fireball](https://daringfireball.net/linked/2020/03/20/mac-assed-mac-apps)
- Best-in-class checklist: fast resize and scroll, full keyboard navigation, respects accent
  color and sidebar size, menu bar completeness, help, attention to small details.
  [Swiftjective-C](https://www.swiftjectivec.com/what-does-a-best-in-class-macos-app-look-like/)
- SwiftUI gaps to plan for: de-emphasized selection in inactive windows (use
  `\.appearsActive`), no hook for "context menu open", `List` selection styling is fragile,
  semantic toolbar placements are unpredictable, text fields swallow arrow keys.
  [pfandrade, 2026](https://pfandrade.me/blog/mac-assed-swiftui-app/)

## 3. Open-source Mac apps: concrete patterns

### CodeEdit (SwiftUI + AppKit, Xcode-like)
- Inspector is a panel with icon tab bar on the trailing side, `.formStyle(.grouped)`,
  `accessibilityElement(children: .contain)` labeled "inspector". [InspectorAreaView.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/InspectorArea/Views/InspectorAreaView.swift)
- Inspector section header: 12 pt bold, `.secondary`, content then `Divider()`, 11 pt
  vertical spacing. [InspectorSection.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/InspectorArea/Views/InspectorSection.swift)
- Empty state: centered, SF Symbol 28 pt `.tertiary`, title, 10 pt description, small
  `.accessoryBarAction` buttons; "No Selection" when nothing is picked.
  [CEContentUnavailableView.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/CodeEditUI/Views/CEContentUnavailableView.swift), [NoSelectionInspectorView.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/InspectorArea/Views/NoSelectionInspectorView.swift)
- Task status as an enum with a color per case (notRunning gray, stopped yellow, running
  orange, failed red, finished green) rendered as a 5 pt dot trailing the name.
  [CETaskStatus.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/Tasks/Models/CETaskStatus.swift), [TaskView.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/ActivityViewer/Tasks/TaskView.swift)
  (Color-only; we should add shape.)
- Status bar: `.background(.bar)`, top `Divider`, height 29 pt pre-26 and 37 pt on macOS 26,
  disabled when window inactive (`\.controlActiveState`), icon tabs with `.help()` tooltips.
  [StatusBarView.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/StatusBar/Views/StatusBarView.swift)
- Tab icons: `.fill` variant for selected pre-26, capsule style on Tahoe; always an
  accessibility label and identifier. [WorkspacePanelTabBar+IconButton.swift](https://github.com/CodeEditApp/CodeEdit/blob/main/CodeEdit/Features/CodeEditUI/Views/WorkspacePanel/WorkspacePanelTabBar%2BIconButton.swift)

### NetNewsWire (AppKit, the reference Mac-assed app)
- Sidebar cell metrics follow `NSTableView.RowSizeStyle`: icon 16/19/22 pt, text 11/13/15 pt
  for small/medium/large. [SidebarCellAppearance.swift](https://github.com/Ranchero-Software/NetNewsWire/blob/main/Mac/MainWindow/Sidebar/Cell/SidebarCellAppearance.swift)
- Count badge: monospaced digits (`monospacedDigitSystemFont`); on macOS 26 no pill, just
  13 pt `secondaryLabelColor` text (white when selected); pill only on macOS 15.
  [UnreadCountView.swift](https://github.com/Ranchero-Software/NetNewsWire/blob/main/Mac/MainWindow/Sidebar/UnreadCountView.swift)
- Timeline row: bold 90% size source and date, semibold title up to 3 lines, 8 pt state
  dot in a fixed left gutter, padding 8/4/10/4, no grid lines.
  [TimelineCellAppearance.swift](https://github.com/Ranchero-Software/NetNewsWire/blob/main/Mac/MainWindow/Timeline/Cell/TimelineCellAppearance.swift)
- Indicator knows `isSelected` and `isEmphasized` so it stays legible on selected rows.
  [UnreadIndicatorView.swift](https://github.com/Ranchero-Software/NetNewsWire/blob/main/Mac/MainWindow/Timeline/Cell/UnreadIndicatorView.swift)

### Whisky (pure SwiftUI NavigationSplitView, closest to our shape)
- `List(selection:)` + `.listStyle(.sidebar)` + `.searchable(placement: .sidebar)`;
  selection persisted in `@AppStorage` and restored on launch; in-flight rows show a small
  `ProgressView` at 50% opacity and `.selectionDisabled`. [ContentView.swift](https://github.com/Whisky-App/Whisky/blob/main/Whisky/Views/ContentView.swift)
- Detail `.id(item.url)` so state resets per selection; `.disabled` while in flight.
- Rows carry a `contextMenu` with `Button(_, systemImage:)` and `.labelStyle(.titleAndIcon)`.
  [BottleListEntry.swift](https://github.com/Whisky-App/Whisky/blob/main/Whisky/Views/Bottle/BottleListEntry.swift)

### Ghostty macOS (SwiftUI overlays on an NSView surface, closest to our live screen)
- Surface is an `NSViewRepresentable` inside a `ZStack` of overlays: resize size overlay,
  search bar, bell border, highlight, key sequence indicator, unfocused dim, error views.
  [SurfaceView.swift](https://github.com/ghostty-org/ghostty/blob/main/macos/Sources/Ghostty/Surface%20View/SurfaceView.swift)
- Unfocused dim is a `Rectangle` with `.allowsHitTesting(false)` and configurable opacity.
- Mode indicator: capsule on `.regularMaterial`, 1 pt `primary.opacity(0.15)` stroke,
  soft shadow, popover explains it; spring transition from an edge.
- Secure Input badge: top trailing lock symbol, tap opens a popover in plain words
  explaining the mode. Good model for a "Driving" badge. [SecureInputOverlay.swift](https://github.com/ghostty-org/ghostty/blob/main/macos/Sources/Features/Secure%20Input/SecureInputOverlay.swift)
- Renderer failure has its own view with a plain explanation, not a blank surface.

## 4. Information design for runs, timelines and live screens

### Status vocabulary
- GitHub Checks split lifecycle from outcome: status `queued | in_progress | completed |
  waiting | requested | pending`; conclusion `success | failure | neutral | cancelled |
  skipped | timed_out | action_required | stale`. [Checks API](https://docs.github.com/en/rest/checks/runs)
  Our verdict state (`none, proposed, accepted, contested, rejected`) is a third axis:
  agreement, separate from run lifecycle and pass/fail.
- Langfuse levels `DEBUG, DEFAULT, WARNING, ERROR` let a long trace default to hiding
  noise while errors stand out. [log levels](https://langfuse.com/docs/observability/features/log-levels)

### Verdict and evidence presentation
- GitHub Actions auto-expands the failed step and gives each log line a permalink.
  [run logs](https://docs.github.com/en/actions/how-tos/monitor-workflows/use-workflow-run-logs)
- Buildkite annotations: Markdown blocks with style `success | info | warning | error` at
  the top of the build page, ordered by priority, updated in place by context key.
  [annotate](https://buildkite.com/docs/agent/v3/cli-annotate) A verdict card is this: one
  styled summary on top, updated in place, not a new message each time.
- BrowserStack: a human can set Status + Reason on a finished session; video progress bar
  has red markers for errors and yellow for warnings. [view results](https://www.browserstack.com/docs/automate/selenium/view-test-results)
- Codex cloud and Claude Code on the web end with summary + diff, then follow-up in the same
  thread or act (open PR). [Codex cloud](https://developers.openai.com/codex/cloud), [Claude Code web](https://code.claude.com/docs/en/claude-code-on-the-web)

### Timeline scrubbing and synced list + media
- PostHog: seekbar plus an activity list of all events; skip inactivity; speed; alt for
  fine seek; floating (hover) vs pinned controls. [PostHog](https://posthog.com/docs/session-replay/how-to-watch-recordings)
- FullStory: clicking an event in the list seeks playback to that moment; matched events
  are tinted on the timeline. [FullStory](https://help.fullstory.com/hc/en-us/articles/360053144654-Can-I-search-for-events-while-watching-a-session)
- Devin: a Progress view merges shell, edits and browser into one list; clicking a step
  scrubs back to that point; the human can take over the browser. [Devin tools](https://docs.devin.ai/work-with-devin/devin-session-tools)
- AgentOps: waterfall of LLM calls, tools, actions and errors with a detail pane on the
  right for the selected event. [AgentOps](https://docs.agentops.ai/v2/introduction)
- Langfuse timeline: color encodes observation type, cmd-scroll zooms, double-click flies
  to a span, "Fit" resets; labels appear progressively with zoom.
  [responsive timeline](https://langfuse.com/changelog/2026-08-28-responsive-timeline), [timeline view](https://langfuse.com/changelog/2024-06-12-timeline-view)

## Takeaways for the companion

1. Keep the three-column shape: sidebar of runs (Today, Yesterday, Earlier; max two
   levels), detail with the live screen, and a trailing `.inspector` for verdict and
   evidence of the selected step. Inspector toggle on the toolbar trailing edge plus a
   View menu command with a shortcut.
2. Model three separate axes and never merge them in one color: lifecycle (queued,
   running, finished, destroyed), outcome (pass, fail, none) and verdict state (none,
   proposed, accepted, contested, rejected).
3. Each state gets a symbol plus a color, never color alone, e.g. `checkmark.circle.fill`
   green, `xmark.octagon.fill` red, `questionmark.diamond` orange for proposed,
   `exclamationmark.bubble` for contested, variable-color or pulsing dot for running.
   Pulsing stops under Reduce Motion.
4. Sidebar rows use system sidebar sizing (no fixed heights), title in Body, secondary line
   in Subheadline `.secondary`, times and durations in monospaced digits.
   Fixed status colors are fine there because they carry meaning.
5. Verdict as one Buildkite-style card pinned at the top of the transcript and inspector:
   state, one-line reason, evidence count, and the Accept / Dispute actions only for
   proposed and contested. Accept is the single `.glassProminent`/`.borderedProminent`
   action.
6. Evidence links everywhere: every step, frame and screenshot has a timestamp; clicking
   it seeks the screen and selects the transcript line (FullStory/Devin sync). Put
   failure and verdict markers on the scrubber (red fail, yellow warning, accent for
   verdict), as BrowserStack does.
7. Replace Screen/Steps/Transcript tabs with a split of screen (top) and a synced list
   (bottom or side), so you never lose the screen while reading evidence. Keep tabs only
   as a compact fallback for narrow windows.
8. Auto-select and auto-expand the failing step when a run finishes failed (GitHub
   Actions), and default the log to hide debug-level noise.
9. Live screen well is permanently dark; floating player controls use `clear` glass with a
   ~35% dim behind, show on hover (PostHog floating) with an option to pin.
10. Driving mode follows Ghostty: a top trailing capsule badge on `.regularMaterial` with a
    red symbol, a popover in plain words, a visible "Release" button and Esc-chord hint;
    dim nothing while driving, but dim the screen with a hit-test-free overlay when the
    lease is lost.
11. Empty and error states via `ContentUnavailableView` with a symbol, one sentence and
    one action: "No run selected", "No frames yet", "Machine gone, recording available",
    "Stream dropped, reconnecting".
12. Type scale uses only text styles: Title 2 for run title in the detail header,
    Headline for card titles, Body for messages, Callout/Subheadline for meta, Caption for
    timestamps. Nothing under 10 pt.
13. Transcript bubbles distinguish coder, verifier, human and system by symbol + label +
    subtle tint, not by color alone; "verifier is working" is a quiet inline row.
14. Every toolbar action (Screenshot, Take control, Destroy, Toggle inspector) exists in
    the menu bar with a shortcut; Destroy is not in the toolbar center and asks for
    confirmation.
15. Test the whole app in Light, Dark, Increase Contrast, Reduce Transparency and Reduce
    Motion, with Full Keyboard Access and VoiceOver; persist sidebar selection and pane
    sizes across launches (Whisky's `@AppStorage` pattern).
