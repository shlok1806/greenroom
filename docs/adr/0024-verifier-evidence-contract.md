# 0024. A verdict is a checklist, and the daemon checks the evidence behind a pass

Date: 2026-09-25
Status: accepted. Changes the `report_verdict` contract from ADR 0006.

## Context

A "verified by Greenroom" badge is only worth what its false pass rate is. The research in
`docs/13-verifier-quality.md` and the field data in `docs/14-verifier-field-data.md` agree on
three things:

- **False passes are the normal judge error.** About two thirds of frontier judge errors on
  computer-use trajectories are false successes, and a judge that sees the agent's own claim
  gets less precise (61.5% to 44.3% in ZeroGUI). Prompt wording barely moves this; structure
  does: a checklist first, evidence per check, and checks the harness can verify
  (`docs/13`, sections 4.2 and 4.3).
- **Greenroom's verdicts were right, but 3 of 19 made claims their evidence did not show**
  (`docs/14`, case 7 and the evidence section). The worst is #116: clicks were lost, and the
  verdict said they had happened. The verifier never looked after acting.
- **The coder's task text mixes a request with claims** ("I fixed it; Each pays now shows
  $48.00"). Today both reach the model as one message, and nothing stops a verdict from
  resting on the claim.

Today `report_verdict` takes `verdict`, a free `summary` and a free `evidence` list of step
numbers or paths. Nothing checks that a cited step exists, that it is an observation, or that
it was taken after the actions it is meant to show.

## Decision

1. **Checks before actions.** When a turn works on an open task, the verifier first calls a
   new tool, `declare_checks`, with the acceptance checks it derived from the task:
   `checks: [{id, criterion}]`, 1 to 12 of them, each one observable. It is posted as a
   progress message carrying the checks, so the coder or a person can correct the plan
   early. Input tools (`machine_click`, `machine_type`, `machine_key`, `machine_scroll`,
   `machine_input`) are refused on an open task until checks are declared. Looking
   (`machine_ui`, `machine_screenshot`, `machine_exec`) is never refused. Declaring again
   replaces the list; the verdict must answer the latest one.

2. **A verdict answers every declared check.** `report_verdict` takes
   `checks: [{id, status, evidence, actions, observed}]`:
   - `status` is `pass`, `fail` or `unchecked`;
   - `evidence` is the step numbers of observations (`machine_ui`, `machine_screenshot`,
     `machine_exec`) that show the result;
   - `actions` is the step numbers of the inputs the check depends on (empty for a check
     that only looks);
   - `observed` is what the evidence showed, in one sentence.
   `summary` stays. The free `evidence` list is kept for artifact paths only.

3. **The daemon checks a verdict before posting it.** The rules:
   - Every declared check is answered, and no unknown id appears.
   - Every cited step exists in this run, was recorded by the verifier, and has the right
     kind (observations in `evidence`, inputs in `actions`).
   - **Freshness:** for each check, at least one evidence step comes after every step in its
     `actions`, and after the latest change of hands of the screen (the handover count from
     ADR/issue #124).
   - **Effect:** a check cannot pass on an action whose effect check (point 4) found no
     change, unless a later observation shows the expected state.
   - `pass` needs every check `pass`, each meeting the rules above.
   - `fail` needs at least one check `fail` with evidence.
   - `inconclusive` is always allowed, and must name the `unchecked` checks.
   A verdict that breaks a rule is not posted. The model gets an error naming each broken
   rule and check, which counts toward the repeat guard (#125). At the turn's limit (#127)
   the closing call's verdict gets the same checks, and a pass that fails them is posted as
   `inconclusive` with the reasons, never as a pass.

4. **Every input reports its effect.** After each verifier input call, the tool reads the
   UI of the frontmost app again, the same read `machine_ui` does, and compares it with the
   verifier's previous read. The tool result ends with either the changed elements (role,
   label and old and new values, capped) or `effect: no change detected`. The step record
   stores the effect, so rule 3 can use it. When the app has no usable accessibility tree,
   the effect is `effect: unknown (no UI tree)`, and the model is told to take a screenshot.

5. **The coder's claim is not evidence.** The coder's task is projected to the model under
   a label saying that statements about what was done are unverified claims: a source of
   checks, never a reason for a verdict. The system prompt says a verdict rests only on
   observations the verifier made.

6. **The checklist is the badge's scope.** The verdict message carries its checks. Clients
   (the Companion now, a PR comment later) show each check with its status, what was
   observed and links to its evidence steps, and list the `unchecked` ones. A pass certifies
   "every listed check was observed on this build", not "the PR works".

7. **The manual brain is exempt.** Its verdicts are a person's judgement typed into the
   conversation (ADR 0005). It keeps the free evidence list.

## Consequences

- A pass costs more steps: a declared checklist, a UI read after every input, and a fresh
  observation per check. Reads take about 2 s (`docs/14`), so a typical 11-call verdict
  gains roughly 10 to 20 s. Field data says model time, not tools, dominates (93%).
- Some true passes become `inconclusive` until the model learns the contract. The verifier
  bench (ADR 0025) measures both the false pass rate and this cost.
- `session.Message` gains `checks` on verdicts and on the declaring progress message. Old
  transcripts without checks still load; a verdict without checks from before this change
  is shown as it was.
- `agent_wait` returns the checks with the verdict, so a coding agent can act on a failing
  check directly.
- Not decided here, left to the bench and a later ADR: an independent judge for every pass,
  a differential run against the base commit, and a different actor model
  (`docs/13`, P1 items 7, 8 and 12).
