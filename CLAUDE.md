# greenroom

Monorepo: pnpm workspaces + Turborepo + TypeScript + Biome.

- `apps/` - deployable things (control plane, GitHub app, CLI)
- `packages/` - shared libraries (SDK, MCP server, protocol types)
- `docs/` - product and architecture notes. `docs/adr/` holds system-wide ADRs.

Commands: `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm build`.

Read `docs/00-idea.md` before proposing features. The product is not decided yet;
the open questions at the bottom of that doc are the current agenda.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `shlok1806/greenroom`, driven with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Multi-context: root `CONTEXT-MAP.md` points at one `CONTEXT.md` per workspace package; system-wide ADRs in `docs/adr/`, context ADRs in `<package>/docs/adr/`. See `docs/agents/domain.md`.
