# NIM model screening for the greenroom verifier (offline, 2026-09-23)

Scope: steps 1 and 2 only (no VMs, no live runs). Data: 40 screenshots hand-labelled from ~/.greenroom/runs (data/gt_manual.py), and 45 brain decision points replayed from 5 TipSplit runs (brain_cases.py), each with greenroom's exact request body (describe: max_tokens 700, temp 0.2; brain: max_tokens 1200, temp 0.2, the 11 real tools, project() history). Top brains ran twice (n=90).

## Candidates (GET /v1/models lists 82; most 404 for this key)
- Image input works: nemotron-3-nano-omni (current), moonshotai/kimi-k3, meta/muse-glimmer-30b, meta/llama-3.2-11b-vision, google/gemma-4-31b-it (148 s p50).
- Tools work: nemotron-3-ultra (current), nemotron-3-super-120b, nemotron-3.5-lightning-30b, z-ai/glm-5.3, kimi-k3, gpt-oss-20b, poolside/laguna-xs-2.1, muse-glimmer-30b, llama-3.2-11b-vision, omni.
- Dropped: 404 for this key (nemotron-nano-3, llama-3.1-nemotron-ultra/70b, cosmos-reason2, vila, neva, phi-3-vision, gemma-3, mistral-large*, kimi-k2.6, jamba, yi, fuyu); >120 s timeouts on a trivial call twice (deepseek-v4.1-flash, glm-5.3-flash, llama-3.2-90b-vision); mistral-nemotron rejects greenroom's history (tool call ids must be 9 chars; no user after tool); muse-glimmer with thinking on returns empty content (reasoning uses all 700 tokens, 6/6); llama-3.2-11b takes at most 1 image and invents values.

## Tables
| describer | n | failed | value recall | frontmost | dialog acc | alerts found | invented values | TipSplit Each pays / selected segment | control hits | median ctrl err | p50 s | p95 s | p95 incl. retries s | 503s | timeouts |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| kimik3-think | 14 | 0 | 1.00 | 0.79 | 1 | 1/1 | 0 in 0 imgs | 6/6 / 6/6 | 55/56 | 0.00 | 50 | 79 | 79 | 0 | 0 |
| kimik3-nothink | 40 | 0 | 0.94 | 0.93 | 1 | 3/3 | 0 in 0 imgs | 12/12 / 12/12 | 105/111 | 0.00 | 39 | 65 | 69 | 0 | 0 |
| gemma4 | 12 | 0 | 0.90 | 0.83 | 1 | 1/1 | 0 in 0 imgs | 3/3 / 3/3 | 22/26 | 0.01 | 148 | 212 | 212 | 0 | 0 |
| muse30-nothink | 40 | 2 | 0.83 | 0.82 | 1 | 3/3 | 0 in 0 imgs | 12/12 / 12/12 | 85/108 | 0.01 | 20 | 83 | 79 | 0 | 0 |
| llama11v | 40 | 0 | 0.77 | 0.72 | 0.84 | 0/3 | 430 in 22 imgs | 12/12 / 6/12 | 7/111 | 0.18 | 19 | 62 | 62 | 0 | 0 |
| omni-nothink | 40 | 9 | 0.74 | 0.81 | 1 | 2/2 | 159 in 17 imgs | 12/12 / 4/12 | 30/101 | 0.01 | 5.10 | 38 | 301 | 23 | 9 |
| omni-think | 40 | 8 | 0.74 | 0.91 | 0.92 | 0/0 | 54 in 8 imgs | 12/12 / 10/12 | 57/95 | 0.01 | 71 | 159 | 301 | 33 | 6 |
| muse30 | 6 | 6 | 0.00 | 0 | 0 | 0/0 | 0 in 0 imgs | 0/0 / 0/0 | 0/0 | - | - | - | 278 | 0 | 1 |

