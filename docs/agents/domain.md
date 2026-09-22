# Domain docs

How engineering skills should read this repo's domain documentation.

## Read first

- `CONTEXT-MAP.md` at the root: every context and whether it has a `CONTEXT.md`.
- `docs/adr/` for system-wide decisions, and `<package>/docs/adr/` if it exists.

If a context has no `CONTEXT.md` or ADR folder yet, proceed silently. Do not flag it or
create it upfront; `/domain-modeling` creates them when a term or decision settles.

## Layout

```
/
├── CONTEXT-MAP.md          every context and where its CONTEXT.md lives
├── docs/adr/               system-wide decisions
├── apps/daemon/            one Go module, several contexts under internal/ (no CONTEXT.md yet)
├── apps/companion/         CONTEXT.md
├── images/                 no CONTEXT.md yet
└── packages/               empty
```

When a workspace package is added, add a row to `CONTEXT-MAP.md`.

## Use the glossary

Name domain concepts (in issues, proposals, test names) with the terms in the relevant
`CONTEXT.md`. If a term is missing, either you are inventing language or there is a gap
to note for `/domain-modeling`.

## Flag ADR conflicts

If your output contradicts an ADR, say so explicitly:

> _Contradicts ADR-0002 (v0 scope: macOS, local first) - but worth reopening because..._
