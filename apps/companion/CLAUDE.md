# companion

SwiftPM macOS app that watches runs, speaks into their conversation and can take a
machine's screen. Vocabulary and invariants: `CONTEXT.md`. Decisions: ADR 0006
(messages), 0007 (the app), 0008 (recording), 0009 (control).

## Commands

```sh
swift run Companion                          # dev loop; needs the daemon on :7777
swift build                                  # pnpm build
swift test                                   # pnpm test
swift build -Xswiftc -warnings-as-errors     # pnpm lint
scripts/bundle.sh                            # .build/Companion.app, ad-hoc signed
scripts/install.sh                           # bundle, replace /Applications/Greenroom Companion.app, open
```

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
    a test per rule. `ControlPilot` holds the lease and send queue.
    `Views/InputSurface.swift` is the only AppKit event code.
- The app only calls the API: no tart, no ssh, no run directory on disk. Missing
  capability means a new daemon route.
- Never add a way to take the lease without a matching way to give it back (switch off,
  tab or run change, machine not ready, quit).
- `ScreenGeometry` is the only place a view point becomes a screen fraction.
- `KeyTranslator`: anything with cmd/ctrl, or with no character (return, arrows, F-keys),
  is a named `key`; everything else is `type` with the produced characters.
- Stream errors: back off 1, 2, 4 ... 10 s, re-read everything open, reconnect.

## UI rules

- One dense row per run, grouped by day. Status is a dot. Only status and verdict get
  strong colour.
- Nothing is drawn over the Screen tab's picture. Position in the recording goes under the
  track.
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
- Unknown enum values decode to `unknown(String)`, never throw. Use `JSONDecoder.daemon()`
  (RFC3339 with or without fractional seconds).
- `apps/daemon/internal/api/api.go` is the authority on shapes. A `step` event carries the
  step number (re-read `/steps`). `machine`, `verdict`, `destroyedAt` come back as
  explicit `null`. Errors are `{"error": "..."}`; a message refused on a contested verdict
  is 409.
- "Save recording" calls `GET /api/runs/{id}/recording.mp4`, which needs `ffmpeg` on the
  daemon host. Show the daemon's error.
