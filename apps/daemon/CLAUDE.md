# greenroom daemon

One Go binary (`greenroom`) on the developer's own Mac. An agent (Claude Code) talks to it
over MCP; it drives Tart VMs as subprocesses and records every call to disk.

```
Claude Code --MCP/Streamable HTTP--> daemon --subprocess--> tart --> macOS VM
                                       |
                                       +--> ~/.greenroom/runs/<runId>/
```

## Commands

```sh
go build ./...
go test ./...                                          # no VM needed; the e2e test is build-tagged
go test ./... -race                                    # run this before any change to boot or the recorder
go test ./... -coverpkg=./... -coverprofile=/tmp/c.out && go tool cover -func=/tmp/c.out | tail -1
go test ./internal/machine -run TestFoo                # single test
go test -tags tart -run TestEndToEnd -v -timeout 10m . # real Tart VM, needs tart on PATH and a pulled image
golangci-lint run ./...
go run ./internal/testsupport/smokeclient -url http://127.0.0.1:7778/mcp  # drive a running daemon over MCP like a coder would
go run . serve                                         # MCP on http://127.0.0.1:7777/mcp, API on http://127.0.0.1:7777/api/
go run . serve -addr 127.0.0.1:7777 -root ~/.greenroom -image <oci image>
```

The e2e test boots a real VM and costs minutes plus tens of GB of disk. It is the only test
that exercises the whole path, so run it after changes to `machine` or `tart`.

## Install

```sh
pnpm run install:daemon     # scripts/install.sh
pnpm run uninstall:daemon   # scripts/uninstall.sh
```

`install.sh` builds `~/.greenroom/bin/greenroom`, writes the launchd user agent
`~/Library/LaunchAgents/com.greenroom.daemon.plist` and starts it on `127.0.0.1:7777` with the
repo's `.env`. The agent has `KeepAlive`, so launchd restarts the daemon when it crashes, and
`RunAtLoad`, so it comes back at login: no terminal holds it open. Everything it writes to
stdout and stderr goes to `~/.greenroom/daemon.log`. The script refuses to take port 7777 from
anything that is not a greenroom daemon. `uninstall.sh` boots the agent out and removes the
plist; the binary and `~/.greenroom`, which holds the run record, stay.

On the first start macOS may ask to allow greenroom on the local network. Nothing in the boot
path needs it any more: the daemon reaches a guest only through `tart exec` over vsock and
through Apple-signed subprocesses (ssh, rsync), none of which the gate applies to. Granting it
is harmless, and refusing it must stay harmless: see the local-network invariant below.

## Layering

Each layer depends only on the one below it. Keep it that way.

- `main.go` - CLI (`serve`, `version`), flags, HTTP mux. `/mcp` serves MCP stateless at the
  protocol layer; `/api/` serves the companion; `/healthz` reports live machine count. It also
  owns the lifecycle bridge that turns manager events into conversation events.
- `internal/mcpserver` - the only agent-facing surface: tool definitions, input structs,
  defaults, PNG-to-JPEG, rounding. It holds no VM logic; anything stateful belongs below it.
- `internal/api` - the companion's read and control surface (ADR 0007), a sibling of
  `mcpserver` over the same manager and the same store: JSON routes, the SSE stream, and
  nothing stateful. Neither sibling may hold logic the other needs.
- `internal/machine` - lifecycle and the source of truth. `Manager` guards the machine map
  with a mutex and persists it; `recorder` owns run evidence on disk.
- `internal/session` - the conversation a run owns (`conversation.jsonl`), beside `machine` and
  below `mcpserver` and the HTTP API. `Registry` hands out one append-only `Store` per run, and
  every participant, coder, human, verifier and the daemon itself, writes through it.
- `internal/verifier` - greenroom's own agent, an actor per run. It waits on the conversation,
  takes a turn when a message needs one, and drives the machine through `Manager`.
- `internal/tart` - subprocess wrapper over the `tart` CLI, and the only place that knows
  tart's argument shapes or parses its output.

## Invariants

