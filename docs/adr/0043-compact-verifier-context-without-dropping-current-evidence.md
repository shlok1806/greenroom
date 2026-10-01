# 0043. Compact verifier context without dropping current evidence

Date: 2026-09-30
Status: accepted. Addresses the context-cost lever of issue #158.

## Context

Every reasoning call resends the transcript and every earlier tool result. Long UI outlines
can recur unchanged, and a new task still receives full observations from earlier tasks
even though evidence review rejects them as stale. The live simple-tier targets in #158
are p95 below three minutes and 150,000 tokens, with no false passes.

## Decision

1. When projecting a new task, replace large earlier-task progress results with explicit
   omission markers. Keep their calls, step identifiers, tasks, declarations, verdicts,
   questions, answers, disputes and human-control events. Do not omit current-task output,
   or old output when there is no newer task.
2. Before each reasoning request, including the closing call, deduplicate large identical
   `machine_ui` and `machine_snapshot` results. Identity requires the same tool, exact call
   arguments and exact output after its first `step N` line. Keep the newest complete
   result. Earlier results retain their original step and name the full copy's step.
   Changed values, refs, visibility flags, geometry or effects prevent deduplication.
3. This is a request projection only. Never mutate the transcript, step ledger or live
   history used to build later requests. Evidence still cites each original step, with
   its original time and freshness. No evidence rule changes.
4. Measure serialized request-byte savings in deterministic fixtures. These measurements
   are not model token counts or proof that the live p95 targets are met.

## Consequences

Repeated outlines and superseded-task output cost less to resend. Every distinct current
observation, including transient UI states, remains in full. This avoids a lossy summary
of evidence or an extra summarizer model. Live model and VM benchmarks remain necessary
before closing #158 or claiming the latency, token and accuracy targets are achieved.
