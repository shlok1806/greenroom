# Tech stack report

Status: accepted 2026-09-11 as [ADR 0004](adr/0004-go-daemon-official-mcp-sdk.md).

## What kind of application this is

greenroom v0 is a **background service on the developer's Mac** with no GUI. It owns
VMs, records runs, and speaks MCP so Claude Code can call it. A small CLI starts it,
pulls images, and lists runs. The hosted version later is the same service on a fleet
of Mac minis behind an API.

## Where the latency actually is

The question "is Go faster than TypeScript" is the wrong question for this system. The
measured cost of the language in an MCP server is under a millisecond per request, and
every other step in the loop is hundreds to thousands of times slower.

| Step in one agent turn                    | Cost (order of magnitude)     | Set by                     |
| ----------------------------------------- | ----------------------------- | -------------------------- |
| MCP request handling in the daemon        | 0.5 ms (Go), 0.8 ms (TS)      | language                   |
| `tart exec` round trip to the guest       | tens of ms (gRPC to agent)    | Tart, measured in spike    |
| Screenshot: capture, encode, transfer     | 100 ms to 1 s                 | resolution, format, channel|
| rsync of the working tree                 | 100 ms to seconds             | delta size                 |
| Build inside the VM                       | seconds to minutes            | the project                |
| Machine create: clone plus boot           | seconds (cold), less if resumed from suspend | Tart, snapshot strategy |
| The model thinking about the screenshot   | seconds                       | the LLM                    |

Numbers for the first row come from a cross-language MCP benchmark over Streamable HTTP
on Apple silicon: proxy p50 of 0.50 ms for Go and 0.76 ms for TypeScript. The rest are
estimates to be replaced by spike measurements.

What makes greenroom fast is therefore none of the language choice and all of:

- **Warm machines.** `tart suspend` a booted, logged-in machine and resume it, instead
  of cold booting per run. Keep one warm clone ready.
- **Cheap screenshots.** Capture at reduced scale, JPEG not PNG when the agent is just
  looking, stream back over the exec channel rather than a second ssh session.
- **Persistent channels.** One connection to the guest per machine, not one per call.
- **Incremental sync.** rsync deltas, excludes for build output.

## The daemon language, decided on the right criteria

Given latency is a wash, the criteria that remain are: memory footprint on a host that
shares its RAM with two VMs, single-binary distribution and launchd installation,
concurrency model for managing several machines, and fit with the surrounding
ecosystem.

