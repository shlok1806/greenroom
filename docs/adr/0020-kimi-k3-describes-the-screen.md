# 0020. kimi-k3 describes the screen by default; the brain stays nemotron-3-ultra

Date: 2026-09-24
Status: accepted. Supersedes the screen-reading model in ADR 0005 and its rule that the
describer has no built-in default.

## Context

ADR 0005 chose two NIM models: `nvidia/nemotron-3-ultra-550b-a55b` reasons and calls tools,
and `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning` describes screenshots for it. Both
came from short probes. Two evaluations have since measured the models on greenroom's own
work.

**Offline screening** (`docs/image-experiment/verifier-models/report.md`) sent greenroom's
exact describe request to each candidate for up to 40 hand-labelled screenshots from real
runs. The first row is what the daemon ships: kimi-k3 with its default thinking on, since
`nim.Client.Describe` sends no chat-template settings. That setup was measured on only 14
shots, a smaller sample than the other rows:

| describer | failed | value recall | invented values | selected TipSplit segment | control centres inside the control | p50 | p95 with retries |
| --- | --- | --- | --- | --- | --- | --- | --- |
| kimi-k3, thinking on (shipped, 14 shots) | 0 of 14 | 1.00 | none | 6 of 6 | 55 of 56 | 50 s | 79 s |
| nano-omni, thinking on (the old default) | 8 of 40 | 0.74 | 54 in 8 images | 10 of 12 | 57 of 95 | 71 s | 301 s |
| kimi-k3, thinking off (needs a chat-template knob the daemon does not send yet) | 0 of 40 | 0.94 | none | 12 of 12 | 105 of 111 | 39 s | 69 s |

nano-omni's failures were 503 ResourceExhausted and 300 s timeouts. Its controls section
made elements up. kimi-k3 never named a value that was not on screen in either setup, and
with thinking off it found every alert (3 of 3).

**The realistic suite** ran Claude Code as the coding agent on 7 real tasks, 2 trials each,
per configuration. A trial passed only if the hidden oracle passed, the tests were not
weakened, the run shows red then green in the VM, and, where a verdict was required, the
verifier's last verdict was right. The kimi-k3 describer rows ran the same setup the daemon
ships (thinking on, no patch):

| configuration | pass rate | verdicts obtained / requested | wrong verdicts | failed model turns | median wall, UI tasks |
| --- | --- | --- | --- | --- | --- |
| ultra + nano-omni (the old default) | 10 of 14 (71%) | 6 of 10 | 0 | 28 | 401 s |
| ultra + kimi-k3 describer | 11 of 14 (79%) | 7 of 10 | 0 | 23 | 382 s |
| glm-5.3 + kimi-k3 describer | 11 of 14 (79%) | 6 of 10 | 1 (disputed) | 0 | 785 s |
| kimi-k3 sees the screen itself | 13 of 14 (93%) | 10 of 10 | 0 | 1 | 1412 s |

Every failure in the first two rows was a missing verdict: the ultra brain's turns failed
with 429 or 500 after retries. The glm-5.3 losses came from verdicts cut off at the old 1200
token step budget (issue #71, since fixed).

## Decision

- With `GREENROOM_VISION_MODEL` unset, the daemon uses `moonshotai/kimi-k3` as the describer
  (`defaultVisionModel` in `apps/daemon/env.go`). A set value always wins. `none` runs the
  verifier without seeing the screen, which used to be what unset meant.
- The brain stays `nvidia/nemotron-3-ultra-550b-a55b` in `.env.example`. It still has no
  built-in default: without `GREENROOM_VERIFIER_MODEL` the verifier refuses to start, as
  before.

A built-in default for the describer departs from ADR 0005's "a configured model name, not
a hardcoded one". The measured default is better than none: without a describer the
verifier is blind, and that is not this product. `GREENROOM_VISION_MODEL` remains the one way
to change it when a model disappears from the account.

## Consequences

- Machines that set nothing now get a describer, one extra model call per screenshot,
  about 50 s at p50.
- Installs that pinned `GREENROOM_VISION_MODEL=nvidia/nemotron-3-nano-omni-...` in `.env` or
  the launchd plist keep it until they change it. `.env.example` now shows kimi-k3.
- **Follow-up:** move to a single multimodal kimi-k3 verifier (brain and eyes in one model),
  which returned a verdict every time it was asked, with no model errors and the best pass
  rate. It needs the knobs change that lets the brain receive the screenshot itself
  (`GREENROOM_VERIFIER_SEES_SCREEN`, from `nimeval/verifier-model-knobs.patch`) and a longer
  `-verifier-budget` (the suite used 20 m): every kimi-k3 step with an image takes about
  1.5 min, so a UI task took 3.5 times as long as with the describer. Adopt it once that
  change lands and the latency is acceptable, with its own ADR.
