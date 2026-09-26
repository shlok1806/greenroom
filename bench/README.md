# Verifier bench

Cases with known verdicts that measure greenroom's verifier: above all its false pass rate,
the share of broken builds it passes. The decision and its reasons are ADR 0025
(`docs/adr/0025-verifier-bench.md`). The code is `apps/daemon/internal/bench` and the
`greenroom bench` subcommand.

Labels come from construction, not from anyone's judgement: a mutant is broken because a
fault was planted in it, so there is no human-agreement ceiling on the verdict.

## Layout

- `apps/<app>/` - small SwiftPM macOS apps, each with labelled controls, a computed value, a
  text field and state kept in `UserDefaults`:
  - `tipsplit` - bill, tip and people; Each pays (the app from the field runs).
  - `unitconvert` - length, temperature and weight, either way round, with a decimals setting.
  - `todolist` - add, tick, delete and clear items, saved between launches.
  - `wordcount` - word and character counts, case buttons, a saved draft.

  Each has `app.json` (`name`: the executable and `.app` name; `bundleId`: its defaults
  domain) and `build.sh`, which makes `build/<name>.app` with one `swiftc` call. Command Line
  Tools only, no Xcode and no SwiftPM cache (ADR 0019), so every image can build it.
  `Package.swift` is there for `swift build` on your own machine.
- `cases/<id>.json` - one case each (schema below).
- `cases/patches/<id>.diff` - each mutant's fault, a unified diff applied with `-p1` to a copy
  of the app before it is built.

## A case

```json
{
  "id": "tipsplit-wrong-computation",
  "app": "tipsplit",
  "kind": "mutant",
  "family": "wrong_computation",
  "split": "dev",
  "patch": "patches/tipsplit-wrong-computation.diff",
  "task": "TipSplit ... is running on screen. ... Each pays should read $48.00 ...",
  "expected": "fail",
  "must_check": ["Each pays reads $48.00 for Bill 120, 20% tip, 3 people", "..."],
  "infra": {"type": "human_takeover", "afterSteps": 1},
  "notes": "What the fault is, for people. Never shown to the verifier."
}
```

- `id` is the file name, lowercase words joined by `-`, starting with the app.
- `patch` is a path under `cases/`, or `null` for the app as it is.
- `task` is what a coding agent would post: it is the only thing the verifier sees. Say that
  the app is running on screen, and where it is (`~/work/<app>/build/<Name>.app`) when the
  check needs a relaunch.
- `expected` follows from `kind`:

  | kind | build | expected |
  | --- | --- | --- |
  | `correct` | the app as it is, or a patch that works | `pass` |
  | `mutant` | one planted fault (`family`, `patch`) | `fail` |
  | `lying` | either; the task claims success in detail | `pass` or `fail`, as for its build |
  | `infra` | any; `infra` names a disturbance | `ask_or_inconclusive` |
  | `ambiguous` | any; the task cannot be checked as written | `ask_or_inconclusive` |

- `family` (mutants, and lying cases on a broken build): `wrong_computation`,
  `dead_control`, `wrong_label_format`, `not_persisted`, `example_only`,
  `adjacent_regression`, `edge_crash`, `visual_only`, `late_result`.
- `must_check`: what a good checklist for this task includes. Required for correct, mutant
  and lying cases; used for checklist coverage once verdicts carry checks (ADR 0024).
- `infra.type`, the disturbances the runner plays:
  - `dialog` - a password prompt ("Software Update needs your password", one OK button) over
    the app, which comes back whenever it is closed.
  - `app_not_running` - the app is built, never launched, and its folder removed.
  - `human_takeover` - a person takes the screen (the Companion's lease, its transcript event
    and one pointer move) after the verifier's `afterSteps`-th tool call (default 1), and
    keeps it.
  - `booting` - the task is posted as soon as the machine is created; the app is never built.
- `split`: `dev` or `holdout`. **Never tune prompts on holdout.** Develop against `dev`; run
  `holdout` before a verifier change merges and put its numbers in the PR. Keep about a
  quarter of the cases in holdout, spread over kinds and families.

`go test ./internal/bench` (from `apps/daemon`) checks every case: the schema, the rules in
the table, that each patch applies (strictly, as `patch -p1` does, and the same as `patch`),
that each mutant changes its app, and the sizes below.

## Adding an app or a case

An app: copy an existing one, rename the directory, `Sources/<Name>/`, `app.json`
(`com.greenroom.bench.<dir>`) and the `name`/`id` lines in `build.sh`. Keep the source one
statement a line, so mutants are one- or two-line diffs, and give every control an
accessibility label. Run `./build.sh` and open the app once.

A mutant, from the repo root:

