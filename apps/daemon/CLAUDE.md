# daemon

One Go binary, `greenroom`. MCP on `/mcp`, companion API on `/api/`, `/healthz`. Drives
Tart as a subprocess. State and evidence under `~/.greenroom/` (`state.json`,
`daemon.lock`, `runs/<runId>/`).

## Commands

```sh
go build ./...
go test ./...                                   # no VM; fake tart
go test -race ./...                             # before touching boot, recorder or sessions
go test ./internal/machine -run TestFoo
go test -tags tart -run TestEndToEnd -v -timeout 10m .         # real VM
go test -tags tart -run TestEndToEndSession -v -timeout 12m .  # guest pty, ^C, a 3 MB flood and close in a real VM
go test -tags tart -count=1 -timeout 20m ./...  # whole VM suite, as CI runs it
golangci-lint run ./...

go run . serve                                  # 127.0.0.1:7777, root ~/.greenroom
go run . serve -verifier manual                 # no model; a person types instructions
go run . serve -image greenroom-base -max-machines 2 -frame-interval 2s
go run . serve -tart <path>                     # or GREENROOM_TART
go run . prepare-image -vm <running vm>          # build-image.sh runs it; not on its own
go run . check-image -image <local image> [-out dir]   # the dialog gate, on a clone of a clone
go run ./internal/testsupport/smokeclient -url http://127.0.0.1:7777/mcp [-live <dir>]

scripts/install.sh      # launchd agent com.greenroom.daemon; honours GREENROOM_VERIFIER, GREENROOM_IMAGE, GREENROOM_ENV
                        # image default: local greenroom-lean-a, then greenroom-base, then upstream Cirrus
scripts/uninstall.sh    # keeps the binary and ~/.greenroom
scripts/build-image.sh [-base <oci>] [-name greenroom-base] [-lean] [-force]   # ends with check-image
```

`usage()` prints each subcommand's flag set, so `greenroom` with no arguments lists every flag.
`-verifier` defaults to `GREENROOM_VERIFIER`, then `nim`. `nim` without `NVIDIA_API_KEY`
runs with no verifier and says so in each run's transcript, after every message that starts a
turn (`noVerifierNotice` + kind), so a client never waits for an answer.

HTTP has no authentication. `api.LocalOnly` wraps every route: 403 unless `Host` is loopback
(DNS rebinding) and any `Origin` is loopback (cross-site). Writes under `/api` with a body
must be `application/json` (415). The companion and smoke client send a loopback Host and no Origin.

## Layering

Each layer depends only on the ones below. Keep it that way.

- `main.go` - flags, HTTP mux, and the lifecycle bridge (manager events to transcript
  events: ready, failed, stopped, destroyed).
