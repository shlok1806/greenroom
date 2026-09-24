# 0007. A macOS companion app that watches runs and speaks, but does not drive

Date: 2026-09-18
Status: accepted (implemented). The "what it may not do" rule is narrowed by ADR 0009, which lets a
person take the machine's mouse and keyboard under a recorded lease. Watch mode over VNC,
expected here as the eventual Screen tab, is retired by ADR 0016. Everything else
here stands.

## Context

Today the only views of a run are daemon log lines, the run directory on disk, and, with
watch mode, a VNC window of the screen. There is no way to see which runs exist, what
the coding agent asked for, what the verifier is doing right now, or to say anything to
either of them while they work. ADR 0006 gives every run a conversation that a human
could join; this decision is about the surface they join it from.

The person watching wants three things: to see (runs, machines, the screen, the
transcript, the evidence), to add context and talk to the agents, and to intervene
(take a screenshot, kill a machine). They do not want to become the operator. The
coding agent is the operator; the point of greenroom is that it runs without you.

## Decision

**The daemon grows a read and control HTTP API** under `/api/` on the same listener as
`/mcp`. It is thin: it calls the same `machine.Manager` and session store the MCP tools
call. There is no state the API has that the tools do not, and no state the tools have
that the API cannot show.

```
GET  /api/runs                            every run: live machine status, verdict, last activity
GET  /api/runs/{id}                       manifest plus live machine (status, ip, vncUrl when watched)
GET  /api/runs/{id}/steps                 steps.jsonl as JSON
GET  /api/runs/{id}/messages?after=N      conversation.jsonl as JSON
GET  /api/runs/{id}/artifacts/{name}      a file from the run directory (screenshots)
GET  /api/events?runId=                   SSE: run.created, run.status, step, message, run.destroyed
POST /api/runs/{id}/messages              { kind, text, replyTo? } from human
POST /api/runs/{id}/screenshot            capture now; the PNG lands in the run as a step
POST /api/runs/{id}/destroy
```

`/api/events` is one stream for the whole daemon, filterable by run. Every write path
that changes a run already goes through the manager or the store, so those two places
publish events and nothing else has to remember to.

**The companion is a native macOS app**, SwiftUI, in `apps/companion/`. It is a Swift
package with an executable target so `swift build` and `swift run` work without an
Xcode project file, and a `package.json` shim so `pnpm build` and `pnpm test` at the
root cover it like every other workspace. It talks to `http://127.0.0.1:7777` and
nothing else.

**What the companion may do**, in order of how much it changes the world:

- Read everything: runs, machine status, steps, artifacts, the conversation, live.
- Show the screen: screenshot polling first, at a rate the daemon can afford (one
  capture every 2 s while the tab is visible). Embedding VNC is the better answer and is
  blocked by issue #7 (a graphics-enabled VM restarts its GPU); the screenshot path does
  not depend on it.
- Speak: `note`, `answer`, `accept`, `dispute`, `task`. This is the human seat in the
  ADR 0006 conversation. `accept` and `dispute` from here close a contested verdict.
- Intervene: screenshot and destroy. Both land in the conversation as `event` messages
  so the coder learns what happened on its next `agent_wait`.

**What it may not do**: create machines, sync code, run commands. Each of those changes
what the coding agent believes about the machine without telling it. `exec` from the
app is the likely first exception; if it is added, its output goes into the
conversation as a `note` from `human`, under the same rule as screenshot and destroy.
The companion never holds a session with the daemon that the tools cannot see.

**No authentication.** The daemon listens on loopback and so does the app. A token comes
with the first non-loopback listener, not before.

## Consequences

**Two client surfaces, one core.** `internal/mcpserver` and `internal/api` are siblings
over `internal/machine` and `internal/session`. Neither may hold logic the other needs.
The daemon CLAUDE.md layering rule applies: anything stateful belongs below both.

**Swift enters the repo.** The `Go for deployables, TypeScript only where needed` rule
in ADR 0004 gets a third language for one reason: a native Mac app is the only kind that
can later embed a VNC view, show a menu bar item, and feel like a Mac tool. A web view
served by the daemon was considered and rejected on those grounds; it would also invite
the daemon to grow a UI, which it should not. The companion's `CLAUDE.md` owns Swift
conventions; nothing else in the repo needs to know Swift exists.

**Screenshots at 2 s cost the machine.** `screencapture` in the guest is a real command
on a VM that is also building. Polling is paused when the screen tab is hidden and
stops when the machine is not `ready`. Watch mode over VNC is the fix once #7 is closed.

**Events are best-effort.** SSE clients that disconnect miss events; on reconnect the
app re-reads `/api/runs` and each run's messages `after` the last `seq` it saw. The
files on disk are the truth, the stream is a hint.

**Deferred.** Notifications (a verdict arrived, a question is waiting), several daemons,
remote hosts, the menu bar item, a run viewer for finished runs that no longer have a
machine (the API already serves them; the app shows them read-only).