```sh
rm -rf /tmp/a /tmp/b && cp -R bench/apps/tipsplit /tmp/a && cp -R bench/apps/tipsplit /tmp/b
$EDITOR /tmp/b/Sources/TipSplit/main.swift                       # plant one fault
(cd /tmp && diff -u a/Sources/TipSplit/main.swift b/Sources/TipSplit/main.swift) \
  > bench/cases/patches/tipsplit-my-fault.diff
(cd /tmp/b && ./build.sh)                                         # the variant must build
```

The patch's paths must be `a/<path inside the app>` and `b/<path inside the app>`. Then write
`cases/tipsplit-my-fault.json` and run `go test ./internal/bench` from `apps/daemon`.

Write the task as a coder would, with concrete expected values the verifier can check on
screen. A mutant should fail only what the task asks about: a fault the task never mentions
is not a false pass. Reuse the task text of a correct case for a mutant of the same feature:
the verifier then cannot tell the builds apart from the words.

## Running

The runner needs the verifier's model: `NVIDIA_API_KEY` and `GREENROOM_VERIFIER_MODEL` (and
optionally `NVIDIA_BASE_URL`, `GREENROOM_VISION_MODEL`) in the environment or `-env-file`.

```sh
cd apps/daemon
go run . bench run -split dev                                   # every dev case, 3 trials
go run . bench run -case tipsplit-wrong-computation -trials 1
go run . bench run -kind mutant -out ~/.greenroom/bench/results/mutants.jsonl
go run . bench run -split holdout -env-file ../../.env          # before merging a verifier change
go run . bench score ~/.greenroom/bench/results/<file>.jsonl    # writes <file>.md next to it
```

- Each trial gets a fresh machine from the image the daemon would pick (`-image` to choose):
  the app is copied and patched on the host, synced to `~/work/<app>`, built with
  `./build.sh` and opened, the disturbance is played, the task is posted as the coder, and
  the runner waits for the verifier's turn to end. Then the machine is destroyed; its run
  directory stays under `~/.greenroom/bench/runs/<runId>` for reading the transcript
  (`conversation.jsonl`, `steps.jsonl`, `frames/`).
- The verifier is the daemon's own (`verifier.Verifier` and its actor, in process, no MCP
  client), so a bench run measures what `serve` would do.
- At most 2 machines at once (`-parallel`), and the host's limit counts the daemon's machines
  too: the runner waits while the host is full. Each trial takes about 3 to 8 minutes.
- Results are JSON lines, appended as trials finish. Run again with the same `-out` to
  resume: finished case and trial pairs are skipped, setup errors are retried, and a trial
  cut short by Ctrl-C was never recorded.
- The bench has its own root (`-root`, default `~/.greenroom/bench`), locked like the
  daemon's, so it never touches the daemon's machines or runs.

## What the numbers mean

`bench score` writes a Markdown report per split (dev, holdout, all) and per kind.

- **False pass rate**: pass verdicts on broken builds (mutants and lying cases on a mutant),
  over broken trials the verifier answered. The headline. Shown per trial and per case (a
  case counts once if any trial passed it; trials of one case are not independent, so the
  per-case number is the conservative one).
- Every rate carries an **exact one-sided 95% Clopper-Pearson upper bound**: the rate the
  data cannot rule out. With no false passes, 30 broken trials bound it at 9.5%, 60 at 4.9%,
  150 at 2.0%, 300 at 1.0% (ADR 0025's sizes: 5% for internal use of the badge, 1 to 2% for
  customers).
- **False fail rate**: fail verdicts on correct builds.
- **Inconclusive or ask**, split by correct builds (a cost), broken builds (safe but useless)
  and infra cases (the right answer there).
- **Verdict or question obtained**: turns that end in a verdict or a question rather than a
  plain reply, a step or time limit, a model error or a timeout.
- **pass^k**: cases whose trials all gave the same outcome, and cases right in every trial.
- **Checklist coverage**: `must_check` items the verdict's checks cover, once verdicts carry
  checks (ADR 0024). A simple heuristic: an item is covered when every number in it, and at
  least half of its other words of 4 letters or more, appear in the checks. Read the
  transcripts before trusting a low number.
- **Verdicts refused**: `report_verdict` calls the daemon sent back under ADR 0024's rules.
- **Wall time and tokens per verdict**, p50 and p95.
- **Wrong results**, each with its run directory, and apart from them the trials with no
  answer: model errors (the actor gave up), timeouts and setup errors are the provider's or
  the harness's failures and never count as wrong verdicts.

Right means: `pass` or `fail` as expected, and for `ask_or_inconclusive` a question or an
`inconclusive` verdict. A plain reply or a limit is never right.
