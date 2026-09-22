# companion

The macOS app that watches runs: see the machines, read the conversation, look at the
screen, take the human seat in the transcript, and take the machine's mouse and keyboard
when watching is not enough. ADR 0007 at the repo root decides what it is; ADR 0006
decides what a message is; ADR 0009 decides what driving a screen means.

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
- `Model/ScreenControl.swift` and `Model/ControlPilot.swift` are the Screen tab's control
  half (ADR 0009). The first is pure: where a click on a letterboxed picture lands, what a
  key press means, how a queue of actions is trimmed. The second holds the lease and the
  send queue. `Views/InputSurface.swift` is the only AppKit event code in the app.
- `Model/FrameTimeline.swift`, `Model/StepSummary.swift` and `Model/RichText.swift` are the
  rest of the pure layer, and they follow `ScreenControl`'s rule: a value type, no I/O, and a
  test for every rule rather than an eyeball over a screenshot. They exist because each
  answers a question a view kept answering badly on its own - where a frame sits on a track
  of a given width and where the steps fall along it, what a step actually did in one line,
  and how much of an agent's Markdown a transcript honours.

## What the interface is trying to be

The window is a session inspector, not a debugger's dump. Three rules carry most of it, and
a change that breaks one of them is a regression even when it compiles.

- **Density with restraint.** The run list is read far more often than it changes: one dense
  row per run, grouped by day, status as a dot rather than the word "finished" twenty times
  down a column. Status and verdict are the only things allowed a strong colour. A role, a
  count and a state are told apart by weight and position first.
- **One focal area.** On the Screen tab the picture gets the room and nothing is drawn on top
  of it: a label over the guest's own menu bar hides the thing the person came to see. Where
  in the recording you are belongs under the track, in time, frames and steps.
- **Evidence is read, not expanded.** A row that says only `machine_input` is a log. Every
  list row carries a summary a person can read without opening it (`StepSummary`), and the
  transcript's progress rows use the step's own record when the app holds it, rather than the
  raw JSON the message text carries.

Two formatting rules that are easy to get wrong:

- **A run's clock time comes from `createdAt`, never from its id.** The daemon names a run in
  UTC, so parsing `20260921-050808` for a label put the list hours away from every other time
  in the window. `Chrome.runHash` takes the identifying tail from the id; the time is
  formatted from the date.
- **Spans go through `Chrome.clock`.** Minutes and seconds alone turn an eight-hour run into
  "476:12".

## Rules from ADR 0007 and ADR 0009

- **The app only calls the API.** It never shells out to `tart`, never opens an SSH
  connection, and never reads or writes a run directory on disk. If something is not a
  route, the app cannot do it, and the answer is a route on the daemon, not a shortcut
  here.
- **The app does not drive the run, but it may drive the screen.** No creating machines,
  no syncing code, no running commands: the coding agent is the operator. ADR 0009 adds
  one exception, the mouse and keyboard, and it is an exception because it is louder in
  the transcript than anything else the app does, not quieter (see below).
- **Every control lands in the conversation.** A screenshot, a destroy or a message from
  the app is written into the run's transcript by the daemon, so the coding agent learns
  of it on its next `agent_wait`. The app must never gain a way to change a run that the
  transcript does not show.
- **A frame is decoded off the main actor.** `RunStore.frameImage` turns the JPEG into pixels
  in a detached task and caches the finished image. `NSImage(data:)` on its own defers the
  decode to the first draw, which puts it back on the main thread, and scrubbing a
  two-thousand-frame recording asks for that many times a second.
- **The app never polls for screenshots.** The daemon captures frames on its own and
  pushes each one down the event stream as `event: frame`; the Screen tab is a player
  over `GET /api/runs/{id}/frames` and `.../frames/{file}`, not a timer that asks for a
  fresh image. "Screenshot" in the toolbar is the one place the app still triggers a
  capture itself, and it is a single request, never a loop. "Save recording..." asks the
  daemon to assemble the run's frames into an mp4 (`GET /api/runs/{id}/recording.mp4`),
  which needs `ffmpeg` on the daemon host; a daemon without it answers with an error the
  app surfaces rather than a generic failure.
- **Taking the screen is a lease, and the lease is given back.** "Take control"
  (ADR 0009) asks the daemon for the machine's mouse and keyboard, which it grants to one
  holder at a time and expires after a minute of silence. The app gives it back when the
  switch goes off, when the Screen tab or the run changes, when the machine stops being
  ready, and when the app quits. Never add a path that takes the lease without one that
  gives it back: a machine that believes a person is at its keyboard captures frames four
  times as often and refuses everyone else.
- **The app sends fractions, never pixels.** A click is `0.25, 0.5` of the guest's screen.
  The app is looking at a resized JPEG scaled to fit a window it does not control, so it
  cannot know the resolution and must not try; the daemon multiplies. `ScreenGeometry` is
  the one place that turns a point in a view into a fraction, and it is pure so the
  arithmetic is tested rather than trusted: a wrong fraction is a click in the wrong place
  on someone else's machine.
- **A shortcut is a key, ordinary typing is text.** `KeyTranslator` sends anything with
  command or control held, and anything with no character to type (return, tab, the
  arrows, the function keys), as a named `key` with modifiers; everything else goes as
  `type` with the characters the keyboard produced. That is what makes accented and
  non-Latin input work without the app knowing any keyboard layout, and command-A arrive
  as Select All rather than as a control character.
- **The stream is a hint, the API is the truth.** SSE events are best effort. On any
  stream error the store backs off (1 s, 2 s, 4 s, capped at 10 s), re-reads the run list
  and every open run, and reconnects.

## Reading the event stream

Never cut the stream into lines with `URLSession.AsyncBytes.lines`. That sequence drops
empty lines, and in server-sent events the empty line is not filler: it is the terminator
that ends a frame. Fed through `.lines`, every `event:`/`data:` pair arrived but no frame
ever closed, `SSEParser.dispatch()` never ran, and the app yielded exactly zero events
while the footer still said "Live" — the transcript only moved when something else
refetched it, which is what a human sending a message happens to do. `SSELineSplitter`
cuts the bytes itself and keeps the blank lines; `SSELineSplitterTests` holds that line.

How that was pinned down, if the stream ever goes quiet again:

```sh
curl -N http://127.0.0.1:7777/api/events      # the daemon flushes each frame at once
swift run Companion 2>&1 | tee /tmp/companion.log   # with prints in events()/apply()
```

The daemon's side was never in doubt — `curl -N` showed the blank lines and delivered a
posted message within the same second. The app's log showed the same lines *without the
blank ones*, and not one call into `RunStore.apply`. Compare those two outputs first: it
says in one step whether the stream, the parse or the merge is at fault.

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
