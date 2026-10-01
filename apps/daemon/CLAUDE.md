# daemon

One Go binary, `greenroom`. MCP on `/mcp`, companion API on `/api/`, `/healthz`. Drives
Tart as a subprocess. State and evidence under `~/.greenroom/` (`state.json`,
`daemon.lock`, `runs/<runId>/`).

Decisions about the daemon alone go in `apps/daemon/docs/adr/` ("daemon ADR NNNN"); a bare
"ADR NNNN" here means the root `docs/adr/`.

## Commands

```sh
go build ./...
go test ./...                                   # no VM; fake tart
go test -race ./...                             # before touching boot, recorder or sessions
go test ./internal/machine -run TestFoo
go test -tags tart -run TestEndToEnd -v -timeout 10m .         # real VM
go test -tags tart -run TestEndToEndSession -v -timeout 12m .  # guest pty, ^C, a 3 MB flood and close in a real VM
go test -tags tart -count=1 -timeout 45m ./...  # whole VM suite, as CI runs it
# every e2e test clones the local GREENROOM_BASE_IMAGE (default greenroom-base), never pulls Cirrus
golangci-lint run ./...

go run . serve                                  # 127.0.0.1:7777, root ~/.greenroom
go run . serve -verifier manual                 # no model; a person types instructions
go run . serve -image greenroom-base -max-machines 2 -frame-interval 2s
go run . serve -tart <path>                     # or GREENROOM_TART
go run . serve -sweep-orphans-after 0           # keep orphaned run clones (default: sweep those older than 6h at start)
go run . serve -desktop-toolkit                 # desktop looks and inputs through one guest agent per machine (daemon ADR 0005)
go run . sweep-orphans [-delete] [-older-than 6h] [-root dir]   # list (or delete) what that sweep takes (#103)
go run . serve -public-host gr.example.com      # or GREENROOM_PUBLIC_HOST; needs GREENROOM_TOKEN; -dist <dir>
go run . prepare-image -vm <running vm> [-xcode <Xcode.app>]   # build-image.sh runs it; not on its own
go run . check-image -image <local image> [-out dir]   # the dialog gate, on a clone of a clone
go run ./internal/testsupport/smokeclient -url http://127.0.0.1:7777/mcp [-live <dir>]
go run . connect [-url URL] [-token T] [-config F] [-dir D]   # stdio MCP server for a daemon on another host
go run . connect -check                          # prints "ok: <url> (<n> tools)" or the reason, exit 1
go run . bench run -split dev [-case a,b] [-kind mutant] [-tier simple] [-trials 3] [-out f.jsonl] [-min-free-gb 5] [-disk-wait 10m]   # ADR 0025; real VMs and the model
go run . bench score [-tier simple] [-bench dir] <results.jsonl>   # writes <results>.md

scripts/install.sh [-rebuild] [-dry-run]   # launchd agent com.greenroom.daemon; honours GREENROOM_VERIFIER, GREENROOM_IMAGE, GREENROOM_ENV
                        # image default: local greenroom-lean-a, then greenroom-base, then upstream Cirrus
                        # then checks those images (image-status) and prints, or with -rebuild runs, the rebuild
                        # stamps the build (ADR 0033) and records the checkout as GREENROOM_CHECKOUT in the plist
                        # unset GREENROOM_* reuse the replaced job's settings (scripts/install-settings.sh)
../../scripts/update.sh [--check]   # fast-forward main, install.sh, then the Companion's install.sh (ADR 0033)
go run . version                # "greenroom 0.0.2 abc1234 (local changes), built <time>", or "unstamped build"
go run . image-status [-image a,b] [-rebuild-args]   # local images' input helper and recipe against this daemon's (#159)
scripts/uninstall.sh    # keeps the binary and ~/.greenroom
scripts/build-image.sh [-base <oci>] [-name greenroom-base] [-lean] [-force] [-xcode <app>] [-disk-size 90]   # ends with check-image
```

`usage()` prints each subcommand's flag set, so `greenroom` with no arguments lists every flag.
`-verifier` defaults to `GREENROOM_VERIFIER`, then `nim`. `nim` without `NVIDIA_API_KEY`
runs with no verifier and says so in each run's transcript, after every message that starts a
turn (`noVerifierNotice` + kind), so a client never waits for an answer.

`api.Guard` wraps every route (ADR 0021). A loopback `Host` needs no token: 403 unless any
`Origin` is loopback too (cross-site). The `-public-host` (or `GREENROOM_PUBLIC_HOST`) name
needs `Authorization: Bearer $GREENROOM_TOKEN` (401 otherwise) and no `Origin` at all (403).
Any other `Host` is 403 (DNS rebinding); the message keeps the word "loopback", which the
Companion's advice keys off. Writes under `/api` with a body must be `application/json`
(415), except `PUT /api/runs/{id}/sync`, which must be `application/gzip` (no HTML form can
send either). The companion and smoke client send a loopback Host and no Origin.

- `api.Guard` marks a request it admitted through the public host (`api.FromPublicHost`,
  a context value only Guard sets). `routes` answers such an MCP request with a second
  server built with `mcpserver.ForPublicHost()`, chosen per request by the SDK's getServer
  callback. Its `machine_pull` refuses a `dest` (`ErrRemoteDest`) and copies into the run
  directory only (ADR 0022): a host path from the tunnel is host code execution
  (`~/.zshrc`, LaunchAgents). Any new tool that writes a caller-named host path must refuse
  it there too. `TestRoutesKeepAPublicHostPullInsideTheRunDirectory` pins it.
- The daemon stays on loopback; `cloudflared` on the same host forwards the public name to
  it. Tunnel traffic also arrives from 127.0.0.1, so `Host` is the only thing that tells it
  from local traffic. Never trust the peer address, and never bind a public interface: a
  remote client could then send a loopback `Host` and skip the token.
- The MCP SDK has its own DNS-rebinding check (a non-localhost Host from 127.0.0.1 is 403),
  which refuses every tunnel request. `routes` turns it off (`DisableLocalhostProtection`)
  only when a public host is set, leaving `api.Guard` as the one Host check.
  `TestRoutesServeMCPToThePublicHostWithTheToken` pins it.
- `serve` refuses to start with a public host and a token under 32 characters (after
  trimming), before it locks the root. The token is never logged.
- `GET /api/version` (`api.VersionHandler`, root ADR 0033) is the build (`internal/buildinfo`: commit,
  dirty, builtAt), `inputHelper`, `imageRecipe`, the verifier (`nim`, `manual`, `none`) and its
  models, and `checkout` (`GREENROOM_CHECKOUT`). `main.go`'s `buildVersion` fills it once at
  start; every key is always present (empty when unknown). It is behind Guard like every
  route: the public host needs the token. There is no route that updates, rebuilds or
  restarts the daemon, and there must never be one: anything reachable through the tunnel
  that makes the host build or run code is host code execution. Updating is
  `scripts/update.sh`, run on the host by a person or the Companion.
- `GET`/`HEAD /install.sh` and `/dl/<bare name>` (`api.Dist`, files in `-dist`, default
  `<root>/dist`) are the only public routes without a token (`installPath` in `guard.go`).
  Anything put in `dist` is world-readable through the tunnel. `Cache-Control: no-store`.
- `PUT /api/runs/{id}/sync?dest=&name=&mirror=&exclude=` (`upload.go`) unpacks into
  `<root>/uploads/<runId>/<name>` (emptied first), then calls `Manager.Sync` from there,
  so the default dest is `work/<name>`. `internal/tarball` (`Untar`) takes only files, dirs
  and symlinks, refuses `..`, absolute names and links that leave the directory, never
  writes through a symlink (one already in the directory included), and keeps modes and
  mtimes (rsync's quick check needs them, or every sync copies everything). Caps: 2 GiB
  body, 4 GiB unpacked, 200000 entries (413). A run's uploads go on its "destroyed" event,
  and `api.New` sweeps those of runs with no machine at start.
- `GET /api/runs/{id}/report?format=md|json[&embed=true]` (`report.go`, ADR 0034) is the
  run's proof, the same report `run_report` returns; `text/markdown` or JSON, `no-store`.
- Run summaries (ADR 0036, `internal/summary`, `api/summary.go`). `GET /api/summary` is the
  board: `{groups: [{id, title, count, runs}], macs: {free, total, text}, updatedAt}`, all
  three groups (`needs-you`, `running`, `done`) always present, newest status first.
  `GET /api/runs/{id}/summary` is one run's `summary.Summary`. The event stream sends
  `event: summary` `{runId, summary, macs}` when a run's summary changes: listeners only mark
  the run (they run under the store's and manager's locks, and a summary reads both), and the
  stream derives marked runs every `SummaryEvery` (250 ms), dropping one equal to the last it
  sent but for `elapsedSeconds`, `updatedAt` and `lastFrame`. Steps are read only for an open run or a
  failed check's picture; a run with no live machine is cached (`summaries`) until its
  conversation length or manifest mtime changes. `macs` counts the manager's machines only,
  never other VMs on the host (that needs `tart list`). `checks.items` is every check as the
  Companion's check rows read it (failed, not checked, passed; a pass carries the value it read
  as `saw`, `Agreement`), with each check's picture and mark, so steps are read for any verdict
  whose checks cite evidence (`citesEvidence`). `machine.warning` follows the files watch
  (`Machine.Files.Warning`).
  Example, the failed TipSplit run of the golden fixture:

  ```json
  {"runId": "20260923-044138-de31017819a86d84", "name": "TipSplit: split the bill", "source": "Claude Code",
   "state": "failed", "status": "Failed", "tone": "fail", "group": "needs-you",
   "detail": "Proposed by the verifier after 3:26.",
   "checks": {"total": 4, "passed": 2, "failed": 2, "pending": 0, "text": "2 of 4 checks",
              "current": {"id": "each-25", "text": "Each pays becomes $50.00 at 25%", "state": "fail", "saw": "$10.00"},
              "items": [{"id": "each-25", "text": "Each pays becomes $50.00 at 25%", "state": "fail", "saw": "$10.00"},
                        {"id": "each", "text": "Each pays is $48.00 for 3 people", "state": "fail", "saw": "$8.00"},
                        {"id": "window", "text": "Window shows Bill, Tip and People", "state": "pass"},
                        {"id": "tip", "text": "Tip is $24.00 for $120 at 20%", "state": "pass"}]},
   (each verdict row also carries its proof for the Companion: "expected", "observed", "step",
    "picture", "mark"; a pass's "saw" is the number it read, `withProof`)
   "failing": {"text": "Each pays becomes $50.00 at 25%", "expected": "$50.00", "saw": "$10.00",
               "observed": "After choosing 25%, Each pays reads $10.00.", "step": 5,
               "picture": {"kind": "screenshot", "file": "005-screenshot.png", "url": "/api/runs/<id>/artifacts/005-screenshot.png"},
               "mark": {"x": 0.515, "y": 0.635, "w": 0.23, "h": 0.05}},
   "primaryAction": {"id": "accept", "label": "Accept fail"}, "secondaryActions": [{"id": "reject", "label": "Reject"}],
   "machine": {"status": "on"}, "since": "...", "startedAt": "...", "elapsedSeconds": 986, "lastFrame": {...}, "updatedAt": "..."}
  ```
- `POST /api/runs/{id}/reboot` (`control.go`, daemon ADR 0004) is `Manager.Reboot` for a person:
  202 with the machine rebooting and its step, 409 for any refusal. The Companion has no button
  for it yet. Guest routes answer 409 while a machine reboots (`failMachine`, `ErrRebooting`).
- `GET /api/runs/{id}/pull?src=&exclude=` (`pull.go`, ADR 0022) is `Manager.PullArchive`:
  the guest's `tar czf -` streamed through `tart.ExecTo` as `application/gzip`, never held.
  The step number is the `Greenroom-Step` header; a missing source is 404 before any byte.
  tar failing after the archive has begun cannot change the 200, so the `Greenroom-Error`
  trailer names it (empty on success). Only literal `exclude` names reach the guest's tar.

## connect (ADR 0021)

