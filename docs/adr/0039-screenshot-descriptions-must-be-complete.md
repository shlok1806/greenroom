# 0039. Screenshot descriptions must be complete

Date: 2026-09-30
Status: accepted. Extends ADR 0030 and ADR 0032.

## Context

Issue #258 reports screenshot descriptions cut off mid-list at the 700-token limit.
`Describe` discarded the endpoint's finish reason, so the verifier received an incomplete
description as an observation. Missing text could then be mistaken for missing UI.

## Decision

1. Give descriptions a 2048-token output budget, recorded through `DescribeOptions`.
   Keep muse-glimmer's thinking disabled.
2. Keep exact transcription of every visible window string. Ask for compact control rows
   and no commentary, prioritizing window text over control descriptions.
3. Refuse `finish_reason: length`, including an empty answer, with a recognizable
   "description cut off" error. Never return the fragment as an observation.
4. Retry once on the same saved image, asking for a more compact answer without dropping
   window text. This shares the existing two-attempt limit for unreadable descriptions.
   Other endpoint errors are not retried by the verifier's describe loop.
5. If the retry fails, the screenshot remains available and its transcript step states
   why it could not be described. The verifier can use another observation instead.

## Consequences

Descriptions have more room but can cost more output tokens. Recovery costs at most one
extra description request; it does not capture a different screen or hide partial output
inside the reasoning context. No live-model latency or accuracy improvement is claimed
without a new benchmark.
