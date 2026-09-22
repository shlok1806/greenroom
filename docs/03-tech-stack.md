# Tech stack

**Decided: Go daemon, official Go MCP SDK over Streamable HTTP, stateless MCP, state in
the daemon.** Recorded as [ADR 0004](adr/0004-go-daemon-official-mcp-sdk.md). Tart
ownership and licence: [ADR 0010](adr/0010-tart-license-and-openai-ownership.md).

## Why Go

Language speed does not matter here. MCP handling is ~0.5 ms (Go) vs ~0.8 ms (TS) per
request; everything else in a turn costs 100 ms to minutes (exec, screenshots, sync,
builds, the model). What matters for a daemon next to two VMs:

| | Go | TypeScript |
| --- | --- | --- |
| Idle RSS | 21 MB | 162 MB |
| Startup | 33 ms | 287 ms |
| Distribution | one static binary, launchd | needs Node |
| Concurrency | goroutines, contexts | async, clumsier cancellation |
| MCP SDK | `modelcontextprotocol/go-sdk`, official, lags spec by weeks | reference SDK |

The MCP surface is small, so lagging a spec revision by weeks is acceptable.

## Stack as built

| Layer | Choice |
| --- | --- |
| VM engine | Tart CLI as a subprocess, pinned 2.37.0 |
| Guest exec | `tart exec` (guest agent over vsock) |
| File sync | system `rsync -a` over ssh |
| Screenshot | `screencapture` in the guest, base64 over exec |
| Computer use | Swift helper compiled in the guest (CGEvent) |
| MCP | `go-sdk`, Streamable HTTP on `127.0.0.1:7777/mcp`, stateless |
| State | `~/.greenroom/state.json` plus run directories (ADR 0004 said SQLite; not done) |
| HTTP | `net/http` stdlib |
| CLI | same binary, stdlib `flag` (`serve`, `prepare-image`, `version`) |
| Tests | `go test`; real-VM tests behind `-tags tart` |
| TypeScript | none yet; pnpm/turbo/biome kept for a future SDK or viewer |

Claude Code connects with `claude mcp add --transport http greenroom <url>` and retries
HTTP servers on disconnect. Stateless MCP means a dying Claude Code session never loses a
machine.

## Speed comes from elsewhere

- Cheap screenshots: scaled JPEG for the model, PNG kept as evidence.
- Incremental sync with excludes.
- Warm build caches at a fixed guest path (`10-build-transport.md`).
- Warm machines via suspend/resume: measured, not worth it on the base image
  (`09-image-strategy.md`, `10-build-transport.md`).

## Sources

- [MCP benchmark across five languages](https://github.com/desty2k/mcp-benchmark)
- [Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- [MCP 2026-07-28 spec post](https://blog.modelcontextprotocol.io/posts/2026-07-28/)
- [Claude Code MCP docs](https://code.claude.com/docs/en/mcp)
- [Tart guest agent](https://tart.run/blog/2025/06/01/bridging-the-gaps-with-the-tart-guest-agent/)
