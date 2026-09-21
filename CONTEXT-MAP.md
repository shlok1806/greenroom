# Context map

greenroom is a multi-context repo. Each workspace package is one bounded context and
owns its own `CONTEXT.md` (glossary and invariants) and `docs/adr/` (decisions scoped to
it). System-wide decisions live in `docs/adr/` at the root.

Contexts follow the component plan in `docs/01-plan.md`. Status means:

- **planned**: no code yet.
- **code**: the package exists; its `CONTEXT.md` is not written. Create it when the first
  real term or invariant is settled, not before.
- **documented**: `CONTEXT.md` exists.

| Context    | Path                                | Owns                                                                    | Status  |
| ---------- | ----------------------------------- | ----------------------------------------------------------------------- | ------- |
| daemon     | `apps/daemon/`                      | The `greenroom` binary: HTTP server, CLI, wiring                        | code    |
| machine    | `apps/daemon/internal/machine/`     | Machine lifecycle, run recording (manifest, steps, artifacts)           | code    |
| mcp        | `apps/daemon/internal/mcpserver/`   | MCP tool surface over the machine manager and the session               | code    |
| tart       | `apps/daemon/internal/tart/`        | Subprocess wrapper around the Tart CLI                                  | code    |
| verifier   | `apps/daemon/internal/verifier/`    | greenroom's own agent: the turn loop over NIM (ADR 0005)                | code    |
| session    | `apps/daemon/internal/session/`     | The conversation per run: messages, participants, agreement (ADR 0006)  | code    |
| api        | `apps/daemon/internal/api/`         | Read and control HTTP API for the companion (ADR 0007)                  | code    |
| companion  | `apps/companion/`                   | macOS app: watch runs, see the screen, talk to the agents (ADR 0007)    | documented |
| guest      | `apps/daemon/internal/machine/guest/` | What runs inside the VM: the input helper the daemon compiles there (ADR 0009) | code    |

## Shared vocabulary (cross-context)

Terms every context uses the same way. Move a term into a context's own `CONTEXT.md`
once only that context cares about it.

- **Machine**: one running VM, cloned from a snapshot, destroyed after use.
- **Snapshot**: an immutable VM image layer (base, greenroom base, project snapshot).
- **Run**: one verification session by an agent against one machine, recorded as it happens.
- **Evidence**: the artifacts a run produces (manifest, tool calls, screenshots, recording).
- **Control lease**: the right to drive one machine's screen, held by one seat (human,
  verifier) at a time and recorded in the run's conversation (ADR 0009).
