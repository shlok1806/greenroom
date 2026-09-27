# 0032. muse-glimmer is the default screenshot describer

Date: 2026-09-27
Status: accepted. Supersedes ADR 0020's default describer and ADR 0030's condition that the
switch waits for a bench comparison.

## Context

ADR 0030 made describers configurable per model and measured, on the 40 hand-labelled
Greenroom screenshots with the exact describe request, on 2026-09-26:

| describer | failed | recall | invented | p50 | p95 |
| --- | --- | --- | --- | --- | --- |
| nano-omni, thinking on (then in use via `.env`) | 10 + 4 `<unk>` | 0.72 | 20 | 62 s | 143 s |
| muse-glimmer-30b, thinking off | 1 (fine on retry) | 0.91 | 0 | 4.2 s | 11.9 s |
| kimi-k3, thinking on (then the built-in default) | 11 | 0.72 | 0 | 20 s | 102 s |

ADR 0030 kept kimi-k3 as the default until a bench run confirmed muse. The maintainer chose
to switch now: muse is better on every measured axis, invents nothing, and ADR 0027's
not-drawn marks and the ADR 0030 retry cover its known failure (an occasional empty answer).
The maintainer's `.env` already moved to muse the same day.

## Decision

1. The built-in default describer is `meta/muse-glimmer-30b`, requested with thinking off
   (`nim.describeFields`, ADR 0030).
2. The bench comparison ADR 0030 asked for still runs, now as a check rather than a gate:
   the simple tier on muse against the omni runs (no new wrong verdicts, 0 false passes,
   lower p95). A regression there reopens this decision.

## Consequences

- A fresh install describes screens with muse. An `.env` that pins another describer keeps
  it; the "verifier enabled" log line names the model in use (#154 makes it visible in runs).
- kimi-k3 and omni keep working by name.
