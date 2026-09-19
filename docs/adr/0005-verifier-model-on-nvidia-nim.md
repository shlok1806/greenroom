# 0005. The verifier agent runs on NVIDIA NIM

Date: 2026-09-18
Status: accepted

## Context

ADR 0003 says the agent that uses a machine is the developer's own Claude Code session,
and that greenroom's own verifier agent is deferred. That verifier is now wanted, so that
the noise of a failed build never enters the context of the agent that writes code.

A key for NVIDIA NIM is available, so the verifier model comes from there. NIM publishes
82 models on one OpenAI-compatible endpoint. None of them was chosen from memory. Every
number below is measured against `integrate.api.nvidia.com` on 2026-09-18 with two probes
that match the real job.

Probe A gives the model three tools (`machine_exec`, `machine_screenshot`,
`read_host_file`) and the symptom of a machine stuck in `booting`. A correct answer calls
a tool. Probe B gives the real failure from this repository, the host VM limit, and asks
for the cause and the daemon change in one sentence each.

## What the measurements show

| Model | Tool call | Diagnosis | Notes |
| ----- | --------- | --------- | ----- |
| `nvidia/nemotron-3-ultra-550b-a55b` | 3 of 3 correct, 1.5 to 6.6 s | correct, 2.6 s | continues the loop after a tool result |
| `z-ai/glm-5.3` | correct, 47.5 s | correct, 74.4 s | correct but slow |
| `z-ai/glm-5.3-flash` | correct, 39.7 s | correct, 77.1 s | not faster than the model it shortens |
| `nvidia/nemotron-3-super-120b-a12b` | HTTP 500 with tools | correct, 2.7 s | tool calling failed |
| `moonshotai/kimi-k3` | timed out at 120 s | timed out | not usable today |
| `deepseek-ai/deepseek-v4-flash-0731` | timed out at 120 s | timed out | not usable today |
| `moonshotai/kimi-k2.6` | HTTP 404 | HTTP 404 | not entitled on this account |

Both probes name the host VM limit correctly on the models that answer. The difference is
tool calling and speed, and those are the two things an agent in a loop needs.

## Decision

**Reasoning and tool calling: `nvidia/nemotron-3-ultra-550b-a55b`.** It called the right
tool with the right argument in every run, it answered the diagnosis probe in 2.6 s
against 74 s for the nearest correct alternative, and it continued the loop with a
sensible next action after a tool result.

**Screens: `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning`.** The chosen reasoning model
cannot accept images. It answers `multimodal processing is not enabled`, and so does
`z-ai/glm-5.3`. The omni model read a real screenshot from one of our machines in 26.5 s.
It named the dialog that covered the page and quoted its first line correctly.

A verifier that cannot look at a screen is not this product, so the agent uses two models:
one to think and drive tools, one to look. The daemon already returns both a JPEG and a
PNG path from `machine_screenshot`, so the split costs nothing at the tool layer.

## Consequences

**The verifier cannot be Claude Code.** Claude Code authenticates against Anthropic,
Bedrock, Vertex or Foundry. NIM speaks the OpenAI protocol. Running Claude Code against
NIM would need a protocol translation proxy in front of it. ADR 0003 assumed the verifier
was Claude Code in v0, and that assumption no longer holds for this path. The verifier
becomes our own loop over the NIM endpoint, which is the "later it is ours" case in
`docs/00-idea.md` arriving earlier than planned.

**Two models mean two failure modes.** The reasoning model can be right while the vision
model misreads a screen, and the opposite. Evidence stays authoritative: the run record
holds the PNG, so a person can check what the agent claimed to see.

**Availability is not guaranteed.** Three of the eight models tested timed out or returned
404 on this account. The verifier needs a configured model name, not a hardcoded one, and
a clear error when the endpoint refuses.

**The key never goes in a machine.** `docs/05-transport.md` rule 1 says a credential must
not enter an image or a snapshot. The verifier runs on the host beside the daemon, and the
machine keeps no NIM key.

## Configuration

`.env` at the repository root, git-ignored, mode 600:

```
NVIDIA_API_KEY=...
NVIDIA_BASE_URL=https://integrate.api.nvidia.com/v1
GREENROOM_VERIFIER_MODEL=nvidia/nemotron-3-ultra-550b-a55b
GREENROOM_VISION_MODEL=nvidia/nemotron-3-nano-omni-30b-a3b-reasoning
```

`.env.example` carries the same names with no values.