These are the rules a future change is most likely to break.

**Create is serialized.** `createMu` is held for the whole of `Create`. The capacity check reads
the host and the create then acts on it, so two overlapping creates would both pass a limit with
room for one. A local clone takes about 0.1 s, so serializing costs little next to a boot.

**Create is asynchronous and must stay that way.** Boot takes 30 to 120 s while Claude Code
gives an HTTP tool call 60 s to first byte, so `machine_create` returns immediately in
`booting` and the agent polls `machine_wait` (capped at 50 s per call).

**Ready means usable, not merely booted.** Readiness is four phases in order: the guest agent
answers over vsock, tart reports an IP, the ssh key goes in, and sshd inside the guest accepts a
connection on 127.0.0.1:22, probed over vsock with `tart exec`. Without the last one the first
`machine_sync` after `ready` can fail with "No route to host", because sshd starts later than the
guest agent. A phase that times out carries the last probe error, so a failed boot names its
cause instead of only reporting a deadline.

**The daemon never opens a TCP connection to a guest itself.** macOS gates local-network access
per binary identity and every `go build` is a new identity, so an in-process dial to
192.168.64.x fails with "no route to host" while Apple-signed subprocesses (ssh, rsync, nc) and
`tart exec` over vsock are unaffected. Anything that must reach the guest goes through one of
those.

The two waiting phases each get their own budget of `readyTimeout`. They must not share one,
because a slow guest agent would then consume the time the ssh phase needs and the machine would
fail with an error that blames ssh. Measured on a loaded host: the agent phase alone took 112.6 s
of a 180 s budget while the ssh phase took 0.0 s.

**Every boot records its phase timings.** `machine_boot` carries `agentSeconds`, `ipSeconds`,
`keySeconds` and `sshSeconds`. Boot time varies a lot with host load, from 32 s to 121 s on the
same Mac, so "which phase was slow" is a question only the recording can answer.

**A machine whose `tart run` process exits has failed.** tart stays in the foreground for the
life of a VM, so an exit during boot means the VM is gone. `waitReady` watches
`tart.Process.Exited` and returns `Err`, which prefers the tail of `vm.log` because that is where
tart writes the real cause, for example the host VM limit. Never wait out the readiness timeout
for a process that has already gone.

**`machine_create` refuses to go above the host machine limit.** Apple permits two macOS guests
for each host, and `checkHostCapacity` counts running VMs before anything is cloned. The default
is 2 and `-max-machines` changes it. The error names the machines that hold the slots.

**Only the recorder hands out step numbers.** `begin` claims a number under the recorder lock and
`complete` records that step. Any tool that names a file claims its number first, because two
callers that compute `manifest.Steps + 1` themselves choose the same name and overwrite each
other's evidence.

**Only the session store hands out message sequence numbers.** `Store.Append` numbers a message
under its own lock, for the same reason only the recorder hands out step numbers: two writers
that each compute `len(msgs) + 1` choose the same `seq` and one of them is lost.

**The verifier is reached only through the conversation.** Nothing calls `Verifier.Turn` except
the actor in `internal/verifier`, which reacts to what lands in the store. There is no
`machine_verify` tool any more: the coder posts a task with `agent_send` and reads the reply with
`agent_wait`. A verdict is a proposal, open to `accept` or `dispute`, and after `-max-disputes`
rounds it is contested and only a human can close it.

**A human is always answered.** Every message a human sends starts a verifier turn, a `note`
included, and the verifier answers it in the transcript with a `reply`, a `question` or a
`verdict`. It can talk while the machine is booting or dead: the turn runs whatever the machine
is doing, the machine's status goes in front of the model at the start of the turn and again
when it changes, and a tool call on a machine that is not ready comes back as a readable error.
A coder `note` is context, not a turn, because the coder's own reply channel is its next
`agent_wait`.

