# companion

The macOS app that watches runs: see the machines, read the conversation, look at the
screen, and take the human seat in the transcript. ADR 0007 at the repo root decides what
it is; ADR 0006 decides what a message is.

Swift is the one exception to "Go for deployables" in the root CLAUDE.md, and it exists
for one reason: a native app is the only kind that can later embed a VNC view and feel
like a Mac tool. Nothing else in the repo needs to know Swift exists.

## Commands

```sh
swift build            # pnpm build runs this through package.json
swift test             # pnpm test
swift run Companion    # opens the window; this is the dev loop
swift build -Xswiftc -warnings-as-errors   # pnpm lint; warnings are failures

scripts/bundle.sh      # pnpm bundle; builds .build/Companion.app
scripts/install.sh     # pnpm install:app; bundles, installs, opens
```

To get the app into `/Applications`, run
`pnpm --filter @greenroom/companion install:app` (or `scripts/install.sh` directly). It
builds a release bundle, replaces `/Applications/Greenroom Companion.app` and opens it.
`scripts/bundle.sh` stops one step earlier and leaves the bundle in `.build/`. The bundle
is ad-hoc signed (`codesign --sign -`), which is enough for it to keep its identity
between launches and not enough to hand to anyone else. Its icon is drawn at build time
by `scripts/make-icon.swift`, so no artwork is checked in. `swift run Companion` stays
the dev loop: it is faster and needs no install.

The daemon must be running at `http://127.0.0.1:7777` for the app to show anything. The
app never starts it. `GREENROOM_URL` in the environment overrides that address
(`DaemonClient.defaultBaseURL`), for a daemon on another port or host.

This is a SwiftPM package with no `.xcodeproj` and no third-party dependencies. Keep it
that way: a dependency has to earn itself against the whole build being `swift build`.
Swift 6 language mode is on; new code is written to pass strict concurrency rather than
opting out of it.

## Layering

```
Views  ->  RunStore  ->  DaemonClient  ->  HTTP
```

- `Views/` render what the store holds and call store methods. A view never builds a URL,
  never decodes JSON, never holds its own copy of a run.
- `Model/RunStore.swift` is the single `@Observable @MainActor` source of truth: the run
  list, the open details, the transcripts, the steps, and the event stream that keeps them
  fresh. `apply(_:)` is the merge, and it does no I/O, so it is testable on its own.
- `Model/DaemonClient.swift` is the only file in the app that knows HTTP exists. One
  method per route in ADR 0007.
- `Model/Models.swift` holds the wire types. They mirror the daemon's Go structs.

## Rules from ADR 0007

- **The app only calls the API.** It never shells out to `tart`, never opens an SSH
  connection, and never reads or writes a run directory on disk. If something is not a
  route, the app cannot do it, and the answer is a route on the daemon, not a shortcut
  here.
- **The app does not drive.** No creating machines, no syncing code, no running commands.
  The coding agent is the operator. The companion watches, speaks and, at most,
  intervenes.
- **Every control lands in the conversation.** A screenshot, a destroy or a message from
  the app is written into the run's transcript by the daemon, so the coding agent learns
  of it on its next `agent_wait`. The app must never gain a way to change a run that the
  transcript does not show.
- **The app never polls for screenshots.** The daemon captures frames on its own and
  pushes each one down the event stream as `event: frame`; the Screen tab is a player
  over `GET /api/runs/{id}/frames` and `.../frames/{file}`, not a timer that asks for a
  fresh image. "Screenshot" in the toolbar is the one place the app still triggers a
  capture itself, and it is a single request, never a loop. "Save recording..." asks the
  daemon to assemble the run's frames into an mp4 (`GET /api/runs/{id}/recording.mp4`),
  which needs `ffmpeg` on the daemon host; a daemon without it answers with an error the
  app surfaces rather than a generic failure.
- **The stream is a hint, the API is the truth.** SSE events are best effort. On any
  stream error the store backs off (1 s, 2 s, 4 s, capped at 10 s), re-reads the run list
  and every open run, and reconnects.

## Decoding

Unknown enum values decode into an `unknown(String)` case rather than throwing. The
daemon may grow a message kind before the app knows about it, and a new kind must never
crash a window. Dates are RFC3339 with or without fractional seconds; use
`JSONDecoder.daemon()` for every response.

`apps/daemon/internal/api/api.go` is the authority on every shape here. Two of its
choices the models have to match: a `step` event on the stream carries the step's
*number*, not the record (the store re-reads `/steps` when one arrives, and the decoder
accepts a whole record too), and `machine`, `verdict` and `destroyedAt` come back as
explicit `null` rather than being left out. A failing route answers
`{"error": "..."}`, and a message the conversation will not accept because the verdict is
contested answers 409.
