# 0042. Size the default verifier step budget to the checklist

Date: 2026-09-30
Status: accepted.

## Context

Issue #192 reports ten-check UI tasks exhausting the fixed 40-step turn before their last
checks. Inputs and the observations that verify them take several model rounds per check.
A second task repeats setup and takes another five to nine minutes. The evidence contract
from ADR 0024 must remain intact.

## Decision

An unspecified `Config.MaxSteps` starts at 40 model rounds. For an open task with accepted
declared checks, its cap becomes the greater of 40 and `12 + 8 * check count`: twelve rounds
for setup, declaration and reporting, and eight per check for actions and observations.
The existing maximum of twelve checks bounds this at 108 rounds. These are total rounds,
not additional rounds for each declaration. A turn's cap may grow but never reset or shrink.
Invalid declarations do not extend it. A resumed turn uses the current task's declaration;
older tasks and declarations without an open task do not extend the default.

A positive configured `MaxSteps`, including an explicit 40, remains an exact fixed cap.
The wall-clock budget remains ten minutes by default, with the existing single closing call
bounded separately at ninety seconds. There is no automatic continuation or new turn.
Evidence validation, repeated-error and dead-control guards remain unchanged.

The existing loop counts model rounds, including cut-off retries and messages with multiple
tool calls. This decision keeps that accounting; changing tool-call batching is separate work.

## Consequences

Large checklists get room for their last observations while small tasks keep their old cap.
The constants are a bounded allowance, not a promise of completion; a slow or looping task
still reaches its time or step cap. Live model evaluation is needed to tune this allowance.
Explicit caller limits remain predictable, and one shared Verifier cannot leak a run's
expanded cap into another run.
