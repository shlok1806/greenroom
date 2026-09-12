# v0 plan

Decisions locked in [ADR 0002](adr/0002-v0-scope.md): macOS, local, shell +
screenshot then computer use, solo dev via MCP.

## The v0 story, end to end

You are in Claude Code on a Mac app repo. You say:

> Build the app on a clean machine, launch it, and show me the main window.

Claude Code calls the greenroom MCP server, which runs on your Mac. The agent does:

1. `machine.create` - clone the project snapshot, boot it. Target: under 15 s.
2. `machine.sync` - push the working tree (including uncommitted changes) into the VM.
3. `machine.exec` - `xcodebuild ...`, sees the compiler error, fixes it locally, syncs
   again, rebuilds.
4. `machine.exec` - launches the built `.app`.
5. `machine.screenshot` - gets a PNG back, looks at it, notices the window is empty.
6. Reads the app log via `machine.exec`, finds the crash, fixes, reruns, screenshots.
7. `machine.destroy`. The run directory on disk holds every screenshot and command.

Later milestones let it click through the app and post the run to the PR.

## Components

```
apps/daemon        long-running process on the host Mac. Owns VMs, snapshots, runs.
packages/mcp       MCP server. Thin: maps tools onto the daemon's API.
packages/protocol  shared types for tools, runs, evidence.
packages/guest     what runs inside the VM (v0: nothing custom, just ssh + macOS
                   built-ins; grows a helper binary for computer use).
```

Keep the MCP server separate from the daemon so a CLI, a GitHub App, or a hosted
control plane can drive the same daemon later.

## VM layer: Tart

Tart (Cirrus Labs) wraps Apple's Virtualization.framework. It gives us:

- Prebuilt images with Xcode: `ghcr.io/cirruslabs/macos-<release>-xcode:<version>`.
- `tart clone` is an APFS copy-on-write clone, so per-run VMs cost seconds and
  near-zero disk.
- `tart run --no-graphics`, `tart ip`, SSH into the guest (`admin`/`admin` in the
  Cirrus images).
- Suspend/resume so a VM can be checkpointed with the app already running.

Apple's limit is two running macOS VMs per host. On a personal Mac that also means
"two agents verifying at once", which is fine for v0.

### Image layers

```
cirruslabs base + Xcode            (pulled once, ~50-80 GB)
  -> greenroom base                (auto-login, TCC permissions granted for
                                    screen recording and accessibility, sshd,
                                    guest helper installed)
    -> project snapshot            (repo cloned, dependencies resolved, one
                                    warm build done)
      -> per-run clone             (what the agent gets; destroyed after)
```

The project snapshot is the thing that makes the loop fast. Rebuild it when the
lockfile or Xcode version changes, not on every run.

Disk on this Mac: ~90 GB free with Xcode already installed on the host. One base
image plus one project snapshot fits. Two projects probably do not. v0 needs a
`greenroom images prune` command from the start, and the daemon should refuse to
create a VM when free space is under a threshold rather than filling the disk.

## Tool surface

### v0 (shell + screenshot)

| tool                 | notes                                                       |
| -------------------- | ----------------------------------------------------------- |
| `machine.create`     | from project snapshot; returns machine id, ip               |
| `machine.sync`       | rsync working tree host -> guest; respects .gitignore       |
| `machine.exec`       | run a command, stream stdout/stderr, timeout, cwd           |
| `machine.screenshot` | full screen PNG; optional window-by-title crop              |
| `machine.fetch`      | copy a file guest -> host (logs, crash reports, artifacts)  |
| `machine.destroy`    |                                                             |
| `run.list`, `run.show` | inspect past runs and their evidence                      |

### M2 (computer use)

| tool                        | notes                                          |
| --------------------------- | ---------------------------------------------- |
| `machine.click`             | x,y or accessibility target; left/right/double |
| `machine.type`, `machine.key` | text, key chords                             |
| `machine.scroll`            |                                                |
| `machine.ax_tree`           | accessibility tree of the frontmost app, so   |
|                             | the agent can target elements without pixels   |
| `machine.record.start/stop` | screen recording for the evidence bundle       |

The accessibility tree is the underrated one. Pixel-coordinate clicking is fragile;
"click the button titled Sign In" is not. macOS exposes this through AXUIElement and
it works for native apps, which is the whole point of doing macOS first.

### Not in v0

Simulator control (iOS), snapshot management as agent-callable tools, PR posting,
multi-host, anything hosted.

## Evidence

Every run gets a directory: `~/.greenroom/runs/<run-id>/` with

- `manifest.json` - project, git sha, dirty files, machine image, start/end, tools called
- `NNN-<tool>.json` - each tool call and its result
- `NNN-screenshot.png` - screenshots in call order
- `recording.mp4` - once recording exists

The manifest is what later gets rendered into a PR comment or a replay page. Designing
it now, even though nothing consumes it yet, keeps the evidence story honest: the run
is recorded as it happens, not reconstructed by the agent afterward.

## Config

One file in the project, `greenroom.yaml`:

```yaml
image: macos-sequoia-xcode:16.2
setup:                # runs when building the project snapshot
  - xcodebuild -resolvePackageDependencies
  - xcodebuild build -scheme App
sync:
  exclude: [build/, DerivedData/]
```

No test plan in the config for v0. The agent decides what to verify from the task.
Whether a checked-in "verification plan" is worth having is an open question below.

## Milestones

**M0 - spike (1 to 2 days).** No code in this repo. Install Tart, pull an Xcode
image, clone, boot, ssh, build something real, `screencapture` over ssh, look at the
PNG. Measure: pull time, disk used, clone-to-ssh time, screenshot latency. Write the
numbers into `docs/02-spike.md`. This decides whether the plan survives.

**M1 - daemon + MCP, shell + screenshot.** The v0 tool table. Dogfood on one real
Mac app. Done when Claude Code can build, launch, screenshot, and fix a deliberately
planted bug without a human touching the VM.

**M2 - computer use.** Guest helper binary (Swift, CGEvent + AXUIElement), the M2
tool table, screen recording.

**M3 - evidence out.** `gh pr comment` from the run manifest. Cheap once the manifest
exists.

**M4 - decide about hosted.** Only after M1 to M3 have been used for a few weeks.

## Open questions that remain

1. **What does the agent do with secrets?** Real apps need API keys, signing
   certificates, a backend. Probably: an explicit allowlist of env vars to forward,
   and the guest gets the host's network by default so localhost backends reach it.
2. **Machine lifetime.** One VM per Claude Code session, or per task? Per session is
   simpler and keeps builds warm. The daemon should reap idle ones after N minutes.
3. **Checked-in verification plan or not.** A `verify:` section in the yaml with
   steps like "launch, open settings, screenshot" would make runs repeatable and PR
   evidence comparable across commits. It also adds a thing to maintain. Lean no for
   v0, revisit at M3.
4. **Sync vs git.** rsync is faster and carries uncommitted work. Git is cleaner and
   matches what a PR-triggered run would see. v0 uses rsync; the manifest records the
   dirty files so nobody is fooled.
5. **Language for the daemon.** TypeScript drives Tart over its CLI fine. The guest
   helper must be Swift. If the daemon ends up needing Virtualization.framework
   directly (for suspend, or to drop Tart), it becomes Swift too.
6. **Name.**
