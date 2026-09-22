# 0010. Tart is OpenAI-owned and FSL-licensed: put it behind a driver interface

Date: 2026-09-21
Status: accepted. Adds a constraint to ADR 0004, which chose Tart as the VM engine
without recording who owns it or under what licence.

## Context

Checked against the GitHub API on 2026-09-21:

| Old path | Now resolves to |
| ----------------------------- | ------------------------- |
| `cirruslabs/tart` | `openai/tart` (HTTP 301) |
| `cirruslabs/orchard` | `openai/orchard` |
| `cirruslabs/tart-guest-agent` | `openai/tart-guest-agent` |

OpenAI owns the VM engine, the guest agent, and Orchard, which schedules macOS VMs
across several hosts and does roughly what greenroom's fleet layer would do.
`cirruslabs/macos-image-templates` stayed in `cirruslabs` under MIT, so the image
recipes are unaffected.

`openai/tart` is under the Functional Source License 1.1 with an Apache-2.0 future
licence, `Copyright 2022-2026 OpenAI`. The clause that matters:

> A Competing Use means making the Software available to others in a commercial product
> or service that:
> 1. substitutes for the Software;
> 2. substitutes for any other product or service **we** offer using the Software that
>    exists as of the date we make the Software available;
> 3. offers the same or substantially similar functionality as the Software.
>
> Permitted Purposes specifically include using the Software: 1. for your internal use
> and access; ...

Tart has always been FSL, including under Cirrus. What changed is who "we" refers to.
Clause 2 now measures against OpenAI's products, and OpenAI ships a coding agent that
would plausibly want macOS machines. Each Tart version becomes Apache-2.0 two years
after its release.

That gives two readings, and the difference between them is the risk:

- A local daemon on a developer's own Mac. The user runs Tart on their own hardware for
  their own agents. The licence names this as a Permitted Purpose.
- A hosted greenroom fleet renting macOS VMs to other people. This is a commercial
  service built on Tart, which is what clause 2 describes.

`docs/00-idea.md` lists platform risk, meaning Anthropic or OpenAI or Cursor adding
macOS machines. It is now also a supply risk. The engine v0 runs on belongs to a likely
competitor who sets the terms for future versions.

## Decision

1. v0 stays on Tart. The local-daemon case is a Permitted Purpose. M1 does not change.
2. The daemon talks to Tart through one driver interface. No other package runs `tart`
   or parses its output. The interface covers what ADR 0004 already needs: create,
   clone, run, suspend, exec, ip, delete, push, pull.
3. A hosted fleet does not ship on Tart without a written legal opinion. This gates the
   hosted product, not v0, and has to clear before any work that assumes a fleet.
4. Pin the Tart version and record its release date, so we know when that version turns
   Apache-2.0.
5. Correct `docs/03-tech-stack.md`, which says OpenAI "maintains a fork".

## Consequences

- The driver interface costs little now and keeps three exits open: write directly
  against `Virtualization.framework` (what Cua and Lume did), pin a Tart version old
  enough to have converted to Apache-2.0, or buy a licence from OpenAI.
- Pinning Tart conflicts with wanting new Tart features. `tart clone --stacked` shipped
  in 2.36.0 on 2026-08-25 and is the cheap image-layering mechanism
  `docs/09-image-strategy.md` relies on. Using it pins us to a version that converts in
  2028.
- Orchard being OpenAI's removes a buy option for the fleet layer.
- The guest agent went from v0.12.0 to v0.14.2 between 2026-08-12 and 2026-09-02, adding
  exec-as-user, signal delivery, background processes that survive the RPC, and public
  gRPC bindings. The bindings would let the Go daemon import them instead of running
  `tart exec`, which is tempting and also deepens the coupling this ADR is trying to
  limit. Use the CLI in the driver until the legal question is answered.
- This says something about where greenroom competes. The machine and toolchain-image
  layers are becoming commodities under the engine's new owner. The evidence layer,
  meaning the verifier and the run manifest and the PR hand-back, is not.

## Implementation notes

- Decision 2: the boundary is the `internal/tart` package. It is the only code that runs
  `tart` or parses its output. A Go interface is extracted when a second engine exists;
  until then it would have one implementation and no caller that needs it.
- Decision 4: pinned to Tart 2.37.0 (`tart.PinnedVersion`), released 2026-09-09.
