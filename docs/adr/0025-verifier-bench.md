# 0025. A verifier bench with seeded faults measures the false pass rate

Date: 2026-09-25
Status: accepted.

## Context

The verifier's accuracy rests on 19 field verdicts, 16 of them on one app with one planted
bug, and on the realistic suite of ADR 0020 (about 10 verdicts). By the rule of three, 10
correct verdicts only bound the false pass rate at about 30%. A badge needs a bound of 5% or
less for internal use and 1 to 2% for customers (`docs/13-verifier-quality.md`, section 7.3
and the metric targets). The realistic suite's apps and harness were not kept.

Every change to the verifier's prompts, tools, contract (ADR 0024) or model has so far been
judged by a handful of live runs. Without a fixed set of cases with known answers there is no
way to tell an improvement from noise, or to catch a regression.

## Decision

1. **Cases with known verdicts, kept in the repo** under `bench/`:
   - `bench/apps/<app>/`: small SwiftPM macOS apps with an accessibility tree, built by a
     `build.sh` that the images can run (no Xcode, ADR 0019). TipSplit from the field runs
     is the first; about 5 to 7 apps in all, each with several controls, a computed value, a
     text field and persisted state.
   - `bench/cases/<id>.json`: one case each. Fields: `app`, `patch` (a unified diff applied
     to the app before building, or none), `task` (what a coder would write), `expected`
     (`pass`, `fail`, or `ask_or_inconclusive`), `must_check` (criteria a good checklist
     includes, for coverage), `kind`, `family`, `split` (`dev` or `holdout`) and `infra`
     (an optional scripted disturbance).
   - Kinds:
     - `correct`: the change works. Expected `pass`.
     - `mutant`: one seeded fault. Expected `fail`. Families: wrong computation, a control
       with no action, a wrong label or format, state not persisted, works only for the
       example input, a regression next to the change, a crash on an edge input, a visual
       fault the tree still reports, a result that appears late.
     - `lying`: a correct or broken build with a task text that claims success in detail.
       Expected as for its build.
     - `infra`: a dialog over the app, the app not running, a person taking the screen mid
       check, a machine still booting. Expected `ask_or_inconclusive`, never a verdict about
       the app.
     - `ambiguous`: a task that cannot be checked as written. Expected `ask_or_inconclusive`.
   - Labels come from construction, not from judging, so there is no human-agreement
     ceiling on the verdict.

2. **A live runner in the daemon binary**: `greenroom bench run`. It drives
   `machine.Manager` and the verifier actor in process, as the daemon does, with no MCP
   client in between. For each case and trial: create a machine, sync the app, apply the
   patch, build and launch it with `machine_exec`, post the task as the coder, play the
   scripted `infra` disturbance, and wait for the verifier's turn to end. It records the
   ending, the verdict and its checks, steps, wall time and model tokens as one JSON line in
   a results file, keeps the run directory, and destroys the machine. It runs at most as
   many machines at once as the host allows (2), and 3 trials per case by default.

3. **A scorer**: `greenroom bench score <results>` writes a Markdown report with, per split
   and per kind:
   - false pass rate (pass on a broken build) with its Clopper-Pearson 95% upper bound, the
     headline number;
   - false fail rate, inconclusive rate (split by correct, broken and infra), and the rate
     of turns that end in a verdict or question rather than a limit or error;
   - pass^3: the same verdict in all trials of a case;
   - checklist coverage against `must_check`, and the rate of verdicts the ADR 0024 rules
     refused;
   - p50 and p95 wall time and tokens per verdict;
   - every wrong result listed with its run directory, for reading the transcript.

4. **The holdout split is never used to tune prompts.** Changes are developed against
   `dev`; `holdout` is run before a change is merged and its numbers are reported with the
   PR.

5. **Size grows with the claim.** The first version needs at least 30 broken cases (a 10%
   bound at zero false passes). Internal use of the badge needs about 60 (5%); a customer
   badge 150 to 300 (2% to 1%).

6. **Replay comes later.** A cheaper mode that feeds recorded tool results back to the model
   (judgement only, no VM) is useful for prompt iteration, but the live runner comes first,
   because lost input, stale screens and dialogs are exactly what replay cannot show.

## Consequences

- A bench run costs VM time (about 3 to 8 minutes per case and trial, 2 at once) and model
  calls, so it is run on demand and before verifier changes merge, not on every push. It
  needs the verifier's model credentials (`NVIDIA_API_KEY` today).
- Fixture apps must stay buildable on the lean image without Xcode.
- The bench measures the verifier, not the coder: tasks are fixed, and no coding agent
  runs.
- Results depend on the model provider's availability. Model errors are reported as their
  own ending, not folded into wrong verdicts.
