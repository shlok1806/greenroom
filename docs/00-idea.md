# greenroom - machines for AI agents to build, run, and verify on

Working name. "Greenroom" is where you get ready before going on stage.

## The pain

Contributing to an open source Mac app (Wispr Flow) means fixing many small issues at
once, so you run many coding sub-agents in parallel, one per issue, each on its own
branch heading for its own PR. Every one of those branches needs its own build, its own
launch, and its own screenshots before the PR is worth opening.

Today all of that happens on the one Mac you are sitting at. The agents contend for it,
the builds contend for it, and you cannot use it for anything else while they run. And
the verification step is still mostly you: build this one, launch it, click, screenshot,
next branch.

The bottleneck is not writing the fix. It is proving each fix works, N times over, on
a machine that is also your laptop.

## The thesis

Every agent gets its own machine. The machine is disposable, boots from a snapshot with
the toolchain ready, and the agent can build on it, launch the app, look at the screen,
click through it, and hand back evidence. Your Mac goes back to being yours. Five
sub-agents means five machines, not five fights over one.

Three layers, built in this order:

1. **Machines.** Ephemeral VMs, macOS first, restored from a snapshot in seconds.
2. **Tools.** What an agent calls on a machine: exec, sync, screenshot, click, type,
   accessibility tree, logs. Exposed over MCP so any agent can use them.
3. **An agent.** greenroom's own verifier that takes a task ("verify this branch fixes
   #123") and returns a report with evidence. In v0 this is an existing agent (Claude
   Code) running with greenroom's tools; later it is ours.

The long-run shape is "Supabase for agent verification": a hosted primitive with great
developer experience and an MCP, plus an agent that knows how to use it. A coding agent
anywhere calls greenroom, gets routed to a machine in our fleet, and gets evidence back.

## How the pieces talk

```
your Claude Code (orchestrator, runs sub-agents)
   |  MCP: verify(branch, task)  or  machine.* tools
   v
greenroom                               <- daemon today, cloud API later
   |  allocates a machine slot
   v
machine (macOS VM)
   |  greenroom agent runs INSIDE with the machine tools
   v
run report: manifest + screenshots + recording  -> back to the caller, and to the PR
```

Two ways in, both supported from v0:

- **Low level.** `machine.*` tools. The caller's agent does the thinking. This is how
  we build and debug the machine layer.
- **High level.** `verify(...)`. greenroom's agent does the thinking on the machine and
  returns a report. This is what a sub-agent orchestrator actually wants: fire it,
  keep working, collect the report.

In v0 the greenroom agent behind `verify` can be Claude Code itself, run headless inside
the machine with a verification skill and the machine tools. That gives us "our agent"
without writing one, and a clean slot to swap in a purpose-built agent later.

## Why macOS is the wedge

Linux sandboxes for agents are a commodity: E2B, Daytona, Modal, Runloop, Morph,
Vercel Sandbox, Cloudflare Sandboxes, and the sandboxes inside Cursor, Codex and Claude.

macOS is structurally different. Apple's license only allows macOS VMs on Apple
hardware, at most two per host. That keeps hyperscaler-style sandbox products out. The
macOS market is served today by CI providers (MacStadium, AWS EC2 Mac, GitHub macOS
runners), built for batch test runs, not for an agent that wants an interactive machine
with a screen it can look at and click on.

Apple's Virtualization.framework plus Tart (Cirrus Labs) makes the primitive cheap:
APFS-cloned images boot in seconds on a Mac mini. Nobody has wrapped that in an
agent-facing product with computer use and PR evidence. Mac apps and iOS apps both need
this, and both run on the same hosts; which one we polish first is a product choice, not
an architectural one.

## What it is not

- Not CI. No agent in the loop, pass/fail only, no screenshots of an app being explored.
- Not a Linux code sandbox. Those exist and are cheap.
- Not a browser agent (Browserbase, Stagehand). Those only see the web.
- Not a replacement for the coding agent. It is the machine, the tools, and the
  verifier the coding agent hands work to.

## Risks to be honest about

- **Two VMs per host.** This caps how many agents one Mac serves. For the parallel
  sub-agent use case that is the core problem, one Mac is not enough. Fleet economics
  (Mac mini cost per slot-hour) decide whether hosted works.
- **Image size.** A macOS image with Xcode is 50GB+. Snapshotting, distributing and
  storing project snapshots per customer needs care.
- **Computer use on macOS is finicky.** Accessibility and screen recording permissions,
  Retina scaling. Must be baked into the image.
- **Platform risk.** Anthropic, OpenAI and Cursor could add macOS machines. Linux-only
  today; Apple's hardware rule makes it awkward for them.
- **Evidence trust.** The daemon records the run, not the agent, so a reviewer is
  looking at what happened rather than what the agent says happened.

## Decided

See `docs/adr/`. macOS first; local daemon first; shell + screenshot then computer
use; first caller is Claude Code over MCP; the verifier is an existing agent in v0.

## Open questions (current agenda)

1. **Concurrency on day one.** How many sub-agents run at once in practice? If it is
   more than two, the "local first" daemon must manage more than one host from the
   start (your Mac plus a rented Mac mini), or queue runs. Decide before M1.
2. **Job or session.** `verify` is a job: submit, get a report. `machine.*` is a
   session. Does v0 need both, or is `verify` implemented as a Claude Code skill over
   `machine.*` until the fleet exists?
3. **How the caller and the verifier communicate.** MCP tool call that blocks until
   the report is ready, or submit-and-poll with a run id? Sub-agent orchestrators
   need submit-and-poll.
4. **Evidence into the PR.** For the parallel-PR workflow the report has to land on
   each PR, which moves PR posting earlier than "later".
5. **Secrets and networking** into the machine.
6. **Name.**
