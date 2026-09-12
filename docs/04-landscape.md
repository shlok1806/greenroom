# 04 - Landscape: macOS machines for coding agents

Status: DRAFT v1 (research in progress, being extended). Date: 2026-09-12.

## Summary

- Linux sandboxes for agents are a crowded, well-funded commodity (E2B $21M A, Daytona $24M A, Runloop $7M seed, Modal, Vercel, Cloudflare). None of them offer macOS.
- macOS is gated by Apple's SLA: VMs only on Apple hardware, max two macOS guests per host, and hosted leases must be 24 hours minimum. That is why the Linux vendors stay out.
- The macOS VM tooling layer is consolidating around OpenAI: Cirrus Labs (Tart, Orchard) joined OpenAI's Agent Infrastructure team on April 7, 2026.
- Cua (YC X25, $500K) is the only open-source "macOS VM for agents" startup still running; its cloud fleet is priced per vCPU-hour.
- Scrapybara (YC) shut its VM service on Oct 15, 2025 and pivoted to a coding agent. Cyberdesk (YC) pivoted away from "VMs for computer-use agents" because customers already had VMs.
- Devin now runs macOS cloud agents (Xcode, simulator, computer use) via Namespace devboxes, as of July 2026. This is the closest existing product to greenroom.
- Claude Code has a local-only iOS Simulator integration and macOS computer use; neither works in cloud sessions. Codex cloud and Cursor cloud agents are Ubuntu only.
- Developer pain is real and documented: agents cannot see the simulator, laptops get tied up running 4-5 parallel agents, and cloud agents cannot build Xcode projects.
- Whitespace: a hosted, ephemeral, snapshot-restored macOS machine with a verification agent that hands screenshots back to a PR, callable over MCP from any coding agent.
- Strongest counterargument: OpenAI, Anthropic, and Cognition each now own or can trivially build the macOS runner layer, and Apple's 24-hour lease rule makes true per-minute ephemeral hosting legally awkward.

(Sections below are being filled in; see Sources for what has been verified so far.)
