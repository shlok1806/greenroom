# 0008. greenroom never calls a run "yours", and says who ends one

Date: 2026-09-28
Status: accepted.

## Context

Issue #217. Several agents share one daemon. At the host limit `machine_create` answered every
caller with `host is at its limit of 2 machines; call machine_destroy on one of runId A (idle
5m), runId B (active 7s ago) first`: every run this daemon held was offered as the caller's own.
An agent that had created neither destroyed A, which another agent and a person in the Companion
were using ("idle 5m" counts tool steps and messages, not a person watching the screen). Minutes
later a third session called `run_finish` on a run it had not created, which wrote its outcome
and summary into that run's proof and destroyed the machine. The step and the transcript said
only `machine_destroy` and `machine destroyed`, and the daemon log had no line for either.

The issue asks the daemon to call "yours" only the runs the calling session created, and to
refuse `machine_destroy` and `run_finish` on another session's run. That needs to know who is
calling, and the daemon cannot:

- `/mcp` is stateless, and clients now speak the 2026-07-28 protocol (SEP-2575: `server/discover`
  and per-request `_meta`), which has no session at all. There is no `Mcp-Session-Id` to key on.
- The only per-request identity is `clientInfo` and the `User-Agent`, and every Claude Code
  session sends the same one (`claude-code`, measured: no conversation id reaches an MCP
  server).
- An owner key returned by `machine_create` and required by `machine_destroy` and `run_finish`
  would be proof, but a client keeps the tool schemas it fetched when it connected (daemon ADR
  0007), so every running agent would be refused on its own runs until it reconnects, and an
  agent that lost the key with its context could never finish its run. Hiding other runs'
  runIds instead breaks what `machine_list` is for: picking a run back up.

What the daemon does know: the runs it holds, their names and creating clients, their idle time,
and whether a person is watching a run's live screen or holding its screen-control lease.

## Decision

1. **No run is called "yours".** At the limit, `machine_create` lists every run on the host as
   a run in progress: its name, the client that created it, how long ago, idle time, and a person
   watching or driving it. Runs of this daemon are named by the last 8 characters of their runId
   only (`...2a0bd799`), enough for an agent to recognise a runId its own `machine_create`
   returned, never a full runId to paste into `machine_destroy`. The message says greenroom
   cannot tell which run is the caller's, that a run is the caller's only if its own
   `machine_create` returned that runId, and to wait and retry otherwise. It never tells anyone
   to destroy anything.
2. **Presence counts.** `machine_list` gains `watchers` (live-screen viewers, the Companion's
   screen) and `driver` (who holds the screen-control lease, when a person does), and the limit
   message words them ("a person is watching its screen", "a person is driving it"), so an idle
   run a person is looking at does not read as abandoned.
3. **Every end says who.** `machine_destroy` and `run_finish` record their caller: the step's
   `by` is `agent (<client>)` (the client name as `clientName` finds it, or `agent` alone), the
   transcript event reads `machine destroyed by agent (<client>) through machine_destroy` (or
   `run_finish`), and the daemon log has an info line with the runId, the tool and the caller.
   The Companion's destroy records `by` as `human`. The event keeps the `machine destroyed`
   prefix every reader matches on.
4. **The descriptions say it.** `machine_destroy`, `run_finish` and `machine_list` tell an agent
   that only a runId its own `machine_create` returned is its to end, and that a run it did not
   create is another agent's or a person's work, however idle.
5. Refusing on ownership stays open until an MCP client can be told apart from another: a
   protocol-level session or client instance id, or a client that keeps a run key across its
   context. When one exists, `machine_destroy` and `run_finish` refuse another caller's run.

## Consequences

- An agent at the limit is never pointed at another's machine, and a person looking at a run
  shows in both places agents read before they act.
- A careless or confused agent can still end a run it did not create with a full runId from
  `machine_list`; when it does, the step, the transcript and the log name the client, so the
  person reading the run knows it was not its own agent.
- The limit message is longer. Runs of another daemon or outside greenroom read as before:
  someone else's, wait.
