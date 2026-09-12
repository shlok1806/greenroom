# Plan

Scope per [ADR 0003](adr/0003-one-agent-one-machine.md): one agent, one machine, same
Claude Code session, mechanics only. Everything else is deferred.

## What v0 does

You are in Claude Code on your Mac and say:

> Boot a machine, build the app on it, launch it, and show me the window.

Claude Code calls greenroom's MCP tools, which run on your Mac:

1. `machine.create` - clone a snapshot, boot it, return `{ runId, machineId }`
2. `machine.sync` - copy the working tree into the machine
3. `machine.exec` - run a command, get stdout, stderr, exit code
4. `machine.screenshot` - get a PNG of the machine's screen
5. `machine.destroy`

That is the whole surface. Every response carries the `runId`.

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

**M1 - the five tools over MCP.** Done when, from a Claude Code session, the agent can
create a machine, sync a repo, build it, launch it, screenshot it, and destroy it,
without the developer touching the VM.

**M2 - computer use.** Click, type, key, scroll, accessibility tree. Then the xcode
image and a real Mac app.

Beyond M2 is not planned. See the deferred list in ADR 0003.
