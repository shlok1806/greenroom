# 0005. One guest agent per machine on one framed channel

Date: 2026-09-27
Status: accepted. Implements root ADR 0037 decisions 1 and 2 (docs/21 section 4) for wave 1
(#212). Builds on daemon ADRs 0002 (the `tart run` file limit) and 0003 (bounded looks), and
on root ADRs 0009 (the lease), 0011 (the `--serve` framing) and 0017 (plain pipes).

## Context

Every desktop call reaches the guest through `tart exec`: about three per `machine_ui` (tree,
capture, desktop read for the ink test), one per input batch, one per screenshot, one per
recorder frame (30 a minute), one a minute for the capture-approval check. tart 2.37 leaks one
descriptor in `tart run` on every exec for the life of the VM (daemon ADR 0002, #186, #207),
and each exec is a new process, a new AX connection and a new chance of a control-socket error
(docs/21 section 1.4 case 3). Root ADR 0037 decided on one long-lived agent in the guest,
reached through one `tart exec -i` pipe. It left the wire details, the failure handling and how
the daemon's existing looks move over to it to this package.

## Decision

### The agent and its start

1. **`greenroom-input --agent`** (helper 9) is the agent: the same Swift binary, a new mode. It
   is started by `tart.StartPipe` (plain pipes, never a pty) with `agentScript()`, which first
   `pkill`s an older `--agent` (the guest agent does not close a helper's stdin when its host
   exec dies, root ADR 0011) and then `exec`s the helper. Its responsible process is
   tart-guest-agent, so it has the image's Accessibility, PostEvent and ScreenCapture grants and
   replayd approvals; nothing new is granted. It listens on nothing.
2. **The helper is several Swift files** under `internal/machine/guest/helper/` (the top level
   and `logic/`), embedded with `go:embed`, written into the guest by `installHelperScript` and
   compiled with one `swiftc` call. `logic/` holds pure code (Foundation and CoreGraphics only:
   geometry, the ref table, actionability and visibility decisions, the scroll plan, text
   limits) that also compiles on the host with the tests in `guest/helper/tests/`
   (`helper_swift_test.go`). `--version` prints `greenroom-input 9 <source hash>`; the hash is
   of the embedded sources, written by the install script as `SourceHash.swift`, and the
   install and boot checks compare the whole line, so a changed source with an unchanged
   version still recompiles. Globals that touch WindowServer live outside `main.swift`, where
   Swift initializes them lazily, so `--version` still answers on a wedged screen (daemon ADR
   0003).
3. **The channel is off unless `serve -desktop-toolkit`** (and `bench run -desktop-toolkit`)
   is set, until wave 4 makes it the default. With it on, the daemon starts the agent right
   after boot's helper phase (never fatal: a failure is `agentError` in the boot step) and
   restarts it whenever it dies. A reboot stops it with the old boot (`Machine.gen`) and the
   new boot starts a new one. Destroy and `detachLocked` stop it.

### Frames

Every message either way is `[type u8][length u32 big-endian][payload]`, the `--serve` format.
A payload over 4 MiB is a broken channel: the reader closes it rather than allocate. Payloads
are JSON unless said otherwise.

| Type | Direction | Payload |
| --- | --- | --- |
| `HELLO` 0x01 | agent to host, first frame | `{version, source, protocol, pid, trusted:{accessibility, screen, postEvent}, screen:{width, height, scale}, caps:[op...]}` |
| `REQUEST` 0x20 | host to agent | `{id, op, args, deadlineMs, reader, input}` |
| `RESPONSE` 0x21 | agent to host | `{id, ok:true, result, blob?:{bytes, mime}, ms}` or `{id, ok:false, error:{code, message, retryable, detail?}, ms}` |
| `BLOB` 0x22 | agent to host | binary `[id u32][seq u16][last u8][bytes]`, at most 1 MiB of bytes a frame, all sent before the request's RESPONSE |
| `CANCEL` 0x23 | host to agent | `{id}` |
| `EVENT` 0x24 | agent to host | `{kind, ...}`: `log {message}`, `stalled {in, seconds}`, `recovered {in}` |
| `PING` 0x25 / `PONG` 0x26 | host to agent / back | `{t}`, echoed |
| `PAUSE` 0x27 / `RESUME` 0x28 | host to agent | `{holder}` / `{}` |
| `STREAM` 0x29 | agent to host | reserved for wave 3's live video; unused in wave 1 |

- `protocol` is 1 and bumps only on a breaking change; `caps` lists the ops the agent answers,
  so the daemon never guesses them from the version. A request for an op not in `caps` fails
  in the daemon, before it is sent, naming the helper as stale.
- `id` is a request number unique on the connection; a RESPONSE, its BLOBs and a CANCEL carry
  it. `reader` is the seat whose refs the request uses (`coder`, `verifier`, `human`);
  `input` is true for an op that posts events or changes a value, which PAUSE refuses.
- Error codes: `bad_request`, `unknown_op`, `stale_ref`, `not_found`, `ambiguous`, `refused`
  (an actionability check failed; `detail.reason` is `covered`, `hidden`, `offscreen`,
  `disabled`, `unstable`, `modal`, `not_editable` or `not_frontmost`), `paused`, `cancelled`,
  `deadline`, `not_trusted`, `not_responding` (the target app does not answer AX), `ax_error`,
  `capture_failed`, `internal`. `retryable` is true only where retrying the same request can
  work (`deadline`, `not_responding`, `capture_failed`); the daemon never retries an `input`
  request itself.

### Deadlines, concurrency, cancellation

4. **Every request has `deadlineMs`**, at most 45 s (the MCP cap is 50 s, root ADR 0015). The
   agent bounds itself to it and answers `deadline` with what it knows; the daemon waits the
   deadline plus 2 s, then fails the call with `ErrAgentDeadline`. A request with no answer at
   its deadline plus 5 s means the agent is wedged: the daemon ends the channel.
5. **Reads run on a queue per target app** (by pid), so a hung app blocks only reads of it;
   **inputs run on one serial queue**, since a desktop has one pointer and one keyboard. AX
   calls carry `AXUIElementSetMessagingTimeout` (0.5 s per element on action paths, 1 s for
   walks), and a walk has a wall budget (3 s by default) after which it answers what it has
   with `truncatedBy: "time"`.
6. **CANCEL** stops a request at its next wait point (an actionability retry, a settle, a wait
   poll, between typed characters). An input already posted is never undone and never
   repeated: a request cancelled after it posted still answers with its effect. A caller that
   gave up before the answer gets its context's error; the late RESPONSE is dropped.

### Health

7. **Heartbeat.** The daemon sends PING every 2 s. No PONG for 6 s (three missed), EOF, a write
   error or an unreadable frame ends the channel: the pipe is closed (SIGINT, then SIGKILL,
   daemon ADR 0002), every pending call fails at once with `ErrAgentLost` ("the guest agent's
   channel was lost during the call; an input may or may not have been posted"), and a new
   connection is started with backoff 0.5, 1, 2, 5, 5... s.
8. **The agent ends itself** on stdin EOF, and also when no frame at all (PINGs included) has
   arrived for 15 s, since a dead host exec does not always close its stdin. An orphan
   therefore never keeps the permissions for long, and the next start `pkill`s it anyway.
9. **Agent watchdog.** A thread in the agent watches every AX walk and capture in flight; one
   running over 10 s sends `EVENT stalled {in: "ax"|"capture", seconds}` and `recovered` when
   it ends. The daemon logs both and, while stalled, answers captures and walks with
   `ErrScreenNotAnswering` (daemon ADR 0003) instead of queueing more behind it.
10. **Refs die with the connection.** Each connection has a generation; the daemon remembers,
   per machine and reader, the generation its refs came from, and refuses a ref from an older
   one before sending anything: "e17 is from an earlier connection to the guest agent (it
   restarted); take a new machine_snapshot".
11. **Counting.** `Machine.AgentReconnects` (like `Files`, set only on copies, never in
   `state.json`) counts connections after the first, so a reconnect loop shows next to the fd
   count in `machine_list` and `/api/runs` before it matters.

### What goes over the channel

12. **Every desktop operation the daemon makes while the channel is up**: the toolkit ops (daemon
   ADR 0006), and the existing looks and inputs as agent ops with their old shapes: `screen`
   (the screen size), `capture` (PNG or JPEG, whole screen or a rect, for screenshots, the
   render check and the recorder's frames), `ui` (the `--ui-base64` tree), `desktop` (the
   `--desktop` window and app list), `input` (a `machine_input` batch) and `sh` (a script the
   daemon wrote, run with a timeout: the capture-approval check). The recorder keeps its
   interval and asks for one `capture` per frame; `STREAM` stays for wave 3's live video, which
   is still `--serve`. Boot's own checks, the install, `machine_exec`, sessions, sync, pull and
   reboot keep `tart exec`.
13. **Degraded mode (until wave 4).** A read that finds the channel down waits for it for up to
   10 s after it went down, then falls back to its one-shot exec, and the step records
   `degraded: true`. An input on the legacy tools falls back only when nothing was sent on the
   channel (never posted twice), recorded the same way. **Toolkit ops never fall back**: an
   action without actionability checks is what this design removes, and a snapshot without the
   agent has no refs. They fail with `ErrAgentUnavailable`, which says the agent is
   reconnecting and to try again in a few seconds.
14. **The lease reaches the agent.** When a seat other than the verifier and the coder takes the
   screen fresh (a human in the Companion), the daemon sends `PAUSE {holder}`; when it gives
   it back or its lease lapses, `RESUME`. A paused agent cancels queued and running inputs of
   other holders at their next event boundary and refuses new ones with `paused`; reads go on.
   The daemon still checks the lease before it sends an input (root ADR 0009); PAUSE is the
   guard for an input already in flight when the human took over.

## Consequences

- One `tart exec` per machine per connection for every desktop op and frame instead of about
  four per verified action, 30 a minute for frames and one a minute for approvals, so `tart
  run`'s descriptors stay flat while a machine is driven (measured in the #212 PRs).
- A new single point of failure, guarded by heartbeats, deadlines, the watchdog, reconnects and
  the degraded path for reads. Killing the channel mid-call fails that call by name at once
  (EOF) or within 6 s (a silent stall), and the next connection starts within about a second.
- The daemon owns the wire (`internal/guestagent`, no daemon imports: frames, `Conn`,
  `Supervisor`); the machine package owns when it runs and what goes over it.
- Tests: `internal/guestagent` runs `Conn` and `Supervisor` against an in-process fake agent
  over `net.Pipe`; `internal/testsupport/fakeagent.go` makes the fake tart run a fake agent (the
  test binary again, like `fakescreen.go`) so machine, MCP and verifier tests drive the real
  manager; `helper_swift_test.go` typechecks the whole helper on the host and runs the `logic/`
  tests there.
- The one-shot helper modes (`--json-base64`, `--ui-base64`, `--desktop`) and `--serve` stay,
  for degraded mode and the live screen, until wave 4 and wave 3 remove them.
