# 0004. Go for the daemon, official Go MCP SDK over Streamable HTTP

Date: 2026-09-11
Status: accepted, amended: daemon state is a JSON file (`~/.greenroom/state.json`),
not SQLite. Supersedes the "TypeScript is the default for everything" line of
ADR 0001; the pnpm/Turborepo/Biome tooling from ADR 0001 stays for future TypeScript
packages (JS SDK, run viewer).

## Context

See `docs/03-tech-stack.md`. Latency is not a language question: the daemon's share of
one agent turn is under a millisecond in either language, while VM boot, screenshots,
sync and the model itself cost seconds. The criteria that remain are memory next to two
VMs, single-binary distribution, concurrency with cancellation, and fit with Tart's
gRPC guest agent.

## Decision

- The daemon and its CLI are one Go binary, `greenroom`, in `apps/daemon`.
- MCP is served by the official `github.com/modelcontextprotocol/go-sdk` over Streamable
  HTTP at `http://localhost:7777/mcp`, stateless at the MCP layer. All state (machines,
  runs) lives in the daemon's own SQLite store under `~/.greenroom/`, keyed by run id.
- Tart is driven as a subprocess. `tart exec` for commands and screenshots, ssh + rsync
  for file sync.
- Computer use (M2) is a Swift helper inside the guest.
- TypeScript is used only for packages that need it: a JS SDK or a web run viewer, if
  and when they exist.

## Consequences

- `go build` is the build; Turborepo wraps it through a `package.json` in `apps/daemon`
  so the root `pnpm build`/`pnpm test` still run everything.
- The Go MCP SDK trails the TypeScript SDK by weeks on protocol revisions. Our tool
  surface is small; we accept the lag.
- Contributors need Go 1.26+ and Tart. `golangci-lint` is the linter.
