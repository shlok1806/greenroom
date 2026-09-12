# greenroom - a machine for AI agents to verify their work on

Working name. "Greenroom" is where you get ready before going on stage.

## The pain

Coding agents (Claude Code, Codex, Cursor, Devin) are good at writing code and bad at
proving it works. For a web app they can sometimes spin up a headless browser. For a
desktop or mobile app (a Mac menu bar app, an iOS app, an Electron app) they have
nothing: no machine to build on, no way to launch the app, click through it, see the
screen, or catch the crash. So the loop ends at "I wrote the code, please test it",
and a human does the build-run-click-screenshot cycle by hand.

That cycle is now the bottleneck. Writing the code is fast. Verifying it is slow and manual.

## The thesis

Give the agent a real machine, the tools to drive it, and a way to hand evidence back.

1. **A machine.** An ephemeral VM, restored from a snapshot in seconds, with the
   toolchain installed and the branch checked out. macOS first, Linux second.
2. **A driver.** A small set of tools the agent calls: run a command, sync files,
   take a screenshot, click and type, read app logs, control the simulator.
   Exposed as an MCP server and an SDK so it works with any agent, not just one.
3. **Evidence.** Every run produces a verification report: screenshots, a screen
   recording, logs, test output. It lands on the PR as a comment or a check.

The loop the agent runs: open PR -> get a machine -> build -> launch -> drive the UI
against a test plan -> capture evidence -> post it -> if something broke, fix and rerun.

## Why macOS is the wedge

Linux sandboxes for agents are a commodity: E2B, Daytona, Modal, Runloop, Morph,
Vercel Sandbox, Cloudflare Sandboxes, and the managed sandboxes inside Cursor, Codex
and Claude. Competing there means competing on price against people with more capital.

macOS is different for a structural reason. Apple's license only allows macOS VMs on
Apple hardware, and at most two VMs per host. That keeps hyperscaler-style sandbox
products out and means the macOS agent-VM market is served today by CI providers
(MacStadium, AWS EC2 Mac, GitHub macOS runners), which are built for batch test runs,
not for an agent that wants an interactive machine for 20 minutes with a screen it can
look at and click.

Apple's Virtualization.framework plus Tart (Cirrus Labs) makes the primitive cheap:
APFS-cloned VM images boot in seconds on a Mac mini. Nobody has wrapped that in an
agent-facing product with computer use and PR evidence.

The wedge is: "the agent can build, run, and click through your Mac or iOS app and
show you the screenshots in the PR." That is the thing you could not do on Wispr.

## What it is not

- Not CI. CI runs a script with no agent in the loop and produces pass/fail, not
  screenshots of an agent exploring the app.
- Not a Linux code sandbox. Those exist and are cheap.
- Not a browser agent (Browserbase, Stagehand). Those only see the web.
- Not a coding agent. It is the machine the coding agent uses. It should work with
  Claude Code today and whatever replaces it next year.

## Rough architecture (v0)

```
 agent (Claude Code, Codex, ...)
   |  MCP / SDK
   v
 control plane API          <- allocates machines, stores evidence, talks to GitHub
   |
   v
 host daemon (on a Mac)     <- Tart: VM pool, snapshot restore, port forward
   |
   v
 in-VM agent                <- exec, file sync, screenshot, input events, logs
```

v0 can skip the control plane: the host daemon is the product. Run it on your own Mac
mini, point Claude Code's MCP config at it, and you have the loop working locally.
The hosted version is the same daemon on a fleet of Mac minis behind an API.

## Risks to be honest about

- **Two VMs per host.** Apple's limit caps density. Hosted economics depend on Mac
  mini cost vs what people will pay for a verification run.
- **Image size.** A macOS image with Xcode is 50GB+. Snapshotting and distributing it
  needs care. Tart handles this with OCI registries but pulls are slow.
- **Computer use on macOS is finicky.** Accessibility permissions, screen recording
  permissions, and Retina scaling all bite. Needs to be baked into the image.
- **Platform risk.** Anthropic, OpenAI and Cursor could add macOS sandboxes. They are
  Linux-only today, and Apple's hardware constraint makes it awkward for them.
- **Evidence trust.** A screenshot the agent took is only useful if the human trusts
  the agent did not fake the flow. Recording the whole session and making it
  replayable matters more than individual screenshots.

## Open questions (current agenda)

1. **macOS first or Linux first?** Recommendation: macOS. It is the pain you actually
   felt and the only place there is a moat.
2. **Local daemon first or hosted first?** Recommendation: local daemon. It is the core
   of the hosted product anyway, and you can dogfood it on a Mac mini this month.
3. **Interaction primitive.** Shell + screenshot only, or full computer use (click,
   type, scroll)? Recommendation: build both, ship shell + screenshot first, since it
   unblocks build and crash verification with far less fragility.
4. **Who is the first user?** A solo dev running Claude Code, or a team via a GitHub
   App on every PR? Recommendation: solo dev via MCP. The GitHub App is a distribution
   layer on top once the loop is reliable.
5. **Evidence format.** Screenshots in a PR comment, a hosted replay page, or a check
   run with annotations? Undecided.
6. **Name.** greenroom is a placeholder.