**Every human or coder action that changes a machine lands in the conversation.** A destroy is
announced by the lifecycle bridge in `main.go`, which subscribes to `Manager.Listen` and posts
"machine is ready", "machine failed to boot" and "machine destroyed" from the one place that
knows they really happened, whoever asked for them. The companion's own controls post what the
manager cannot know: the app's screenshot says a human took it, and its destroy says a human
asked. A control that leaves no message is a second, hidden source of truth.

**A transient model failure is retried, not reported.** The model is hosted and the transport is
the internet, so a 500 that a curl a minute later does not reproduce must not end a turn. Two
schedules cover it. `nim.RetryBackoff` retries one HTTP request after 1 s, 2 s, 4 s and 8 s (five
attempts) on 429, 500, 502, 503 and 504 and on a transport failure, honouring `Retry-After`
seconds on 429 and 503; every other 4xx is the request's own fault and is returned at once. The
final error names the attempt count ("returned 500 after 5 attempts"). Above it,
`verifier.TurnRetryDelays` takes the same turn again after 30 s, 60 s and 120 s (four attempts),
announcing each with "verifier retrying the turn (attempt N of 4)", and stops early if a new
turn-starting message makes the old turn moot. After the last failure the actor posts "verifier
gave up on this turn after 4 attempts; send another message to try again", because an actor that
silently waits for the next message leaves the task that started the turn unanswered forever.
Both schedules are package vars so tests can zero them.

**`machine_sync` stays inside the guest home.** `source` must be an absolute host path and
`guestDest` refuses a `dest` that is absolute or that climbs above the home.

**Every guest-touching operation calls `awaitReady` first**, and fails with a "call
machine_wait and try again" message rather than blocking on a booting machine.

**Every tool call records itself** via `rec.step(...)` with input, output, error and duration.
The run directory is the product's evidence, not a debug log: a new tool that touches a
machine records itself the same way.

**A guest command that exits non-zero is not an error.** `tart.Exec` reports it as
`ExitCode`; an `error` means tart itself failed (VM gone, agent unreachable, cancelled).
Collapsing the two would make every failing build look like an infrastructure fault.

**`runId` is the single handle**: machine map key, VM name (`greenroom-<runId>`), and run
directory name.

**The daemon outlives its own process.** SIGINT shuts down HTTP but leaves machines running;
`loadState` reattaches on next boot and drops machines tart no longer lists.

## Test seams

The suite drives the real code with no VM. Three options make that possible, and they are the
only way to reach the failure paths:

- `WithTartBin` points the manager at `internal/testsupport`, a fake `tart` script that answers
  every subcommand, records each argument list, and turns on failures through control files
  (`fail-clone`, `fail-run`, `fail-ip`, `fail-exec`, `fail-keyinstall`, `fail-stop`,
  `fail-delete`, `exec-exit-<n>`, `agent-down`, `ssh-down`, `list-empty`, `vmnames`).
- `WithSSHProbe` replaces the in-guest port 22 check. The fake tart answers the real probe too,
  so the default path is covered as well: `ssh-down` makes it refuse.
- `WithReadyTimeout` shortens the three minute budget so a failure test takes seconds.

`Sync` needs no option: a fake `rsync` earlier on `PATH` records its arguments.

Test at the highest seam that can observe the behavior. The MCP seam in
`internal/mcpserver/server_test.go` runs a real MCP client against a real HTTP server, and it
covers every tool: the seven `machine_*` tools there, and the three `agent_*` tools in
`agenttools_test.go`, which play the verifier by appending to the store directly. The companion
seam is the same idea one package over: `internal/api/api_test.go` drives the real routes over
`httptest`, including the SSE stream, which it cancels to prove the handler lets go.

## Guest conventions

Commands run as `admin` through `zsh -lc`. Sync is rsync over ssh with an ed25519 key
generated into the state root and installed during boot. A machine is not reported ready until
that ssh path works. Screenshots are `screencapture`
inside the guest, base64 back over `tart exec`.

## Known divergence from ADR

`docs/adr/0004-go-daemon-official-mcp-sdk.md` specifies a SQLite store for daemon state; the
implementation uses a JSON file (`state.json`). The ADR is the intended destination.
