# companion

SwiftPM macOS app that watches runs, speaks into their conversation and can take a
machine's screen. Vocabulary and invariants: `CONTEXT.md`. Decisions: ADR 0006
(messages), 0007 (the app), 0008 (recording), 0009 (control), 0011 (live screen), and the
package's own `docs/adr/0001` (the run window, superseded by 0004), `0002` (one derived
run state, colour meanings, verdict trust, the snapshot tool), `0003` (verdict actions, one
status vocabulary, the daemon changes the UI waits on), `0004` (the glyph-native interface
on a character grid), `0005` (keys and the action registry), `0006` (motion, signature
moments, click marks), `0007` (the dependency allowlist) and `0008` (readable type and the
olive brand, amending 0004's type and colour decisions). Design: `docs/design-spec.md`
(spacing and the accent, type, roles and the brand, layout, motion, states, keys),
`docs/design-research.md`. Design data: `design/themes/*.json` and `design/tokens.json`
at the repo root.

The views still draw the round-2 window (system styles, `NavigationSplitView`,
`Theme.swift`). ADR 0004 to 0006, as amended by 0008, describe the new window being built
layer by layer; until a layer lands, the rules below that name round-2 types describe the
code as it is, and the new rules apply to everything built from now on.

## Commands

```sh
swift run Companion                          # dev loop; needs the daemon on :7777
swift build                                  # pnpm build
swift test                                   # pnpm test
swift build -Xswiftc -warnings-as-errors     # pnpm lint
scripts/bundle.sh                            # .build/Companion.app, ad-hoc signed
scripts/install.sh                           # bundle, replace /Applications/Greenroom Companion.app, open
swift build && GREENROOM_SNAPSHOTS=<dir> GREENROOM_URL=http://127.0.0.1:7851 \
  .build/out/Products/Debug/CompanionSnapshots   # design screenshots, see below
```

- Targets: `Companion` is a library holding everything; `CompanionApp` is the one-line
  `main.swift` and the only product; `CompanionSnapshots` is the design harness. Tests
  `@testable import Companion`.
- `CompanionSnapshots` renders every key state, light and dark, at three sizes, into
  `<dir>`. Point it at a daemon serving copied runs (`greenroom serve -root <scratch> -tart
  /usr/bin/false`), never at VMs. `GREENROOM_SNAPSHOTS_EMPTY_URL` (a daemon with no runs)
  adds the welcome state; `GREENROOM_SNAPSHOTS_ONLY` filters by scenario name.
- The harness must never show on the person's screen: it runs `.prohibited` (no Dock
  icon), its windows sit far off every display and are never key, and it captures with
  `cacheDisplay`. It never launches the app or a bundle. It is an executable, not a test,
  because Xcode's `xctest` host links a newer SDK and draws a different look than the app.
  Its windows are not key, so selection and prominent buttons draw unemphasized.
- `swift build -c release` of the whole package fails (the harness uses `@testable`);
  release builds take `--product Companion`, as `bundle.sh` does.

- `GREENROOM_URL` overrides `http://127.0.0.1:7777` (`DaemonClient.defaultBaseURL`).
- The app never starts the daemon.
- The icon is drawn at build time by `scripts/make-icon.swift`; no artwork is checked in.

## Rules

- No `.xcodeproj`. Swift 6 language mode, strict concurrency; do not opt out.
- Dependencies are an allowlist (ADR 0007): Apple and swiftlang packages that build in
  Swift 6 mode with no warnings; swift-markdown (parse only), swift-collections,
  swift-async-algorithms and SwiftTerm. Anything else needs its own companion ADR (why,
  licence, Swift 6 mode, exit plan) before it goes into `Package.swift`. Rejected:
  Textual and MarkdownUI (they break the grid), animation libraries, Highlightr.
- Beautiful UI's components are ported as behaviour, credited under MIT in the app's
  acknowledgements; no code from it is copied into the Mac app (ADR 0007).
