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
go run . serve -verifier manual                        # a person answers the conversation instead of a model, no API key needed
go run . serve -verifier-max-steps 40 -verifier-budget 10m  # tool calls and wall-clock cap for a single verifier turn
```

`-verifier` picks the brain: `nim` (default, model-driven, needs `NVIDIA_API_KEY`) or `manual`, which reads the
last task, note, answer or dispute in the conversation and runs it as one instruction per line: `run <shell
command>`, `screenshot`, `verdict pass|fail|inconclusive <summary>`, `ask <question>`, or `help` for the full
grammar. `GREENROOM_VERIFIER` sets the default when the flag is not given.

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
  with a mutex and persists it; `recorder` owns run evidence on disk: `manifest.json`,
  `steps.jsonl`, and, while frame capture is enabled, `frames/<unix-ms>.jpg` and `frames.jsonl`
  (ADR 0008). `input.go` owns computer use (ADR 0009): the control lease and the guest-side
  helper, whose Swift source is embedded from `internal/machine/guest/input.swift`.
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

**The record is on disk before Wait returns.** `finishBoot` writes the `machine_boot` step and
only then closes `ready`, so a caller that reads the run directory the moment `machine_wait`
answers sees what `machine_wait` said. `manifest.json` is replaced atomically (temp file and
rename) for the same reason: the API and the companion read it while the recorder writes it.
The slow part of a failed boot, stopping the VM, still happens after the signal.

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

**`manifest.Steps` is a high-water mark, and nothing reports it as a count.** `begin` claims a
number before the work runs, so a daemon stopped between `begin` and `complete` leaves a number
claimed that no line in steps.jsonl ever uses. It stays a high-water mark on purpose: handing out
a number the run has already spent would overwrite that step's artifact, and reattaching takes
`max(manifest.Steps, highest seq in steps.jsonl)` so a manifest that lost writes cannot walk the
numbering backwards. Anything that reports what a run did counts the record instead, through
`machine.ReadStepLog`, which is why `/api/runs` agrees with `/api/runs/{id}/steps`. A run was
found reporting `steps: 0` in the list with six steps on disk, from an older daemon that rebuilt
the manifest on reattach.

**Every way a run can end is recorded.** `destroyedAt` is not only written by `Destroy`: a boot
that fails stops and deletes the VM and stamps it, a `tart run` process that exits after the
machine was ready is watched by `watchProcess`, which fails the machine and stamps it, and a
reattach that finds tart no longer listing a machine stamps it from the run's own evidence,
dated at the end of the last step or frame rather than at the restart. A run with no end reads
as a machine still running days later, and every duration computed from it is wrong.

**Only the session store hands out message sequence numbers.** `Store.Append` numbers a message
under its own lock, for the same reason only the recorder hands out step numbers: two writers
that each compute `len(msgs) + 1` choose the same `seq` and one of them is lost.

**The verifier is reached only through the conversation.** Nothing calls `Verifier.Turn` except
the actor in `internal/verifier`, which reacts to what lands in the store. There is no
`machine_verify` tool any more: the coder posts a task with `agent_send` and reads the reply with
`agent_wait`. A verdict is a proposal, open to `accept` or `dispute`, and after `-max-disputes`
rounds it is contested and only a human can close it.

**The manual brain is the model brain with a person for a model.** `verifier.Manual` answers the same
conversation, in the same message kinds, and drives the same machine through the same `Manager` calls as
`verifier.Verifier`; it only replaces the model's tool calls with a person's typed instructions. Anything that
works with `-verifier manual` works with `-verifier nim`, and a test written against one brain's `Turn` is
proof about the shape of a turn, not about a model.

**A human is always answered.** Every message a human sends starts a verifier turn, a `note`
included, and the verifier answers it in the transcript with a `reply`, a `question` or a
`verdict`. It can talk while the machine is booting or dead: the turn runs whatever the machine
is doing, the machine's status goes in front of the model at the start of the turn and again
when it changes, and a tool call on a machine that is not ready comes back as a readable error.
A coder `note` is context, not a turn, because the coder's own reply channel is its next
`agent_wait`.

**Only one hand on the mouse, and the conversation is told whose.** A machine has at most
one control lease (`Manager.TakeControl`, ADR 0009) and `Manager.Input` refuses a batch
that is not backed by it, so the verifier and a person can never post events at the same
time. The lease expires after `ControlTTL` of silence and every batch renews it: a
companion that crashes holding the screen must not lock it for good. Taking it and giving
it back each write one message into the conversation, and each batch is one
`machine_input` step, whatever its length, so a drag reads as one thing a person did.

**The verifier holds the screen lease per call, not per turn.** `machine_click`,
`machine_type`, `machine_key`, `machine_scroll` and `machine_input`, in `internal/mcpserver`
for a coder and natively in `internal/verifier` for the model and manual brains, all take
the lease as holder `verifier`, post one batch, and release it before the call returns. The
take-post-release sequence lives once, in `machine.Manager.InputAs` (issue #12): both
packages call it rather than each keeping its own copy, because `internal/verifier` cannot
import `internal/mcpserver` and the two used to duplicate the same three lines. A lease held
for a whole turn would let one long turn lock a watching human out of a machine they are
meant to be able to take back at any moment; a lease held only for the one batch a call
posts means a human can take the screen the instant a call returns, which issue #11
requires and `TestAHumanCanTakeTheScreenBackBetweenVerifierCalls` and
`TestTurnClicksAtAFractionAndRecordsOneStep` both check for. A human already holding the
lease is not a transport failure: `InputAs` turns it into a readable error naming them, the
machine untouched, so the caller can simply try again in a moment.

**Input coordinates are fractions of the screen, and the manager is the only place that
knows otherwise.** The caller sends 0 to 1 because it is looking at a scaled frame; the
manager reads the guest's real resolution once per machine (`ScreenOf`) and multiplies.
A coordinate outside the picture is clamped, not refused: a drag off the edge is a hand,
not a bad request. Never move this arithmetic up into `api` or `mcpserver`: both would
then need a resolution neither of them owns.

**The guest input helper is compiled in the guest, once, and never on the host.** Posting
a real event needs `CGEvent` from a process in the guest's own login session, so
`installInputHelper` writes `guest/input.swift` into the machine as base64 and builds it
with `swiftc -swift-version 5` at `~/.greenroom/bin/greenroom-input-<version>`. Change the
Swift and bump `inputHelperVersion`, or a machine that is already running keeps calling
the old binary. Nothing a person types is ever read by a shell: both the source and every
batch travel as one base64 argument.

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

**Every ready machine is recorded.** From the moment a machine becomes ready until it is
destroyed, the daemon captures its screen every `-frame-interval` (default 2s, ADR 0008) into
`runs/<runId>/frames/<unix-ms>.jpg`, with one line per frame appended to
`runs/<runId>/frames.jsonl` (`at`, `file`, `step`, `bytes`). A frame is evidence alongside
steps.jsonl, not a step itself: it never claims a number of its own, it only cites whichever
step was current when it was taken. A capture failure is logged once for the run and then
retried silently every interval after that; it never fails the run. `-frame-interval 0` turns
the recorder off entirely, for a run with no frames at all.

**A guest command that exits non-zero is not an error.** `tart.Exec` reports it as
`ExitCode`; an `error` means tart itself failed (VM gone, agent unreachable, cancelled).
Collapsing the two would make every failing build look like an infrastructure fault.

**`runId` is the single handle**: machine map key, VM name (`greenroom-<runId>`), and run
directory name.

**The daemon outlives its own process.** SIGINT shuts down HTTP but leaves machines running;
`loadState` reattaches on next boot, drops machines tart no longer lists, and records the end of
each run it drops.

## Test seams

The suite drives the real code with no VM. Three options make that possible, and they are the
only way to reach the failure paths:

- `WithTartBin` points the manager at `internal/testsupport`, a fake `tart` script that answers
  every subcommand, records each argument list, and turns on failures through control files
  (`fail-clone`, `fail-run`, `fail-ip`, `fail-exec`, `fail-keyinstall`, `fail-stop`,
  `fail-delete`, `exec-exit-<n>`, `exec-codes`, `agent-down`, `ssh-down`, `list-empty`, `vmnames`,
  and for computer use `fail-input-install`, `input-down` and `screen`).
  `exec-codes` is a queue: one exit code per line, consumed on each `tart exec` call, for a test
  where a single turn runs several commands and needs their exit codes to differ.
- `WithSSHProbe` replaces the in-guest port 22 check. The fake tart answers the real probe too,
  so the default path is covered as well: `ssh-down` makes it refuse.
- `WithReadyTimeout` shortens the three minute budget so a failure test takes seconds.

`Sync` needs no option: a fake `rsync` earlier on `PATH` records its arguments.

Test at the highest seam that can observe the behavior. The MCP seam in
`internal/mcpserver/server_test.go` runs a real MCP client against a real HTTP server, and it
covers every tool: the twelve `machine_*` tools there (create, wait, list, sync, exec,
screenshot, destroy, and the five computer-use tools click, type, key, scroll and input), and
the three `agent_*` tools in `agenttools_test.go`, which play the verifier by appending to the
store directly. The companion
seam is the same idea one package over: `internal/api/api_test.go` drives the real routes over
`httptest`, including the SSE stream, which it cancels to prove the handler lets go.

## Guest conventions

Commands run as `admin` through `zsh -lc`. Sync is rsync over ssh with an ed25519 key
generated into the state root and installed during boot. A machine is not reported ready until
that ssh path works. Screenshots are `screencapture`
inside the guest, base64 back over `tart exec`.

## Image

A fresh clone of the daemon's default OCI image pays two one-time costs on its first control
request: `swiftc` compiles the guest input helper (issue #9), and the ssh key gets installed.
Both are idempotent scripts, so paying them once per machine is correct but slow: the compile
alone measures in the tens of seconds. `scripts/build-image.sh` bakes both into a `greenroom-base`
VM ahead of time, so every later clone of it skips both:

```sh
scripts/build-image.sh                                    # clones defaultImage, prepares, stops
scripts/build-image.sh -base <oci image> -name my-base     # a different source or name
scripts/build-image.sh -force                              # replace an existing stopped VM of that name
```

It clones the base image, boots it with `--no-graphics`, waits for the guest agent, runs
`go run . prepare-image -vm <name>` (`prepare.go`, `machine.PrepareGuest`), then stops the VM.
`prepare-image` can also be run by hand against any already-running VM:

```sh
greenroom prepare-image -vm <name> [-root ~/.greenroom]
```

`inputHelperVersion` (`internal/machine/input.go`) is the contract between a prepared image and
the daemon: the helper is baked in at the exact path that version names
(`.greenroom/bin/greenroom-input-<version>`), and `installHelperScript`'s own short-circuit is
what makes a clone's first control request a no-op instead of a second compile. Bump the version
when `guest/input.swift` changes, and a `greenroom-base` built before the bump goes back to
paying the compile on every machine, silently, until it is rebuilt: nothing checks a running
image's baked-in version against the daemon's own. Run the daemon against the prepared image with
`greenroom serve -image greenroom-base`.

**`PrepareGuest` ends with `sync` in the guest, and that is load-bearing.** A real run against a
real VM found that `tart stop` right after preparing does not by itself flush the guest's dirty
filesystem pages: the compiled helper answered `--version` while the VM was still running
(`verifyHelper` proved it), the script then stopped the VM, and the very next boot of that image
had an empty `.greenroom/bin/` -- the whole image was rebuilt for nothing, silently, and only a
stop/reboot cycle by hand caught it because the automated proof test's own first `ScreenOf` was
still fast enough (a partial recompile) to slip under the no-compile ceiling once by accident.
`sync` is the fix and `TestPrepareGuestSyncsBeforeReturning` pins that PrepareGuest still runs it.
Do not remove it to save a round trip.

## Known divergence from ADR

`docs/adr/0004-go-daemon-official-mcp-sdk.md` specifies a SQLite store for daemon state; the
implementation uses a JSON file (`state.json`). The ADR is the intended destination.