| brain | n | best | ok | wrong | failed call | score | verdicts right | clicks right | wrong verdicts | prose | p50 s | p95 s | p95 incl. retries s | calls retried | 5xx | 429 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| kimik3 | 45 | 44 | 1 | 0 | 0 | 0.99 | 9/9 | 11/11 | 0 | 0 | 45 | 68 | 68 | 2 | 0 | 2 |
| mm-kimik3 | 45 | 44 | 1 | 0 | 0 | 0.99 | 11/11 | 11/11 | 0 | 0 | 31 | 67 | 67 | 4 | 0 | 4 |
| glm53 | 90 | 85 | 5 | 0 | 0 | 0.97 | 18/18 | 19/22 | 0 | 0 | 12 | 75 | 75 | 0 | 0 | 0 |
| kimik3-nothink | 90 | 80 | 10 | 0 | 0 | 0.94 | 18/18 | 16/22 | 0 | 0 | 40 | 76 | 79 | 19 | 0 | 24 |
| mm-kimik3-nothink | 90 | 80 | 9 | 0 | 1 | 0.94 | 21/22 | 18/22 | 0 | 0 | 42 | 76 | 81 | 13 | 0 | 21 |
| ultra-nothink | 45 | 40 | 3 | 1 | 1 | 0.92 | 9/9 | 8/11 | 0 | 0 | 3.59 | 24 | 123 | 29 | 20 | 84 |
| super | 90 | 77 | 7 | 6 | 0 | 0.89 | 17/18 | 18/22 | 0 | 2 | 3.36 | 16 | 35 | 30 | 47 | 0 |
| mm-muse30-nothink | 45 | 38 | 0 | 7 | 0 | 0.84 | 9/11 | 6/11 | 0 | 0 | 2.92 | 21 | 21 | 0 | 0 | 0 |
| mm-omni | 45 | 35 | 6 | 4 | 0 | 0.84 | 9/11 | 10/11 | 0 | 1 | 7.21 | 62 | 108 | 24 | 42 | 0 |
| laguna | 45 | 37 | 1 | 6 | 1 | 0.83 | 7/9 | 9/11 | 0 | 0 | 1.52 | 108 | 123 | 6 | 11 | 0 |
| ultra | 90 | 65 | 1 | 2 | 22 | 0.73 | 9/18 | 15/22 | 0 | 0 | 5.61 | 44 | 240 | 52 | 54 | 138 |
| lightning | 45 | 32 | 1 | 12 | 0 | 0.72 | 5/9 | 10/11 | 1 | 8 | 8.11 | 94 | 94 | 0 | 0 | 0 |
| mm-omni-nothink | 45 | 28 | 4 | 12 | 1 | 0.67 | 7/11 | 8/11 | 5 | 0 | 1.15 | 7.10 | 23 | 8 | 16 | 0 |
| gptoss20 | 45 | 21 | 4 | 19 | 1 | 0.51 | 4/9 | 7/11 | 2 | 11 | 26 | 110 | 113 | 0 | 0 | 0 |
| mm-llama11v | 45 | 14 | 2 | 15 | 14 | 0.33 | 0/11 | 5/11 | 5 | 1 | 2.41 | 17 | 13 | 0 | 0 | 0 |
| mnemotron | 45 | 0 | 0 | 5 | 40 | 0.00 | 0/9 | 0/11 | 0 | 5 | 0.48 | 100 | 4.99 | 2 | 3 | 0 |

Columns: "failed" = no usable answer after 5 attempts (5/15/30/60 s backoff), counted as zero recall. "invented values" = $ or % strings not in the image (mostly fabricated controls). "control hits" = stated centre inside the labelled control (TipSplit centres from the AX tree). Brain "best" = the right next call, "ok" = safe but not ideal (e.g. an extra screenshot), "wrong" = wrong element, wrong verdict, invalid args or prose.

## Findings
- Current describer (omni, thinking on): p50 71 s, p95 159 s, 8/40 failed (33 x 503 ResourceExhausted, 6 timeouts at 300 s). Values right when it answers (TipSplit Each pays 12/12) but the controls section invents elements (54 invented strings).
- omni with enable_thinking false: p50 5 s but still 9/40 failed, and section 4 is fabricated: it copies the prompt's example "25% segment at (0.60, 0.47)" and invents a "3% segment" (159 invented strings, selected segment right 4/12). Do not ship this alone.
- kimi-k3 describer: 0 failures, 0 invented values, alerts 3/3, controls 105/111 with median error 0.00, recall 0.94 (thinking off) / 1.00 (thinking on, 14 shots). p50 39-50 s: queue time, not faster than omni's p50 but no 503s and a far lower tail.
- Current brain (ultra): when it answers it is right (0.95 best), but 24/90 calls failed after all retries (429 x138, 500 x56, timeouts, a few empty 404s); p95 incl. retries 240 s. It answered a trivial probe in 1.9 s at 06:40, so this is load, largely shared-key contention.
- glm-5.3: 85/90 best, 0 wrong, 0 failed, p50 12 s. kimi-k3 (thinking on) 44/45 best, p50 45 s. super: fast (3.4 s) but sends mods as the string "[cmd]" (schema-invalid) and writes prose; 6 wrong. lightning emits degenerate "ellsellsells" text. gpt-oss-20b 0.51.
- Single multimodal kimi-k3 (image in the brain's context): 44/45 best, 11/11 verdicts, including the two points where the describer had failed or omitted values (the image shows $48.00 so it can decide). p50 31 s per call. muse-glimmer multimodal is fast (2.9 s) but presses return instead of clicking (0.84). omni multimodal 0.84 with many 5xx.
- Turn cost estimate for a TipSplit turn (12-16 model calls, 1-2 screenshots) at p50: default ~3.7 min plus retry tails; ultra+kimi describer ~3 min; glm+kimi ~4.5 min; single kimi ~7 min, close to the 10 min -verifier-budget.

## Rate-limit caveat
Before about 02:55 I ran ~20 concurrent requests; that caused 429s on ultra and kimi-k3 (per-model limits on the shared key) and may have disturbed the other evaluations. After the coordinator's note all work ran at <=4 total, <=2 on ultra and on omni. Affected rows: ultra and ultra-nothink pass 1 (failed rows were rerun), kimi-k3 rows with 429 retries (they succeeded, latencies include backoff), and the omni describer 503s (server capacity "Worker local total request limit", which also happens with no load of mine). Ultra pass 2 (04:06-05:11) ran under the cap and still failed 21/45, so its availability problem is real under the other evaluations' load. A final rerun of remaining failed rows was still running at handback (logs/queue4.log); it does not change the ranking.
