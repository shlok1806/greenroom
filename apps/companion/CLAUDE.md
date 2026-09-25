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

ADR 0004 to 0006, as amended by 0008, describe the new window being built layer by layer.
Layer 1 (foundation and restyle) has landed: the theme, the two bundled faces, the spacing
and radii, the window's own chrome, and every view restyled in that language. Layer 2
(keys) has landed: the action registry, the key router, the hint bar with its `?` help,
the Cmd-K palette, the menu bar built from the registry, and the 5 s undo on accept and
dispute. Layer 3 (layout) has landed: the three width classes, the screen with the steps
under it (no more Screen and Steps tabs), the runs strip and the one-row steps track, one
pane at a time in a narrow window, and `z` zoom (design spec, Layout). Layer 4 (the stage)
has landed: the screen's title row and well states, the loader, the player bar, and the
steps as a thinking trace (below). Not yet built: the motion vocabulary beyond the settle
spring, the tick and the loader, signature moments, click marks (`m`), `GridMetrics`.
Until a layer lands, the rules below that name round-2 behaviour describe the code as it is.

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
scripts/sync-design.sh                       # copy design/ into the bundled resources
swift build && GREENROOM_SNAPSHOT=<dir> GREENROOM_SNAPSHOT_RUN=<run id> \
  .build/out/Products/Debug/Companion        # the running app, written in all four themes
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

