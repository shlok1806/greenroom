# Idea

**Every coding agent gets its own disposable macOS machine to build, run, click through
and prove its change on, and hands back evidence.** Your own Mac stays yours.

"Greenroom" is where you get ready before going on stage. Working name.

## The pain

Parallel coding agents (one per issue, one branch each) all need their change built,
launched and screenshotted before the PR is worth opening. Today that happens on the one
Mac you are using. The agents fight over it, and verification is still mostly you:
build, launch, click, screenshot, next branch. Writing the fix is not the bottleneck.
Proving it works, N times, is.

## The product, in three layers

1. **Machines.** Ephemeral macOS VMs cloned from an image in seconds.
2. **Tools.** exec, sync, screenshot, click, type, sessions, over MCP so any agent can use
   them.
3. **A verifier.** greenroom's own agent drives the machine, asks when stuck, and proposes
   a verdict with evidence (ADR 0005, 0006).

A coding agent can use the tools directly or hand a task to the verifier through the run's
conversation. The daemon records the run itself, so a reviewer sees what happened, not
what an agent claims.

Long run: a hosted service ("Supabase for agent verification") where any agent calls
greenroom, gets a machine from a fleet, and gets evidence back.

## Why macOS

- Linux agent sandboxes are a commodity (E2B, Daytona, Modal, Vercel, Cloudflare, and
  those inside Cursor, Codex, Claude).
- Apple's licence allows macOS VMs only on Apple hardware, at most two per host. That
  keeps hyperscalers out.
- Mac capacity today is sold as CI (MacStadium, EC2 Mac, GitHub runners): batch jobs, not
  an interactive machine with a screen an agent can use.
- Virtualization.framework plus Tart makes the primitive cheap: APFS clones boot in
  seconds. Nobody has wrapped it for agents with computer use and PR evidence.

## What it is not

Not CI, not a Linux code sandbox, not a browser agent, not a coding agent.

## Risks

- **Two VMs per host** caps parallelism per Mac. Fleet economics decide whether hosting
  works.
- **Image size.** Base 50 GB virtual, 27 GB pull; with Xcode 140 GB virtual, 69 GB pull.
- **Computer use on macOS is finicky** (TCC, Retina). Handled in the image (`09-...`).
- **Platform risk.** Anthropic, OpenAI or Cursor could add macOS machines. OpenAI now owns
  Tart (ADR 0010).
- **SIP is off** in the image. Say so when claiming "verified".

## Decided

See `docs/adr/`: macOS first, local daemon first, Claude Code over MCP as the first
caller, Go daemon, verifier on NVIDIA NIM, one conversation per run, companion app,
recorded screen, human control under a lease, Tart behind a driver interface.

## Open questions

1. **Parallelism.** v0 is one machine per session (ADR 0003). More than two agents at once
   needs a second host or a queue.
2. **Evidence into the PR.** The parallel-PR workflow wants the report on each PR.
3. **Secrets and network egress** for the machine (`05-transport.md`).
4. **Hosted fleet** is gated on a legal opinion about Tart's licence (ADR 0010).
5. **Name.**
