# Verifier configurations for the live suite

Offline evidence: report.md (40 labelled screenshots, 45 replayed brain decisions x1-2). Patch: verifier-model-knobs.patch (against origin/main, applies to e1e33d2, go test ./... green). It adds three env vars and changes nothing when they are unset:
- GREENROOM_VERIFIER_SEES_SCREEN=1: the brain gets each screenshot as an image right after its tool result (no describer; system prompt and tool text say so).
- GREENROOM_VERIFIER_TEMPLATE_KWARGS / GREENROOM_VISION_TEMPLATE_KWARGS: JSON sent as chat_template_kwargs.
Apply: `git apply verifier-model-knobs.patch` in a checkout, then `go run ./apps/daemon serve ...` with the env below.

## 0. Current default (baseline, no code change)
GREENROOM_VERIFIER_MODEL=nvidia/nemotron-3-ultra-550b-a55b
GREENROOM_VISION_MODEL=nvidia/nemotron-3-nano-omni-30b-a3b-reasoning
Evidence: brain right 0.95 when it answers, but 24/90 calls failed after retries (429/500/timeouts), p95 240 s. Describer p50 71 s, p95 159 s, 8/40 failed (503/timeouts), invents controls in 8/40 images.

## 1. Keep ultra, switch describer to kimi-k3 (no code change)
GREENROOM_VERIFIER_MODEL=nvidia/nemotron-3-ultra-550b-a55b
GREENROOM_VISION_MODEL=moonshotai/kimi-k3
Evidence: describer 0/40 failed, 0 invented values, alerts 3/3, control centres 105/111 hit (median error 0.00), recall 0.94-1.00, p50 39-50 s, p95 65-79 s. Isolates the describer change. Brain availability problem remains.
Variant 1b (patch): add GREENROOM_VISION_TEMPLATE_KWARGS='{"thinking": false}' (p50 39 vs 50 s; recall 0.94 on 40 vs 1.00 on 14).

## 2. glm-5.3 brain + kimi-k3 describer (no code change)
GREENROOM_VERIFIER_MODEL=z-ai/glm-5.3
GREENROOM_VISION_MODEL=moonshotai/kimi-k3
Evidence: glm-5.3 85/90 best, 0 wrong, 0 failed calls, verdicts 18/18, p50 12 s, p95 75 s. Estimated TipSplit turn ~4.5 min.

## 3. Single multimodal kimi-k3 (patch)
GREENROOM_VERIFIER_MODEL=moonshotai/kimi-k3
GREENROOM_VERIFIER_SEES_SCREEN=1
(GREENROOM_VISION_MODEL unused)
Run the daemon with -verifier-budget 20m: at p50 31 s per call a 12-16 call turn is ~7 min, near the 10 min default.
Evidence: 44/45 best, 11/11 verdicts, including the two points where the recorded describer failed or omitted values; 0 failed calls; p50 31 s, p95 67 s. Removes the describer hop and its failure mode. Risk: one model for everything; kimi-k3 has a per-model 429 limit on this shared key (4 retried calls in 45).

## Rejected (evidence in report.md)
- omni with enable_thinking false: 5 s p50 but 9/40 failed and section 4 fabricates controls (copies the prompt example "25% segment at (0.60, 0.47)", invents a "3% segment"); selected segment right 4/12.
- nemotron-3-super: fast but schema-invalid args ("mods": "[cmd]") and prose; ultra with thinking off: fast but same availability issue; lightning: degenerate output; gpt-oss-20b, laguna, muse-glimmer, llama-3.2-11b-vision, mistral-nemotron, gemma-4 (148 s): worse or incompatible.