| Criterion                        | Go                                        | TypeScript                                  |
| -------------------------------- | ----------------------------------------- | ------------------------------------------- |
| Idle memory (benchmark RSS)      | 21 MB                                     | 162 MB                                      |
| Startup                          | 33 ms                                     | 287 ms                                      |
| Distribution                     | one static binary; `brew install`, launchd plist | needs Node runtime; npm global or bundle |
| Concurrency for N machines       | goroutines, contexts, cancellation built in | async fine, cancellation clumsier          |
| ssh client                       | `golang.org/x/crypto/ssh`, native         | `ssh2` package or shell out                 |
| gRPC (Tart's guest agent speaks it) | native, first class                    | usable, heavier                             |
| Official MCP SDK                 | `modelcontextprotocol/go-sdk` v1.7, tier 1, maintained with Google, supports 2026-07-28 | `@modelcontextprotocol/sdk` v2, the reference implementation, what Claude Code's own client uses |
| Spec churn risk                  | Go SDK ships spec changes weeks after TS  | first to get everything                     |
| Neighbours                       | Tart's guest agent, Docker, Tailscale are Go | most community MCP servers are TS          |

**Recommendation: Go for the daemon.** Not for latency, but because it is a daemon:
it should be a single small binary that installs with one command, idles in 20 MB
next to two VMs, and manages concurrent machines with goroutines and contexts. That is
what Go is for, and it is what Tart's own guest agent, Docker, and Tailscale chose for
the same reasons.

The one real cost is spec churn on the MCP layer: the 2026-07-28 protocol revision was
a large rewrite, and the Go SDK caught up weeks after TypeScript. The MCP surface here
is five tools; that surface is thin enough that lagging a spec revision by a few weeks
does not hurt.

## MCP choice

- **SDK:** the official `github.com/modelcontextprotocol/go-sdk`, v1.7 or later. Not
  the older community `mcp-go`; the official SDK is where maintenance and spec support
  now go.
- **Transport:** Streamable HTTP on `http://localhost:7777/mcp`, served by the daemon.
  Claude Code connects with `claude mcp add --transport http greenroom <url>`, treats
  HTTP servers as reconnectable (five backoff attempts, versus none for stdio), and
  since v2.1.232 negotiates the 2026-07-28 revision with HTTP servers automatically.
- **Session model:** stateless at the MCP layer, as the 2026-07-28 revision wants and as
  the Go SDK requires for that revision. All real state (machines, runs, run ids) lives
  in the daemon's own store, keyed by run id, which is what ADR 0003 asked for anyway.
  A Claude Code session dying never loses a machine.

## Rest of the stack

| Layer               | Choice                                        | Why                                                     |
| ------------------- | --------------------------------------------- | ------------------------------------------------------- |
| VM engine           | Tart CLI as a subprocess                      | Wraps Virtualization.framework; clone, suspend, exec, pull built in |
| Guest exec          | `tart exec` (gRPC to Tart's guest agent, included in non-vanilla Cirrus images); ssh fallback | No network or keys needed for commands |
| File sync           | system `rsync` over ssh                       | Do not reimplement rsync                                |
| Screenshot          | `screencapture` in the guest, via exec        | Built in; needs screen-recording permission in the image|
| Computer use (M2)   | Swift CLI in the guest (CGEvent, AXUIElement) | Only Swift can do this on macOS                         |
| Run store           | SQLite (`modernc.org/sqlite`, pure Go) plus evidence files under `~/.greenroom/` | Single file, no cgo, no server |
| HTTP                | `net/http` stdlib                             | Enough for MCP plus a small JSON API                    |
| CLI                 | same binary, `cobra` subcommands              | `greenroom daemon`, `greenroom images pull`, `greenroom runs` |
| Tests               | `go test`; Tart-backed e2e behind a build tag | Unit tests everywhere; e2e needs a Mac with Tart        |
| TypeScript in repo  | kept for a future JS SDK and run viewer only  | pnpm/turbo skeleton stays; nothing TS is on the v0 path |

## A competitive signal found on the way

OpenAI maintains a fork of `tart-guest-agent`. Someone there is running macOS VMs on
Tart, most plausibly for Codex. That confirms the wedge is real and that the platform
risk in the idea doc is not hypothetical. Worth watching.

## Decisions needed

1. Go for the daemon and the guest side, TypeScript only for a later SDK and UI.
2. Official Go MCP SDK over Streamable HTTP, stateless MCP sessions, state in the daemon.
3. `~/.greenroom/` for state and `localhost:7777` for the endpoint.

## Sources

- [MCP benchmark across five languages](https://github.com/desty2k/mcp-benchmark)
- [Go SDK v1.7.0 release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
- [Go SDK repository](https://github.com/modelcontextprotocol/go-sdk)
- [TypeScript SDK v2 docs](https://ts.sdk.modelcontextprotocol.io/v2/)
- [MCP 2026-07-28 specification post](https://blog.modelcontextprotocol.io/posts/2026-07-28/)
- [SDK betas for 2026-07-28](https://blog.modelcontextprotocol.io/posts/sdk-betas-2026-07-28/)
- [Claude Code MCP docs](https://code.claude.com/docs/en/mcp)
- [Tart guest agent announcement](https://tart.run/blog/2025/06/01/bridging-the-gaps-with-the-tart-guest-agent/)
- [cirruslabs/tart-guest-agent](https://github.com/cirruslabs/tart-guest-agent)
- [openai/tart-guest-agent fork](https://github.com/openai/tart-guest-agent)
- [Tart FAQ](https://tart.run/faq/)
