# 0030. A faster screenshot describer: muse-glimmer-30b with thinking off, once the bench agrees

Date: 2026-09-26
Status: accepted. Extends ADR 0020 (which describer, and how it is chosen).

## Context

The describer turns each verifier screenshot into text for the brain (ADR 0005). ADR 0020
made `moonshotai/kimi-k3` the built-in default, but a set `GREENROOM_VISION_MODEL` always
wins, and the maintainer's `.env` sets `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning` (thinking
on). Every daemon and bench run so far used omni, including the simple-tier runs with no false
pass (ADR 0028, ADR 0029). Omni is the real baseline; kimi-k3 is the reference.

The simple-tier bench (90 trials) says the describer is the biggest single cost the verifier
controls: 27% of all verifier time, about 60 s per screenshot. The targets are p95 under 3 min
and 150k tokens per verdict (ADR 0029 measured 4 min 55 s and 206k).

The offline screening (`docs/image-experiment/verifier-models/report.md`: 40 hand-labelled
greenroom screenshots, greenroom's exact describe request) found `meta/muse-glimmer-30b`
fast and accurate with thinking off, and useless with it on: it spent the whole 700-token
budget reasoning and returned no text, 6 of 6. Thinking off needs
`chat_template_kwargs: {"enable_thinking": false}` in the request, which `nim.Client` could
not send.

### Offline re-check, 2026-09-26

Same harness, same 40 screenshots and labels, same request (the prompt matches today's
`visionPrompt` byte for byte), at most 2 requests at a time per model. A bench run on the
same key was in progress, so omni and kimi-k3 shared their quota with it; muse did not.
Outputs are outside the repo, in the session's `describer-recheck/` job directory.

"Failed" counts every answer the daemon now refuses as unreadable (below): an error, `<unk>`,
empty, or fewer than 10 letters. Recall counts a failed answer as 0. Latency is per
successful call; "p95 with retries" includes the harness's retries of 429/503 and its 300 s
timeout.

| describer | failed | value recall | invented values | TipSplit values / selected segment | alerts found | control centres inside the control | p50 | p95 | p95 with retries |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| omni, thinking on (in use) | 10 of 40 (6 timeouts, 4 empty) + 4 `<unk>` | 0.72 | 20 in 3 images | 10 of 10 / 7 of 10 | 2 of 2 answered | 46 of 83 | 62 s | 143 s | 300 s |
| muse-glimmer-30b, thinking off | 1 of 40 (empty) | 0.91 | none | 12 of 12 / 12 of 12 | 2 of 2 answered | 84 of 109 | 4.2 s | 11.9 s | 11.9 s |
| kimi-k3, thinking on (ADR 0020 default) | 11 of 40 (empty) | 0.72 | none | 10 of 10 / 10 of 10 | 3 of 3 | 81 of 87 | 20 s | 102 s | 106 s |
| kimi-k3, thinking off | 10 of 40 (`!!!!...`) | 0.72 | none | 9 of 9 / 9 of 9 | 3 of 3 | 76 of 81 | 15 s | 33 s | 88 s |

The screening two days earlier, for comparison: omni 8 failed, recall 0.74, 54 invented values
in 8 images, p50 71 s, p95 159 s; muse 2 failed, recall 0.83, none invented, p50 20 s, p95
83 s; kimi-k3 thinking off 0 failed, recall 0.94, p50 39 s, p95 65 s. Muse held up and got
faster; kimi-k3 degraded today (a third of its answers empty or punctuation, and 429s), and
omni stayed slow and still names values that are not on screen.

Muse's one failure (the tart-guest-agent alert screen) came back with `finish_reason:
length` and empty content: even with thinking off it sometimes reasons in
`reasoning_content`. Asked again, that screen was described in full (recall 1.0, alert
quoted) both times. The two screens that failed in the earlier screening came back with text
both times as well. With the daemon's one retry, muse would have had no failure in 40.

## Decision

1. **The describer request carries per-model fields.** `nim.describeFields` is a table keyed
   by model id; `Describe` adds a model's fields to its request, and `Chat` never does. It has
   one row: `meta/muse-glimmer-30b` gets `chat_template_kwargs: {"enable_thinking": false}`.
   A model not in the table gets exactly the request it got before, so omni and kimi-k3 are
   unchanged. `GREENROOM_VISION_MODEL=meta/muse-glimmer-30b` is all it takes to use muse
   correctly.

   A table, not a `GREENROOM_VISION_THINKING=off` setting: muse with thinking on returns
   nothing, so there is no second value worth choosing, and a separate switch could be left
   on (or copied to another model whose template names the knob differently: kimi-k3's is
   `thinking`, not `enable_thinking`). The fact belongs to the model, next to the request
   that needs it, and is pinned by a request-body test.
2. **An unreadable description is retried once.** `Verifier.describe` already retried a
   `<unk>` answer once. It now retries any answer `readableDescription` refuses: `<unk>`,
   empty, or fewer than 10 letters. A second one is an error the brain is told about, never
   a description. Every describer above produced one of these today.
3. **The default moves to muse only if the bench agrees.** The coordinator runs the
   simple-tier bench with `GREENROOM_VISION_MODEL=meta/muse-glimmer-30b` against the omni runs.
   Muse becomes the configured describer (the `.env` line, and `defaultVisionModel` in a
   follow-up change) if that run has no wrong verdict that the omni runs did not have, 0 false
   passes, and a lower p95 time per verdict. Until then nothing changes: the built-in default
   stays kimi-k3 and the maintainer's `.env` stays omni. The maintainer changes `.env`; this
   change does not.

## Consequences

- A weaker reader is less risky than it was. ADR 0027 marks text the tree reports but the
  screen does not draw, and a visual check needs a screenshot, so a visual claim no longer
  rests on the describer alone. What must not change is invented values: 0 in both screenings
  for muse, where omni had 20 today and 54 before. A describer that names a value the screen
  does not show can make a false pass; the bench run must keep false passes at 0.
- Muse reads less than kimi-k3 did on a good day (recall 0.83 and 0.91 against 0.94) and
  places controls less exactly (about 77% of centres inside the control against 93% or more).
  The brain aims from the UI tree (ADR 0012), not from the describer's coordinates, so this
  matters less than recall.
- At about 5 s per screenshot instead of about 60 s, a verdict with 3 to 5 screenshots saves
  several minutes of wall time, which is most of the gap between today's p95 and the 3 min
  target. Tokens per verdict barely move: the describer's own tokens are not counted, only
  the text it hands the brain, and muse's descriptions are a little longer than omni's
  (median 916 characters against 662): about 70 tokens more per screenshot, resent with the
  context on every later step of the turn.
- One NIM model's behaviour is now written into the client. If NVIDIA changes muse's chat
  template, the request-body test still passes and the offline harness is what notices; rerun
  it before trusting a new default.
- Latency on the hosted endpoint varies by day and by load (muse's p95 was 83 s two days ago,
  12 s today). The bench, not this table, decides.
