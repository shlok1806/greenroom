# companion

SwiftPM macOS app that watches runs, speaks into their conversation and can take a
machine's screen. Vocabulary and invariants: `CONTEXT.md`. Decisions: ADR 0006
(messages), 0007 (the app), 0008 (recording), 0009 (control), 0011 (live screen), and the
package's own `docs/adr/0001` (the run window), `0002` (one derived run state, colour
meanings, verdict trust, the snapshot tool) and `0003` (verdict actions, one status
vocabulary, the daemon changes the UI waits on). Design: `docs/design-spec.md` (tokens,
states, shortcuts), `docs/design-research.md`.

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

- No `.xcodeproj`, no third-party dependencies. Swift 6 language mode, strict concurrency;
  do not opt out.
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

## UI rules

- A run is named by a short title from its task (`RunTitle.short`, made distinct with
  `RunTitle.distinct`), never by its id. The sidebar pins "Needs You" and "Running"
  above the days; a row is the title, start time and counts, and its state in words.
- The verdict's Accept and Dispute live only in `VerdictCard`, pinned above the
  conversation (or above the stage when the conversation is hidden). Its headline is the
  state and who decided (`VerdictReview`); a verdict an agent accepted says no human
  reviewed it. The transcript shows the live verdict as one line, never a second card.
- Each colour means one thing (`Palette`): green pass, red failure, orange needs you,
  teal live; driving uses the system accent. Booting and offline carry none. Every state
  is also a word, and the words are one vocabulary (ADR 0003): the sidebar's
  `rowStatus` and the card's `VerdictReview.state` must say the same thing.
- A verdict's actions are only the ones the daemon's session rules accept. When it
  refuses (an agent-accepted verdict), the card says so and offers the nearest real
  action (a re-check task), never a button that will fail. The verifier stops only when
  the machine is destroyed (`RunFacts.verifierListens`); then the re-check is disabled and
  the card and composer say nothing will answer.
- The verdict's drafts (the action and its reason, whether evidence was opened, the
  accept confirmation) live in `RunStore.verdictDrafts`, per run and verdict. A draft ends
  when its verdict changes or closes, or its run leaves the list; a resync keeps it. The verdict
  flow has no sheet, alert or dialog: accepting rebuilds the card, and a sheet whose
  presenter goes away leaves the window unable to take a click. Ask inline.
- The player shows one source chip (live, connecting, recording, driving). Take Control
  / Give Back exists once, in the toolbar.
- Nothing is drawn over the Screen stage's picture. The driving bar sits above it, and
  position, the step under the pointer, live state and stream errors go under the track.
  The well takes the picture's shape, so there is no letterbox.
- The conversation column is a hand-made split (`ColumnDivider`), not `.inspector` as
  ADR 0001 says, nor `HSplitView`: inside `NavigationSplitView` both add hundreds of points to the window's
  minimum width, even while hidden. Below 1000 pt `RootView` folds the sidebar, only while
  shrinking, so a sidebar shown by hand stays.
- While driving, keys follow focus: the screen while it was clicked last (Command
  shortcuts included), the composer once it is clicked. "Give Back", clicked, returns the
  screen (in the bar above the picture, the player and the toolbar). Taking control from
  the toolbar or menu switches the stage to the Screen first.
- Every list row has a readable summary (`StepSummary`), not raw JSON.
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
- A `Text` with `.fixedSize(horizontal: false, vertical: true)` in an empty state can make
  the window grow to thousands of points tall when first laid out narrow. Let it wrap.
- "Export recording" calls `GET /api/runs/{id}/recording.mp4`, which needs `ffmpeg` on the
  daemon host. Show the daemon's error.
