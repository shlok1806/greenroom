# Plan: conversations and the companion

**Status: done.** This was the build plan (2026-09-18) for
[ADR 0006](adr/0006-one-conversation-per-run.md) (one conversation per run) and
[ADR 0007](adr/0007-companion-app.md) (companion app). Read the ADRs and the package
docs for how it works now.

What each phase produced:

1. **Session store** - `apps/daemon/internal/session`: `conversation.jsonl` per run,
   `seq` assigned under lock, blocking wait, verdict state written to the manifest.
2. **Verifier as an actor** - `apps/daemon/internal/verifier`: one goroutine per run,
   turns started by conversation messages, `ask` ends a turn with a question, verdicts
   cite evidence, disputes capped by `-max-disputes`.
3. **MCP tools** - `agent_send`, `agent_wait` (50 s cap), `agent_transcript`.
   `machine_verify` removed.
4. **HTTP API** - `apps/daemon/internal/api`: ADR 0007 routes plus SSE at `/api/events`.
5. **Companion** - `apps/companion`, SwiftUI, no dependencies.
6. **End to end** - the conversation path in `apps/daemon/e2e_test.go`, plus manual
   sessions.

Changed since the plan: the Screen tab plays daemon-captured frames (ADR 0008) instead of
polling screenshots, and the companion can drive the screen under a lease (ADR 0009).
