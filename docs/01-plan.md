# Plan

Scope per [ADR 0003](adr/0003-one-agent-one-machine.md): one agent, one machine, same
Claude Code session, mechanics only. Everything else is deferred.

## What v0 does

You are in Claude Code on your Mac and say:

> Boot a machine, build the app on it, launch it, and show me the window.

Claude Code calls greenroom's MCP tools, which run on your Mac:

1. `machine_create` - clone a snapshot and start it; returns `runId` at once with status `booting`
2. `machine_wait` - block until the machine is `ready` (or `failed`); returns ip and bootSeconds
3. `machine_sync` - rsync a host directory into the machine
4. `machine_exec` - run a command, get stdout, stderr, exit code
5. `machine_screenshot` - JPEG of the screen for the agent, lossless PNG saved in the run
6. `machine_destroy`
7. `machine_list` - live machines and their status, to recover a runId after a session restart

Create is asynchronous because a boot under load took 62 to 81 s in testing and Claude
Code gives an HTTP tool call 60 s to produce its first byte. Every response carries the
`runId`; every call is appended to the run's `steps.jsonl`.

## Components

```
apps/daemon        one process on the host Mac. Owns the VM, records the run.
packages/mcp       MCP server that maps the five tools onto the daemon.
packages/protocol  shared types.
```

If the daemon and the MCP server can be one process for v0 without making the split
harder later, do that.

## VM layer

Tart (Cirrus Labs) over Apple's Virtualization.framework.

- `tart pull` a Cirrus image, `tart clone` per run (APFS copy-on-write, seconds),
  `tart run --no-graphics`, `tart ip`, ssh as `admin`.
- Screenshot via `screencapture` inside the guest over ssh. Needs screen recording
  permission granted in the image and a logged-in desktop session.
- Start with a **base** image (no Xcode, roughly 20 GB) to prove boot, exec and
  screenshot. Move to the **xcode** image (50 GB+) only when building a real app.
  This Mac has about 90 GB free.

## Milestones

**M0 - spike, no code in the repo.** Install Tart, pull a base image, clone, boot, ssh,
run a command, take a screenshot, view it. Record pull time, disk used, clone-to-ssh
time, screenshot latency in `docs/02-spike.md`.

**M1 - the tools over MCP.** Status 2026-09-12: daemon built (`apps/daemon`), Tart-backed
end-to-end test passes, a headless Claude Code session created, drove, screenshotted and
destroyed a machine through the tools. Remaining for M1: build and launch a real GUI app
from a synced repo, and the greenroom base image (prompt-free, ssh key baked in).

**M1.5 - the conversation and the companion.** Status 2026-09-18: ADRs 0006 and 0007
proposed, plan in `docs/08-sessions-and-companion-plan.md`. Every run gets a durable
conversation shared by the coding agent, greenroom's verifier and a human; verdicts
become proposals that can be disputed and accepted; a macOS companion app watches runs
and speaks into the conversation. Slotted before M2 because a verifier that can be
asked and disagreed with is what makes the computer-use tools worth giving it.

**M2 - computer use.** Status 2026-09-20: the machine layer, the companion and the verifier
all have it. `internal/machine/input.go` posts click, type, key and scroll into a guest
through a Swift helper it compiles there, under a one-holder lease the conversation records
(ADR 0009), and the companion's Screen tab is a screen you can use. The verifier has a mouse
too now (issue #11): `machine_click`, `machine_type`, `machine_key`, `machine_scroll` and a
general `machine_input` batch (for a drag, compose down, move and up) are MCP tools in
`internal/mcpserver`, and the model and manual brains in `internal/verifier` carry the same
five natively, each call taking the screen lease, posting, and releasing it before it
returns, so a person watching can take the screen back between calls rather than only
between turns. Remaining for M2: the accessibility tree, and then the xcode image and a
real Mac app.

Beyond M2 is not planned. See the deferred list in ADR 0003.
