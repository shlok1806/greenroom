# daemon

One Go binary, `greenroom`. MCP on `/mcp`, companion API on `/api/`, `/healthz`. Drives
Tart as a subprocess. State and evidence under `~/.greenroom/` (`state.json`,
`runs/<runId>/`).

## Commands

```sh
go build ./...
go test ./...                                   # no VM; fake tart
go test -race ./...                             # before touching boot, recorder or sessions
go test ./internal/machine -run TestFoo
go test -tags tart -run TestEndToEnd -v -timeout 10m .         # real VM
go test -tags tart -run TestEndToEndSession -v -timeout 12m .  # real pty in a real VM
go test -tags tart -timeout 20m ./...           # whole VM suite, as CI runs it
golangci-lint run ./...

go run . serve                                  # 127.0.0.1:7777, root ~/.greenroom
go run . serve -verifier manual                 # no model; a person types instructions
go run . serve -image greenroom-base -max-machines 2 -frame-interval 2s
go run . serve -tart <path>                     # or GREENROOM_TART
go run . prepare-image -vm <running vm>
go run ./internal/testsupport/smokeclient -url http://127.0.0.1:7777/mcp [-live <dir> [-watch]]

scripts/install.sh      # launchd agent com.greenroom.daemon; honours GREENROOM_VERIFIER, GREENROOM_IMAGE, GREENROOM_ENV
scripts/uninstall.sh    # keeps the binary and ~/.greenroom
scripts/build-image.sh [-base <oci>] [-name greenroom-base] [-force]
```

`serve` flags not in `usage()`: `-env-file` (default `.env`), `-tart`, `-open-viewer`.
`-verifier` defaults to `GREENROOM_VERIFIER`, then `nim`. `nim` without `NVIDIA_API_KEY`
runs with no verifier and says so in each run's transcript.

The smoke client's own `-url` default is `:7778`; always pass `-url`.

## Layering

Each layer depends only on the ones below. Keep it that way.

- `main.go` - flags, HTTP mux, and the lifecycle bridge (manager events to transcript
  events: ready, failed, stopped, destroyed).
- `internal/mcpserver` - the only agent-facing surface. Tool schemas, defaults, PNG to
  JPEG. No VM logic.
- `internal/api` - the companion's routes and SSE stream (ADR 0007). Sibling of
  `mcpserver`. Neither holds logic the other needs.
- `internal/verifier` - greenroom's agent, one actor per run. `Verifier` (NIM) and
  `Manual` share one turn shape.
- `internal/session` - a run's conversation (`conversation.jsonl`). Not the same thing as
  `machine.PTYSession`; never name that type `Session`.
- `internal/machine` - lifecycle and source of truth. `Manager`, the recorder
  (`manifest.json`, `steps.jsonl`, `frames/`, `frames.jsonl`), computer use (`input.go`,
  guest helper in `guest/input.swift`), pty sessions (`ptysession.go`).
- `internal/nim` - OpenAI-compatible client for NVIDIA NIM.
- `internal/tart` - the only package that knows tart's arguments and output.

## Invariants

Boot and lifecycle

