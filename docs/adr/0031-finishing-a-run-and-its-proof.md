# 0031. A coding agent finishes a run, and the run gives back its proof

Date: 2026-09-27
Status: accepted.

## Context

A coding agent can create a machine, drive it, ask the verifier for a verdict, and accept or
dispute that verdict (`agent_send` kind `accept` or `dispute`, ADR 0006). It cannot say that
its work is finished. Nothing records how the work ended (shipped with a verified change,
shipped without one, or given up), which commit or PR it became, or when.

The self-improvement demo showed the cost. To put the verifier's evidence in its PR, the
coding agent pulled screenshots with `machine_pull`, uploaded them to a branch by hand, and
wrote the checklist into the PR body itself. The Companion could only show the run as
"Ended" once the machine was destroyed, the same as a run abandoned half way. The proof the
product exists to produce had no shape of its own.

## Decision

1. **`run_finish`**, an MCP tool for the coding agent:
   `{runId, outcome, summary, ref: {branch, commit, pr}, destroy}`.
   - `outcome` is `verified`, `unverified` or `abandoned`.
   - The daemon accepts `verified` only when the run's current verdict is a **pass** that was
     **accepted** (by the coder or a human). Otherwise it refuses `verified` with the reason,
     and the agent may finish as `unverified`. An agent cannot mark its own work verified.
   - `summary` is required: one or two sentences of what changed.
   - `ref` is optional; each field is free text (a PR URL, a commit sha, a branch name).
   - `destroy` (default true) destroys the machine after recording, as `machine_destroy`.
   - Finishing is recorded in the transcript as a System Event with `finish` data, and in the
     run manifest (`finish: {outcome, summary, ref, at}`). A run finishes once; a second call
     is refused.
   - An open verifier turn is not interrupted; finishing while one runs is refused, so the
     agent waits for its reply first.
2. **`run_report`**, an MCP tool (read only) and an HTTP route
   (`GET /api/runs/{id}/report?format=md|json`): the run's proof.
   - The task, the outcome and summary, the ref, the models that verified it (brain and
     describer), times, and the verdict with every check: kinds, status, what was observed,
     and its evidence steps.
   - Each evidence screenshot as a link the reader can open: through `greenroom connect` or
     the public host, the artifact route; locally, the file path. Screenshots can also be
     embedded as data when `embed=true` (for a PR comment with no reachable host).
   - Unchecked checks are listed as not checked. The report states its scope: "every listed
     check was observed on this build", not "the PR works" (ADR 0024 point 6).
   - Markdown is shaped to paste into a PR body or comment.
   - `run_finish` returns the same report, so finishing and getting the proof is one call.
3. **The Companion** shows a finished run as **Done**, with the outcome (`Verified`,
   `Unverified`, `Abandoned`), the summary and the ref (a PR link opens in the browser), in
   the run header and as a word in the run's row. "Verified" uses the pass colour; the others
   are neutral. An unfinished run keeps today's states.
4. `agent_send` descriptions and the server instructions say how a job ends: get a verdict,
   accept it, then `run_finish`.

## Consequences

- The proof has one shape, made by the daemon, so a future GitHub App or CI step posts the
  same report the coding agent gets.
- "Verified" means something checkable: an accepted pass on this run. A coding agent that
  skips the verifier can still finish, but only as `unverified`, and says so.
- The manifest gains a field; old runs without it read as not finished.
- Not decided here: posting the report to GitHub by the daemon itself, and signing the
  report. Both belong to the GitHub App work.