- `design/` is the source of colours, faces, cell metrics, spacing, radii, motion timings
  and layout thresholds. `DesignDataTests` gates it: every role maps to a slot, every
  theme meets its contrast (4.5:1 for the foreground and every role, dim included; 7:1
  for all of them in `-hc` themes), `brand` clears 3:1 against the background,
  `brandText` and the foreground on `chromeTint` clear the same 4.5:1 / 7:1 text
  threshold, and `brand` and `pass` sit at least 25 degrees apart in hue (ADR 0008).
  Change a colour in the JSON, never in Swift.
- Layering: `Views -> RunStore -> DaemonClient -> HTTP`.
  - Views never build URLs, decode JSON or keep their own copy of a run.
  - `Model/RunStore.swift` is the single `@Observable @MainActor` source of truth.
    `apply(_:)` merges events and does no I/O.
  - `Model/DaemonClient.swift` is the only file that knows HTTP. One method per route.
  - `Model/Models.swift` mirrors the daemon's Go structs; `OpenEnums.swift`, `DaemonJSON.swift`
    (dates, coders) and `EventStream.swift` (SSE) hold the rest of the wire layer.
  - `ScreenControl`, `FrameTimeline`, `StepSummary`, `RichText` are pure value types with
    a test per rule. `ControlPilot` holds the lease and send queue; it talks through
    `ControlClient` and `PilotHost` so its tests need no daemon.
    `Views/InputSurface.swift` is the only AppKit event code.
  - `Model/RunFacts.swift` is the one derived state per run (phase, whose turn, last
    activity, duration, failures, whether the machine can be watched or driven). Every
    indicator reads `RunStore.facts(_:)`; no view works out state from raw fields.
  - `Model/RunPresentation.swift` and `Model/VerdictReview.swift` hold the pure
    presentation rules (titles, evidence, tool names, transcript grouping, connection
    state, who decided a verdict, checks against the record), each with a test.
  - `Views/Theme.swift` is the design tokens (`Space`, `Radius`, `Palette`) and shared
    components. No spacing, radius or state colour literals elsewhere.
  - Menu commands reach the open run through focused scene values (`RunCommands`,
    `ScreenCommands`); they never hold their own state.
- The app only calls the API: no tart, no ssh, no run directory on disk. Missing
  capability means a new daemon route.
- Never add a way to take the lease without a matching way to give it back (Give Back,
  leaving the Screen stage, run change, machine not ready, quit). Quit waits up to 2 s for the release
  (`AppDelegate.applicationShouldTerminate`); every way out lets go of a held button first.
- Control that breaks under the person (input or renewal fails) is never silent:
  `ControlPilot.endedReason` shows the daemon's words under the player and the run is re-read.
- While driving with the screen focused, Command shortcuts (Cmd-Q too) go to the guest.
  "Give Back", clicked with the mouse, is the way out.
- `ScreenGeometry` is the only place a view point becomes a screen fraction.
- A wheel event becomes a scroll through `InputBatch.scroll`, which negates AppKit's deltas:
  the daemon's positive `deltaY` scrolls down, AppKit's positive `scrollingDeltaY` scrolls up.
- `KeyTranslator`: anything with cmd/ctrl, or with no character (return, arrows, F-keys),
  is a named `key`; everything else is `type` with the produced characters.
- Live screen (ADR 0011): streams only while the Screen stage is on screen, following live,
  on a ready machine. Anything else stops it, and the recording shows (also whenever the
  stream is down, with a status line under the track). `ScreenStream.swift` is the pure wire
  layer; `LiveScreen` owns the connection. Frames never hop through the main actor, and the
  renderer is only touched on `VideoOutput`'s queue. A decoder failure reconnects, because a
  new connection is how the daemon is asked for a keyframe.
- `LiveScreenHostView` puts the display layer on `ScreenGeometry.fitted`, so the video
  and `InputSurface` share one letterbox. Do not size the layer any other way.