- `runId` is the one handle: map key, VM name (`greenroom-<runId>`), run directory.
- `Create` holds `createMu` for its whole length, so the host-capacity check and the clone
  cannot interleave. Default limit 2 (Apple's), `-max-machines` changes it.
- `machine_create` returns `booting` at once; callers poll `machine_wait` (capped at 50 s,
  under Claude Code's 60 s first-byte timeout). `agent_wait` has the same cap.
- Ready means usable: guest agent answers, IP known, ssh key installed, sshd accepts on
  guest 127.0.0.1:22 (probed via `tart exec`). The waiting phases each get their own
  `readyTimeout` (3 min). A timeout names the last probe error.
- `machine_boot` step records `agentSeconds`, `ipSeconds`, `keySeconds`, `sshSeconds`.
- `finishBoot` writes the step before closing `ready`. `manifest.json` is written by
  temp file and rename.
- `waitReady` watches `tart run`'s process; if it exits, fail at once with the tail of
  `vm.log`. `watchProcess` does the same after ready.
- Every way a run ends stamps `destroyedAt`: destroy, failed boot, VM exit, and a reattach
  that finds the VM gone (dated from the run's last evidence).
- SIGINT stops HTTP only. Machines keep running; `loadState` reattaches on next start.
- Every guest-touching call goes through `awaitReady` and fails with "call machine_wait
  and try again" rather than blocking.
- The daemon never dials a guest over TCP in-process. macOS gates local-network access
  per binary and each `go build` is a new identity. Use `tart exec` (vsock) or Apple-signed
  subprocesses (ssh, rsync, nc).

Evidence

- Only the recorder hands out step numbers (`begin` then `complete`). Claim the number
  before naming a file.
- `manifest.Steps` is a high-water mark, not a count. Anything that reports a count reads
  `machine.ReadStepLog`.
- Every tool call records itself (input, output, error, duration). A new tool does too.
- Frames: every `-frame-interval` (default 2 s, 0.5 s while a control lease is held,
  0 disables) into `frames/<unix-ms>.jpg` plus a line in `frames.jsonl`. A frame cites the
  current step; it never claims a number. Capture failures are logged once, never fatal.
- A guest command's non-zero exit is `ExitCode`, not an `error`. `error` means tart failed.

Conversation and verifier

- Only `session.Store.Append` assigns message `seq`.
- The verifier is reached only through the conversation. Nothing but the actor calls
  `Turn`. There is no `machine_verify` tool.
- A verdict is a proposal. After `-max-disputes` (default 2) disputes it is contested and
  only a human can close it.
- Every human message starts a turn and gets a `reply`, `question` or `verdict`, even
  while the machine boots or is dead. A coder `note` does not start a turn.
- `Manual` answers exactly like `Verifier`, through the same `Manager` calls. Anything
  that works under `-verifier manual` works under `nim`.
- Model failures retry: `nim.RetryBackoff` (1, 2, 4, 8 s on 429/5xx/transport, honours
  `Retry-After`; a timeout is never retried), then `verifier.TurnRetryDelays` (30, 60, 120 s). After the last, the
  actor posts that it gave up. Both are package vars so tests can zero them.
- Every action that changes a machine lands in the transcript. Lifecycle events come only
  from the bridge in `main.go`.

Computer use (ADR 0009)

- At most one control lease per machine; `Manager.Input` refuses input without it. Lease
  expires after `ControlTTL` (60 s) of silence; each batch renews it. A human taking
  or releasing it posts to the transcript (`internal/api`); each batch is one
  `machine_input` step.
- The verifier takes the lease per call, not per turn, via `Manager.InputAs`. A human
  holding it is a readable error, not a failure.
- Coordinates are fractions 0 to 1. Only the manager converts to points (`ScreenOf`);
  out-of-range is clamped. `machine.Shot` carries `width`, `height`, `scale`; never
  hardcode Retina 2.
- The input helper is compiled in the guest with `swiftc` to
  `~/.greenroom/bin/greenroom-input-<inputHelperVersion>`. Bump `inputHelperVersion`
  when `guest/input.swift` changes, and rebuild `greenroom-base`. Nothing detects a stale
  image except a slow first control request. Source and input travel base64, never
  through a shell.

Sync

- `source` must be absolute; `dest` must stay inside the guest home.
- Default `dest` is `~/work/<basename>` (`GuestWorkDir`). This is pinned: SwiftPM caches
  are keyed to their absolute path and fail hard elsewhere. `images/scripts/firstboot.sh`
  uses the same path; change both together.
- rsync uses `-a`, not `-az`. Compression makes a local VM sync ~4x slower
  (`docs/10-build-transport.md`). `TestSyncBuildsTheRsyncCommand` asserts it.

Interactive sessions (`machine_session_*`)

- A session is a host `tart exec -i -t` child keyed by `(runId, sessionId)`, never a guest
  pid. Not in `state.json`; a restart drops them.
- `tart exec -t` must get a host pty (`internal/tart/pty.go`), never a pipe, or tart
  crashes on `TIOCGWINSZ`. Set the window size before start; close the slave after
  start; `EIO` on the master is clean EOF.
- Output buffer is the last 1 MiB, read by absolute offset; reads cap at 256 KiB and
  report `dropped` and `pending`. `cleanTTY` strips escapes on the way out.
- `forgetLocked` is the only way a machine leaves the map, and it detaches its sessions so
  no `tart exec` child outlives the machine.
- A pty echoes. Tests must not be satisfiable by the echoed command line.

## Tart

The daemon resolves tart in this order: `-tart`, `GREENROOM_TART`, the pinned install at
`~/.local/tart-<PinnedVersion>/tart.app/Contents/MacOS/tart`, then `PATH`.
`tart.PinnedVersion` (2.37.0) is the single source of truth. `CheckTart` logs a mismatch
and never refuses to start.

Homebrew cannot install current tart (tap stuck at 2.32.1, formula broken, project moved
to `openai/tart`, ADR 0010). Install from the signed release:

```sh
V=2.37.0
curl -sLO "https://github.com/openai/tart/releases/download/$V/tart.tar.gz"
curl -sL  "https://github.com/openai/tart/releases/download/$V/tart_${V}_checksums.txt" \
  | grep tart.tar.gz | shasum -a 256 -c -
mkdir -p ~/.local/tart-$V && tar xzf tart.tar.gz -C ~/.local/tart-$V
~/.local/tart-$V/tart.app/Contents/MacOS/tart --version
```

2.32.1 cannot read a VM made by `clone --stacked` in 2.37.0.

## Image

`scripts/build-image.sh` clones the default image, boots it, runs `prepare-image`
(`machine.PrepareGuest`: compile the input helper, install the ssh key) and stops it.
Clones of `greenroom-base` skip the ~28 s first-control compile.

- `PrepareGuest` ends with `sync` in the guest. `tart stop` does not flush guest pages;
  without `sync` the helper is gone on next boot. `TestPrepareGuestSyncsBeforeReturning`
  pins it.
- This is a different `greenroom-base` from the Packer build in `images/`. See the
  inconsistency note there.

## Test seams

- `WithTartBin` points at the fake tart in `internal/testsupport/faketart.go`. It records
  every call; control files turn on failures. The list is in that file's header comment, plus
  `fail-keyinstall` and `tart-version` (fake a version mismatch). It writes
  `session-stdin` (`tty <rows> <cols>` or `pipe`) so tests prove a session got a pty.
- `WithSSHProbe`, `WithReadyTimeout` shorten or replace boot waits.
- A fake `rsync` earlier on `PATH` covers `Sync`.
- Test at the highest seam that sees the behaviour: `internal/mcpserver/*_test.go` runs a
  real MCP client over HTTP against every tool; `internal/api/api_test.go` drives the real
  routes and SSE over `httptest`.

## Known divergence from ADRs

- ADR 0004 specifies a SQLite store; the code uses `state.json`.
