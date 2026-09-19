# 0006. A run has one conversation, and every participant speaks into it

Date: 2026-09-18
Status: proposed

## Context

ADR 0005 gave greenroom its own agent, the verifier, behind one tool: `machine_verify`
takes a task and returns a verdict. The whole exchange is a function call. That shape
cannot do four things the product needs.

**It cannot continue.** The verifier's transcript lives in a Go slice for the length of
the call and is dropped. A follow-up ("now try it with the dark theme") is a fresh run of
the loop with no memory of the first.

**It cannot ask.** When the verifier lacks something, such as the build command, which
scheme to use, or whether a system dialog is expected, it guesses. The people who know
the answer (the coding agent that wrote the branch, the developer watching) have no way
to be asked.

**It cannot be disagreed with.** A verdict is final on arrival. If the verifier ran the
wrong command and reported `fail`, the coding agent's only move is to start over and
hope. Two agents with different knowledge of the same machine have no way to reconcile
what they saw.

**It cannot survive the transport.** `machine_verify` holds the MCP call open for the
whole loop. Claude Code gives an HTTP tool call 60 s to produce its first byte (the
reason `machine_create` became asynchronous in `docs/01-plan.md`), and a task with a
build in it takes minutes. The tool works in tests and times out in use.

The companion app (ADR 0007) needs the same thing from the other side. A developer
watching a run wants to add context, answer the verifier's questions, and read what the
coding agent told it, without taking control away from the coding agent.

The `docs/00-idea.md` agenda item 3, "how the caller and the verifier communicate", is
this decision.

## Decision

**Every run owns one conversation.** It is append-only, lives on disk as
`conversation.jsonl` in the run directory beside `steps.jsonl`, and it is the only way to
reach the verifier. Nothing talks to the verifier except by appending a message.

**Four participants** share it: `coder` (the coding agent, over MCP), `human` (the
companion app, over the HTTP API), `verifier` (greenroom's agent), and `system` (the
daemon, for machine events). Every message carries who said it. Both agents and the
human read the same transcript, so nobody is told about a decision second-hand.

**The verifier is an actor per run, not a function.** It takes a turn when a message that
needs an answer arrives, and the turn ends when it posts a reply or exhausts its budget.
Its model context is a projection of the conversation, rebuilt from disk at the start
of every turn. That is what makes it continue: the file is the memory. It is also what
makes the daemon restartable mid-conversation, which `apps/daemon/CLAUDE.md` already
promises for machines.

### Messages

```
{ seq, at, from, kind, text, replyTo?, step?, verdict?, evidence? }
```

| kind       | from            | meaning                                                             |
| ---------- | --------------- | ------------------------------------------------------------------- |
| `task`     | coder, human    | Work for the verifier. Starts a turn.                               |
| `note`     | coder, human    | Context. From the coder: read at the next turn. From a human: starts a turn, the verifier replies. |
| `reply`    | verifier        | A plain answer to whoever spoke, with no verdict. Ends the turn.   |
| `question` | verifier        | The verifier is blocked and says what it needs. Ends the turn.      |
| `answer`   | coder, human    | Reply to a question (`replyTo`). Starts a turn. First answer wins.  |
| `progress` | verifier        | What it just did, one per tool call; `step` points at `steps.jsonl`. |
| `verdict`  | verifier        | A proposal: `pass`, `fail` or `inconclusive`, summary, evidence. Ends the turn. |
| `accept`   | coder, human    | The verdict in `replyTo` is final.                                  |
| `dispute`  | coder, human    | Why the verdict in `replyTo` is wrong. Starts a turn.               |
| `event`    | system          | Machine ready, destroyed, turn failed, budget spent, human action.  |

Only the session store hands out `seq`, for the same reason only the recorder hands out
step numbers: two writers must never choose the same number.

### How the two agents reach agreement

A verdict is a proposal, not an outcome. The rules:

1. **Evidence is the ground.** A verdict names the artifacts and step numbers it rests
   on. A dispute is expected to do the same. Neither side can edit the run record, so
   the argument is about what the record shows, not about what either agent claims.
2. **A dispute reopens the turn.** The verifier reads the objection with its full
   transcript, may run more tools, and posts a new verdict: revised if the objection
   holds, restated with the reason if it does not. It is prompted to change its mind
   when the evidence says so and to hold when it does not, and to say which.