- `internal/mcpserver` - the only agent-facing surface. Tool schemas, defaults, PNG to
  JPEG. No VM logic. `recoverPanics` turns a handler panic into that call's error: the SDK
  runs handlers on its own goroutines, beyond net/http's recovery, so a panic there ends the
  whole daemon (issue #50).
- `internal/api` - the companion's routes and SSE stream (ADR 0007). Sibling of
  `mcpserver`. Neither holds logic the other needs.
- `internal/verifier` - greenroom's agent, one actor per run. `Verifier` (NIM) and
  `Manual` share one turn shape.
- `internal/session` - a run's conversation (`conversation.jsonl`). Not the same thing as
  `machine.PTYSession`; never name that type `Session`.
- `internal/machine` - lifecycle and source of truth. `Manager`, the recorder
  (`manifest.json`, `steps.jsonl`, `frames/`, `frames.jsonl`), computer use (`input.go`,
  `ui.go`, guest helper in `guest/input.swift`), the live screen (`screen.go`), pty sessions
  (`ptysession.go`).
- `internal/nim` - OpenAI-compatible client for NVIDIA NIM.
- `internal/tart` - the only package that knows tart's arguments and output.

## Invariants

Boot and lifecycle

- `runId` is the one handle: map key, VM name (`greenroom-<runId>`), run directory.
- `Create` holds `createMu` for its whole length, so the host-capacity check and the clone
  cannot interleave. Default limit 2 (Apple's), `-max-machines` changes it.
- `machine_create` returns `booting` at once; callers poll `machine_wait` (capped at 50 s,
  under Claude Code's 60 s first-byte timeout). `agent_wait`, `machine_exec` and
  `machine_exec_wait` have the same cap. No tool may block longer.
- Ready means usable: guest agent answers, IP known, ssh key installed, sshd accepts on
  guest 127.0.0.1:22 (probed via `tart exec`). The waiting phases each get their own
  `readyTimeout` (3 min). A timeout names the last probe error.
- `machine_boot` step records `agentSeconds`, `ipSeconds`, `keySeconds`,
  `captureAlertSeconds`, `desktopPrefsSeconds`, `timeZoneSeconds`, `inputHelperSeconds`,
  `toolchainSeconds`, `desktopSeconds`, `sshSeconds`.
- Boot reads the image's toolchain manifest (`base.go`, `ToolchainPath`, ADR 0019) into
  `Machine.Toolchain` as the image wrote it, `{"known":false}` when absent. The daemon never
  interprets it and never assumes a toolchain. Error key `toolchainError`, never fatal.
- Boot checks the desktop once (`desktopcheck.go`, ADR 0018), after the login settles (Dock
  and Finder up, Finder running 12 s, since loginwindow relaunches apps about then):
  `greenroom-input --desktop` lists on-screen windows and regular apps, compared with the allowlist the image gate uses.
  The result is `Machine.Desktop` (and `desktopFindings` in the step). It surfaces, never
  sweeps: nothing in the daemon closes a window or quits an app it did not open, and a boot
  `pkill` of an app the image starts is not a fix (issue #60). Fix the image instead.
- Boot puts the guest in the host's time zone (`timezone.go`, from `TZ` or `/etc/localtime`, step
  keys `timeZone`, `timeZoneError`, issue #77): the image runs in UTC, and the recording's
  menu bar clock disagreed with every time the companion prints. Only a tz database name
  reaches the guest shell. Never fatal. `WithHostTimeZone` replaces the lookup in tests.
- Boot checks the image's input helper (`helperboot.go`, issue #41). One older than
  `inputHelperVersion` is logged with the fix (`build-image.sh -force`), recorded as
  `inputHelperStale` and `inputHelperFound`, and compiled before ready, so the first UI
  call does not pay 30 to 50 s. A failure is `inputHelperError`, never fatal.
- Boot writes replayd's screen-capture approvals (`capturealert.go`, ADR 0013) before ready, so
  before the frame recorder's first capture and the live helper. Without them macOS 15+
  shows "tart-guest-agent is requesting to bypass the system private window picker" over
  the screen. The alert is decided by `kScreenCaptureApprovalLastUsed` alone, which
  replayd sets to now on every capture and resets after 30 idle days or a clock jump, so
  boot writes LastUsed, LastAlerted and the hint date in 3024 unconditionally (a baked
  record is as old as the image), for tart-guest-agent and sshd-keygen-wrapper, paths
  resolved each boot. replayd caches the file, so it is stopped across the write and
  killed after, then kickstarted and waited for (until it is back every capture fails
  with "could not create image from display"). `ensureCaptureApproval` runs before each
  screenshot and frame and when a live stream starts, never during one, at most once a
  minute by the wall clock (the monotonic clock stops while the host sleeps). Its check
  only reads; a reset or aged record is rewritten. Killing replayd stops every
  ScreenCaptureKit session, so a rewrite, and `machine_approve_capture` (a record for an
  app under test, keyed by its bundle URL), end a running live stream first with a
  reason; viewers reconnect. Both hold `input.approval.mu`, so writes never overlap. A failure is logged and recorded as
  `captureAlertError`, never fatal: the machine works under the alert. In `PrepareGuest`
  it is fatal.
- Boot also sets desktop preferences (`desktopprefs.go`, step key `desktopPrefsSeconds`,
  `desktopPrefsError`): "Click wallpaper to reveal desktop" off, so a missed click cannot
  hide every window, window restore at login off, automatic text substitutions off (issue
  #81: "a  b" was typed as "a. b"), and display sleep, screensaver and
  screen lock off (a sleeping guest display makes every capture black, with no error).
  Also never fatal. `prepare-image`
  bakes both with the same scripts, so build time and boot time cannot disagree.
- `finishBoot` writes the step before closing `ready`. `manifest.json` is written by
  temp file and rename.
- `waitReady` watches `tart run`'s process; if it exits, fail at once with the tail of
  `vm.log`. `watchProcess` does the same after ready; a reattached machine has no process,
  so it polls `tart list` every `WithVMPollInterval` (15 s) instead.
- `Destroy` cancels the boot and waits for `finishBoot` to return, which then records
  nothing. It also waits for the frame recorder to return, so no frame lands in the run
  directory after it. The machine stays in the map and `state.json` (marked `destroying`) until its
  VM is deleted; stop and delete ignore the caller's context.
- `state.json` is written atomically outside `m.mu` (`saveState`, ordered by `stateMu`).
  A corrupt file is moved to `state.json.corrupt-<ts>`; a run that cannot be reopened is
  logged and skipped. Neither stops the daemon.
- Every way a run ends stamps `destroyedAt`: destroy, failed boot, VM exit, and a reattach
  that finds the VM gone (dated from the run's last evidence).
- SIGINT stops HTTP only. Machines keep running; `loadState` reattaches on next start.
- `serve` takes an exclusive `flock` on `<root>/daemon.lock` (holding its pid) and binds
  `-addr` before it reads `state.json` or starts a verifier. A second daemon on the same root
  or address exits without touching either: one that got as far as its actors answered live
  runs and duplicated their seqs (issue #63). The kernel drops the lock on any exit.
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
  The waits (`machine_wait`, `agent_wait`, `machine_exec_wait`) and `machine_list` only
  read, and record nothing.
- A result's `step` is claimed before its output is recorded, so `output.step` equals
  `seq` (issue #47). `recorder.step` is only for outputs that carry no step.
- A JSONL record's last line with no newline is torn (a crash mid-append) or still being
  written: every reader skips it, and `newRecorder` cuts it off before its first append so
  the next line is not glued onto it (issue #67). A bad line that ends in a newline is damage
  and stays an error.
- Frames: every `-frame-interval` (default 2 s, 0.5 s while a control lease is held,
  0 disables) into `frames/<unix-ms>.jpg` plus a line in `frames.jsonl`. A frame cites the
  current step; it never claims a number. The first capture failure and the first recovery
  after it are logged, never fatal. A host that sleeps (lid closed) suspends the VM: frames
  stop for the whole sleep, and the first capture after wake can fail once with "could not
  create image from display".
- A guest command's non-zero exit is `ExitCode`, not an `error`. `error` means tart failed.
- Last activity (`Manager.LastActivity`, `/api/runs` `lastActivity`, `machine_list`
  `idleSeconds`, the host-limit error) is the newest step end or message, never a frame:
  the recorder captures an idle machine too. Messages reach the manager through
  `SetMessageActivity`, wired in `main.go`. greenroom reports idle time and never destroys
  a machine on its own; reaping is the user's call.

Exec

- `machine_exec` runs `/bin/sh -c execWrapper greenroom-exec <script>`: the login zsh
  writes to temp files that are printed after it exits. `tart exec` returns only when
  every holder of the guest's stdout/stderr pipes closes them, so without the wrapper
  `./App &` (or `(cd x && ./App) &`) holds the call until its timeout. `cmd.WaitDelay`
  on the host does not help: tart itself stays up. Output a background child writes
  after the shell exits is lost. zsh `-c` runs `a && b &` with `a` in the foreground;
  that is zsh, not us.
- The timeout is enforced in the guest (ADR 0014, issue #28): the wrapper puts zsh in its
  own process group (`set -m`) and a watchdog TERMs it at the timeout, KILLs it 5 s later.
  The result keeps the output so far, exit 124, `timedOut`. The host waits the timeout
  plus `execHostGrace`. The wrapper's stderr is `/dev/null` (job notices); the command's
  goes out through fd 3, which children must not inherit (`3>&-`).
- The login zsh is not interactive and never reads `/etc/zshrc`, so the wrapper runs its
  `disable log` itself (issue #40): `log` is `/usr/bin/log`, not zsh's builtin. Other
  interactive-only settings from `/etc/zshrc` are not applied.
- `machine_exec` waits at most `waitSeconds` (max 50), then returns `running` and an
  `execId`; `machine_exec_wait` collects the rest (ADR 0015, issue #39). The command is a
  job owned by the machine (`execjob.go`), detached from the call, cancelled by
  `detachLocked`. Its step is claimed at start and written when it ends. The verifier's
  `Manager.Exec` blocks on the same job.
- Each stream keeps its first `ExecHeadLimit` (8 KiB) and last `ExecTailLimit` (24 KiB),
  with `stdoutBytes`/`stderrBytes` and `*Truncated` (issue #29). `tart.ExecTo` streams
  into that bounded writer, so no output is ever held whole. The tool description quotes
  the limits; change both together.
- A `cwd` or sync `dest` of `~` or `~/x` means the guest home (`homeRelative`). Both are
  otherwise shell-quoted, so a tilde would never expand and rsync would make a dir `~`.

Conversation and verifier

- Only `session.Store.Append` assigns message `seq`.
- The verifier is reached only through the conversation. Nothing but the actor calls
  `Turn`. There is no `machine_verify` tool.
- A verdict is a proposal. After `-max-disputes` (default 2) disputes it is contested and
  only a human can close it: the coder may accept or dispute only a `proposed` verdict
  (issue #34, ADR 0006 rule 4).
- Every human message starts a turn and gets a `reply`, `question` or `verdict`, even
  while the machine boots or is dead. A coder `note` does not start a turn. Once the
  machine is destroyed the run has no actor, so `Actors.answerEnded` answers the last
  unanswered turn-starting message, once, with an event saying nothing will answer: on the
  "destroyed" event (a turn queued or cut short by `Stop`) and on each later message (issue #33).
- `agent_wait` keeps waiting while everything new is verifier `progress`, and returns the
  batch when anything else lands or at the timeout: one call per verifier turn, not one per
  step (issue #46).
- `Manual` answers exactly like `Verifier`, through the same `Manager` calls. Anything
  that works under `-verifier manual` works under `nim`.
- Model failures retry: `nim.RetryBackoff` (1, 2, 4, 8 s on 429/5xx/transport, honours
  `Retry-After`; a timeout is never retried), then `verifier.TurnRetryDelays` (30, 60, 120 s). After the last, the
  actor posts that it gave up. Both are package vars so tests can zero them.
- A step the endpoint cut off (`finish_reason: length`, or text holding a `<tool_call>`) is
  never posted: the fragment stays out of the context and the model is told to answer again,
  shorter; on the last step the reply says the answers were cut off (issue #71). Chat asks
  for `nim.ChatMaxTokens` (8192) because a reasoning model thinks inside that budget.
- A task owes a verdict: while a coder or human task has no verdict after it
  (`hasOpenTask`), a turn that tries to end in `reply` (tool or prose) is sent back once
  with `openTaskNudge` for `report_verdict` or `ask`. A message arriving mid-turn made the
  model answer it and leave the task without a verdict (issue #89). Once per turn, never on
  the last step, so a model that replies again is heard.
- `project` rebuilds the verifier's own past messages (progress, reply, ask, verdict) as
  the assistant tool calls that made them, with results; never as assistant prose. A
  model imitates its history: projected as "[I reported verdict ...]" text, it answered a
  later task with a prose verdict, stored as a reply, so an accepted fail stood. Prose
  that still looks like a verdict (`proseVerdict`) is sent back once per turn to call the tool.
- Every action that changes a machine lands in the transcript. Lifecycle events come only
  from the bridge in `main.go`.

Computer use (ADR 0009)

- At most one control lease per machine; `Manager.Input` refuses input without it. Lease
  expires after `ControlTTL` (60 s) of silence; each batch renews it. A human taking
  or releasing it posts to the transcript (`internal/api`); each batch is one
  `machine_input` step. Expiry is lazy, so a human lease that lapsed is posted ("human lost
  control of the screen after N actions") when the human takes the screen again
  (`TakeControlReporting`) or lets go of it, never as a second "took control" (issue #57).
- The verifier takes the lease per call, not per turn, via `Manager.InputAs`. A human
  holding it is a readable error, not a failure. The actor marks each turn with
  `SetVerifierTurn`, and while one is open the coder's `InputAs` is refused with words
  pointing at `agent_wait` (issue #82): per-call leases let both drive the same app. A human
  is never refused for it.
- `validateActions` also refuses a click, down, up or move without both `x` and `y`: the
  helper would post it at the pointer (issue #85). The verifier's `machine_input` decodes with
  `DisallowUnknownFields`, so an `element` in a batch is an error, not a click at the pointer.
- Coordinates are fractions 0 to 1. Only the manager converts to points (`ScreenOf`), and
  back for the UI tree; out-of-range is clamped. `machine.Shot` carries `width`, `height`,
  `scale`; never hardcode Retina 2 (the tahoe guest is 1024x768 at scale 1).
- The input helper is compiled in the guest with `swiftc` to
  `~/.greenroom/bin/greenroom-input-<inputHelperVersion>`. Bump `inputHelperVersion`
  when `guest/input.swift` changes, then rebuild the image `install.sh` serves by default:
  `greenroom-lean-a` when it exists (`build-image.sh -lean -name greenroom-lean-a -force`),
  and `greenroom-base` as the rollback target (`build-image.sh -force`). Locally boot detects a
  stale image, warns and compiles the helper (see Boot and lifecycle). The VM suite workflow bakes and tests
  `greenroom-base-v<inputHelperVersion>` itself, so a bump rebuilds its image once. Source and input travel base64, never
  through a shell.
- `InputAction` means what the tools say: positive `deltaY` scrolls down, positive `deltaX`
  right. A positive CGEvent wheel scrolls up and left, so `pixels` negates both on the way to
  the helper (issue #51) and clamps them to Int32, where the helper's conversion would trap
  (issue #36). The helper never sees the tools' sign; the companion converts AppKit's.
- `validateActions` refuses an unknown action type, button or modifier before a batch posts
  anything, because the helper drops an unknown modifier and makes an unknown button a left
  click (issue #31). Its name lists mirror `flags` and `mouseButton` in `input.swift`; change
  them together.
- A shortcut posts real modifier key downs and ups around the key (`press` in
  `input.swift`). A flag on the key event alone leaves the window server thinking the
  modifier is held, and the next typed text arrives as command-1, command-2.

UI tree (ADR 0012)

- `machine_ui` (both the verifier and MCP) is `greenroom-input --ui-base64`: the frontmost
  or named app's on-screen AX elements, frames clipped to window and scroll areas, menu
  bar and bare layout skipped, capped at `limit` (default 250, verifier 200, max 1000).
  It needs Accessibility, which the image grants to tart-guest-agent; the helper inherits
  it. No lease. Every read is a `machine_ui` step with the whole tree.
- `Manager.UI` keeps the last good tree per machine and reader (`HolderCoder`,
  `HolderVerifier`); `machine_click {element}` aims at the caller's own tree via
  `ElementCenter` without re-reading, so a verifier read never retargets a coder's ids
  (issue #35). An optional `uiStep` refuses a click whose ids are not from the caller's
  latest read. Nothing checks that the app is still frontmost. `UITree.Outline` is the text both
  surfaces show a model; keep it one element a line with its id and center.
- The verifier's prompt makes the tree the way to aim and a coder's constraints hard rules
  (`verifier.go`). `TestSystemPromptBindsConstraintsAndAimsFromTheTree` pins the phrases.

Live screen (ADR 0011)

- Every VM boots `tart run --no-graphics`. Graphics mode (`--vnc-experimental`, the old
  `watch`) is retired (ADR 0016, issue #7: the guest GPU restarts and a crash dialog
  covers the screen). Do not add a graphics or VNC path; a person watches through this
  stream in the companion.
- One `greenroom-input --serve` per machine, on plain pipes (`tart.StartPipe`, never a
  pty). The first `WatchScreen` starts it; it stops `WithScreenIdle` (30 s) after the last
  viewer leaves, and at once in `detachLocked`. The start command first pkills an orphan
  `--serve`: the guest agent does not close a helper's stdin when its host exec dies.
- Fan-out never blocks the reader or another viewer. A full viewer loses its backlog, gets
  the cached FORMAT, and resumes at the next keyframe. Every new viewer and every drop sends
  KEYFRAME: a still screen sends nothing on its own.
- LOG goes to the daemon log, never to viewers.
- `Manager.Input` uses the stream (INPUT, then its ACK) while it runs, else the one-shot
  exec. The ACK deadline is what the queued batches take to post (`inputCost`: sleeps and
  typed keys) plus 10 s, never a fixed limit: long sleeps and `type` are valid batches. It
  falls back only if nothing was sent, so a batch is never posted twice. Lease, step and
  scaling are the same on both paths.
- `/screen/live` answers 409 at once for a machine that is not ready; it never waits in
  `awaitReady`.

Sync

- `source` must be absolute; `dest` must stay inside the guest home.
- Default `dest` is `~/work/<basename>` (`GuestWorkDir`). This is pinned: SwiftPM caches
  are keyed to their absolute path and fail hard elsewhere.
- rsync uses `-a`, not `-az`. Compression makes a local VM sync ~4x slower
  (`docs/10-build-transport.md`). `TestSyncBuildsTheRsyncCommand` asserts it.

Interactive sessions (`machine_session_*`)

- A session is a host `tart exec -i` child (plain pipes, never `-t`) keyed by
  `(runId, sessionId)`, never a guest pid. Not in `state.json`; a restart drops them.
- The pty is made in the guest (ADR 0017, issue #30): `sessionWrapper` runs `script -q -F`
  into `${TMPDIR:-/tmp}/greenroom-session.<id>` with its stdout to `/dev/null`. Output must
  never stream through tart: `tart exec -t`, and even guest `script` writing to a non-tty
  exec's stdout, stalls on fast output and wedges every guest call on the machine. The
  long-lived exec carries only input.
- A follower goroutine per session copies the file into the window with short `tart exec`
  reads (`sessionReadScript`), skipping to the last 1 MiB when the guest is further ahead
  (counted as `dropped`). A send or read wakes it; idle it polls every 2 s. `running` goes
  false only after the file is read to its end (`PTYSession.ended`).
- Close runs `sessionCloseScript` (HUP then KILL to script and everything on its pty,
  remove the files) before killing the host exec: killing `tart exec` never reaches the
  guest. Destroy only ends host processes; the VM takes the rest.
- A close can beat the wrapper (start returns once the host exec spawns). Close leaves a
  `<file>.closed` tombstone; the wrapper writes its pid, then checks it and starts nothing.
  Keep that order in both scripts, or a racing close orphans the command.
- Evicting ended sessions at the 16-session cap cleans their guest files in one background
  exec, so `machine_session_start` never waits on the guest for it.
- The size (40x120) is fixed at start; there is no resize from the host.
- Output buffer is the last 1 MiB, read by absolute offset; reads cap at 256 KiB and
  report `dropped` and `pending`. `cleanTTY` strips escapes on the way out.
- A finished session's read carries `exitCode` (tart forwards the guest's), absent while
  running and when tart itself failed (`error`) (issue #62).
- `forgetLocked` is the only way a machine leaves the map, and it detaches its sessions so
  no `tart exec` child outlives the machine.
- A pty echoes. Tests must not be satisfiable by the echoed command line.

## Environment

Read from the process environment, or from `.env` at the repo root (`-env-file`).
Values already set in the environment win.

| Variable | Default | Effect |
| --- | --- | --- |
| `GREENROOM_VERIFIER` | `nim` | `nim` (model), or `manual` (you type the instructions). `-verifier` overrides. |
| `NVIDIA_API_KEY` | none | Without it the `nim` verifier is off; machine tools still work. |
| `NVIDIA_BASE_URL` | `https://integrate.api.nvidia.com/v1` | OpenAI-compatible endpoint. |
| `GREENROOM_VERIFIER_MODEL` | none, required with a key | Model for verifier turns. |
| `GREENROOM_VISION_MODEL` | none | Model that describes screenshots. Unset: the verifier works without seeing the screen. |
| `GREENROOM_TART` | none | tart binary, see below. `-tart` overrides. |

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

The only image recipe (ADR 0018, `images/README.md`). `scripts/build-image.sh` clones the
default image to `<name>-building`, boots it, runs `prepare-image` (`machine.PrepareGuest`:
input helper, ssh key, screen-capture approvals, desktop preferences, `guest/base.sh`,
`guest/toolchain.sh`; then `-lean`'s `guest/lean.sh`; then `DisableSoftwareUpdate` last,
because lean.sh still talks to softwareupdated), stops it, runs `check-image` on it and
renames it to `<name>` only if that passes. A failed gate deletes the build.

- BASE gets every fix that needs no click; LEAN adds only hiding. A fix that makes a
  machine work goes in `base.sh`, never `lean.sh`, so `greenroom-base` stays complete.
- `base.sh` and `lean.sh` read every setting back and fail by check name. `base_test.go`
  and `lean_test.go` run the real scripts in `/bin/sh` under stubs (real SQLite and
  plutil for base).
- Apple Events rows are per target bundle id, for tart-guest-agent (path resolved at build,
  never pinned) and sshd-keygen-wrapper, in the system and the tccd-open user TCC.db. A new
  target app that agents script needs a row there.
- loginwindow relaunches every app in
  `~/Library/Group Containers/group.com.apple.loginwindow.persistent-apps/persistantApps` at
  login, whatever `TALLogoutSavesState` says, and the list follows running apps. Anything
  left running during a build comes back on every machine; `base.sh` stops the listed apps
  and cuts the list to Finder (issue #60).
- Software Update is off through two launchd jobs, `com.apple.softwareupdated` and
  `com.apple.mobile.softwareupdated`; the first alone lets the daemon start after a reboot.
- `check-image` (`imagecheck.go`) never writes what boot writes (approvals, desktop
  prefs): the image must pass alone. Its exercises run under a process-group watchdog like
  `execWrapper`'s, because a blocked osascript keeps `tart exec` open after its shell dies.
  The allowlist lives in `desktopcheck.go` and is shared with boot; widen it only for a
  window every clean desktop has, with a screenshot as evidence.

- `PrepareGuest` ends with `sync` in the guest. `tart stop` does not flush guest pages;
  without `sync` the helper is gone on next boot. `TestPrepareGuestSyncsBeforeReturning`
  pins it.
- `-lean` (`prepare-image -lean`, `machine.ApplyLeanProfile`, script `guest/lean.sh`) is
  variant A of `docs/image-experiment/`: only the core apps in the Dock, the other apps'
  gui-domain agents `launchctl disable`d, widgets, banners, Siri, indexing, update
  downloads and installs, Time Machine and setup prompts off. (Update checks are off in
  base, where softwareupdated is disabled.) It writes preferences and launchd's disabled list only;
  never SIP, the authenticated root or the sealed volume. It runs as the user through
  `/bin/sh`, not zsh: zsh does not word-split `$list`, and one disable of a newline-joined
  "label" once passed a substring read-back. The read-back matches labels exactly. It
  cannot hide apps from macOS 26's Apps view (the Launchpad replacement); that needs the
  sealed volume. Boot applies nothing lean; the image carries it.
  `lean_test.go` runs the real script under stub `defaults`/`launchctl`/`sudo`.

## Test seams

- `WithTartBin` points at the fake tart in `internal/testsupport/faketart.go`. It records
  every call; control files turn on failures. The list is in that file's header comment, plus
  `fail-keyinstall`, `fail-capture-approval`, `fail-desktop-prefs`, `fail-timezone`, `fail-lean`, `ui.json` (what `--ui-base64`
  prints), `desktop.json` (what `--desktop` prints), `toolchain.json` (the image's manifest),
  `fail-base`, `fail-toolchain`, `fail-softwareupdate`, `fail-check-<exercise>` and
  `softwareupdate` (image build and gate), `tart-version` (fake a version mismatch), `exec-sleep` and `exec-stdout` (a slow or
  loud machine_exec), `input-stale` (an image with an old helper) and `session-exit-code`. It writes
  `session-stdin` (`tty <rows> <cols>` or `pipe`) so tests prove a session reaches tart on a
  pipe. It models a session with the host's real `script` running `cat`, and runs the real
  session read and close scripts, with `TMPDIR` set to the control dir.
- `internal/machine/sessionguest_test.go` runs the session wrapper, read and close scripts
  for real on the host (same `script`, `stat`, `ps` as the guest).
- The fake tart runs `exec -i ... --serve` as the fake live screen helper by re-executing
  the test binary (`testsupport/fakescreen.go`, gated by an env var in its `init`). Its control
  files are listed there; `testsupport.ServeStarts` counts starts.
- `WithSSHProbe`, `WithReadyTimeout` shorten or replace boot waits; `WithScreenIdle` the
  live screen's idle stop.
- A fake `rsync` earlier on `PATH` covers `Sync`.
- Test at the highest seam that sees the behaviour: `internal/mcpserver/*_test.go` runs a
  real MCP client over HTTP against every tool; `internal/api/api_test.go` drives the real
  routes and SSE over `httptest`.

## Known divergence from ADRs

- None known. ADR 0004 is amended for `state.json`.
