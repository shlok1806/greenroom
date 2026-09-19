# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# greenroom

A machine for AI coding agents to build, run, click through and verify work on. macOS first.

Monorepo: Go daemon in `apps/daemon`; pnpm workspaces + Turborepo + Biome wrap it and any TypeScript packages.

- `apps/daemon` - the Go daemon and CLI (`greenroom`). Other deployables land beside it.
- `packages/` - TypeScript libraries only when needed (JS SDK, run viewer). Empty for now.
- `docs/` - product and architecture notes. `docs/adr/` holds system-wide ADRs.

Read `docs/00-idea.md` before proposing features. The product is not decided yet;
the open questions at the bottom of that doc are the current agenda. `docs/01-plan.md`
holds milestone status (currently M1).

## Commands

Turborepo fans these out to every workspace package, including the Go daemon through its
`package.json` shim. Language-specific commands live in the package's own CLAUDE.md.

```sh
pnpm install
pnpm lint       # biome check . && turbo run lint
pnpm lint:fix
pnpm typecheck
pnpm test
pnpm build
```

## Conventions

**Go is the default for deployables, TypeScript only where it is needed.** ADR 0004
supersedes ADR 0001 on this: the daemon is Go, and the pnpm/Turborepo/Biome layer stays so
that a future JS SDK or run viewer has a home. A new package adds a `package.json` with
`build`/`test`/`lint` scripts so the root commands keep covering everything.

**Decisions are recorded before they are implemented.** System-wide choices go in
`docs/adr/`; choices scoped to one package go in `<package>/docs/adr/`. When code and an
ADR disagree, say so in that package's CLAUDE.md rather than quietly letting them drift.

**CONTEXT.md and CLAUDE.md are different documents.** `CONTEXT-MAP.md` points at a
`CONTEXT.md` per package holding domain glossary and invariants for humans and agents alike;
CLAUDE.md holds working instructions. Do not duplicate one into the other.

## Living documentation

This project uses per-folder CLAUDE.md files. After any session where you introduce a new
module, dependency, or architectural convention, update the CLAUDE.md closest to where the
change lives - not this file unless the change is project-wide. Write what a future agent
could not derive from reading the code: rules, boundaries, ownership decisions.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `shlok1806/greenroom`, driven with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Multi-context: root `CONTEXT-MAP.md` points at one `CONTEXT.md` per workspace package; system-wide ADRs in `docs/adr/`, context ADRs in `<package>/docs/adr/`. See `docs/agents/domain.md`.
