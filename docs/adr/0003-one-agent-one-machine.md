# 0003. v0 is one agent, one machine, same session

Date: 2026-09-11
Status: accepted

## Context

The long-run product (hosted fleet, greenroom's own verifier agent, parallel sub-agents
each with a machine, evidence on every PR) kept pulling the plan toward questions that
cannot be answered before the basic mechanics exist. Parallelism in particular is a
capacity question that needs data from real runs.

## Decision

v0 proves one thing: a developer in a Claude Code session on their Mac can pull up a
machine and, in that same session, build, run, and look at the screen on it.

- One agent, one machine, one host (the developer's own Mac).
- The agent that uses the machine is the same Claude Code session the developer is in.
  No separate greenroom agent, no context hand-off.
- Every tool response carries a run id so the daemon can become stateful later. Whether
  runs persist across sessions is deferred.
- Tools first: create, sync, exec, screenshot, destroy. Computer use next.

## Deferred, deliberately

Parallelism and multi-host, greenroom's own verifier agent, giving that agent context,
hosted, PR posting, secrets, image distribution. Each gets its own ADR when the
mechanics are proven.
