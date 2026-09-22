# greenroom

greenroom gives an AI coding agent a disposable macOS VM to build, run, click through and
verify its work on. A daemon on your Mac clones a VM per run with Tart, records everything
the run does, and serves it over MCP to the agent and over HTTP to a companion app. Its
own verifier agent drives the machine and proposes verdicts with evidence.

Why it exists: [docs/00-idea.md](docs/00-idea.md). Status: [docs/01-plan.md](docs/01-plan.md).
Decisions: [docs/adr/](docs/adr/).

## Requirements

- Apple silicon Mac, Xcode or the Command Line Tools.
- Tart 2.37.0 at `~/.local/tart-2.37.0` (Homebrew's tap is broken; see
  [apps/daemon/CLAUDE.md](apps/daemon/CLAUDE.md#tart) for the install), then
  `tart pull ghcr.io/cirruslabs/macos-tahoe-base:latest` (about 27 GB, once).
- Go 1.27+, Node 22+, pnpm (`corepack enable`).
- Optional: `ffmpeg`, to export a run's recording as mp4. `jq`, for `build-image.sh`.

## Quickstart

1. Install the daemon as a launchd agent on `127.0.0.1:7777`:

   ```sh
   pnpm install
   cd apps/daemon
   GREENROOM_VERIFIER=manual scripts/install.sh
   ```

   `manual` needs no model: you type instructions and the verifier runs them. For a real
   model, copy `.env.example` to `.env` at the repo root, add your NVIDIA key, and run
   `scripts/install.sh` without the variable. Log: `~/.greenroom/daemon.log`. Remove with
   `scripts/uninstall.sh`.

   Optional, makes the first click on each machine ~30 s faster:
   `scripts/build-image.sh`, then rerun `scripts/install.sh` (it picks up `greenroom-base`).

2. Install and open the companion (`/Applications/Greenroom Companion.app`):

   ```sh
   cd ../companion && scripts/install.sh
   ```

   It talks to `127.0.0.1:7777`; set `GREENROOM_URL` to change that.

3. Boot a machine with your project in it. The companion only watches, so use the smoke
   client, which speaks MCP like a coding agent would:

   ```sh
   cd ../daemon
   go run ./internal/testsupport/smokeclient -url http://127.0.0.1:7777/mcp \
     -live /path/to/project -watch
   ```

   `-live` creates a machine, syncs the directory to `~/work/<name>` in the guest and
   posts a first task. `-watch` opens the guest screen in a VNC window.

4. Talk to the verifier in the companion's Transcript tab. In `manual` mode, one
   instruction per line:

   ```
   run swift build
   screenshot
   click 0.5 0.5
   verdict pass the window opened and the build was clean
   help
   ```

   The Screen tab plays the recording and follows live. **Take control** lends you the
   guest's mouse and keyboard (ADR 0009). Steps lists every command and its output.

## Use with Claude Code

```sh
claude mcp add --transport http greenroom http://127.0.0.1:7777/mcp
```

Then ask: "Boot a machine, sync this repo into it, and ask greenroom to build it and
show me the window." The agent uses `machine_*` tools for the VM and `agent_send` /
`agent_wait` to talk to the verifier. You, the agent and the verifier share one transcript.

## Develop

```sh
pnpm install
pnpm lint        # biome, golangci-lint, swift build with warnings as errors
pnpm build
pnpm test        # go test ./..., swift test
```

- `apps/daemon` - Go daemon. Commands and invariants in its `CLAUDE.md`.
- `apps/companion` - SwiftPM macOS app. `CLAUDE.md` and `CONTEXT.md`.
- `images/` - Packer recipes for the VM image.
- `docs/` - notes (`00`-`10`) and ADRs.

Tests need no VM: a fake `tart` drives the whole lifecycle. The real-VM suite is
build-tagged: `cd apps/daemon && go test -tags tart -timeout 20m ./...`.

## CI

- **Checks (self-hosted Mac)** (`vm.yml`): every PR and push to `main`. gofmt, go vet
  (with and without `-tags tart`), build, `go test -race -count=2`, golangci-lint, swift
  build and test, `pnpm lint`.
- **VM suite (self-hosted Mac)** (`vm-suite.yml`): the `-tags tart` suite, on PRs that
  touch `internal/machine`, `internal/tart`, `internal/testsupport` or `e2e*_test.go`, or
  by hand. Needs `greenroom-base` on the runner. Hosted runners cannot boot macOS guests.
- **CI (hosted)** (`ci.yml`): manual clean-room run on `macos-15`
  (`gh workflow run "CI (hosted)"`, add `-f soak=true` for a race soak of
  `internal/machine`). Manual because hosted macOS minutes cost 10x.
