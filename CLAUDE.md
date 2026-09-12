# greenroom

Monorepo: Go daemon in `apps/daemon`; pnpm workspaces + Turborepo + Biome wrap it and any TypeScript packages.

- `apps/daemon` - the Go daemon and CLI (`greenroom`). Other deployables land beside it.
- `packages/` - TypeScript libraries only when needed (JS SDK, run viewer). Empty for now.
- `docs/` - product and architecture notes. `docs/adr/` holds system-wide ADRs.

Commands: `pnpm lint`, `pnpm test`, `pnpm build` at the root; `go build ./...` and `go test ./...` inside `apps/daemon`.

Read `docs/00-idea.md` before proposing features. The product is not decided yet;
the open questions at the bottom of that doc are the current agenda.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `shlok1806/greenroom`, driven with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Multi-context: root `CONTEXT-MAP.md` points at one `CONTEXT.md` per workspace package; system-wide ADRs in `docs/adr/`, context ADRs in `<package>/docs/adr/`. See `docs/agents/domain.md`.