`greenroom connect` (`connect.go`, `internal/remote`) runs on the agent's computer, not the
daemon's host. It is a stdio MCP server that dials `<url>/mcp` with the token, mirrors every
remote tool (definition unchanged, daemon name, version and instructions) and forwards each
call with its raw arguments, returning the daemon's result as is. A transport failure
re-dials and retries once only for the tools in `readOnlyTools` (`client.go`); any other
call is never sent twice (the daemon may have acted before the answer was lost), so its
error says it may have run. An error the daemon answered is never retried. A new tool that
only reads belongs in that list (`run_report` is; `run_finish` never is). `run_report` and
`run_finish` are forwarded unchanged: the daemon already links screenshots through its
artifact route for a public-host call, which the connect token opens.

- Config: `-url`/`-token`/`-config`, then `GREENROOM_URL`/`GREENROOM_TOKEN`, then
  `~/.greenroom/client.json`, per field.
- `machine_sync` is never forwarded: the daemon cannot read this computer. connect tars and
  gzips `source` while walking it (no temp file), honouring `exclude` itself (rsync's simple
  forms, `Excludes` in `sync.go`; no `**`), and PUTs it to `/api/runs/{id}/sync?dest=&name=`,
  plus `mirror=true` and every `exclude` pattern (trimmed), which the daemon's rsync needs to
  keep the guest's excluded paths out of a mirror and the stray count.
  Symlinks stay symlinks, never followed; sockets, devices and fifos are skipped; owners are
  not sent. Its description gains `remote.SyncNote`. The result is the route's JSON as
  structured content plus one text copy; a refusal is a tool error with the HTTP status.
- `machine_pull` is never forwarded either: the daemon would write on its own host. connect
  GETs the pull route and unpacks as it reads with `tarball.Untar` (the upload route's
  rules: a guest decides what is in the archive), applying every `exclude` itself
  (`Excludes.Covers`). `dest` must be absolute on this computer; the default is
  `<dir>/runs/<runId>/NNN-pull` with NNN from `Greenroom-Step`. `-dir` (`Options.Dir`)
  defaults to `~/.greenroom/connect` (`DefaultDir`); tests always pass a temp dir. The
  description gains `remote.PullNote`. It is not in `readOnlyTools`: that list governs
  forwarded calls only.
- `machine_screenshot` is forwarded, then its PNG is fetched through
  `/api/runs/{id}/artifacts/{name}` into `<dir>/runs/<runId>/` and `path` is rewritten in
  the structured result and its text copy (temp file, then rename). The image content is
  never touched. A failed download keeps the daemon's path and adds a text item saying so.
- Stdout is the MCP channel. Log to stderr only; nothing in `internal/remote` may print to
  stdout except `Check`, through the writer it is given. The SDK's own info logs are dropped.
- `internal/remote` does not import the daemon's packages; `connect.go` passes the version.
  `internal/tarball` is the exception: a leaf that imports nothing of the daemon's. The pull
  route's header names are written in both `api/pull.go` and `remote/pull.go`; change both.
- Tests: `internal/remote/remote_test.go` runs a real stateless MCP server behind a bearer
  check plus fake sync, pull and artifact routes. The real route and connect's `Pull` meet
  only in the tart-tagged `TestEndToEnd`.

## Layering

Each layer depends only on the ones below. Keep it that way.

