# 0001. Monorepo tooling

Date: 2026-09-11
Status: accepted

## Context

The product will have several deployable parts that share types and a protocol:
a host daemon, an in-VM agent, an MCP server, an SDK, possibly a control plane and a
GitHub App. They should live in one repo so protocol changes land atomically.

## Decision

- pnpm workspaces with `apps/*` and `packages/*`.
- Turborepo for task orchestration and caching.
- TypeScript with a strict shared `tsconfig.base.json`.
- Biome for lint and format (one tool, fast, no plugin sprawl).
- Node 26.

Language for the host daemon and in-VM agent is not decided. TypeScript is the default
for everything; a Go or Swift component is allowed if the VM layer needs it (Tart is
driven over its CLI, and Virtualization.framework is Swift-only).

## Consequences

- One `pnpm install`, one `pnpm lint`, one `pnpm test` at the root.
- Non-TypeScript packages, if added, get a `package.json` with matching script names
  so Turborepo can still orchestrate them.
