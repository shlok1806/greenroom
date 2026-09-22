# CLAUDE.md

Working instructions for agents in this repo. What greenroom is and how to run it:
`README.md`. Current milestone and next steps: `docs/01-plan.md`.

## Layout

- `apps/daemon` - Go daemon `greenroom`. See its `CLAUDE.md`.
- `apps/companion` - SwiftPM macOS app. See its `CLAUDE.md` and `CONTEXT.md`.
- `images/` - Packer recipes and guest scripts for the VM image.
- `packages/` - empty; TypeScript packages only when one is needed.
- `docs/` - notes `00`-`10`, ADRs in `docs/adr/`.
- `spikes/` - throwaway measurement scripts. Nothing imports them.

## Commands

Turborepo runs each package's `package.json` scripts (the Go and Swift apps have shims).

```sh
pnpm install
pnpm lint        # biome check . && turbo run lint
pnpm lint:fix
pnpm build
pnpm test
pnpm typecheck
```

## Rules

- Go for deployables; Swift only for the companion; TypeScript only if a JS SDK or web
  viewer appears (ADR 0004 over ADR 0001). A new package needs `build`/`test`/`lint`
  scripts so root commands cover it.
- Record a decision before implementing it: system-wide in `docs/adr/`, package-scoped in
  `<package>/docs/adr/` (create it with the first one). If code and an ADR disagree, say so in that package's CLAUDE.md.
- ADRs are history. Do not rewrite them; supersede them with a new one.
- `CONTEXT.md` is the glossary and invariants (see `CONTEXT-MAP.md`). `CLAUDE.md` is
  working instructions. Do not copy one into the other.
- After adding a module, dependency or convention, update the CLAUDE.md nearest to it.
  Write what the code does not tell you: rules, boundaries, gotchas.
- No em dashes in docs.

## Issues

- GitHub Issues on `shlok1806/greenroom` via `gh`. See `docs/agents/issue-tracker.md`.
- Triage labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`,
  `wontfix` (`docs/agents/triage-labels.md`).
- Domain docs layout: `docs/agents/domain.md`.