- `GREENROOM_SNAPSHOT=<dir>` (debug builds only, `SnapshotHook` in `WindowChrome.swift`):
  the real app, against whatever daemon it talks to, opens `GREENROOM_SNAPSHOT_RUN`
  with `GREENROOM_SNAPSHOT_PANE` (`screen` or `steps`: the stage part with the keys, and
  in a medium or narrow window whether the steps list is open), writes the window in each theme
  of `GREENROOM_SNAPSHOT_THEMES` (default `dark,light,dark-hc,light-hc`) to
  `<dir>/<GREENROOM_SNAPSHOT_NAME>-<theme>.png` with `cacheDisplay` (no screen-recording
  permission), puts the person's theme back and quits. It shows a window on screen; the
  harness above does not. `GREENROOM_SNAPSHOT_KEYS` (comma-separated: `g`, `?`, `cmd+k`,
  `esc`, `enter`, `tab`, `z`, `shift+enter`, `text:words`; `g,v,z` zooms the screen, `g,t,z`
  the transcript) posts real key events through the app's
  queue first, so the router sees them as typed (hint bar, help, palette, composer
  states); `GREENROOM_SNAPSHOT_MENU=1` prints the View and Run menus as AppKit holds them. `GREENROOM_SNAPSHOT_SIZE=820x560`
  sizes the window's content first.
  Never press keys that send (accept, dispute, a composer's Return) against a real run.
- `GREENROOM_URL` overrides `http://127.0.0.1:7777` (`DaemonClient.defaultBaseURL`).
- The app never starts the daemon.
- The icon is drawn at build time by `scripts/make-icon.swift`; no artwork is checked in.

## Rules

- No `.xcodeproj`. Swift 6 language mode, strict concurrency; do not opt out.
- No SwiftUI macros (`@Entry`, `#Preview`): their plugin ships only with Xcode, and the
  self-hosted runner may build with the Command Line Tools alone. Spell out the
  `FocusedValueKey`/`EnvironmentKey` instead. `@Observable` is fine.
- Dependencies are an allowlist (ADR 0007): Apple and swiftlang packages that build in
  Swift 6 mode with no warnings; swift-markdown (parse only), swift-collections,
  swift-async-algorithms and SwiftTerm. Anything else needs its own companion ADR (why,
  licence, Swift 6 mode, exit plan) before it goes into `Package.swift`. Rejected:
  Textual and MarkdownUI (they break the grid), animation libraries, Highlightr.
- Beautiful UI's components are ported as behaviour, credited under MIT in the app's
  acknowledgements; no code from it is copied into the Mac app (ADR 0007).
- `design/` is the source of colours, faces, cell metrics, spacing, radii, motion timings
  and layout thresholds. The app ships a copy (`Sources/Companion/Resources/Design`,
  written by `scripts/sync-design.sh`, read by `DesignData.shared`); SwiftPM bundles only
  files inside a target. `DesignRuntimeTests` fails when the copy and `design/` differ,
  so run the script after every change there. `DesignDataTests` gates the data: every
  role maps to a slot, every theme meets its contrast (4.5:1 for the foreground and every role, dim included; 7:1
  for all of them in `-hc` themes), `brand` clears 3:1 against the background,
  `brandText` and the foreground on `chromeTint` clear the same 4.5:1 / 7:1 text
  threshold, and `brand` and `pass` sit at least 25 degrees apart in hue (ADR 0008).
  Change a colour in the JSON, never in Swift. The only colours Swift works out are
  derived ones with no hue of their own (`Theme.surface`, `hairline`, `highlight`) and
  `Theme.legible`, which moves a role or dim toward the foreground just far enough to
  keep 4.5:1 (7:1 `-hc`) on a ground other than the background (`Ground.surface`, the
  `chrome` tint); `DesignRuntimeTests` checks every role on every ground.
- Layering: `Views -> RunStore -> DaemonClient -> HTTP`.
  - Views never build URLs, decode JSON or keep their own copy of a run.
  - `Model/RunStore.swift` is the single `@Observable @MainActor` source of truth.
    `apply(_:)` merges events and does no I/O.
  - `Model/DaemonClient.swift` is the only file that knows HTTP. One method per route.
  - `Model/Models.swift` mirrors the daemon's Go structs; `OpenEnums.swift`, `DaemonJSON.swift`
    (dates, coders) and `EventStream.swift` (SSE) hold the rest of the wire layer.
  - `ScreenControl`, `FrameTimeline`, `StepSummary`, `RichText` are pure value types with
    a test per rule. `ControlPilot` holds the lease and send queue; it talks through
    `ControlClient` and `PilotHost` so its tests need no daemon. `PaneLayout` (the width
    classes, `ZoomState`, what `tab` cycles, every pane's frame) is pure too; the views
    place panes only through its three containers in `Views/PaneLayouts.swift`.
    `Views/InputSurface.swift` (the guest's input) and `Views/KeyRouter.swift` (the app's
    one key monitor) are the only AppKit event code.
  - `Model/RunFacts.swift` is the one derived state per run (phase, whose turn, last
    activity, duration, failures, whether the machine can be watched or driven). Every
    indicator reads `RunStore.facts(_:)`; no view works out state from raw fields.
  - `Model/RunPresentation.swift` and `Model/VerdictReview.swift` hold the pure
    presentation rules (titles, evidence, tool names, transcript grouping, connection
    state, who decided a verdict, checks against the record), each with a test.
  - `Design/` is the design system: `DesignData` (the bundled `design/`, `AppResources`),
    `Theme` (roles, brand, grounds, `ThemePreference`, the `\.theme` and `\.ground`
    environment values) and `Typography` (`BundledFonts`, `Typeface`, `TypeScale`,
    `readingStyle`, `monoStyle`, `headingStyle`). `Views/Components.swift` holds `Space`
    and `Radius` (read from the tokens), the panel, hairline, section label, spinner,
    status text and the button, toggle and switch styles; `Views/WindowChrome.swift` the
    themed root, the window, the top bar and the wordmark. No spacing, radius, colour,
    face or point-size literal elsewhere: views ask for a `TypeScale` step, a `Space`
    step and a role.
  - Keys (ADR 0005): `Model/ActionRegistry.swift` is the one list of actions (id,
    title, keys, contexts, help group, hint order, menu placement) and the pure rules
    (`ActionRules`: live contexts, enabled, why not; `KeyResolver`: what a key does).
    `Model/ActionPresentation.swift` works out the hint bar, the help and the palette
    (`Fuzzy`, `PaletteModel`); `Model/UndoWindow.swift` the undo. `Views/Keyboard.swift`
    holds `KeyboardModel` (pane focus, modes, the handlers views offer) and is the only
    file with `.keyboardShortcut` or `.onKeyPress`, each built from an entry
    (`keyboardShortcut(for:)`, `sendOnReturn`). `KeyRouter` turns each key event into a
    `KeyChord` and asks `KeyboardModel.handle`. The menu bar (`RunMenuCommands`) is built
    from `ActionRegistry.menu(_:)` and performs through the model.
- The app only calls the API: no tart, no ssh, no run directory on disk. Missing
  capability means a new daemon route.
- Never add a way to take the lease without a matching way to give it back (Give Back,
  run change, machine not ready, quit). Layout never gives it back and never strands it:
  a zoom, a width class change or a narrow window's pane switch keeps `ScreenView` in the
  tree, covered; its `InputSurface` lets go of the keyboard (`active: driving && visible`)
  and Give Back stays in the top bar. `KeyRoutingTests` drives through all of them. Quit
  waits up to 2 s for the release (`AppDelegate.applicationShouldTerminate`); every way
  out lets go of a held button first.
- The heartbeat renews with `renewControl` (`{"renew": true}`), never `takeControl`: another
  window on the same human seat may have given the screen back, and a take would undo that
  silently (#100). A refused renewal ends driving like any other failure.
- Control that breaks under the person (input or renewal fails) is never silent:
  `ControlPilot.endedReason` shows the daemon's words under the player and the run is re-read.
- While driving with the screen focused, Command shortcuts (Cmd-Q too) go to the guest.
  "Give Back", clicked with the mouse, is the way out.
- `ScreenGeometry` is the only place a view point becomes a screen fraction.
- A wheel event becomes a scroll through `InputBatch.scroll`, which negates AppKit's deltas:
  the daemon's positive `deltaY` scrolls down, AppKit's positive `scrollingDeltaY` scrolls up.
- `KeyTranslator`: anything with cmd/ctrl, or with no character (return, arrows, F-keys),
  is a named `key`; everything else is `type` with the produced characters.
- Live screen (ADR 0011): streams only while the screen shows (`ScreenView.visible`: not
  covered by a zoom or another narrow pane), following live, on a ready machine. Anything
  else stops it, and the recording shows (also whenever the stream is down, with a status
  line under the track). `ScreenStream.swift` is the pure wire
  layer; `LiveScreen` owns the connection. Frames never hop through the main actor, and the
  renderer is only touched on `VideoOutput`'s queue. A decoder failure reconnects, because a
  new connection is how the daemon is asked for a keyframe.
- `LiveScreenHostView` puts the display layer on `ScreenGeometry.fitted`, so the video
  and `InputSurface` share one letterbox. Do not size the layer any other way; the stage
  resizing (a zoom, the steps opening) only changes the view's bounds.
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
  `chromeTint` is the faint brand tint behind the sidebar, the top bar and the hint bar
  (with its help) only. Hue only for
  meaning and the brand; emphasis is weight or inverse video. Every state is also a word.
  Bright slots never carry meaning.
- Every key is an action in the one registry (ADR 0005). The hint bar, `?` help, Cmd-K
  and the menu bar read it; `ActionRegistryTests` fails on a `.keyboardShortcut`,
  `.onKeyPress`, `onExitCommand`, key monitor or menu item declared anywhere else. To add
  a key: an `ActionID` and an `ActionSpec`, then either a rule in `ActionRules.isEnabled`
  (window-level actions `KeyboardModel.perform` runs itself) or an offer from the view
  that performs it (`.offersActions(context, ids, refresh:)`, offering only what applies
  now: an action is enabled only while offered). Never a shortcut on a button.
  - Nothing destructive has a bare key (a test checks `destructive` entries). Destroy is
    Cmd-Backspace, asked inline in the top bar and the hint bar (Return destroys, any
    other key keeps it), never in a dialog.
  - Accept and dispute (the card's Reject) act only on a verdict open for review and are
    held in `RunStore.verdictUndo` for `tokens.json` `motion.undoMs`: the card and the
    hint bar count down with Undo (`u`), and `RunStore.sendHeldVerdictChoice` sends when
    it ends, only if that verdict is still the open one. A relaunch inside the window
    drops the choice. A re-check is a task and goes at once.
  - A typing context (the first responder is a text view) swallows bare keys: the
    router passes them to the field and owns only esc (leave, plus a field's own
    `.onLeave`, such as the reason form's Cancel) and Cmd-K. Driving (`InputSurfaceView`
    is first responder with the lease held) passes every key through, Cmd-Q included,
    exactly as before the registry; only a click on Give Back exits.
  - Bare keys never go on the menu bar (they would fire while typing): a menu item gets
    an entry's key equivalent only for a Command or Control chord.
  - Key labels render through `KeyLabel`: Monaspace Neon for letters and words, the
    system face for the Mac's symbols (arrows, ⏎, ⌘, ⇧, ⌫), which the mono face draws
    small or lacks.
- Motion uses only the vocabulary in ADR 0006 and `tokens.json` `motion`. Motion never
  blocks input; effects never own content; accessibility sees the final state; Reduce
  Motion makes every change instant.
- Click marks (transient, `m` toggles; static on a paused step) are the only thing drawn
  over the screen's picture.
- A run is named by a short title from its task (`RunTitle.short`, made distinct with
  `RunTitle.distinct`), never by its id. The sidebar pins "Needs You" and "Running"
  above the days; a row is the title, start time and counts, and its state in words.
- The verdict's Accept and Dispute live only in `VerdictCard`, pinned above the
  conversation (or above the stage when the conversation is hidden or has no room). A
  narrow window keeps it with the conversation, one pane away; `a` and `d` bring the
  card forward first (out of a zoom, to the conversation). A hidden conversation holds
  no card (its reason field would take the keyboard out of sight). Its headline is the
  state and who decided (`VerdictReview`); a verdict an agent accepted says no human
  reviewed it. The transcript shows the live verdict as one line, never a second card.
- Each colour means one thing: the roles above, through `Theme.color(_:on:)`,
  `tone(_:on:)` and `outcome(_:on:)`. Booting and offline carry none. Every state is also
  a glyph and a word (`StatusText`: `●` live, `!` needs you, `✓` pass, `✗` failure, the
  tick while working), and the words are one vocabulary (ADR 0003): the sidebar's
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
  / Give Back exists once, in the top bar (`RunView.actions`, published with `.topBar`).
  The top bar survives every zoom and width class.
- Nothing but click marks is drawn over the screen's picture. The driving bar sits above it, and
  position, the step under the pointer, live state and stream errors go under the track.
  The well takes the picture's shape, so there is no letterbox.
- The window has its own chrome (`.windowStyle(.hiddenTitleBar)`, `WindowConfigurator`):
  no system toolbar, title or `NavigationSplitView`. `RootView` is the themed root, the
  top bar (drawn from the `TopBarItemsKey` preference the open view sets with
  `.topBar`), the panes and the hint bar. `WindowConfigurator` keeps the traffic lights
  centred in the 44 pt top bar; AppKit puts them back on many passes, so it re-places
  them on every window update.
- Layout (ADR 0004 decision 8; design spec, Layout): the window's width picks a
  `WidthClass` from `tokens.json` `layout` (points: wide from 1280, medium from 960,
  narrow under that). `KeyboardModel` holds the class, focus and `ZoomState`;
  `KeyboardModel.layout` is the `PaneLayout` every view reads. Wide: runs column
  (`SidebarDivider`, width in `@AppStorage`), stage (screen, then the steps list),
  conversation (`ColumnDivider`, 340 to 560 pt; it gives way before the stage's 440 pt
  minimum, and the verdict card moves above the stage). Medium: `RunsStrip` (the whole
  list opens over the run while the runs have the keys, `PaneLayout.runsOverlay`) and
  the one-row `StepsTrack` (the list while the steps have the keys). Narrow: the focused
  pane alone; `tab` cycles `PaneLayout.cycle`. Ctrl-Cmd-S folds a wide window's runs to
  the strip. A width change never opens the runs over the run (`settleFocus`).
- The splits are hand-made (`SidebarDivider`, `ColumnDivider`, custom `Layout`s), not
  `.inspector` as ADR 0001 says, nor `HSplitView` or `NavigationSplitView`: those add
  hundreds of points to the window's minimum width, even while hidden. The custom
  layouts report no minimum.
- Every pane stays in the tree in every arrangement (`paneShown`: no opacity, no hits,
  hidden from VoiceOver, under what shows). Never put a pane behind an `if` on the width
  class or zoom: `ScreenView` would give the lease back on `onDisappear` and lose the
  player's place, and the composer its draft. Mark panes with `keyboardPane(_:active:)`
  and `stagePart(_:active:)` so a click on a hidden one does not move focus.
- `z` zooms the focused pane (the stage's focused part, not the whole stage); `z` or esc
  restores (esc before anything else it does), and focus moving to another pane restores.
  Zoom, a width class change, the runs overlay and the help move on
  `PaneMotion.settle` (`tokens.json` `motion.settle`), instant under Reduce Motion,
  through `.animation(_:value: layout)` in `RootView`.
- Keyboard focus is drawn, not only named in the hint bar: `focusRule` (a 2 pt brand
  rule on the pane's top edge) and the pane's label in `Theme.brandInk`, only where more
  than one pane shows.
- `?` opens `KeyHelpPanel` as a sheet over the panes above the hint bar, at most
  `layout.helpMaxShare` of the window; it never pushes the panes up.
- The steps list beside the screen follows the playhead (`PlayheadStepKey`, from
  `ScreenView`) until the person moves through it (`j`/`k`); the one-row track reads the
  same key.
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
  screen (in the top bar). Taking control from the top bar or menu brings the screen
  forward first (`KeyboardModel.showScreen`: the stage, its screen, no zoom elsewhere).
- The stage (layer 4): `ScreenView` is a title row (`SCREEN`, the machine's image, and
  `SourceLabel`: live, connecting, recording position, driving), the well, then
  `Views/PlayerBar.swift` (play, the scrubber, the step at the playhead in words under it,
  speed `f`, Follow Live). What an empty well says is `WellState` (pure). The scrubber's
  ticks are `FrameTimeline.stepTicks`: a failure always gets its tick, even with no frame
  of its own, and a plain tick never hides one.
- `Loader` (`Views/Loader.swift`, math in `Model/LoaderMotion.swift`, timings in
  `tokens.json` `motion.loader`) is the one waiting indicator for long work: booting,
  connecting, the first connection. It is reusable (the transcript may adopt it for
  "verifier is working"); its timer counts from the work's own start (`since`), never from
  when the view appeared. Reduce Motion freezes the grid, the timer keeps ticking.
- The steps read as a trace (`Model/StepTrace.swift`): done is a dim `✓`, errored is `✗`
  and the word (`exit 1`, `error`) in the failure role. The daemon records a step only when
  its call ends, so no recorded step is "running": while the verifier has the turn on a
  live machine (`StepTrace.working`) the list ends in a ticking row and the one-row track
  in a tick after its cells. Never mark the newest recorded step as running.
- Every step and tool-call row reads in plain words (`StepSummary.phrase`: "Clicked Bill
  field", "Typed 120", "Pressed ⌘A", "Ran swift test", "Took a screenshot"); the tool
  name, time and raw JSON are one click (expand) away. A click is named by the control
  under it in the latest earlier `machine_ui` read (`StepSummary.hit`, `name`), else by
  its place. `StepSummary.line` stays the terse data form (evidence captions, search), and
  every `machine_*` tool has a title (`ToolCatalog`). Input keys the daemon records for itself
  (`reader` on `machine_ui`) never show. A tool-call row whose step is not held reads its
  progress message through the same rules (`StepSummary.line(ofProgress:)`).
- Only `RunView` sets a `.navigationTitle` (the run's short title); with no run open the
  window is "Greenroom Companion". A title on a pane (the conversation) names the whole window.
- Composer keys (the registry's `send` and `newline`, through `sendOnReturn`): Return and
  Cmd-Return send; Shift-Return and Option-Return insert a line
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

- Sender identity in the transcript is the name in words plus a 2 pt left edge
  (`Theme.edge`: the person in the brand, the verifier in the foreground, the rest a
  hairline). Messages are not bubbles and all sit on the left.
- Section labels are small uppercase mono (`SectionLabel`) with whitespace around them,
  never a drawn rule (`today ─── 3`).
- Prose (`readingStyle`) sits at `tokens.json` `reading.lineHeight` (1.45): the gap is
  worked out from the face's own line (`Typeface.lineSpacing`), never a fraction of the
  size added on top.

## Gotchas

- The fonts register per process (`BundledFonts.ensureRegistered`, from `CompanionMain`
  and from every `Typeface` use). In the `.app` the resource bundle is in
  `Contents/Resources` (`scripts/bundle.sh` copies it and fails without it);
  `AppResources` looks there before `Bundle.module`, which would stop the process when
  it finds no bundle beside the executable. Xcode's build system writes the bundle with
  `Contents/Resources` inside, the Command Line Tools a flat one; `Bundle.resourceURL`
  finds both. To check a build shows Mona Sans and not a fallback, `lsof -p <pid> | grep
  otf` on the running app.
- A vertical `TextField` sizes to its placeholder wrapped at a narrow width first: a long
  placeholder makes the composer two lines tall with one line of text. Keep them short.

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
- `ScreenView` claims SwiftUI focus a turn after it appears (`Task`), once the layout has
  placed it: claimed at once it was dropped and the window gave the keyboard to the run
  search. `StepsView` beside the screen does not claim (`claimsFocus`): two claims cancel.
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
