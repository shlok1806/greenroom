# greenroom

A machine for AI coding agents to build, run, click through, and verify their work on.
macOS first.

Read [docs/00-idea.md](docs/00-idea.md) for the idea and [docs/01-plan.md](docs/01-plan.md)
for where it is. Decisions live in [docs/adr/](docs/adr/).

## What you get

- A daemon (`greenroom`) on your Mac that clones a macOS VM per run, records everything
  the run does (steps, screenshots, a screen timelapse, the conversation), and exposes it
  to a coding agent over MCP and to people over HTTP.
- greenroom's own verifier: an agent that drives the machine, asks when it is stuck,
  replies to whoever talks to it, and proposes verdicts with evidence. It runs on NVIDIA
  NIM, or in `manual` mode where you are the brain and no model is needed.
- A companion app for macOS that shows every run, its screen, its transcript and its
  evidence, and lets you talk to the verifier and step in.

## Requirements

- Apple silicon Mac, macOS 15 or later. Xcode or the command line tools.
- [Tart](https://tart.run): `brew install cirruslabs/cli/tart`, then
  `tart pull ghcr.io/cirruslabs/macos-tahoe-base:latest` (about 20 GB, once).
- Go 1.22+, Node 22+ and pnpm (`corepack enable`).
- Optional: `brew install ffmpeg` to export a run's timelapse as an mp4.

## Run it without a coding agent

This is the quickest way to see the whole thing work. You boot a machine, the companion
shows it, and you drive the verifier by typing to it.

1. Install the daemon as a background service on `127.0.0.1:7777`:

   ```sh
   pnpm install
   cd apps/daemon
   GREENROOM_VERIFIER=manual scripts/install.sh
   ```

   `manual` means the verifier needs no model: whatever you type to it is carried out
   on the machine. To use a real model instead, put your key in `.env` at the repo root
   (copy `.env.example`) and run `scripts/install.sh` without the variable. The service
   restarts on crash and at login; `scripts/uninstall.sh` removes it. Log:
   `~/.greenroom/daemon.log`.

2. Install and open the companion:

   ```sh
   cd ../companion
   scripts/install.sh      # builds Greenroom Companion.app into /Applications and opens it
   ```

   The app talks to `127.0.0.1:7777`. Set `GREENROOM_URL` before launching it to point
   elsewhere.

3. Boot a machine and hand it a project. Until the companion can create machines (it is
   deliberately a viewer, see ADR 0007), use the smoke client, which speaks MCP to the
   daemon the way a coding agent would:

   ```sh
   cd ../daemon
   go run ./internal/testsupport/smokeclient -url http://127.0.0.1:7777/mcp \
     -live /path/to/your/project -watch
   ```

   `-live` creates a machine, waits for it, syncs the directory into `~/work` in the
   guest and posts a first task. `-watch` also opens the machine's screen in a window on
   your Mac. The run appears at the top of the companion's sidebar within a second.

4. Talk to the verifier in the companion's Transcript tab. In `manual` mode it
   understands one instruction per line:

   ```
   run swift build
   run open ~/work/MyApp/build/MyApp.app
   screenshot
   verdict pass the window opened and the build was clean
   ask which scheme should I use?
   help
   ```

   Every instruction becomes a step in the run's evidence and a `progress` message in the
   transcript, exactly as it would with a model. With a model, type in plain words.

5. Look around. The Screen tab plays the run's timelapse and follows live; the Steps tab
   is every command with its output; the toolbar takes a lossless screenshot, saves the
   recording, or destroys the machine. Everything you do is written into the conversation
   so a coding agent joining later sees it.

6. Take the machine. Turn on **Take control** in the Screen tab and the picture becomes a
   screen: click it, drag in it, scroll it, type into it, use its shortcuts. greenroom
   lends you the mouse and keyboard one holder at a time, writes "human took control of
   the screen" into the run's conversation so the coding agent knows, records each burst
   of clicks and keys as a step in the evidence, and speeds the screen up to two frames a
   second while your hand is on it. Turn the switch off, leave the tab or quit and it is
   given back. See `docs/adr/0009-human-control-of-the-screen.md`.

## Run it with a coding agent

Add the daemon to Claude Code as an MCP server:

```sh
claude mcp add --transport http greenroom http://127.0.0.1:7777/mcp
```

Then in a session: "Boot a machine, sync this repo into it, and ask greenroom to build
it and show me the window." The agent uses `machine_*` tools for the machine and
`agent_send` / `agent_wait` to talk to the verifier; you watch and intervene from the
companion. The transcript is shared, so the coding agent, the verifier and you all see
the same conversation.

## Develop

```sh
pnpm install
pnpm lint          # biome, golangci-lint, swift warnings as errors
pnpm build
pnpm test          # Go under -race, Swift XCTest
```

Layout: `apps/daemon` (Go daemon, MCP and HTTP), `apps/companion` (SwiftPM macOS app),
`docs/` for notes and decision records. Each app has its own `CLAUDE.md` with commands and
invariants, and a `CONTEXT.md` with its vocabulary.

Run a run's whole lifecycle against a fake VM with no Tart at all:
`cd apps/daemon && go test ./...`. The one test that boots a real VM is build-tagged:
`go test -tags tart -run TestEndToEnd -v -timeout 10m .`

### CI

Two workflows, both macOS only:

- **`Full suite (self-hosted Mac)`** (`.github/workflows/vm.yml`) is the day to day
  workflow: it runs on every pull request and on push to `main`. Its `fast` job runs the
  same gates as `pnpm lint`/`pnpm build`/`pnpm test` plus `go vet -tags tart` on a
  self-hosted Apple-silicon Mac. Its `vm` job additionally runs the real
  `go test -tags tart ./...` suite against Tart, but only when the change touches a
  VM-critical path (`internal/machine`, `internal/tart`, the e2e tests or
  `internal/testsupport`) or when triggered manually. This self-hosted runner, on the
  maintainer's own Mac, is the only place the `-tags tart` suite can run at all:
  GitHub-hosted runners are themselves virtual machines without nested virtualisation, so
  they cannot boot the real macOS guests Tart needs.
- **`CI (hosted)`** (`.github/workflows/ci.yml`) is a manual, clean-room run on a
  GitHub-hosted `macos-15` runner, triggered by hand with `gh workflow run "CI (hosted)"`.
  It assumes nothing is pre-installed and proves the whole build from scratch, the way a
  new contributor's machine would. It is manual rather than automatic because this repo
  is private and GitHub bills macOS runner minutes at 10x the Linux rate; run it before a
  release or whenever you want that extra assurance, not on every push. Pass
  `-f soak=true` to also run the `internal/machine` race soak
  (`go test -race -count=10 ./internal/machine/`):
  `gh workflow run "CI (hosted)" -f soak=true`.
