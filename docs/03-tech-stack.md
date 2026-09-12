# Tech stack report

Status: proposal for discussion. Becomes ADR 0004 once agreed.

## What kind of application this is

greenroom v0 is a **background service on the developer's Mac** with no GUI. It owns
VMs, records runs, and speaks MCP so Claude Code can call it. A small CLI starts it,
pulls images, and lists runs. That is the whole application.

The hosted version later is the same service running on a fleet of Mac minis behind an
API. Nothing in v0 should assume a single host or a single user, but nothing in v0
should build for more than one either.

## Recommended stack

| Layer                 | Choice                                  | Why                                                                 |
| --------------------- | --------------------------------------- | ------------------------------------------------------------------- |
| Language              | TypeScript on Node 26                   | One language for daemon, MCP, protocol, CLI. Best MCP SDK. Team fluency. |
| VM engine             | Tart CLI, driven as a subprocess        | Already wraps Virtualization.framework, images exist, clone/suspend/exec built in. |
| MCP                   | `@modelcontextprotocol/sdk`, Streamable HTTP transport | Official SDK. HTTP lets the daemon outlive Claude Code sessions and is the same interface hosted. |
| HTTP server           | Hono                                    | Small, standards-based, runs on Node now and on anything later.     |
| Schemas               | Zod                                     | The MCP SDK uses it for tool input; reuse for protocol types.       |
| Run storage           | `node:sqlite` + files on disk           | Built into Node, zero deps, single file. Evidence (PNGs, logs) as plain files next to it. |
| Guest exec            | `tart exec` (guest agent), ssh fallback | No key management for commands. ssh + rsync for file sync.          |
| File sync             | system `rsync` over ssh                 | Do not reimplement rsync. Respects excludes, fast on repeat syncs.  |
| Screenshot            | `screencapture` in the guest            | Built into macOS. Copy back over the same channel.                  |
| Computer use (M2)     | Small Swift CLI in the guest            | CGEvent for input, AXUIElement for the accessibility tree. Only Swift can do this. |
| CLI                   | Same package as the daemon, `commander` | `greenroom daemon`, `greenroom images pull`, `greenroom runs list`. |
| Tests                 | Vitest; Tart-backed e2e behind a flag   | Unit tests everywhere; the e2e suite needs a Mac with Tart.         |
| Build                 | tsc only                                | No bundler until there is a reason.                                 |

## Why the daemon is TypeScript and not Go or Swift

The daemon's job is orchestration: spawn `tart`, talk to a guest, move files, write a
manifest, serve MCP. None of that is CPU-bound and all of it is ecosystem-bound.

- **Go** gives a single static binary and is a natural daemon language. The cost is a
  second language and a younger MCP SDK. Worth it if distribution becomes the problem.
- **Swift** could call Virtualization.framework directly and drop Tart. Tart already
  does that well and is maintained. Swift is reserved for the in-guest helper, where it
  is the only option.
- **Python** would work. TypeScript wins on the MCP SDK and on being what Claude Code
  users already have installed.

## Why MCP over HTTP from a persistent daemon, not a stdio server

A stdio MCP server is a child of the Claude Code session. When the session ends, the
process dies and the VM it owned is orphaned or must be killed. A daemon that Claude
Code connects to over `http://localhost` keeps the VM alive across sessions, can be
started once by launchd, and is the exact interface the hosted product exposes. Claude
Code adds it with one command:

```sh
claude mcp add --transport http greenroom http://localhost:7777/mcp
```

## Guest side

Cirrus images ship sshd with `admin`/`admin` and, in recent versions, the Tart guest
agent that `tart exec` talks to. The spike confirms which of these work out of the box
on `macos-tahoe-base`. If `tart exec` works, commands and screenshots go through it and
ssh is only for rsync. If not, everything goes over ssh with a key injected at first
boot.

## What is deliberately not chosen yet

- **Web UI for runs.** Not until someone wants to look at a run outside the terminal.
  When it comes it is React, served by the same daemon.
- **Auth, multi-tenancy, control plane, Postgres, queue.** Hosted concerns.
- **Distribution.** `pnpm` from the repo is enough for v0. Homebrew or a single
  binary later.

## How it maps onto the repo

```
apps/daemon        the service and its CLI
packages/mcp       tool definitions and the MCP transport (imported by the daemon)
packages/protocol  Zod schemas and types shared by daemon, mcp, and future clients
packages/guest     the Swift helper, added at M2
```

`packages/mcp` stays separate from the daemon only so the tool surface can be reused by
a future stdio shim or SDK. If that separation costs anything in v0, fold it into the
daemon.

## Decisions needed

1. TypeScript for the daemon (vs Go). Recommendation: TypeScript.
2. MCP over HTTP from a launchd daemon (vs stdio child process). Recommendation: HTTP daemon.
3. Hono + node:sqlite + Zod as the boring core. Any objections?
4. Port and paths: `localhost:7777`, state in `~/.greenroom/`. Fine?