- `main.go` - flags, HTTP mux, and the lifecycle bridge (manager events to transcript
  events: ready, failed, stopped, destroyed, rebooting; a `ready` or `failed` with `reboot`
  says the reboot's outcome).
- `internal/mcpserver` - the only agent-facing surface. Tool schemas, defaults, PNG to
  JPEG. No VM logic. `recoverPanics` turns a handler panic into that call's error: the SDK
  runs handlers on its own goroutines, beyond net/http's recovery, so a panic there ends the
  whole daemon (issue #50). Register every tool with `addTool`, never `mcp.AddTool`
  (daemon ADR 0007): it declares the output schema open at every depth, because clients keep
  the schemas of the build they connected to (`/mcp` is stateless, a restart never makes them
  re-list), and a closed schema turns every field added since into a client-side error
  (#251). Adding a result field is compatible; removing, renaming or retyping one is not.
  `TestOutputSchemasAreOpen` and `TestEveryToolsOutputMatchesItsSchema` (which must call
  every tool that declares an output schema) pin it.
- `internal/api` - the companion's routes and SSE stream (ADR 0007). Sibling of
  `mcpserver`. Neither holds logic the other needs.
- `internal/verifier` - greenroom's agent, one actor per run. `Verifier` (NIM) and
  `Manual` share one turn shape.
- `internal/session` - a run's conversation (`conversation.jsonl`). Not the same thing as
  `machine.PTYSession`; never name that type `Session`.
- `internal/machine` - lifecycle and source of truth. `Manager`, the recorder
  (`manifest.json`, `steps.jsonl`, `frames/`, `frames.jsonl`), computer use (`input.go`,
  `ui.go`, the guest helper in `guest/helper/`), the live screen (`screen.go`), pty sessions
  (`ptysession.go`).
- `internal/nim` - OpenAI-compatible client for NVIDIA NIM.
- `internal/tart` - the only package that knows tart's arguments and output.
- `internal/guestagent` - the guest agent channel's wire (daemon ADR 0005): frames, `Conn` (one
  connection: requests by id, BLOBs, heartbeat, deadlines) and `Supervisor` (restarts with
  backoff, generations, reconnect count, a standing PAUSE). Imports only the standard library:
  it knows nothing of machines, tart or tools. `machine` decides when it runs and what goes over
  it (`agent.go`); a `*tart.Pipe` is its `Transport`.
- `internal/desktop` - the desktop toolkit's meaning layer (daemon ADR 0006): the ops' wire types,
  `Outline`, `Diff`, `EffectOf` and every text a model reads of a toolkit call, and the tool
  arguments MCP and the verifier share (`Normalize`, `DecodeArgs`, `ToolkitCall`). No I/O, the
  standard library only, nothing of the daemon's. Every guest string is rendered quoted (`%q`),
  and a secure field's value is never rendered: lengths only (`SecretText`, `Redacted`).
- `internal/tarball` - unpacking an untrusted gzipped tar (`Untar`); used by `api` and
  `remote`, imports nothing of the daemon's.
- `internal/report` - a run's proof (ADR 0034): `Build` reads the run directory (manifest,
  steps.jsonl) and the conversation, `Report.Markdown` renders it. Below `api` and
  `mcpserver`, above `machine` and `session`; both surfaces build the report here, so there
  is one shape.
- `internal/summary` - a run in the few plain words a person reads first (ADR 0036): `Derive`
  is pure over an `Input` the caller gathers (manifest, live machine, messages, verdict,
  steps, frames); `NewBoard` groups. Below `api` and `mcpserver` (which uses `Name` to cut
  `machine_create`'s name), above `machine` and `session`. The status vocabulary, the groups
  and the action ids are the ADR's table: a new state is a row there and a case in
  `TestEveryStatusHasItsGroupActionAndWords`. Every string must stay plain words:
  `TestNoSummaryUsesAToolNameTimingOrInternalTerm` lists what is forbidden, and verifier prose
  it quotes goes through `plain`. The rules it reads by text: the bridge's "machine is ready",
  "machine rebooted and is ready", "machine failed" (and "machine failed to reboot"),
  "machine stopped", "machine destroyed" and "human destroyed" events, the
  "nobody will answer"/"nothing will answer" notices, and `machine.ScreenNotAnsweringError`'s
  "screen is not answering"; change them together. Golden: `testdata/board.golden.json`, the
  runs docs/20's Figma screens show, and `testdata/live.golden.json`, two runs recorded on a real
  machine (`testdata/<runId>/`: manifest, conversation, steps, frame lines) replayed moment by
  moment; `go test ./internal/summary -update` rewrites both. `found_test.go` holds each
  rule that run taught.
- `internal/diskimage` - a stopped VM's raw disk read on the host (`MountReadOnly`): a
  clonefile copy attached read-only with `hdiutil -nomount`, only its APFS Data volume
  mounted, read-only and `nobrowse`; `Close` unmounts, detaches and removes it. Shells out to
  `hdiutil`, `diskutil` and `plutil` only; imports nothing of the daemon's. Its test makes a
  real raw APFS image with `hdiutil create -format UDTO`.
- `internal/buildinfo` - the build's identity (ADR 0033): `commit`, `dirty` and `builtAt`, set only
  by `install.sh` through `-ldflags -X .../internal/buildinfo.<name>=`. Unstamped (`go run`, tests)
  is empty, never a guess from `debug.ReadBuildInfo`. Renaming a var breaks the stamp silently:
  change `install.sh` with it. A leaf; imports nothing of the daemon's.
- `internal/openfiles` - the open file limit (daemon ADR 0002): `Raise` at `serve` and
  `bench run` start, `Inherited` (what a child started now gets), `Count` (`lsof -p`). A leaf;
  imports nothing of the daemon's.
- `internal/bench` - the verifier bench (ADR 0025): cases, patches, the runner and the
  scorer. Sits beside `api` and `mcpserver`: it drives `machine`, `session` and `verifier`,
  and nothing imports it but `bench.go`.

## bench (ADR 0025)

`greenroom bench run|score` (`bench.go`, `internal/bench`). The cases and fixture apps live in
`bench/` at the repo root (its `README.md` says how to add them and what the numbers mean).

- `go test ./internal/bench` validates every `bench/cases/*.json` (schema, kind rules, the
  v1 sizes), applies every patch with `ApplyPatch` and checks `patch(1)` agrees. A new case
  that breaks a rule fails the build, not a VM run.
- The runner is the daemon in miniature: `machine.Manager`, `bridgeLifecycle` and
  `verifier.NewActors` with the `nimVerifier` serve uses, no MCP client. One `Runner` per
  manager: a second `New` starts a second actor on every run. Steps and tokens come from
  `verifier.WithTurnObserver`; the conversation does not carry them.
- Its own root (`-root`, default `~/.greenroom/bench`), locked with `lockRoot`. Never point it
  at the daemon's root: two managers on one `state.json`.
- The host's VM limit counts the daemon's machines too. `Runner.create` waits while `Create`
  fails with "host is at its limit" (a string match on `checkHostCapacity`'s error; change
  both together).
- Low disk (issue #155, `internal/bench/disk.go`): no trial starts under `-min-free-gb` (5)
  free on tart's volume (`TART_HOME`, else `~/.tart`, via `statfs`). The runner waits up to
  `-disk-wait` (10 min), then stops like Ctrl-C (running trials finish; `Run` returns
  `ErrLowDisk`) and records the trial it could not start as `setup_error` with `cause: disk`.
  A setup step that says the disk is full, a setup error while the disk is low, and a trial
  whose machine stopped (the daemon's "machine stopped"/"machine failed" event) while it is
  low are `cause: disk` too: never a wrong verdict. A failed probe is logged and never stops a
  run. `bench score` counts them in its header and shows the cause in the No answer table.
  `build-image.sh` runs its clone, `prepare-image` and `check-image` under
  `scripts/disk-guard.sh`'s `guarded` (stops the step under `GREENROOM_BUILD_MIN_FREE_GB`, 5,
  exit 75, and the build deletes what it made); `diskguard_test.go` drives it with a fake `df`.
- Each result records `models` (`machine.Models`: brain, reasoning model, describer and the
  options their requests carry) beside the older `model` (issue #154). `bench score` names
  each brain and describer in its header (`modelsLabel`; a result without `models` says
  "describer not recorded", never a guess) and adds a By models table when a file mixes them.
- Results are JSON lines, one per case and trial, appended when a trial ends. A rerun with
  the same `-out` skips recorded pairs except `setup_error`. A trial cut short by a signal is
  never recorded. Model errors, timeouts and setup errors are their own endings and never
  count as wrong verdicts.
- ADR 0024 compatibility: the verdict value is the message's `verdict` field; `checks` (on
  the verdict and the declaring progress message) are read raw from `conversation.jsonl`, so
  the bench works before and after `session.Message` gains them. A refused verdict is
  counted as a verifier progress message whose text starts with `report_verdict` (a posted
  verdict is never progress); if ADR 0024 records refusals another way, update `await`.
- The takeover disturbance copies `internal/api`: the `human` seat, a lease that outlives
  the trial, and the event "human took control of the screen" with `control: taken`. Keep
  them in step with `api/control.go`.
- Fixture apps build with one `swiftc` call in `build.sh`, not SwiftPM: Command Line Tools
  only (ADR 0019), no package cache. `bench/apps/*/build/` is ignored.
- Bounds are exact one-sided Clopper-Pearson (`UpperBound`, the Beta(k+1, n-k) quantile),
  pinned against scipy values in `stats_test.go`.
- Never tune prompts on the `holdout` split; run it before a verifier change merges.
- Tiers: a case's optional `tier` (`Tiers`, only `simple` today; `bench/README.md` defines
  it) is refused on infra and ambiguous cases. `TestTheSimpleTierIsPinned` lists the simple
  cases (30 dev, 11 holdout); change it with the tags. Results record the tier, but
  `bench score` takes each result's tier from the current case files by id (`WithTiers`), so
  re-tagging applies to old results; the recorded tier is only the fallback for a case that
  is gone or a results file scored with no `bench/` around. Absent means not tiered.

## Invariants

Boot and lifecycle

- `runId` is the one handle: map key, VM name (`greenroom-<runId>`), run directory.
- Orphaned run clones (issue #103, `machine/sweep.go`): `serve` starts `SweepOrphans` in the
  background after `loadState`. It deletes only a VM named exactly `greenroom-<runId>`
  (`runCloneRE`; images never match), local, **stopped** (a live `tart run` makes tart list
  it running), not a live machine, with no run directory under the root, the default root
  (`~/.greenroom`, so a scratch-root daemon never takes the real daemon's clones) or a root
  one level under either (the bench's), and older than `-sweep-orphans-after` (6h) by both
  its runId time and its directory in `tart.Home()`. One log line per clone. Every test that
  starts `serve` uses `-tart /usr/bin/false` (the sweep then only warns); never start `serve`
  on the real tart from a test without `-sweep-orphans-after 0`. `greenroom sweep-orphans`
  lists the same set and deletes only with `-delete`.
- `Create` holds `createMu` for its whole length, so the host-capacity check and the clone
  cannot interleave. Default limit 2 (Apple's), `-max-machines` changes it.
- The daemon cannot tell one MCP caller from another (daemon ADR 0008: no session in the
  stateless protocol, every Claude Code sends the same `clientInfo`), so no text may call a
  run the caller's or offer a runId to destroy. `checkHostCapacity` describes each run by
  `describeRun` (short runId, name, creator, age, idle, `Presence`) and says to wait.
  `machine_destroy` and `run_finish` go through `DestroyBy` with `agentCaller(req)`: the step's
  `by`, the `destroyed` event's `By`/`Via` (the bridge's `destroyedText`, which keeps the
  "machine destroyed" prefix) and an info log line; `session.Finish.By` records the finisher.
- `machine_create` takes an optional `name` (cut to five words by `summary.Name`) and records
  it with the calling client's name as the manifest's `name` and `source`
  (`Manager.RecordLabel`, ADR 0036). The server is stateless, so an older-protocol client's
  `clientInfo` never reaches a tool call: `clientName` falls back to the User-Agent's first
  product, skipping HTTP libraries' (`genericAgents`). A label that cannot be written is
  logged; the create stands.
- `machine_create` returns `booting` at once; callers poll `machine_wait` (capped at 50 s,
  under Claude Code's 60 s first-byte timeout). `agent_wait`, `machine_exec`,
  `machine_exec_wait` and `machine_reboot` have the same cap. No tool may block longer.
- Ready means usable: guest agent answers, IP known, ssh key installed, sshd accepts on
  guest 127.0.0.1:22 (probed via `tart exec`). The waiting phases each get their own
  `readyTimeout` (3 min). A timeout names the last probe error.
- `machine_boot` step records `agentSeconds`, `ipSeconds`, `keySeconds`,
  `captureAlertSeconds`, `desktopPrefsSeconds`, `timeZoneSeconds`, `inputHelperSeconds`,
  `toolchainSeconds`, `desktopSeconds`, `sshSeconds`, and with the desktop toolkit
  `guestAgentSeconds` (and `agentError`). `agentSeconds` is tart's guest agent, not ours.
- Boot phases (`bootphase.go`) show the boot while it happens: clone, start (in `Create`),
  agent, ip, key, settings, helper, checks, ssh (in `finishBoot`), each published when it
  starts (no `seconds`) and when it ends (`seconds`, `detail`, `error` on the one a failed
  boot stopped at) as a `boot` lifecycle event. They live on the unexported
  `Machine.boot`, so MCP results and `state.json` never carry them; a reattached machine has
  none. Clone and start end before the run is known, so `Create` publishes them after
  `created`. A machine being destroyed publishes nothing more. `internal/api` shows them as
  `machine.boot` (`LiveMachine`) on `/api/runs/{id}` and on `run` events, and sends each as
  a `boot` SSE event `{runId, phase}`. A new phase is a new wire value: the Companion
  decodes it as unknown and shows its name.
- Boot reads the image's toolchain manifest (`base.go`, `ToolchainPath`, ADR 0019) into
  `Machine.Toolchain` as the image wrote it, `{"known":false}` when absent. The daemon never
  interprets it and never assumes a toolchain. Error key `toolchainError`, never fatal.
  The one field it reads is `imageRecipe` (ADR 0026, `warnStaleRecipe` in `helperboot.go`):
  a known manifest with another recipe than `imageRecipeVersion`, or none, is logged with
  the rebuild command and recorded as `imageRecipeStale` and `imageRecipeFound` (0: none).
  It only reports; the machine boots as it is.
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
  reason; viewers reconnect. Both hold `input.approval.run` (taken with the caller's context;
  the check and write are bounded, 15 s and 30 s), so writes never overlap. A failure is logged and recorded as
  `captureAlertError`, never fatal: the machine works under the alert. In `PrepareGuest`
  it is fatal.
- Boot also sets desktop preferences (`desktopprefs.go`, step key `desktopPrefsSeconds`,
  `desktopPrefsError`): "Click wallpaper to reveal desktop" off, so a missed click cannot
  hide every window, window restore at login off, automatic text substitutions off (issue
  #81: "a  b" was typed as "a. b"), and display sleep, screensaver and
  screen lock off (a sleeping guest display makes every capture black, with no error).
  Also never fatal. `prepare-image`
  bakes both with the same scripts, so build time and boot time cannot disagree.
- Apple Events and the rest of the TCC family (`tccgrant.go`, ADR 0038, issue #252):
  `base.sh` grants `kTCCServiceAppleEvents` for every app bundle it walks under
  `/Applications`, `/System/Applications` and `/System/Applications/Utilities` (bundle ids
  resolved at build time, never pinned; `GREENROOM_TCC_APP_DIRS` overrides the directories
  for the test harness only), plus Finder and System Events, which live in
  `/System/Library/CoreServices` outside those three. An app built or synced at run time has
  a bundle id the image cannot know: `machine_approve_control` (`ApproveControl`,
  `guest/tccgrant.sh`) grants the same Apple Events rows for it, plus Accessibility, screen
  capture (the plain TCC row; `machine_approve_capture` is the separate replayd bypass
  alert), the Desktop/Documents/Downloads folders, the camera and the microphone, all keyed
  to the app's own resolved executable. Call it once the app is built, before anything
  controls, scripts or otherwise touches it: a TCC.db write only prevents the *next* Apple
  Event, measured live it does not cancel a prompt already on screen. `ExecWait` looks at the
  desktop (`attachDesktopIfRunning`, `desktopcheck.go`'s `readDesktop`/`Report`, not
  `machine_ui`, which only reads the frontmost regular app and cannot see a system prompt,
  issue #223) whenever a command has not returned within its wait, exactly the shape of a
  stall on an unanswered prompt; a finding lands in the exec result's `desktop` field and the
  machine's own `Desktop`, never auto-clicked or closed. `check-image`'s exercises include the
  issue's own repro (Calculator, outside the old fixed list) and a freshly built app scripting
  itself, approved by the same guest script.
- Image drift (issue #159, `machine/drift.go`, `imagestatus.go`): `greenroom image-status` reads
  each default image's disk while it is stopped (never a running one) and judges it by the
  helpers under `Users/*/.greenroom/bin` and the manifest at `ToolchainPath` on the Data
  volume: stale unless it has `greenroom-input-<inputHelperVersion>` and recipe
  `imageRecipeVersion`. Unlike boot's `staleRecipe`, no manifest counts as stale here: the
  default images are always greenroom-built. `RebuildArgs` keeps the lean profile for
  `greenroom-lean*`. It reports and exits 0; `install.sh` acts on `-rebuild-args`.
- `finishBoot` writes the step before closing `ready`. `manifest.json` is written by
  temp file and rename.
- A run's `models` (manifest and `Machine`) is what `Manager.SetModels` held when `Create` ran:
  serve and bench set it once, from `Verifier.Models` (or `manual`/`none`), before any run.
  The options come from `nim.ChatOptions`/`nim.DescribeOptions`, the same maps the requests
  are built from, so a new request field is recorded without anyone remembering to.
- File limit (daemon ADR 0002, issue #186): tart 2.37 leaks one vsock fd in `tart run` on
  every `tart exec`, however it ends (about 27 a minute from frame recording at 2 s; measured
  in run `20260927-210125-687fa19deff41e76`), and `tart run` dies with "Error(24)" in
  `vm.log` when it runs out. 65536 lasts about 40 hours, 138240 about 85.
  `raiseFileLimit` must run before the first `tart.Client.Start`: Go gives children the soft
  limit the daemon started with (launchd's 256) until the program calls `syscall.Setrlimit`
  (never `unix.Setrlimit`, which the runtime does not see). `install.sh` also sets the job's
  `SoftResourceLimits`/`HardResourceLimits` `NumberOfFiles`. `filewatch.go` counts each ready
  machine's `tart run` fds every 30 s (a reattached one's pid from `tart.RunPID`, the fcntl
  lock owner of its `config.json`) into `Machine.Files`, set only on copies
  (`publicLocked`), so `state.json` never holds a count; one WARN per machine from 80 percent.
  The watch carries its boot's `gen` and reads `mc.proc` under `m.mu`: a reboot clears
  `files` and `filesWarned`, the old watch stops and a count it had in flight is dropped.
  The upstream fix (tart's `ControlSocket.handleClient`) is not ours; #186 stays open for it.
- `waitReady` watches `tart run`'s process; if it exits, fail at once with the tail of
  `vm.log`. `watchProcess` does the same after ready; a reattached machine has no process,
  so it polls `tart list` every `WithVMPollInterval` (15 s) instead.
- `machine_reboot` (daemon ADR 0004, `reboot.go`, issue #187) stops the VM (`tart stop
  --timeout 20`, then a kill of this daemon's `tart run`), starts the same clone and runs
  `bootGuest` again, under one 5 min bound (`WithRebootTimeout`). Status `rebooting` until
  ready or failed; guest calls on it fail at once with `ErrRebooting` (`awaitReady`), never
  wait. A failed reboot keeps the disk and stops the VM: status failed, run not ended,
  `machine_reboot` retries and Destroy deletes it. Destroy decides whether a VM is left by
  `Machine.vmDeleted`, never by the status. Every boot of a clone has a `Machine.gen`: a
  watcher, `machineGone` or `startFrames` of an older gen stands down, so the reboot's own
  stop never fails the machine or deletes its clone. Anything new that watches a machine's
  VM or process must carry the gen too. `mc.ready` and `mc.proc` are replaced by a reboot:
  read them under `m.mu` (`readyCh`), except in the boot goroutine that set them.
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
  Every request's context ends when shutdown starts (`BaseContext` cancelled by
  `RegisterOnShutdown`), so an open event stream or `agent_wait` does not hold the stop
  to its 5 s timeout, and the root lock is free at once for a restart (issue #98). A
  timeout still exits 0 after closing what is left.
- With no `-image`, the default image is `GREENROOM_IMAGE`, then the first local
  `machine.PreferredImages` (greenroom-lean-a, greenroom-base), then upstream Cirrus: the
  same choice `scripts/install.sh` makes, so a bare `serve` behaves like the installed one.
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
  A step made for a named seat also records `by` (`recorder.completeAs`/`stepAs`): `machine_ui`
  and `machine_screenshot` by their reader, `machine_input` by its holder, `machine_exec` by
  `ExecAs`'s seat (MCP's `ExecStart` is `coder`; plain `Exec` names none). The verifier's
  effect read also records `effect` (ADR 0024). Both are omitempty, so old logs load.
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
  create image from display". `/api/runs` names each run's newest frame as `lastFrame`
  (explicit null for none), read with the frame count, never decoded: the Companion's
  thumbnail (companion ADR 0010).
- A guest command's non-zero exit is `ExitCode`, not an `error`. `error` means tart failed.
- Last activity (`Manager.LastActivity`, `/api/runs` `lastActivity`, `machine_list`
  `idleSeconds`, the host-limit error) is the newest step end or message, never a frame:
  the recorder captures an idle machine too. Messages reach the manager through
  `SetMessageActivity`, wired in `main.go`. greenroom reports idle time and never destroys
  a machine on its own; reaping is the user's call.

Exec

- `machine_exec` runs `tart exec -i <vm> /bin/sh -s greenroom-exec <secs>` with
  `execScript` on stdin (ADR 0023, issue #128): the wrapper, and the command in a quoted
  heredoc with a random delimiter, written to `$d/cmd`. zsh runs it as
  `zsh -lc 'disable log; eval "$(<$d/cmd)"'`, so it parses and runs as `zsh -c` did (whole
  parse first, no positional parameters); only its error prefix is `(eval):N:`, not `zsh:N:`.
  No guest argv carries the command or the wrapper: ps shows two short lines, and a
  `pgrep -f` run through machine_exec does not find itself. Never put either back in argv,
  and never let anything but sh read stdin (zsh and the watchdog get `/dev/null`). The fake
  tart logs the stdin to `exec-stdin` (`testsupport.ExecStdin`) and re-appends it to its
  arguments, so its pattern cases see the command. The login zsh
  writes to temp files that are printed after it exits. `tart exec` returns only when
  every holder of the guest's stdout/stderr pipes closes them, so without the wrapper
  `./App &` (or `(cd x && ./App) &`) holds the call until its timeout. `cmd.WaitDelay`
  on the host does not help: tart itself stays up. Output a background child writes
  after the shell exits is lost. zsh `-c` runs `a && b &` with `a` in the foreground;
  that is zsh, not us.
- A cancelled `tart.Exec`/`ExecTo`/`ExecInputTo` gets SIGINT, and SIGKILL only
  `execInterruptWait` (3 s) later (`interruptOnCancel`, daemon ADR 0002): tart cancels the
  gRPC call on SIGINT, which ends the guest command and lets tart exit cleanly; SIGKILL left
  the guest command running. It does not reduce the fd leak (every exec leaks one either
  way). Never build an exec `exec.Cmd` without it. Sessions and pipes are exempt (a
  session's guest command must outlive its host exec).
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
  `detachLocked`, or aborted by a reboot with `errExecRebooted` as its error
  (`execJob.abort`). Its step is claimed at start and written when it ends. The verifier's
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
  that works under `-verifier manual` works under `nim`. One exception: `Manual` does not
  resume on a Give Back (it would rerun the last instructions); its person types the next.
- The screen coming back can start a turn with no message (issue #124). `internal/api` tags
  its human handover events with `control`: `taken` on "took control", `returned` on "gave
  the screen back" and on a lapsed human lease (`session.ControlTaken`, `ControlReturned`;
  only a system event may carry it, `session.validate`). `StartsTurn` stays false for every
  event: the actor, between turns, starts one on a `returned` event when the brain is a
  `resumer` (`Verifier.resumeOwed`): its last turn ended with the screen question
  (`screenBackAsk` prefix), or a task is open and that turn did not end on another question.
  A handover during a turn reaches it as a late message and `seen` moves past it, so a Give
  Back and a message right after it make one turn. `project` and `projectLate` add
  `takenAdvice` (look, do not act) and `returnedAdvice` (look with `machine_ui` before any
  input) to those events. `lastUnanswered` ignores them, so a daemon restart does not resume.
  An actor's start position (`startSeen`) is read in `Start`, not in its goroutine, so an
  event appended right after `Start` returns is never skipped.
- A describer's model-specific request fields live in `nim.describeFields`, keyed by model id,
  and only `Describe` sends them (ADR 0030). `meta/muse-glimmer-30b` gets
  `chat_template_kwargs: {enable_thinking: false}`: with thinking on it spent the original 700-token
  budget reasoning and returns no text. A model not in the table (kimi-k3, nano-omni) gets the
  plain request. Adding a describer that needs its own fields means a row there plus a
  request-body test in `nim/client_test.go`, never a new environment variable.
- muse-glimmer (thinking off) is the built-in default describer (ADR 0032). A set
  `GREENROOM_VISION_MODEL` always beats it: an old `.env` line pinning omni or kimi-k3 silently
  keeps the slower describer (#154). Check the
  "verifier enabled" log line for `vision=` after a restart.
- `Verifier.describe` retries once any answer `readableDescription` refuses: `<unk>` (omni),
  empty (all three describers; muse about 1 in 40 even with thinking off) or under 10
  letters (kimi-k3's "!!!!"). A second one is an error the brain sees, never the noise.
  A readable one goes through `checkPositions` (daemon ADR 0010): every unquoted `(x, y)` or
  `[x, y]` pair that is not two fractions 0 to 1 becomes `(position unknown)`, never rescaled
  (describers also answer in points and on a 0 to 1000 grid, indistinguishable per pair), and
  a line says how many went and to use `machine_ui`.
- Screenshot descriptions have a 2048-token output budget (ADR 0039, issue #258).
  `nim.Describe` refuses `finish_reason: length` as `ErrDescriptionCutOff`, discarding the
  fragment. `describeWith` retries once on the same image with a compact-answer request,
  sharing the unreadable-answer retry limit. A second failure is recorded in the screenshot's
  transcript progress with its evidence step; the saved image remains available.
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
- Default step limits scale with accepted checks on an open task (ADR 0042, issue #192):
  `max(40, 12 + 8 * checks)`, bounded at 108 by the twelve-check maximum. The loop counts
  model rounds, including retries, rather than individual calls in a batched response.
  A positive `Config.MaxSteps` is a fixed cap, even 40. Both serve and bench default
  `-verifier-max-steps` to 0 for automatic sizing. A turn's local cap only grows,
  never resets on redeclaration; another run cannot inherit it. Time budgets and guards
  still apply. A limit reply reports rounds actually spent, including an expanded cap.
- A limit is not a verdict, but a task left open at one says nothing to whoever waits on it
  (issue #127). At the step cap or the budget, with a task open, `endAtLimit` makes one
  closing call (`closingPrompt`, only `report_verdict` and `ask` offered) on a fresh
  `closingTimeout` (90 s) context from the actor's, since at the budget the turn's own is
  dead. Its verdict or question is posted as any other (`postEnding`, so the #97 rule
  holds). With no task open, or when that call fails, is cut off or answers in prose or
  with another tool, the turn posts the old limit reply with `stop` (`session.StopSteps`
  or `StopTime`). Only a verifier reply may carry `stop` (`session.validate`); `agent_wait`
  returns it on the message and its description tells the coder what it means. A
  last-step cut-off still ends in `cutOffReply`, with no closing call and no `stop`.
- Evidence contract (ADR 0024, issue #116; `evidence.go`, `effect.go`). The checklist comes
  from the transcript and every step fact from the step records; both are files, so a restart
  loses nothing:
  - `declare_checks` posts a verifier `progress` carrying `checks` (1 to 12, id 1 to 40
    characters, unique, each with a criterion and its applied `kinds`, ADR 0027). The checklist a verdict answers is the newest
    declaration after the newest coder or human task (`declaredChecks`); declaring again
    replaces it until the verifier's first input on the task (a progress with a step whose call
    is an input tool, `actedOnTask`). After that, `redeclareRefusal` refuses a declaration that
    drops a check, changes its criterion, loses one of its kinds or lengthens its timing window
    (an `error:` result, no `checks` on the progress, the old list stands): otherwise a model
    could shed the check it is failing. Adding checks is allowed. The prompt and the tool
    description say a check is an outcome the task claims, not a setup step or an action it
    asks for (a todolist run declared "three items appear" and "Milk and Eggs are ticked" as
    visual checks and ended inconclusive), and visual is for claims about appearance or
    visibility only; a model may still declare visual on its own ("stricter is allowed"). The
    visual rule's refusal says to take a screenshot now if the state is still on screen, and
    otherwise to answer the check unchecked. While a task is open and has no declaration, the input tools (click, type,
    key, scroll, input) get `declareFirst`, an ordinary `error:` result with no step that counts
    toward #125. Looking and `machine_exec` are never refused.
  - Step facts come from `Manager.Steps` (steps.jsonl), never from progress text, which is
    display only and may be reworded freely (`ledger`): `tool` (every input is
    `machine_input`), `by` (only `verifier` steps count as the verifier's), `error` (a failed
    step), and the input's effect from the `effect` on the verifier's UI read that followed it.
    That read is an observation the verdict may cite.
    `TestTheReviewDoesNotDependOnProgressWording` pins it.
  - `report_verdict` is reviewed before it is posted (`reviewVerdict`). pass and fail: every
    declared check answered, no unknown id, each pass or fail answer cites observation steps
    (`machine_ui`, `machine_screenshot`, `machine_exec`, not failed) in `evidence` and input
    steps in `actions`; its newest evidence step is after every action and after
    `Manager.HandoverStep` (the step the recorder had claimed at the latest #124 handover); a
    pass answer on an action whose effect was "no change detected" needs evidence after that
    effect read. pass needs every check pass; fail needs one fail answer that holds. A broken
    rule is a tool error naming each rule and check id (`refusal`), posted as the call's
    progress, never as a verdict. inconclusive is never refused: `settle` posts a missing
    answer and any pass or fail answer that broke a rule as `unchecked`, `observed` saying why.
    A fail with one fail answer that holds is settled the same way and posted as fail, the
    summary listing what did not hold (ADR 0031, #153), unless a problem is with the verdict as
    a whole (`whole`: bad arguments, no id, unknown or repeated id, a step in `evidence`, no
    checks declared); a new rule that is not one check's answer must go through `general`.
    The top-level `evidence` is artifact paths only; a step there is refused.
  - Check kinds (ADR 0027; `kinds.go`, rules in `evidence.go`). `kinds` is `["value"]`
    (default), or `visual`, `timing` or both (`["visual", "timing"]`, always in that order);
    `within` (seconds) only with timing. The declaration takes `kinds`, or one `kind` as a
    model may send it. `applyKinds` only adds: criterion words in `visualWords` add visual,
    `timingWords` or "within N s" (`withinRE`) add timing, matched case-insensitively on word
    boundaries (`wordsRE`), and value is dropped once another kind is there. A `within` with
    no kind means timing. The window is the smaller of the declared and worded N, default and floor
    `session.MinWithin` (2 s, the effect read comes 1 to 2 s after an input), cap 600. The
    declaration result lists each check's kind and what it needs, and every upgrade or raised
    window. The applied kind and window are stored on the declaration and copied onto the
    verdict's checks. Every kind's rules apply (`session.Check.Is`), for pass and fail alike
    unless said: `visualRule` needs a `machine_screenshot` evidence step after the check's last
    action; `timingRule` needs an action and an evidence step after it whose `at` is at most
    `within` s after that action's end (`at + durationMs`), for a pass also after an effect
    read that found no change, and a pass may cite no evidence after its action that started
    later, except, on a check that is also visual, a screenshot (it shows how it looks, not
    when; the in-time step is still required, and may be the screenshot); `drawnRule` (pass only, every
    kind) refuses when the verifier's newest `machine_ui` step at or before the check's newest
    evidence step marks an element `rendered` and the check's criterion or observed names
    something of it that no drawn element of that read shows too (`mentionTerms`: one of its
    texts as whole words, or one of its numbers). A number with a decimal point or separator
    counts alone; a bare integer only when it is the element's whole text but for symbols
    ("10", "$10") or one of the element's words (2+ letters) is in the claim too. So "Value
    shows 10" rests on a drawn field reading 10, never on a blank "10 km = 6.21 mi", and a
    drawn "Each pays" label does not carry a blank "$49.56" beside it.
  - A crash is evidence (ADR 0028). A fail answer whose `actions` include an input whose effect
    read found the app gone (`quit`), and whose `evidence` cites that read, holds on it alone:
    no freshness, handover, visual or timing rule applies. A pass citing a `quit` read is
    refused (`quit` rule), whatever else it cites. The prompt says a crash while doing the
    task fails the checks that depend on it, and to relaunch once only for a later check that
    does not. The quit effect line adds "To fail a check that depends on this input, cite step
    N as its evidence." (N the read), since models cited the input itself (bench run 2,
    `wordcount-clear-crash`). A fail that cites the quitting input as evidence, cites another
    read, or cites the quit read without its input in actions gets a `quit` rule naming the
    exact step to cite, and no other rule for that check: a crashed app has nothing to
    screenshot, and the visual rule's "answer it unchecked" turned a crash into inconclusive.
  - An input that changes nothing is evidence (ADR 0029, `deadcontrol.go`). Per turn,
    `deadControls` tallies clicks (`machine_click`, or a `machine_input` batch holding one click
    among sleeps and moves) whose effect was `none`, by `clickTarget`: the element of the
    verifier's read before the click, by `identity` (ids shift), clicked by id or by a point
    inside it (the smallest element under the point of at most `maxControlArea` of the screen),
    else the point (within `pointNear`). An effect `changed` or `quit` clears the tally;
    `unknown` and non-click inputs leave it. From the second no-change click on one control
    the result gets `deadHint` naming the control and every such effect read. It never ends the
    turn (a correct fail must not become a question). In the review, a fail whose last action
    (highest step in `actions`) had effect `none` and whose `evidence` cites that effect read
    holds on it alone, like the quit rule; a pass still needs a later observation (`effect`).
    An input cited as evidence is refused with its effect read's step named.
  - The closing call at a limit (#127) goes through `downgrade`: a pass or fail that breaks a
    rule is posted as inconclusive, the reasons appended to the summary.
  - After every verifier input that ran, `runTool` reads the frontmost app's UI through
    `Manager.UIEffect` as `HolderVerifier` (a step, a look for #124, and the effect written on
    that step's record as `{"of": <input step>, "kind": "changed|none|unknown|quit", "summary"}`) and diffs it against the
    verifier's previous read (`Manager.LastUI`): pairing by role plus identifier, else role,
    title and label, then comparing value, selected, focused and disabled (`diffElements`,
    capped at `maxEffectChanges`). The result ends with `machine_ui step N read the UI after
    this input.` and `effect: <n> change(s)` plus lines, `effect: no change detected`,
    `effect: unknown (no UI tree)` (read failed or no elements) or `effect: unknown (no earlier
    machine_ui read to compare)`. The wording is free to change; the kind is what the review
    reads. The effect read replaces the verifier's tree, so element ids may shift; the result says so
    when elements appeared or went away.
  - `quit` (ADR 0028, `judgeEffect`): the app of the verifier's previous read (one with
    elements, the same read a diff needs) was among that read's running apps (`UITree.apps`, the helper's regular apps) and is not among the effect
    read's, whatever that read lists. An app that was not a regular app before (an accessory
    or agent app) is never judged gone. The line is `effect: <App> is no longer running (it
    quit or crashed).`, then `Crash report: <path>` and its first `"exception"` line when
    `Manager.FindCrashReport` finds one: the newest `~/Library/Logs/DiagnosticReports/<App>*.ips`
    modified since the input by the guest's own clock (the host passes an age, never a time),
    looked for for up to 3 s because ReportCrash writes it after the process dies. The lookup
    is a plain `tart exec` inside the effect read, records no step, and a failure is logged
    and leaves the quit line alone. `crash.go`'s script takes the app name as an argument; its
    real-shell test is `crash_test.go`. The fake tart answers it from `crash-report`.
  - The coder's task is projected with `claimLabel` after it (a human's task is not), and the
    system prompt says a verdict rests only on the verifier's observations.
  - `Manual` is exempt: no gate, no effect check, no review; its verdict keeps the free
    evidence list and no checks.
  - `checks` on a message (`session.Check`; `session.validate` allows it only on a verifier
    progress or verdict, a declaration carrying only id and criterion, a pass verdict only
    `pass` answers). A verdict's checks come in declared order, criterion filled by the
    daemon; `VerdictState.checks` repeats them, and `agent_wait` returns both:

    ```json
    {"kind": "progress", "from": "verifier", "text": "declare_checks {...}\nDeclared 2 checks: total (value), fast (visual: ...; timing within 2 s: ...). ...",
     "checks": [{"id": "total", "criterion": "Each pays shows $48.00", "kinds": ["value"]},
                {"id": "fast", "criterion": "Each pays is shown at once", "kinds": ["visual", "timing"], "within": 2}]}
    {"kind": "verdict", "from": "verifier", "verdict": "fail", "text": "...", "evidence": ["/abs/run/012-screenshot.png"],
     "checks": [{"id": "total", "criterion": "Each pays shows $48.00", "kinds": ["value"], "status": "fail", "evidence": [12], "actions": [9],
                 "observed": "Each pays shows $0.00."},
                {"id": "fast", "criterion": "Each pays is shown at once", "kinds": ["visual", "timing"], "within": 2, "status": "unchecked",
                 "observed": "Not answered."}]}
    ```

    `status` is `pass`, `fail` or `unchecked`; `kinds` is `["value"]`, `["visual"]`,
    `["timing"]` or `["visual", "timing"]` (`session.validate` refuses others, and `within`
    without timing or outside 2 to 600); `kinds`, `within`, `evidence`, `actions` and
    `observed` are left out when empty. Transcripts from before ADR 0024 have no `checks`, and
    checks from before ADR 0027 no `kinds` (read as value); both load as they were. A single
    `"kind"` string, which an early ADR 0027 build wrote, loads as `kinds`
    (`Check.UnmarshalJSON`).
- Finishing a run (ADR 0034; `session/finish.go`, `mcpserver/finishtools.go`,
  `internal/report`). `run_finish` appends a system event carrying `finish`
  (`{outcome, summary, ref?, at}`, text `run finished: <outcome>. <summary>`), then mirrors it
  into the manifest (`Manager.RecordFinish`), then destroys the machine unless `destroy` is
  false (a destroy failure is `destroyError` in the result; the finish stands), then returns
  the report. `session.validate` allows `finish` only on a system event, with a known outcome,
  a summary (at most `MaxFinishSummary`) and ref fields of at most `MaxRefField`. The rules that
  need the transcript live in `Store.Append` (`finishRefusalLocked`), so no other write path
  can skip them: a run finishes once; no turn is owed (`owedTurnLocked`: the last
  turn-starting message has no verifier reply, question or verdict after it and no system
  event either, since the no-verifier notice, the destroyed notice and "gave up" are events);
  `verified` needs `Verdict()` to be a pass with status `accepted` (by the coder or a human).
  Each refusal says what to do instead. The tool also refuses while
  `Manager.VerifierTurnOpen` (the actor's `SetVerifierTurn`), which covers a turn the transcript
  cannot see (a "machine is ready" event lands mid-turn). The conversation is the record:
  `/api/runs`, `/api/runs/{id}` and the report read `Store.Finished()` first and fall back to
  the manifest's copy. `RunSummary.finish` is an explicit null while unfinished; the manifest's
  is omitted, so old runs read as not finished. Nothing refuses messages after a finish.
- Run reports (`internal/report`). `run_report`, `run_finish` and
  `GET /api/runs/{id}/report?format=md|json[&embed=true]` all call `report.FromStore`. The
  task is the newest coder or human task before the verdict. A check's steps are resolved
  from steps.jsonl (tool, at, by); a `machine_screenshot` step's PNG is linked by its base
  name in the run directory only, never by the recorded absolute path, and only if the file
  is there. Links: through the public host, `https://<host>/api/runs/<id>/artifacts/<file>`
  (the MCP server gets the host from `ForPublicHost(host)`, the route from `r.Host`); locally
  the file's path; `embed` makes a data URI (GitHub does not render data URI images in PR
  bodies, so embed is for places that do). Models come from the manifest's `models` (issue
  #154), `models.source` "recorded with the run"; a run from before that record falls back to
  the daemon's verifier configuration at report time (`mcpserver.WithModels`,
  `api.WithModels`, set in `serve` from the same `machine.Models` it records) and says so,
  "daemon configuration at report time". `report.runModels` picks; `report.FromMachine` names
  a nim brain by its reasoning model, manual and none by name. The golden fixture predates
  the record, so it shows the fallback. Goldens:
  `internal/report/testdata/report.{md,json}` from a real recorded run in `testdata/<runId>`
  (outputs trimmed, host paths replaced, the evidence PNG shrunk); `go test ./internal/report
  -update` rewrites them.
- Machine status snapshots (`snapshot` in `context.go`) ignore the turn context's
  cancellation, so a dead budget never reads as "Machine status: gone" to the closing call.
- A failing tool call (a result starting `error:`) is counted per turn by tool, canonical
  arguments (`canonicalArgs`: sorted keys, no spacing) and error text (`repeats`, issue
  #125). The second identical failure gets `repeatWarning` appended; the third posts its
  progress and ends the turn with a question naming the call, its arguments and the error
  (`repeatQuestion`, "My last verdict still stands." when one does). Another call leaves
  the counts alone; a success of the same call clears its counts. Screen-taken refusals are
  left to the #97 rule. A successful `machine_ui` or `machine_screenshot` also clears every
  stale-look count (issue #124), since a look is what that refusal asks for. Every refused
  call's raw arguments are logged at warn ("verifier tool call refused").
- Verifier tool errors say what to send instead and name the fields that arrived
  (`argsProblem`), so a wrong field name is visible. `machine_type` text and `machine_key`
  key take a number as written (`looseString`): `{"text": 160}` was refused as no text.
  `progressText` keeps pretty-printed arguments on one line, or `splitProgress` would
  project them next turn as a call with arguments `{`.
- `project` rebuilds the verifier's own past messages (progress, reply, ask, verdict) as
  the assistant tool calls that made them, with results; never as assistant prose. A
  model imitates its history: projected as "[I reported verdict ...]" text, it answered a
  later task with a prose verdict, stored as a reply, so an accepted fail stood. Prose
  that still looks like a verdict (`proseVerdict`) is sent back once per turn to call the tool.
- The verifier writes plainly: `writingRules` (adapted from stop-slop, MIT) ends the system
  prompt and the reply, ask and report_verdict argument descriptions repeat its limits. Keep
  it short; the small NIM model ignores a long style guide. Daemon-authored verifier texts
  (budget, step cap, closing prompt, repeat question, cut-off, screen taken, stale look,
  handover advice) follow the same rules.
  `TestTheDeliveredSystemPromptCarriesTheWritingRules` pins it. The Companion matches
  "nobody will answer" and "nothing will answer" in system events; keep both phrases.
- Every action that changes a machine lands in the transcript. Lifecycle events come only
  from the bridge in `main.go`.

Computer use (ADR 0009)

- At most one control lease per machine; `Manager.Input` refuses input without it. Lease
  expires after `ControlTTL` (60 s) of silence; each batch renews it. A human taking
  or releasing it posts to the transcript (`internal/api`); each batch is one
  `machine_input` step. A lease that ran out is announced once, by the manager, as a
  `control` event with `Lapsed`, which `internal/api` posts synchronously for a human lease
  ("human lost control of the screen after N actions"), never as a second "took control"
  (issue #57). Whoever finds it expired announces it through `lapseLocked`: the lapse timer
  (`armLapseLocked` keeps one per machine, `checkLapse` fires it, so a Companion that quit
  or crashed still leaves a trace), any take (`TakeControl`, so also a coder's or the
  verifier's `InputAs`) or a release. `inputState.ctlMu` holds each lease change through its
  events, so a take that follows a lapse returns only after the lapse was posted. A machine
  being destroyed announces nothing, and `detachLocked` drops its lease. Snapshots never
  show a lease past its expiry. `POST /control {"renew": true}` (`RenewControl`) extends a held lease and never
  takes a new one: 409 when the seat no longer holds it, so a second window of the same seat
  cannot undo a Give Back (issue #100).
- The verifier takes the lease per call, not per turn, via `Manager.InputAs`. A human
  holding it is a readable error, not a failure. It is `machine.ScreenTakenError`
  (`ErrScreenTaken`); the verifier's tool result then says not to retry. The first refusal,
  while someone else still holds the screen, ends the turn with a question asking for it
  (issue #124; #97 waited for a second), and an `inconclusive` verdict while someone else
  holds it after a refusal or a mid-turn `taken` event becomes that question (`postEnding`),
  so a standing verdict is never replaced by one about the lease (issue #97). The human's
  question says Give Back is enough ("Press Give Back in the Companion and I will continue
  from here."): giving back resumes the turn (see Conversation and verifier). The actor
  marks each turn with `SetVerifierTurn`, and while one is open the coder's `InputAs` is
  refused with words pointing at `agent_wait` (issue #82): per-call leases let both drive the
  same app. A human is never refused for it.
- Screen handovers (issue #124): `inputState.handovers` counts every fresh take, release and
  lapse by any seat but the verifier (coder per-call leases included); the verifier's own
  lease changes never count. `Manager.UI` and `ScreenshotAs` record, per reader, the count
  read before the look began. `InputAs` for `HolderVerifier`, after its take (so a human lease
  that the take found lapsed counts, and a human still holding it is `ScreenTakenError`
  first), refuses with `ErrStaleLook` while the verifier's latest look is older than the
  latest handover, for element and coordinate input alike; a verifier that never looked is
  stale once anyone else held the screen. Nothing is posted and no step is recorded. The coder
  and the human are never refused for it. Plain `Screenshot` (API, MCP) records no look.
  Every handover also stores the recorder's current step (`handedOver`, count first, then the
  step, both under `Manager.mu`); `HandoverStep` reads it for the ADR 0024 freshness rule.
- `validateActions` also refuses a click, down, up or move without both `x` and `y`: the
  helper would post it at the pointer (issue #85). The verifier's `machine_input` decodes with
  `DisallowUnknownFields`, so an `element` in a batch is an error, not a click at the pointer.
  It refuses a `type` with empty text too (issue #126): the helper typed nothing and the call
  reported success with a step. This one check covers MCP, the verifier (both brains dropped
  their own) and the human API. `InputAs` runs it before taking the lease, so a refused batch
  records no step and leaves no lease.
- Coordinates are fractions 0 to 1. Only the manager converts to points (`ScreenOf`), and
  back for the UI tree; out-of-range is clamped. `machine.Shot` carries `width`, `height`,
  `scale`; never hardcode Retina 2 (the tahoe guest is 1024x768 at scale 1).
- The input helper is compiled in the guest with `swiftc` to
  `~/.greenroom/bin/greenroom-input-<inputHelperVersion>`. Bump `inputHelperVersion`
  when the helper's behaviour changes in a way a daemon relies on (a new mode or op); any
  other change under `guest/helper/` needs no bump, because `--version` also carries the
  sources' hash (`helperVersionLine`) and the install and boot checks recompile on it. After
  a bump, rebuild the image `install.sh` serves by default:
  `greenroom-lean-a` when it exists (`build-image.sh -lean -name greenroom-lean-a -force`),
  and `greenroom-base` as the rollback target (`build-image.sh -force`). Locally boot detects a
  stale image, warns and compiles the helper (see Boot and lifecycle). The VM suite workflow bakes and tests
  `greenroom-base-v<inputHelperVersion>-r<imageRecipeVersion>` itself, so a bump of either rebuilds its image once.
  Source and input travel base64, never through a shell.
- `InputAction` means what the tools say: positive `deltaY` scrolls down, positive `deltaX`
  right. A positive CGEvent wheel scrolls up and left, so `pixels` negates both on the way to
  the helper (issue #51) and clamps them to Int32, where the helper's conversion would trap
  (issue #36). The helper never sees the tools' sign; the companion converts AppKit's.
- `validateActions` refuses an unknown action type, button or modifier before a batch posts
  anything, because the helper drops an unknown modifier and makes an unknown button a left
  click (issue #31). Its name lists mirror `flags` and `mouseButton` in `helper/Input.swift`; change
  them together.
- A batch returns only after the window server applied it (`perform`/`settle` in
  `helper/Input.swift`): the session's event counter (`CGEventSource.counterForEventType`, any
  type) must move by as many events as the batch posted, within 3 s, or the batch fails.
  A batch whose action fails part way still waits for what it posted.
  A post only queues the event; the window server checks the poster's TCC PostEvent grant
  by audit token before applying it, and near boot that check can run over 100 ms late.
  A one-shot helper that had already exited then lost the event with no error anywhere
  (the pointer stayed at boot's (10,10), about one `TestEndToEndInput` run in ten). Every
  post goes through `post()`, which counts it; a new post site that bypasses it breaks this.
- A shortcut posts real modifier key downs and ups around the key (`press` in
  `helper/Input.swift`). A flag on the key event alone leaves the window server thinking the
  modifier is held, and the next typed text arrives as command-1, command-2.

UI tree (ADR 0012)

- `machine_ui` (both the verifier and MCP) is `greenroom-input --ui-base64`: the frontmost
  or named app's on-screen AX elements, frames clipped to window and scroll areas, menu
  bar and bare layout skipped, capped at `limit` (default 250, verifier 200, max 1000).
  It needs Accessibility, which the image grants to tart-guest-agent; the helper inherits
  it. No lease. Every read is a `machine_ui` step with the whole tree.
- Text that is not drawn (ADR 0027, `render.go`). A read whose tree has a text element (a
  title, label or value on anything but a window-sized container, `unmarkedRoles`) captures
  the screen once with `captureScreen` and, concurrently, `--desktop`'s window list, then
  sets `UIElement.rendered`: `offscreen` for a frame outside the display, `covered` when
  another app's window in front of the app's own (CGWindowList is front to back; system
  owners from `desktopcheck.go` and alpha 0 never count) holds the whole frame, `blank` when
  the frame, inset a point, has fewer than `minInk` pixels differing from its most common
  colour by more than `inkDelta` in a channel. Frames are points and the capture pixels:
  the scale is measured (capture width over screen points), never assumed. A capture that
  fails or does not match the screen leaves the tree unmarked with `unrendered` saying why.
  `Outline` shows `[not drawn]`, `[offscreen]`, `[covered]` after the element's state, with
  one legend line when any is present. The marks live in the step's output, which the verdict
  review reads, and a change in them is an effect change. The helper already drops elements
  wholly outside the screen, a window or a scroll area, so `offscreen` is rare.
- `Manager.UI` keeps the last good tree per machine and reader (`HolderCoder`,
  `HolderVerifier`); `machine_click {element}` aims at the caller's own tree via
  `ElementCenter` without re-reading, so a verifier read never retargets a coder's ids
  (issue #35). `LastUI` returns that tree (the verifier's effect check diffs against it, ADR
  0024), and the verifier's effect read after each input replaces it. An optional `uiStep` refuses a click whose ids are not from the caller's
  latest read. A click by element (coder and verifier) is `FocusThenClick` (daemon ADR 0009):
  a `focus` action on the tree's `PID` (the helper's `Focus.swift`: `AXFrontmost`, raise the
  window under the point, wait up to 1 s), then a click carrying the pid and the element's size,
  which the helper moves to the nearest point of the element that app owns (`clickPoints`) or
  refuses naming the cover ("covered by Dock"). A click by x and y is never moved.
  `machine_type`, `machine_key` and `machine_scroll` take `app` (`Focused`); input still goes to
  the frontmost app without it. A verifier click after a
  screen handover is refused until it reads again (`ErrStaleLook`, issue #124). `UITree.Outline` is the text both
  surfaces show a model; keep it one element a line with its id and center.
- The verifier's prompt makes the tree the way to aim and a coder's constraints hard rules
  (`verifier.go`). `TestTheDeliveredSystemPromptBindsConstraintsAndAimsFromTheTree` pins the phrases.

Screen looks (daemon ADR 0003, issue #187)

- A look is any call that needs the guest's WindowServer: `captureScreen` (screenshot, frame,
  the render check), and the helper's `--ui-base64`, `--desktop` and screen-size reads. Each
  runs under `lookWatchdogScript` (`look.go`) through `guestLook`/`readHelper`: the guest ends
  it (TERM, KILL 2 s later, exit 124) at `lookTimes.capture` (15 s) or `.ui` (25 s), and the
  host gives up `.grace` (5 s) later. A new look goes through them too, never a bare
  `tart exec`: killing the host's exec never reaches the guest, and a wedged WindowServer then
  collects orphans. Only `machine_input`'s one-shot post is not a look (a batch may sleep).
- `ScreenshotAs` and `Manager.ui` cap the whole call at `lookTimes.cap` (45 s) whatever the
  caller's ctx allows (`lookError`). A timeout is `ErrScreenNotAnswering`
  (`*ScreenNotAnsweringError`); its text names `machine_exec` and `machine_reboot` and is what
  the agent reads, so keep both names in it.
- One guest capture per machine (`inputState.capture`, a `captureGate`). Looks wait for an
  outstanding one within their cap and then capture themselves (never share its picture);
  behind one that timed out they fail at once. The recorder passes `wait=false` and skips.
  The capture runs detached from its caller and holds the slot until its tart exec returns.
  A reboot resets the gate (`inputState.forget`, `captureGate.reset`): a new epoch, the slot
  free and the streak 0; a capture of the old boot releases into nothing. Without it a look
  after machine_reboot waited on the wedged boot's capture and failed telling the agent to
  reboot again. Every acquire's epoch goes back to its own release.
- The recorder backs off after consecutive timeouts (`frameBackoff`: 2 s doubling to 1 min),
  logs once when the screen stops answering and once when it answers again, and never logs a
  timeout or a skipped frame as a frame failure.
- `ensureInput` starts one detached install (`installJob`) per machine; callers wait with their
  own ctx. The helper check runs under the watchdog; only a missing or stale helper compiles.
  `inputState.mu` is never held across a guest call.
- `helper/main.swift` answers `--version` before any top-level code that touches WindowServer. Keep
  it first: the install and boot checks run `--version` on a guest whose screen may be wedged.
- Tests shorten the limits with `withLookTimes`; the fake tart hangs a capture with
  `shot-hang` and a UI read with `ui-hang`, and slows the compile with `input-install-sleep`.
  `look_test.go` runs the watchdog script itself on the host's `/bin/sh`.

Live screen (ADR 0011)

- Every VM boots `tart run --no-graphics --no-audio --no-clipboard` (image builds too). Without
  the last two, tart gives the guest the host's microphone and a two-way clipboard, so code
  under test could read what the person copied. Never drop them. Graphics mode (`--vnc-experimental`, the old
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

Guest agent (daemon ADR 0005, `agent.go`, `internal/guestagent`)

- Off unless `serve -desktop-toolkit` or `bench run -desktop-toolkit` (`WithDesktopToolkit`).
  Off, nothing starts an agent and every look and input is the tart exec it always was
  (`TestTheDesktopToolkitDrivesTheDesktopThroughOneExec` off). Keep it that way until wave 4.
- One `guestagent.Supervisor` per boot of a clone (`Machine.agent`, guarded by `m.mu`, tied to
  `Machine.gen` like every watcher). `bootAgent` starts it in the helper phase, right after
  `bootInputHelper` made the helper current, with `tart.StartPipe` and `agentScript()` (pkills
  an older `--agent` first, like `serveScript`), and waits `bootWait` for the first
  connection: a failure is `agentError` in the boot step, never fatal, and the supervisor keeps
  trying. `loadState` starts one, without waiting, for a reattached ready machine. A failed
  boot stops it; `releaseLocked` stops it (destroy, detach, reboot), `Machine.agentDone` closes
  once its tart exec has ended, and Destroy and the reboot wait for that. A reboot's new boot
  starts its own.
- `agentCall` is the one way in. It waits for a connection only while the channel has been
  down under `degradeAfter` (10 s), never past ctx; refuses an op the agent's HELLO does not
  list (`guestagent.ErrMissingOp`, naming a stale helper) before sending; and returns the
  `Conn` it used, whose `Gen()` refs are scoped to. Nothing was sent when the error is
  `ErrUnavailable` or `ErrMissingOp`. `Supervisor.Pause`, `Resume` and `Reconnects` never block,
  because they run under `m.mu`; nothing in `guestagent` may call back into `machine`.
- The legacy looks and inputs go through `viaAgent`: the screen size (`installInput`: a
  connected agent is the current helper, so no version check), `ui` (`readUI`), `capture`
  (`captureOnce`, still inside `captureGate`, so the recorder's frames and screenshots stay one
  at a time), `desktop` (the render check; boot's `checkDesktop` stays an exec), `sh` (only the
  capture-approval check; the write kills replayd and stays an exec) and `input` (`postInput`,
  `reader` the holder, `input: true`). Boot's checks, the install, `machine_exec`, sessions,
  sync, pull, reboot and the live screen (`--serve`) keep tart exec.
- Degraded mode (point 13): a look whose channel has been down past `degradeAfter`, or ended
  before its request was sent, runs its old exec and records `degraded: true` (`UITree`,
  `Shot`, `InputResult` and their step outputs). An input falls back the same way only when
  nothing was sent; one the channel carried and lost is `ErrLost` ("may or may not have been
  posted"), never posted a second way (`TestAnInputIsNeverPostedTwice`). Toolkit ops must never
  fall back: they call `agentCall` and fail with `ErrUnavailable`.
- A look's agent `deadline` (or `ErrDeadline`) is a `ScreenNotAnsweringError`, counted in the
  capture gate's streak like an exec timeout (`agentLookError`). While the supervisor reports
  `stalled` (the agent's watchdog EVENT), captures and UI reads fail at once with
  `ErrScreenNotAnswering` (`agentStalled`) instead of queueing behind the stall.
- PAUSE and RESUME (point 14): a fresh take by any seat but `HolderCoder` and `HolderVerifier`
  pauses the agent (`TakeControl`); that seat's release or lapse resumes it (`ReleaseControl`,
  `lapseLocked`). The supervisor re-sends a standing PAUSE to a new connection. The lease is
  still checked before any input; PAUSE only stops one already in flight.
- `Machine.AgentReconnects` is set only on copies (`publicLocked`), like `Files`: never in
  `state.json`.
- The helper's queues (`AgentCalls.swift`, `OpKind`): reads run on a serial queue per app
  (`pid:<pid>`), so one hung app blocks only its own reads; inputs on the one input queue;
  captures on the capture queue. `OpKind.wait` (`waitFor`, `expect`) runs off every read queue
  and sleeps between polls, running each poll on its app's read queue through `onReadQueue`, so
  a 40 s wait never holds up that app's snapshots. A new op that reads an app from off the read
  queues and must not overlap its walks (a wait's poll, a capture by ref) goes through
  `onReadQueue` too; it gives up at its deadline with `not_responding` and drops the late answer.
- The settle's tree signature (`logic/Signature.swift`) signs only what shows: an element that
  does not show adds only its role. Tables report stale widths and texts for rows they have not
  drawn, and signing those made an idle window look busy, so the settle never settled. Keep new
  signed fields inside the `shows` guard unless their change is visible when hidden.

Desktop toolkit (daemon ADR 0006, `desktop*.go`, `internal/desktop`)

- Tools only with `-desktop-toolkit` (`mgr.DesktopToolkit()`): MCP (`desktop*.go` in mcpserver)
  and the verifier (`toolsFor`, `systemPromptFor`) add `machine_snapshot`, `machine_find`,
  `machine_press`, `machine_set_value`, `machine_wait_for`, `machine_expect`, and give
  `machine_type`, `machine_key`, `machine_scroll` and `machine_screenshot` new arguments. Off, the
  MCP tool list is byte for byte `mcpserver/testdata/tools-without-toolkit.json`
  (`TestTheToolListWithoutTheToolkitIsUnchanged`; regenerate with `-update` only for a change
  meant for every daemon) and the verifier's tools and prompt are the old ones.
- A shared tool's old call goes the old way unchanged: `desktop.ToolkitCall` routes by the
  arguments (any new one makes it the toolkit's), the same rule on both surfaces. The verifier's
  old tools keep their effect read; a toolkit action has none.
- Every toolkit op goes through `deskCall` (over `agentCall`, never `viaAgent`, never an exec)
  capped at `looks().cap` whatever ctx allows, with the agent deadline under the cap
  (`deskDeadline`). Agent errors become `DesktopError` (or `ScreenTakenError` for `paused`,
  `ScreenNotAnsweringError` for `deadline`), worded for a model; a refusal carries the structured
  `desktop.Refusal`, which the step's output keeps for the bench.
- Refs are scoped to their connection: `inputState.desk` remembers per reader the supervisor and
  generation its refs came from, and a call naming a ref from another one is refused before
  anything is sent. A reboot's new supervisor makes every old ref refused the same way.
- Ref numbers never repeat for a reader on a machine (the fix for B10 of the first live run). A
  new agent starts its tables at `e1`, and once a fresh snapshot moves the reader's origin to the
  new connection, the connection check above no longer catches an `e39` the caller kept from
  before, which then named another element. So `deskCall` records the highest ref of every
  result and error detail (`desktop.HighestRef`, which reads only the ref fields, never a name)
  in `inputState.desk`, and `raiseRefs` sends op `refs {reader, next}` on each new connection
  before that reader's first toolkit call (`agentCallPrepared`'s prepare, so it goes on the same
  connection as the call). The agent only raises its counter, never lowers it, capped at
  `e999999999` (the daemon's ref pattern). A daemon restart loses the high-water mark: the input
  state is not in `state.json`.
- Looks (`deskLook`: snapshot, find, wait_for, expect, cropped screenshot) take no lease and are
  looks for #124 (`noteLook`). Actions (`act`, `Scroll`) take the per-call lease in `deskLease`,
  exactly as `InputAs` (the coder refused in a verifier turn, `ScreenTakenError`, the verifier's
  stale look), send `input: true` with the holder as reader, and record their effect on their own
  step (`Step.Effect.Of` is the step itself; `quit` for an app that went away), which the
  verdict review reads (`stepFact.self`).
- Takeover (ADR 0006 point 11): with an agent, a human's `TakeControl` preempts the coder's or
  the verifier's lease; the action ends with `paused` and returns `ScreenTakenError`; its release
  (`ReleaseControl` by its own holder) cannot release the human's lease.
- Steps never hold a secret: an action's request is recorded redacted (`Action.Redacted`, and
  lengths only when there is no result to say the field was not secure), waits and expectations
  through `Redacted`. A snapshot step holds the whole structured snapshot; an expectation saves a
  best-effort JPEG crop `NNN-expect.jpg` (a failed crop is `cropError`, never a failed step).
- The snapshot runs the ink test on its text (`inkTest`, `render.go`'s pixel logic on each
  node's `vis`) and marks `notDrawn`; a capture that fails leaves it unmarked with `unrendered`.

Sync

- `source` must be absolute; `dest` must stay inside the guest home (`machine.CheckDest`
  lets the upload route refuse a bad one before unpacking).
- Default `dest` is `~/work/<basename>` (`GuestWorkDir`). This is pinned: SwiftPM caches
  are keyed to their absolute path and fail hard elsewhere.
- rsync uses `-a`, not `-az`. Compression makes a local VM sync ~4x slower
  (`docs/10-build-transport.md`). `TestSyncBuildsTheRsyncCommand` asserts it.
- Strays and mirror (daemon ADR 0001, `apps/daemon/docs/adr/`, issue #188). A sync never
  deletes unless `mirror` (rsync `--delete -v`, never `--delete-excluded`: an excluded path
  is never deleted and never a stray). Every sync reports `strays`/`strayPaths` (first
  `maxStrayPaths`) from rsync's own `deleting <path>` lines: a mirror's, or else a second
  `--dry-run --delete` pass with the same excludes. Never count strays by diffing listings in
  Go: rsync's exclude rules are the ones that decide. A failed dry run is logged and leaves
  `strays` out; the sync still succeeds.
- A mirror refuses (`mirrorDest`, `CheckMirrorDest` for the upload route) a dest under two
  components, under `Library` or a hidden top-level directory, and then in the guest
  (`syncGuardScript`, exit 4) one whose physical path is not the home's plus dest: rsync's
  receiver follows a symlinked destination, so `--delete` would empty its target. Keep the
  guard before rsync, with the path as an argument.
- `Manager.Sync` takes `SyncOptions`; the upload route reads `mirror=true` and repeated
  `exclude=` from the query (connect sends every pattern it applied, so the daemon's rsync
  protects the guest's excluded paths although the archive left them out).

Pull (ADR 0022)

- `Manager.Pull` is Sync's rsync reversed. `source` is any guest path: relative to the home,
  `~/x`, or absolute (`guestSource`); `dest` is an absolute host dir, made if missing,
  default `mc.rec.artifactDir(seq, "pull")` (`runs/<id>/NNN-pull`), so its step is claimed
  before the copy. A directory's contents land in `dest` (`src/`); a file lands under its
  own name, read through a link (`--copy-links`, which has nothing else to follow there).
- The file-or-directory probe (`pullProbeScript`) and the archive (`pullTarScript`) take
  the path as an argument to `/bin/sh -c`, never as shell text, run from the home. A missing
  source is `ErrNotInGuest` and records no step, like every refused argument.
- The guest's tar gets `COPYFILE_DISABLE=1 --no-mac-metadata --no-xattrs`, or macOS adds
  `._` AppleDouble members, and `-h` for a file source (the rsync path's `--copy-links`).
  Only literal exclude names go to it (`literalName`): bsdtar matches those as rsync does,
  and nothing else is guaranteed to.

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
| `GREENROOM_VISION_MODEL` | `meta/muse-glimmer-30b` | Model that describes screenshots for the verifier (ADR 0032). `none`: the verifier works without seeing the screen. Other describers (kimi-k3, omni) still work by name; see below. Any value but the default makes `serve` and `bench run` log a warning at start naming both (`warnDescriberOverride`, issue #154). |
| `GREENROOM_TART` | none | tart binary, see below. `-tart` overrides. |
| `GREENROOM_PUBLIC_HOST` | none | Tunnel hostname that may reach the daemon with the token (ADR 0021). `-public-host` overrides. |
| `GREENROOM_TOKEN` | none | Bearer token for the public host, at least 32 characters (`openssl rand -hex 32`). |
| `GREENROOM_CHECKOUT` | none | The checkout the daemon was built from. `install.sh` writes it into the launchd plist; `/api/version` reports it and the Companion runs `scripts/update.sh` there (ADR 0033). |

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

Every image carries the host's Xcode (ADR 0026). `build-image.sh` resolves it (`-xcode`, else
`xcode-select -p`'s app; none fails by name before any clone) and grows the clone's disk
(`tart set --disk-size`, default 90 GB). `prepare-image` runs `machine.InstallXcode` before
`PrepareGuest`, so the manifest measures it: `guest/disk.sh` (wait for the container to fill
the disk, read back), a `ditto -c | ssh | sudo ditto -x --hfsCompression` copy to
`/Applications/Xcode.app`, then `guest/xcode.sh` (select, license, first launch, developer
mode, each read back with the copy's signature).

- Bump `imageRecipeVersion` (`image.go`) with every recipe change an existing image lacks:
  a guest script, `PrepareGuest`, `InstallXcode`, lean, or something new the gate demands.
  It goes into the manifest, the boot warning and the VM suite's image name.
- With Xcode in the manifest, `writeToolchainManifest` fails the build unless
  `xcodeFirstLaunch`, `xctest`, `swiftTesting` and `xcodebuild` are all true
  (`toolchainProblems`), and unless `imageRecipe` reads back as this recipe's.
- Not rsync: Xcode's files are APFS-compressed and rsync writes them expanded (10.2 GB, not
  4.0 GB, for Xcode 27), which the host's sparse disk pays for. ADR 0026 says rsync (see
  Known divergence from ADRs).
- The disk grows at boot by itself: the Cirrus base's LaunchDaemon runs
  `tart-guest-agent --run-daemon` (`--resize-disk`), about 35 s after the agent answers.
  `diskutil apfs resizeContainer` after that fails with -69743 ("must be different"), which
  is why `disk.sh` reads the size before it resizes.
- `lean.sh` turns Spotlight indexing off; nothing Xcode needs uses it here, because
  `xcode-select -s` names the developer directory and nothing searches for Xcode.app.
- The gate's `xcodebuild` exercise has a 240 s watchdog (a cold build in a fresh clone);
  the others keep 30 s. A new exercise sets `seconds` only when it needs more.
- Installing Xcode makes Login Items & Extensions (BTM) post "Multiple Extensions Added" as
  an alert (Xcode's Quick Look previewer and Spotlight importer). An alert stays until closed
  and usernoted keeps it, so without a fix every clone shows it at every login; BTM itself
  never posts twice. `xcode.sh` registers the copy (`lsregister -f`) and waits until
  `sfltool dumpbtm` reads every Xcode item `notified` (check `btm-notified`), so the alert is
  up before `base.sh`; `base.sh` closes it with the alert's Close action through System
  Events (it needs the Apple Events rows written above it, so it cannot live in `xcode.sh`,
  which runs first) and reads back that no alert is left (`notification-alerts: <text>`).
  It closes only the texts in `build_alerts`; a new alert the recipe raises fails the build
  by its text, and belongs in that list only once it is understood. Lean hid this alert by
  disabling `notificationcenterui`, which is why a lean build passed the gate and base did not:
  test base as well as lean when the recipe changes.

- BASE gets every fix that needs no click; LEAN adds only hiding. A fix that makes a
  machine work goes in `base.sh`, never `lean.sh`, so `greenroom-base` stays complete.
- `base.sh`, `lean.sh` and `xcode.sh` read every setting back and fail by check name.
  `base_test.go`, `lean_test.go` and `xcodescript_test.go` run the real scripts in `/bin/sh`
  under stubs (real SQLite and plutil for base, real plutil for xcode; an `osascript` stub
  plays Notification Center's alerts, an `sfltool` stub plays BTM).
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
  the exec wrapper's, because a blocked osascript keeps `tart exec` open after its shell dies.
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
  `fail-base`, `fail-toolchain`, `toolchain-measured` (what the manifest script writes),
  `fail-disk`, `fail-xcode`, `fail-softwareupdate`, `fail-check-<exercise>`, `crash-report` (what
  the effect read's crash report lookup prints, ADR 0028) and
  `softwareupdate` (image build and gate), `tart-version` (fake a version mismatch), `exec-sleep` and `exec-stdout` (a slow or
  loud machine_exec), `shot-hang`, `ui-hang` and `input-install-sleep` (a wedged screen, daemon ADR 0003), `input-stale` (an image with an old helper) and `session-exit-code`. It writes
  `session-stdin` (`tty <rows> <cols>` or `pipe`) so tests prove a session reaches tart on a
  pipe. It models a session with the host's real `script` running `cat`, and runs the real
  session read and close scripts, with `TMPDIR` set to the control dir.
- `internal/machine/sessionguest_test.go` runs the session wrapper, read and close scripts
  for real on the host (same `script`, `stat`, `ps` as the guest).
- The fake tart runs `exec -i ... --serve` as the fake live screen helper by re-executing
  the test binary (`testsupport/fakescreen.go`, gated by an env var in its `init`). Its control
  files are listed there; `testsupport.ServeStarts` counts starts.
- It runs `exec -i ... --agent` as the fake guest agent the same way (`testsupport/fakeagent.go`,
  `GREENROOM_FAKE_AGENT`); `fail-agent` makes that start fail. The fake agent answers `screen`,
  `capture`, `ui`, `desktop`, `input` and `sh` from the fake tart's own files (`screen`,
  `shot.b64`, `ui.json`, `desktop.json`, `input-down`, the capture-approval files), and any op,
  the toolkit ones included, from a canned `agent-<op>.json`; `agent-<op>-sleep`, `agent-hang`,
  `agent-exit`, `agent-nopong`, `agent-slow-hello` and `agent-stalled` break it. It records
  `agent-requests`, `agent-control` (PAUSE, RESUME, CANCEL), `agent-input`, `agent-starts` and
  `agent-exits`; the full list is its header comment. `testsupport.AgentStarts` counts starts in
  calls.log. calls.log prints a script argument over several lines: count invocations
  (`tartCalls` in `agent_test.go`), not lines. An `input: true` request of another holder than a
  PAUSE's is answered `paused`, before it starts and during its `agent-<op>-sleep`: that is how
  the takeover tests stop an action in flight. Toolkit tests can the answer (`can` in
  `desktopsnap_test.go`, `harness.can` in mcpserver, `can` in the verifier) and read the requests.
- `withAgentTimes` shortens the agent's `degradeAfter`, `bootWait` and the channel's own timings
  (`guestagent.SupervisorOptions`: heartbeat, grace, backoff); `agent_test.go`'s `fastAgent` finds
  a dead channel in 300 ms. `internal/guestagent`'s tests run `Conn` and `Supervisor` against an
  in-process fake agent over `io.Pipe`, with every timing an option.
- `WithSSHProbe`, `WithReadyTimeout` shorten or replace boot waits; `WithScreenIdle` the
  live screen's idle stop. `WithFileCheck` replaces the file count's interval, counter, limit
  and pid lookup; `newTestManager` turns it off (`Interval: 0`).
- `internal/openfiles` and `internal/tart` re-execute their test binary as a helper (an env
  var in `TestMain`): one started under `ulimit -Sn 256` proves a child inherits the raised
  limit, one holds tart's `config.json` lock for `RunPID`.
- `InstallXcode` tests (`xcode_test.go`) put a fake `ssh` first on `PATH` and a fake
  Xcode.app (xcodebuild and an Info.plist); the host's real `ditto` makes the stream.
- A fake `rsync` earlier on `PATH` covers `Sync` (it runs twice without mirror: the copy,
  then the stray count's dry run, so a fake that records arguments must append). Pull tests
  and the stray and mirror tests run the host's real rsync with a fake `ssh` that runs the
  remote side in a local shell in a temp `HOME` (`localSSH`); the fake tart runs the pull
  probe and tar and the mirror guard (`greenroom-sync-guard`) for real from the same `HOME`.
  A test that mirrors must set `HOME` to a temp dir first, or the guard's `mkdir -p` lands in
  the real home.
- The fake tart boots a VM again after `tart stop <name>` (it writes `stop-<name>` beside
  `stopped`, and a `tart run <name>` after it clears both) and lists a VM stopped by name as
  stopped: that is what reboot tests run on. `agent-down` holds a reboot in `rebooting`;
  `stop-sleep` slows `tart stop`. `exec-sleep` sleeps in 0.1 s steps so a killed exec
  returns at once, as a real `tart exec` does.
- `internal/bench` runner tests use the real manager on the fake tart, a fake `rsync` that
  copies its source (so the patched app is visible) and a scripted `verifier.Brain`. The fake
  tart stops every VM on one `stopped` file, so they run one trial at a time with
  `fakeTartMachines` clearing it; the machine limit is tested on `countingMachines`.
- `updatescript_test.go` runs the repo's `scripts/update.sh` (ADR 0033) the way `base_test.go`
  runs guest scripts: the real script copied into a scratch repository with a bare origin
  beside it and fake `install.sh`es at the real paths, under `GIT_CONFIG_GLOBAL=/dev/null`.
  The copy finds its repository from its own path, so the test can never reach the real
  checkout, `/Applications` or launchd. Every refusal, a failing install at each step, a failed
  fetch and the fast-forward that rewrites `update.sh` itself are covered. `update.sh`'s
  output lines (`main`, `ahead`, `commit`, `refused:`, `step:`, `done:`, `failed:`) are parsed
  by the Companion (`Builds.swift`); change both.
- `install.sh` keeps how the daemon was installed, so an update (ADR 0033), which sets no
  `GREENROOM_*`, changes nothing but the build: `scripts/install-settings.sh` takes
  `GREENROOM_VERIFIER`, `_IMAGE`, `_ENV` and `_TART` from the environment, else from the plist it
  replaces, and every other variable of that plist's `EnvironmentVariables` is written again.
  A chosen image, env file or tart is recorded in the plist's `EnvironmentVariables`, so the
  next install can tell it from install.sh's own pick (an image it picked stays its pick, so a
  newer `greenroom-lean-a` is still found). A plist from before that is read from its arguments:
  `-verifier` always, `-image` only when it is none of install.sh's own picks, `-env-file` only
  when it differs from the one install.sh would pick now. `installscript_test.go` runs the real
  install.sh with `HOME` in a temp dir and fake `launchctl`, `lsof`, `curl`, `go` and `tart`,
  and refuses to start unless each resolves to its fake: never point it at the real plist.
- Test at the highest seam that sees the behaviour: `internal/mcpserver/*_test.go` runs a
  real MCP client over HTTP against every tool; `internal/api/api_test.go` drives the real
  routes and SSE over `httptest`.

## Known divergence from ADRs

- ADR 0004 is amended for `state.json`.
- ADR 0026 copies Xcode "with rsync over ssh"; `InstallXcode` streams `ditto -c` over ssh
  into `ditto -x --hfsCompression`, because rsync writes Xcode's APFS-compressed files
  expanded (10.2 GB instead of 4.0 GB for Xcode 27). It also turns developer mode on
  (`DevToolsSecurity -enable`), which the ADR does not list, so a debugger or test runner
  does not raise the Developer Tools Access password dialog.
- ADR 0024 compares evidence with the #124 handover count; the code compares step numbers with
  `Manager.HandoverStep` (the step claimed when the count last moved), which orders the same
  way because steps are monotonic. Like the count, it resets when the daemon restarts.
- ADR 0024 says inconclusive "must name the unchecked checks": the daemon fills in missing
  answers as `unchecked` rather than refusing. An input with no earlier verifier read reports
  `effect: unknown (no earlier machine_ui read to compare)`, a fourth effect wording.
- ADR 0024 puts the effect on "the step record"; steps.jsonl is append-only and the input's
  line is written before its effect is known, so the effect lives on the UI read that found
  it (`effect.of` names the input step), not on the input's own line.
- ADR 0027: point 4 also covers timing checks, and "the latest read" is the verifier's newest
  `machine_ui` at or before the check's newest evidence step, cited or not; which element a
  check rests on is guessed from its criterion and observed text (`mentions`). The visual
  screenshot rule and the timing window rule apply to fail answers as well as passes, and a
  timing pass may not cite an observation after its action that started late, except a
  screenshot on a check that is also visual: the verifier cannot take a screenshot within 2 s
  of an input (a model step takes longer), so without that a visual and timing check could
  never pass; its in-time evidence is then usually the effect read. The keyword lists add a few
  inflections to the ADR's (invisible, legible, color, instant, without delay). `covered`
  counts only other apps' non-system windows over the whole frame. `offscreen` marks only
  frames the helper still reports outside the display: the walk already drops elements
  outside the screen, a window or a scroll area. Point 4's "rests on" does not fire when a
  drawn element of the same read shows the same thing the claim names (see `drawnRule`),
  and a bare integer names an element only as its whole text or beside one of its words: the
  first simple-tier run refused a correct fail over a Value field reading 10 and a blank
  result reading "10 km = 6.21 mi".
- ADR 0028 detects a quit as "the frontmost app before the input no longer running"; the code
  takes the app of the verifier's previous read (which may be an app it named, not the
  frontmost) and the running regular apps the effect read lists. A pass is refused only when
  it cites the quit read as evidence, as the ADR's consequences say; a pass whose actions
  include the quitting input but cites a later read is judged by the other rules.
- ADR 0034 asks for "the models that verified it"; the report takes them from the manifest
  (issue #154), but a run recorded before that has none, so its report names the daemon's
  configured models at report time and says so in `models.source`, which can differ from what
  verified it. It also refuses a finish while a turn is owed
  (a turn-starting message nothing has answered yet), not only while one runs.
- ADR 0025: a case's `patch` is the path of a `.diff` under `bench/cases/` (`patches/<id>.diff`),
  not the diff inline in the JSON, so patches read and review as diffs and lying cases reuse
  their mutant's. v1 has 4 fixture apps, not the 5 to 7 the ADR expects in all. Checklist
  coverage matches `must_check` to the checks' text by a token heuristic (`covers`), not by
  judgement.
