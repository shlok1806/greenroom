# CLAUDE.md

Working instructions for agents in this repo. What greenroom is and how to run it:
`README.md`. Current milestone and next steps: `docs/01-plan.md`.

## Layout

- `apps/daemon` - Go daemon `greenroom`. See its `CLAUDE.md`.
- `apps/companion` - SwiftPM macOS app. See its `CLAUDE.md` and `CONTEXT.md`.
- `design/` - themes and tokens for the companion (and a web dashboard later). See its `README.md`.
- `images/` - how the VM images are built (README only; the recipe is
  `apps/daemon/scripts/build-image.sh`, ADR 0018).
- `packages/` - empty; TypeScript packages only when one is needed.
- `bench/` - the verifier bench (ADR 0025): fixture macOS apps, cases with known verdicts and
  their mutant patches. See its `README.md`; the runner is `greenroom bench`.
- `docs/` - notes `00`-`15` (`12`-`15`: iOS, verifier quality, field data, market research), ADRs in `docs/adr/`.
- `scripts/remote/` - the host's tunnel, token and client-artifact commands (`host.sh`) and
  the client installer it serves (`client-install.sh`, POSIX sh). ADR 0021, `docs/11-remote-test.md`.
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
- Every PR body ends with the footer in `.github/pull_request_template.md`: "Made with Greenroom", plus
  "verified in run `<runId>`" when a Greenroom run verified the change (drop that part otherwise).

## Issues

- GitHub Issues on `shlok1806/greenroom` via `gh`. See `docs/agents/issue-tracker.md`.
- Triage labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`,
  `wontfix` (`docs/agents/triage-labels.md`).
- Domain docs layout: `docs/agents/domain.md`.
