# Plan: conversations and the companion

Implements ADR 0006 (one conversation per run) and ADR 0007 (companion app). Written
2026-09-18. Phases are in dependency order; each one ends green (`go test ./...`, or
`swift build`) and each one is usable on its own.

## Phase 1. Session store (Go, `internal/session`)

The conversation as a file plus a waiter.

- `Message` type with the fields and kinds in ADR 0006.
- `Store` per run: `Append(msg) (seq, error)` writes one line to `conversation.jsonl`
  under a mutex and claims `seq` there, the recorder's rule. `After(seq) []Message`
  reads. `Wait(ctx, after) []Message` blocks on a broadcast until something newer exists.
- `Open(dir)` reloads an existing file so the daemon restarts mid-conversation.
- Verdict bookkeeping: `Verdict()` returns the latest proposal, whether it is accepted,
  contested, and the dispute count. Written back into `manifest.json` through the
  machine package.
- Tests: append and read back, wait wakes on append, reload from disk, seq is unique
  under 100 concurrent appends.

Nothing here knows about models or MCP.

## Phase 2. Verifier as an actor (Go, `internal/verifier`)

- `Run(task) Report` becomes `Turn(ctx, runID)`: build the model context from the
  store (system prompt, then every message projected to the OpenAI shape, `progress`
  entries as assistant tool calls plus tool results), loop as today, and post
  `progress`, `question` or `verdict` back into the store. Fold in messages that
  arrived mid-turn before each model call.
- New `ask` tool ends the turn with a `question`.
- `report_verdict` gains `evidence` (artifact paths and step numbers) and the prompt
  says a verdict must cite them.
- A dispute prompt: when the last human or coder message is a `dispute`, the model is
  told to re-examine and to say whether it changes its mind and why.
- A `Session` object per run owns the actor: a goroutine that waits for a message that
  starts a turn (`task`, `answer`, `dispute`), runs `Turn`, and posts an `event` if the
  turn errors. One turn at a time. Created when the run is created if a verifier is
  configured; stopped on destroy.
- Tests keep the scripted model. New: a task then a question then an answer then a
  verdict across three turns; a dispute produces a revised verdict; the third dispute is
  refused and the verdict is `contested`; context is rebuilt from disk after a restart.

## Phase 3. MCP tools (Go, `internal/mcpserver`)

- `agent_send`, `agent_wait`, `agent_transcript` as in ADR 0006. `agent_wait` caps its
  timeout at 50 s like `machine_wait`.
- `machine_verify` removed; its test becomes an `agent_*` test that drives a scripted
  verifier through a real MCP client.
- `machine_destroy` posts a `system` event to the conversation first.

After this phase a headless Claude Code session can run a whole verify loop, including
answering a question, with the tools alone.

## Phase 4. HTTP API (Go, `internal/api`)

- The routes in ADR 0007 on the existing mux. JSON in and out; `encoding/json` only.
- SSE at `/api/events`. The manager and the store each get a small event publisher;
  the API subscribes and writes `event:` lines. Heartbeat every 15 s so proxies and
  URLSession keep the connection.
- Run listing merges live machines from the manager with finished runs found on disk.
- Tests with `httptest`: list, detail, messages after N, a human `note` appears to a
  waiting `agent_wait`, SSE delivers a message event, destroy posts an event.

## Phase 5. Companion app (Swift, `apps/companion`)

- `Package.swift`, executable target `Companion`, macOS 15 minimum, SwiftUI, no
  dependencies. `package.json` shim: `build` is `swift build`, `test` is `swift test`.
- `DaemonClient`: `URLSession` for JSON, an `AsyncStream` over the SSE body.
- Model: `RunStore` (`@Observable`) holding runs keyed by id, each with machine status,
  messages and steps; applies SSE events, re-syncs on reconnect.
- Views: a sidebar of runs with status and verdict; a detail split into Transcript
  (messages with a composer and kind picker; accept and dispute buttons on a verdict;
  reply on a question), Screen (latest screenshot, polling while visible), Steps (the
  evidence timeline with thumbnails); a toolbar with Screenshot and Destroy.
- `apps/companion/CLAUDE.md` with the build commands and the one rule: the app calls
  the API, it never calls tart, ssh or the run directory directly.

## Phase 6. End to end

- Extend the `tart`-tagged e2e test: create, sync, `agent_send` a task, answer its
  question, `agent_wait` to a verdict, `dispute` it, get a revised verdict, accept.
- A manual session: headless Claude Code drives a run while the companion is open and a
  human answers a question from the app. Record what broke in `docs/07-test-plan.md`.

## Out of scope here

Issue #7 (GPU crash dialog in graphics mode), the greenroom base image, the GUI app
milestone in M1, VNC embedding, notifications, auth. Each stays where it is listed.
