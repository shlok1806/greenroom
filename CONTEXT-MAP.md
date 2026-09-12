# Context map

greenroom is a multi-context repo. Each workspace package is one bounded context and
owns its own `CONTEXT.md` (glossary and invariants) and `docs/adr/` (decisions scoped to
it). System-wide decisions live in `docs/adr/` at the root.

Contexts follow the component plan in `docs/01-plan.md`. A row is "planned" until the
package exists; create its `CONTEXT.md` when the first real term or invariant is settled,
not before.

| Context    | Path                 | Owns                                                   | Status  |
| ---------- | -------------------- | ------------------------------------------------------ | ------- |
| machine    | `apps/daemon/internal/machine/` | Machine lifecycle, run recording (manifest, steps, artifacts) | exists  |
| daemon     | `apps/daemon/`       | The `greenroom` binary: HTTP server, CLI, wiring       | exists  |
| mcp        | `apps/daemon/internal/mcpserver/` | MCP tool surface over the machine manager   | exists  |
| tart       | `apps/daemon/internal/tart/` | Subprocess wrapper around the Tart CLI            | exists  |
| guest      | `packages/guest/`    | What runs inside the VM: helper for screenshot, input, accessibility tree | planned |

## Shared vocabulary (cross-context)

Terms every context uses the same way. Move a term into a context's own `CONTEXT.md`
once only that context cares about it.

- **Machine**: one running VM, cloned from a snapshot, destroyed after use.
- **Snapshot**: an immutable VM image layer (base, greenroom base, project snapshot).
- **Run**: one verification session by an agent against one machine, recorded as it happens.
- **Evidence**: the artifacts a run produces (manifest, tool calls, screenshots, recording).
