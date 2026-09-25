# Context map

Each context owns a glossary and invariants in its own `CONTEXT.md` once it has settled
terms. System-wide decisions are in `docs/adr/`.

Status: **code** means the package exists with no `CONTEXT.md` yet (write one when the
first real term settles). **documented** means `CONTEXT.md` exists.

| Context   | Path                                  | Owns                                                        | Status     |
| --------- | ------------------------------------- | ----------------------------------------------------------- | ---------- |
| daemon    | `apps/daemon/`                        | The `greenroom` binary: HTTP server, CLI, wiring            | code       |
| machine   | `apps/daemon/internal/machine/`       | Machine lifecycle, run recording, computer use, pty sessions | code       |
| guest     | `apps/daemon/internal/machine/guest/` | The input helper compiled inside the VM (ADR 0009)          | code       |
| mcp       | `apps/daemon/internal/mcpserver/`     | MCP tools over the manager and the conversation             | code       |
| api       | `apps/daemon/internal/api/`           | HTTP API and SSE for the companion (ADR 0007)               | code       |
| session   | `apps/daemon/internal/session/`       | A run's conversation: messages, verdicts (ADR 0006)         | code       |
| verifier  | `apps/daemon/internal/verifier/`      | greenroom's own agent, NIM or manual (ADR 0005)             | code       |
| nim       | `apps/daemon/internal/nim/`           | Client for NVIDIA NIM's OpenAI-compatible API               | code       |
| tart      | `apps/daemon/internal/tart/`          | Tart CLI wrapper, pinned version, long-lived execs          | code       |
| companion | `apps/companion/`                     | macOS app: watch runs, see the screen, talk, take control   | documented |
| images    | `images/`                             | Image layers and the dialog gate (docs only)                | docs       |

## Shared vocabulary

- **Machine**: one running VM, cloned from an image, destroyed after use.
- **Image**: an immutable VM to clone from (Cirrus base, `greenroom-base`,
  `greenroom-xcode<N>`).
- **Run**: one agent's session against one machine, recorded as it happens. Outlives the
  machine.
- **Evidence**: what a run leaves on disk: manifest, steps, screenshots, frames, conversation.
- **Conversation**: the run's shared transcript between coder, verifier, human and system.
- **Control lease**: the right to drive one machine's screen, held by one seat at a time
  (ADR 0009).
- **Live screen**: a machine's screen as H.264, streamed from the guest while someone
  watches (ADR 0011). Not the recording: frames stay the run's evidence.