- Stream errors: back off 1, 2, 4 ... 10 s, re-read everything open, reconnect. The
  backoff resets once a connection opens. A resync drops what is held for runs that are
  not open (the daemon may have restarted with other data), and a read that lands after
  a newer one of the same piece is discarded. URLSession holds an SSE response until the first
  bytes (the daemon's 15 s ping), so "Live" follows the resync, not the stream opening.
  While the stream is open, any failed read of the run list (a resync, Try Again, or the
  `perform(.runs)` an event triggers) starts the resync retry on the same backoff until the
  list answers (`retryTheListIfItFailed`; one at a time, cancelled when the stream ends),
  or the window would say "not answering" for as long as the stream lives.
- Connection state follows the last list read (`ConnectionState`): `offline` only when
  nothing answered; an HTTP error status is `refused`, shown in the daemon's words, never as
  "not running". The composer is disabled only while `offline`.
- The open run leaves when the daemon comes back without it: a resync whose fresh list lacks
  it and whose detail read answers 404 clears the selection, drops what was held and sets
  `RunStore.goneRun`, which "No run open" names. A list alone is not enough (a list read from
  before the run existed misses it too).

## UI rules

- No strict cell grid over the whole window (ADR 0008 narrows ADR 0004's grid rule).
  Chrome and data (step and command rows, code, output, ids, times, key hints, small
  uppercase labels) snap to the mono grid from `GridMetrics`; everything else uses the
  4/8 pt spacing scale from `tokens.json` `spacing`, with thin 1 px hairlines and small
  radii (`tokens.json` `radii`) instead of a box-drawing frame on every pane. The live
  screen is the one thing off the spacing scale, framed as a panel cut into the layout.
- Type follows what is being read, not who is speaking (ADR 0008): `tokens.json`
  `type.mono` (Monaspace Neon) for chrome and data, `type.reading` and
  `type.readingHeading` (Mona Sans, 14-15 pt, line height about 1.5) for anything read as
  sentences (verifier replies, notes, the task, the verdict reason, questions,
  empty-state copy). The five per-voice faces (Xenon, Radon, Argon, Krypton) are gone;
  `MessageGroup` tells a sender apart by its name in words and a thin coloured left edge,
  never by a typeface.
- Colour is an ANSI theme plus a brand; views use roles, never slots or hex: pass (2, a
  bluer emerald since ADR 0008, used only on small status marks), failure (1), attention
  (3), live (6), driving (5), dim (8, now 4.5:1 / 7:1 like every other role). `brand`
  (olive) and `chromeTint` are two more theme keys (`greenroom-brand`,
  `greenroom-brand-text`, `greenroom-chrome-tint`, named in `tokens.json` `themeKeys`),
  not ANSI slots: `brand` marks large calm surfaces and actions (primary buttons, the
  selected run, the active tab, the cursor, the wordmark) and never the content areas
  (screen, steps, transcript), which stay neutral so a screenshot of evidence reads true;
  `chromeTint` is the faint brand tint behind the sidebar and top bar only. Hue only for
  meaning and the brand; emphasis is weight or inverse video. Every state is also a word.
  Bright slots never carry meaning.
- Every key is an action in the one registry (ADR 0005). The hint bar, `?` help, Cmd-K
  and the menu bar read it; a test fails on a shortcut declared anywhere else. Nothing
  destructive has a bare key. Accept and dispute send after a 5 s undo. A typing context
  swallows bare keys; driving sends every key to the guest and only a click exits.
- Motion uses only the vocabulary in ADR 0006 and `tokens.json` `motion`. Motion never
  blocks input; effects never own content; accessibility sees the final state; Reduce
  Motion makes every change instant.
- Click marks (transient, `m` toggles; static on a paused step) are the only thing drawn
  over the screen's picture.
- A run is named by a short title from its task (`RunTitle.short`, made distinct with
  `RunTitle.distinct`), never by its id. The sidebar pins "Needs You" and "Running"
  above the days; a row is the title, start time and counts, and its state in words.
- The verdict's Accept and Dispute live only in `VerdictCard`, pinned above the
  conversation (or above the stage when the conversation is hidden). Its headline is the
  state and who decided (`VerdictReview`); a verdict an agent accepted says no human
  reviewed it. The transcript shows the live verdict as one line, never a second card.
- Each colour means one thing. Until the theme layer lands, `Palette` maps them to
  system colours (green pass, red failure, orange needs you, teal live, the system accent
  for driving); after it, the roles above. Booting and offline carry none. Every state
  is also a word, and the words are one vocabulary (ADR 0003): the sidebar's
  `rowStatus` and the card's `VerdictReview.state` must say the same thing.
- A verdict's actions are only the ones the daemon's session rules accept. When it
  refuses (an agent-accepted verdict), the card says so and offers the nearest real
  action (a re-check task), never a button that will fail. The verifier stops only when
  the machine is destroyed (`RunFacts.verifierListens`); then the re-check is disabled and
  the card and composer say nothing will answer.
- A verdict is older than a task sent after it with no verdict since
  (`VerdictReview.newerTask`): the card says so (`staleNote`), and Re-check is disabled and
  refused by `sendVerdictAction`, since a re-check then pulls the verifier's turn off the
  newer task (issue #89).
- The verdict's drafts (the action and its reason, whether evidence was opened, the
  accept confirmation, whether the card's evidence is open) live in
  `RunStore.verdictDrafts`, per run and verdict, never in `@AppStorage`: a saved
  expansion opened every card and came back after a relaunch (#52). The card is capped at
  `RunLayout.verdictCardShare` of its column; its headline (with Less) and actions stay
  and the body between them scrolls. A draft ends
  when its verdict changes or closes, or its run leaves the list; a resync keeps it. The verdict
  flow has no sheet, alert or dialog: accepting rebuilds the card, and a sheet whose
  presenter goes away leaves the window unable to take a click. Ask inline.
- The player shows one source chip (live, connecting, recording, driving). Take Control
  / Give Back exists once, in the toolbar.
- Nothing but click marks is drawn over the Screen stage's picture. The driving bar sits above it, and
  position, the step under the pointer, live state and stream errors go under the track.
  The well takes the picture's shape, so there is no letterbox.
- The conversation column is a hand-made split (`ColumnDivider`), not `.inspector` as
  ADR 0001 says, nor `HSplitView`: inside `NavigationSplitView` both add hundreds of points to the window's
  minimum width, even while hidden. Widths live in `RunLayout` (stage at least 440, the
  spec). Narrow windows give way in the spec's order: below
  `RunLayout.sidebarFoldWidth` (of the sidebar's measured width; 1032 for an ideal
  sidebar with the conversation) `RootView` folds the sidebar,
  only while shrinking, so a sidebar shown by hand stays; where the conversation then has
  no room beside the stage (`RunLayout.conversation` is nil) it gives way and the verdict
  card moves above the stage, and asking for it folds the sidebar; the player bar's
  second line wraps.
- No hard minimum width on the stage: the detail's minimum adds to the window's, and
  showing the sidebar in a narrow window then widens the window past the screen (#64).
  The harness prints "window grew" when that happens; check its `G` (1024 x 660) shots.
- The first window's size is `RunLayout.defaultWindowSize` of the screen's visible
  frame (`defaultWindowPlacement`), never the bare 1320 x 840.
- `AppDelegate` fits only the `main` scene's window (`isRunWindow`) to its screen, the
  first time it becomes key after launch and on every screen change. Sheets, panels
  and alerts are smaller than `RunLayout.windowMinimum` and would be grown to it.
- While driving, keys follow focus: the screen while it was clicked last (Command
  shortcuts included), the composer once it is clicked. "Give Back", clicked, returns the
  screen (in the bar above the picture, the player and the toolbar). Taking control from
  the toolbar or menu switches the stage to the Screen first.
- Every list row has a readable summary (`StepSummary`), not raw JSON, and every
  `machine_*` tool a title (`ToolCatalog`). Input keys the daemon records for itself
  (`reader` on `machine_ui`) never show. A tool-call row whose step is not held reads its
  progress message through the same rules (`StepSummary.line(ofProgress:)`).
- Only `RunView` sets a `.navigationTitle` (the run's short title); with no run open the
  window is "Greenroom Companion". A title on a pane (the conversation) names the whole window.
- Composer keys: Return and Cmd-Return send; Shift-Return and Option-Return insert a line
  break at the cursor through the active field editor (a newline written into the binding
  while the field is edited is overwritten by the editor); the binding and `selection`
  path is only the fallback with no key window, as in tests. Left to the vertical
  `TextField`, Shift-Return ends editing and selects the whole draft, so the next key
  erases it.
- The verdict card's words about Reject depend on `RunFacts.verifierListens`
  (`VerdictReview.explanation`, and the rejected note): on a destroyed run nothing looks
  again, so nothing may say it will.
- A run's clock time comes from `createdAt`, never from its id (ids are UTC).
  `Chrome.runHash` takes the id's tail for display.
- Durations go through `Chrome.clock`, or an 8-hour run reads "476:12".

## Gotchas

- Never split the SSE body with `URLSession.AsyncBytes.lines`. It drops empty lines, and
  the empty line ends an SSE frame, so no event is ever dispatched. `SSELineSplitter`
  keeps them; `SSELineSplitterTests` pins it. To debug a quiet stream, compare
  `curl -N http://127.0.0.1:7777/api/events` with the app's own log.
- `RunStore.frameImage` parses only the JPEG header off the main actor (~0.2 ms); decode
  (~1.7 ms) happens on first draw, on main. Forcing the decode means ~3.1 MB per cached
  frame (~189 MB at the 60-frame cache), so the cache must be re-bounded in bytes in the
  same change. Do not fix one half alone.
- Read streamed bodies in chunks (`DaemonClient.chunks`), never byte by byte off
  `URLSession.AsyncBytes`: too slow for video.
- Build the H.264 format from the avcC's SPS and PPS
  (`CMVideoFormatDescriptionCreateFromH264ParameterSets`). Handing CoreMedia the avcC atom
  alone gives a 0x0 description and every decode fails with -6661. VIDEO `pts` is not a
  clock (it jumps on a re-encoded keyframe); samples display on arrival. A still screen
  sends nothing, which is not a stall.
- An `AVSampleBufferDisplayLayer` outside a window decodes nothing visible; tests that
  read `displayedPixelBuffer()` host it in an offscreen `NSWindow` (`isReleasedWhenClosed = false`).
- Unknown enum values decode to `unknown(String)`, never throw. Use `JSONDecoder.daemon()`
  (RFC3339 with or without fractional seconds).
- `apps/daemon/internal/api/api.go` is the authority on shapes. A `step` event carries the
  step number (re-read `/steps`). `machine`, `verdict`, `destroyedAt` come back as
  explicit `null`. Errors are `{"error": "..."}`; a message refused on a contested verdict
  is 409.
- `RunSummary.task` is optional: a daemon before it decodes, and the run reads "Run <hash>".
- `ScrollViewReader.scrollTo` in a `LazyVStack` finds a row it has not built only by its
  `ForEach` identity (Steps: the `Step`), never by an `.id` set inside the row. Scroll to
  the identity first, then to the inner id once the row exists (`StepsView.reveal`).
- `HostedViewTests` host real views in an off-display, never-key `NSWindow` (title, scroll
  position, keys into a field editor). The app is `.prohibited`, so there is no key window:
  code that needs one (`NSApp.sendAction(_:to: nil ...)`, `NSApp.keyWindow`) does nothing there,
  so it needs a fallback those tests exercise (the composer's Shift-Return).
- A `Text` with `.fixedSize(horizontal: false, vertical: true)` in an empty state can make
  the window grow to thousands of points tall when first laid out narrow. Let it wrap.
  The verdict card's actions text is the same case: it gets its room from
  `layoutPriority`, not `fixedSize`. A capped `.frame(maxHeight:)` stretches to its cap,
  so cap only the scrolling part.
- "Export recording" calls `GET /api/runs/{id}/recording.mp4`, which needs `ffmpeg` on the
  daemon host. Show the daemon's error.
