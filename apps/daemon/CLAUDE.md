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
go test ./...                                          # 78 tests, no VM needed; the e2e test is build-tagged
go test ./... -race                                    # run this before any change to boot or the recorder
go test ./... -coverpkg=./... -coverprofile=/tmp/c.out && go tool cover -func=/tmp/c.out | tail -1
go test ./internal/machine -run TestFoo                # single test
go test -tags tart -run TestEndToEnd -v -timeout 10m . # real Tart VM, needs tart on PATH and a pulled image
golangci-lint run ./...
go run . serve                                         # MCP on http://127.0.0.1:7777/mcp
go run . serve -addr 127.0.0.1:7777 -root ~/.greenroom -image <oci image>
```

The e2e test boots a real VM and costs minutes plus tens of GB of disk. It is the only test
that exercises the whole path, so run it after changes to `machine` or `tart`.

## Layering

Each layer depends only on the one below it. Keep it that way.

- `main.go` - CLI (`serve`, `version`), flags, HTTP mux. `/mcp` serves MCP stateless at the
  protocol layer; `/healthz` reports live machine count.
- `internal/mcpserver` - the only agent-facing surface: tool definitions, input structs,
  defaults, PNG-to-JPEG, rounding. It holds no VM logic; anything stateful belongs below it.
- `internal/machine` - lifecycle and the source of truth. `Manager` guards the machine map
  with a mutex and persists it; `recorder` owns run evidence on disk.
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
answers over vsock, tart reports an IP, the ssh key goes in, and a TCP connection to guest port
22 succeeds. Without the last one the first `machine_sync` after `ready` can fail with "No route
to host", because sshd starts later than the guest agent.

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
  `fail-delete`, `exec-exit-<n>`, `agent-down`, `list-empty`, `vmnames`).
- `WithSSHProbe` replaces the port 22 check, because a fake machine has no sshd.
- `WithReadyTimeout` shortens the three minute budget so a failure test takes seconds.

`Sync` needs no option: a fake `rsync` earlier on `PATH` records its arguments.

Test at the highest seam that can observe the behavior. The MCP seam in
`internal/mcpserver/server_test.go` runs a real MCP client against a real HTTP server, and it
covers all seven tools.

## Guest conventions

Commands run as `admin` through `zsh -lc`. Sync is rsync over ssh with an ed25519 key
generated into the state root and installed during boot. A machine is not reported ready until
that ssh path works. Screenshots are `screencapture`
inside the guest, base64 back over `tart exec`.

## Known divergence from ADR

`docs/adr/0004-go-daemon-official-mcp-sdk.md` specifies a SQLite store for daemon state; the
implementation uses a JSON file (`state.json`). The ADR is the intended destination.