3. **Rounds are bounded.** Two disputes per run by default (`-max-disputes`). After the
   last one the verdict is marked `contested` and the run's verdict stays open. The
   coder is told this in the reply, so it can stop arguing and escalate.
4. **A human closes a contested verdict.** An `accept` from `human` on any verdict, or a
   `dispute` from `human`, is final. That is the escalation path, and the reason the
   companion exists.
5. **Silence is acceptance.** A run destroyed with an undisputed verdict keeps that
   verdict. Nothing waits on a participant who has gone away.

The final verdict, and how it was reached (accepted by whom, or contested), is written
into `manifest.json` so a reviewer who never opens the transcript still sees it.

### A human is always answered

Every message a human sends starts a turn, a `note` included, and the verifier answers it
in the transcript with a `reply`, a `question` or a `verdict`. The verifier is the voice
of the run: a person watching from the companion talks to it, not to the coding agent
and not to whoever is driving Claude Code. A coder `note` stays context, because the
coder's own reply channel is its next `agent_wait`.

The verifier can talk before it can act. A turn runs whether the machine is booting,
ready, failed or gone: the machine's current status is put in front of the model at the
start of every turn and again when it changes mid-turn, and a tool call on a machine
that is not ready comes back as a readable error rather than a crash. So "what is the
status of the boot" gets an answer from the verifier while the boot is still going, and
a task sent to a dead machine gets a `reply` that says so.

### Questions

The verifier gets an `ask` tool beside `machine_exec` and `machine_screenshot`. Calling
it posts a `question` and ends the turn. Whoever answers first, coder or human, starts
the next turn. Questions are how "give the agent context" happens on demand instead of
up front: the verifier asks for what it needs when it needs it.

### The tools

`machine_verify` is retired. Three tools replace it, on the pattern `machine_create` and
`machine_wait` already set:

- `agent_send(runId, kind, text, replyTo?)` appends a message from `coder` and returns
  its `seq` at once.
- `agent_wait(runId, after, timeoutSeconds)` blocks until a message with `seq > after`
  exists or the timeout (capped at 50 s) passes, then returns every message after
  `after`. The coder polls this the way it polls `machine_wait`.
- `agent_transcript(runId, after?)` returns messages without waiting.

The companion's `POST /api/runs/{id}/messages` appends from `human` through the same
store. There is one write path.

### Turns

One turn runs at a time per run. A turn is bounded by `MaxSteps` tool calls and a
`Budget` of wall time, as today, but per turn rather than per run. Messages that arrive
mid-turn are folded into the model context before its next call, so a human saying
"ignore that dialog, it is the GPU bug" reaches the verifier without waiting for the
turn to end. The verifier's own tool calls are recorded twice, on purpose: in
`steps.jsonl` as what happened to the machine (the evidence), and in `conversation.jsonl`
as `progress` messages carrying the text the model saw (the memory). They cross-reference
by step number.

## Consequences

**Chat costs model calls.** A human note is a turn and a turn is at least one call to the
reasoning model. That is the point: the person is talking to greenroom's agent, and the
agent is the model. Token counts stay in the run record.

**The verifier package changes shape.** `Run(task) Report` becomes `Turn(runID)` driven
by the session store. The existing tests keep their scripted-model seam; they gain a
scripted conversation.

**Every human action is visible to the coder.** A screenshot, destroy or message from the
companion lands in the conversation as an `event` or a message, so the coder learns of it
on its next `agent_wait`. This is the rule that lets the companion have controls without
becoming a second, hidden source of control (ADR 0007).

**Context grows.** Rebuilding the model context from the whole transcript means every
turn costs the tokens of every turn before it. Token counts are recorded per turn. A
summarising projection is the fix when a real run hits the limit; it is not built until
one does.

**No merging of authority.** The coder decides when the run is done and destroys the
machine. The human can accept or dispute but does not create machines or sync code from
the companion. The verifier only ever proposes.

**Deferred.** Several verifiers per run, a human-only run with no coder, and the verifier
addressing a message to one participant rather than the room. Each is a message kind or
a field away and none is needed to prove the loop.
