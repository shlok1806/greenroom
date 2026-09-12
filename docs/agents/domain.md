# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT-MAP.md`** at the repo root. It points at one `CONTEXT.md` per context. Read each one relevant to the topic.
- **`docs/adr/`** for system-wide decisions. Also check `apps/<name>/docs/adr/` and `packages/<name>/docs/adr/` for context-scoped decisions in the area you're about to work in.

If a context's `CONTEXT.md` or ADR folder doesn't exist yet, **proceed silently**. Don't flag its absence; don't suggest creating it upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

This is a multi-context repo. Contexts are workspace packages under `apps/` and `packages/`.

```
/
├── CONTEXT-MAP.md                     ← lists every context and where its CONTEXT.md lives
├── docs/adr/                          ← system-wide decisions
├── apps/
│   └── daemon/
│       ├── CONTEXT.md
│       └── docs/adr/                  ← context-specific decisions
└── packages/
    ├── protocol/
    │   ├── CONTEXT.md
    │   └── docs/adr/
    └── mcp/
        ├── CONTEXT.md
        └── docs/adr/
```

When a new workspace package is added, add a row to `CONTEXT-MAP.md` for it.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in the relevant `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0002 (v0 scope: macOS, local first) - but worth reopening because…_
