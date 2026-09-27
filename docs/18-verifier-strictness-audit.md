# 18 - Verifier strictness audit: when the evidence rules help and when they only cost

Tracking issue: #166. Related: #153 (open PR #168, ADR 0031).

The verifier reviews every verdict against the evidence rules of ADR 0024 (checklist,
freshness, effect), ADR 0027 (check kinds, text that is not drawn), ADR 0028 (a crash is
evidence) and ADR 0029 (an input that changes nothing is evidence) before it posts it. A refused
verdict goes back to the model as a tool error that names each broken rule. At a turn's limit,
a verdict that fails review is posted as inconclusive. This note measures, from every refusal
recorded so far, which rules prevent wrong verdicts and which only cost steps, time or
correctness, and proposes changes. The rule of thumb stays asymmetric: nothing that guards
against a false pass is loosened.

## Data and method

- Bench (ADR 0025), simple tier, dev split, model `nvidia/nemotron-3-ultra-550b-a55b`, image
  `greenroom-lean-a`, 3 trials per case:
  - `simple-dev-0027-r1`: 90 trials, the ADR 0027 branch before #143.
  - `simple-dev-main-r2`: 90 trials, main after #143.
  - `simple-dev-main-r3`: 84 trials, main at `3e75ae0` (after #152, ADR 0029). This is the
    code closest to today's main.
  - `baseline-pre0024-dev`: 41 trials before ADR 0024, mixed tiers. It has no refusals and
    serves only as the before picture for false passes.
- Live dogfooding runs under `~/.greenroom/runs`: `20260926-231011-600cf88cfbdbcba8` (the #153
  run, 4 tasks) and `20260926-234503-3057f9fab7ae9133` (the PR #168 check, 3 tasks).
  `20260927-000233-acbc2b008dfc6a8f` had no verdict yet. Older live runs predate ADR 0024 and
  have no refusals.
- A refusal is a verifier progress message that starts `report_verdict` and whose result starts
  `error: report_verdict refused`. The count matches the `verifier tool call refused` lines in
  each bench log (41, 31 and 35) and the bench's own `refusedVerdicts`.
- For each refusal: the rules it names, the next `report_verdict` call in the same task (the
  verdict, the check statuses and the cited steps that changed), the tool calls in between,
  the seconds to the next call and to the posted verdict, and the posted verdict against the
  case's expected verdict (bench) or the evident truth (live, judged from the transcript and the
  coder's accept or dispute).
- Classes:
  - (a) prevented a wrong verdict: the refused verdict was wrong and the posted one right.
  - (b1) caught a check-level false pass: same verdict, but a check answered pass was changed
    to fail after the refusal.
  - (b2) improved evidence: same verdict, new observations cited (usually a screenshot), a
    pass the model could not show withdrawn to unchecked, or a forgotten check answered.
  - (c) cost only: same verdict, same observations, citations moved around.
  - (d) harmful: a right verdict became inconclusive, wrong, or no verdict (a question).
  - (x) not attributable (one live case, explained below).
- Scripts (python3, read-only on the data): `extract.py`, `classify.py`, `simulate.py` and
  `show.py` in `/Users/shlokthakkar/.claude/jobs/1b9b4166/tmp/strictness/`. Manual judgements
  for the live runs are listed in `classify.py` (`OVERRIDE`).

Limits: one model, four small fixture apps, 30 cases. The three bench runs are different code,
so a rule's count falls as its fixes land. n is small; read the numbers as proportions of kinds
of cost, not as rates to two digits.

## Headline

| | r1 | r2 | r3 | live | all |
| --- | --- | --- | --- | --- | --- |
| Trials (tasks for live) | 90 | 90 | 84 | 5 | |
| Trials with at least one refusal | 36 | 29 | 22 | 4 | |
| Refused verdicts | 41 | 31 | 35 | 9 | 116 |
| (a) prevented a wrong verdict | 1 | 0 | 0 | 0 | 1 |
| (b1) caught a check-level false pass | 3 | 3 | 4 | 0 | 10 |
| (b2) improved evidence | 13 | 2 | 1 | 1 | 17 |
| (c) cost only | 20 | 24 | 30 | 4 | 78 |
| (d) harmful | 4 | 2 | 0 | 3 | 9 |
| (x) not attributable | 0 | 0 | 0 | 1 | 1 |
| Seconds from first refusal to posted verdict | 1425 of 14177 (10.1%) | 556 of 9747 (5.7%) | 450 of 7428 (6.1%) | | |

- **Two thirds of refusals are pure cost (78 of 116), and on today's code it is 30 of 35
  (86%).** They are cheap one at a time: 94 of the 107 bench refusals were fixed with no new
  step, median about 10 s. They add up to about 6% of bench wall time.
- **Only one refusal changed a verdict from wrong to right** (a timing pass on
  `unitconvert-late-result`). Ten more caught a check-level false pass inside a fail verdict
  (7 `rendered`, 3 `timing`).
- **The rules' big win is not in the refusals.** Before ADR 0024 the bench had 4 false passes
  in 26 broken-build trials (`tipsplit-each-pays-invisible`, `todolist-late-add`,
  `unitconvert-late-result`, `unitconvert-result-invisible`). Since then: 0 in 63, 63 and 59.
  The model now declares visual and timing checks and collects screenshots and in-time reads
  before it reports, so most of the guarding happens before any refusal.
- **9 refusals were harmful.** 6 are fixed already (the bare-integer `rendered` match, the
  setup-step visual checks of #143, the crash citation of #152). The 3 open ones are all live,
  and all come from the text heuristics, not from the evidence rules proper: a `rendered`
  match on the word "steps" (twice) and a timing kind taken from quoted text.
- **Two deterministic taxes on today's code:** every persistence trial is refused once for
  citing its relaunch (`machine_exec`) in `actions` (44 of 45 persistence trials over three
  runs), and in r3 the not-persisted trials then ping-pong between two opposite `quit`
  messages (21 refusals in 8 trials, 296 s, about 44% of those trials' median 84 s).
- **Limit downgrades: 1** (live, the #153 case). No bench trial was downgraded at a limit.
  Before ADR 0028, r1 lost 3 crash trials to self-chosen inconclusive and 1 to the time limit
  with no refusal at all: the model knew a gone app could not be cited. ADR 0028 fixed those
  (r2 and r3 crash trials are right).

What the model did after a refusal (bench, 107): fixed its citations and posted the same verdict
83 times; was refused again with the same verdict 20 times (chains of 2 to 5); changed the
verdict once (the (a) case); gave up to inconclusive twice (r2 crash, fixed by #152); ended the
turn with no verdict once (r1, the bare-integer loop). Live (9): 3 fixed, 4 refused again, 1
gave up to inconclusive, 1 ended with no verdict.

Other refusals: 22 `declare_checks first` (an input before any checks; always followed by
`declare_checks`, median 4 s; cheap and it structures the run), 1 `declare_checks refused`
(the checklist lock, live, case 10), no stale-look refusals.

## By rule

Refusals naming each rule family. A refusal can name several; the `fail:` line is the
consequence line "no check is fail with valid evidence", which appears whenever every fail
answer broke a rule.

| Rule | Refusals | r1 / r2 / r3 / live | a | b1 | b2 | c | d | x | Fixed with no new step | Median s to next call | Total s |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `fail:` consequence line | 45 | 13 / 14 / 16 / 2 | 0 | 0 | 4 | 38 | 3 | 0 | 40 | 10 | 955 |
| `kind`: `machine_exec` in actions (the relaunch) | 44 | 15 / 15 / 14 / 0 | 0 | 0 | 0 | 44 | 0 | 0 | 44 | 9 | 500 |
| `visual`: no screenshot after the actions | 26 | 16 / 6 / 1 / 3 | 0 | 0 | 16 | 6 | 4 | 0 | 10 | 56 | 1896 |
| `rendered`: rests on text not drawn | 17 | 5 / 2 / 3 / 7 | 0 | 7 | 1 | 3 | 5 | 1 | 14 | 12 | 699 |
| `timing`: no in-time observation | 13 | 3 / 4 / 4 / 2 | 0 | 3 | 0 | 9 | 1 | 0 | 12 | 18 | 244 |
| `quit`: a quit read cited for a pass | 13 | 0 / 2 / 11 / 0 | 0 | 0 | 0 | 13 | 0 | 0 | 13 | 12 | 191 |
| `quit`: cite the quit read (fail) | 8 | 0 / 0 / 8 / 0 | 0 | 0 | 0 | 8 | 0 | 0 | 8 | 7 | 56 |
| `timing`: late observation on a pass | 7 | 2 / 2 / 1 / 2 | 1 | 3 | 0 | 1 | 1 | 1 | 5 | 22 | 167 |
| `answered`: a check not answered | 4 | 1 / 1 / 2 / 0 | 0 | 0 | 1 | 3 | 0 | 0 | 4 | 17 | 110 |
| `arguments`: `checks` sent as a JSON string | 3 | 0 / 1 / 2 / 0 | 0 | 0 | 0 | 3 | 0 | 0 | 3 | 17 | 99 |
| `kind`: an input cited as evidence | 2 | 0 / 2 / 0 / 0 | 0 | 0 | 0 | 1 | 1 | 0 | 2 | 9 | 18 |
| `kind`: `machine_ui` or screenshot in actions | 1 | 0 / 0 / 0 / 1 | 0 | 0 | 0 | 1 | 0 | 0 | 1 | 23 | 23 |

The rules at check level (each refused answer once, all runs): which status the refused answer
had, inside which verdict.

| Rule | Pass answer in a pass verdict | Pass answer in a fail verdict | Fail answer |
| --- | --- | --- | --- |
| `machine_exec` in actions | 34 | 18 | 43 |
| `visual` | 11 | 13 | 20 |
| `timing` (either) | 1 | 10 | 20 |
| `rendered` | 6 | 11 | 0 |
| `quit` | 0 | 13 | 13 |
| input cited as evidence | 0 | 0 | 6 |

Reading it:

- **`visual`** costs the most time (median 56 s: it takes a screenshot) and falls fast as
  guidance lands (16, 6, 1). 16 of 26 improved the evidence. Its 4 harmful cases are fixed
  (#143, #152) or were an outage of the screenshot describer. Keep it.
- **`rendered`** is the rule with the most real catches (7 check-level false passes on the
  invisible-text mutants) and also the most harm (5): the bare-integer match (fixed) and a
  word match on "steps" (open, case 11). Keep it; fix the match and give it a way out.
- **`timing`** is cheap (12 of 13 fixed with no new step) and made the only verdict-level catch
  plus 3 check-level ones. Its one harmful case came from a timing kind the daemon read into
  quoted text (case 12), not from the rule.
- **`machine_exec` in actions** and both **`quit`** messages are pure cost: 65 refusals, none
  of them changed a verdict or a check's status.
- **39 of the 84 refused fails already had a fail answer that held**; they were refused over
  other answers. That is exactly #153 (PR #168).

## Case studies

Quotes are from `conversation.jsonl`; `...` marks a cut. Step numbers are the run's.

### 1. The one prevented false pass: timing (r1 `unitconvert-late-result`, trial 1, run `20260926-053624-5591e41e41946d84`)

The swap-units result updates about 1.5 s late. The model reported pass on every check, citing
for `swap-units` a read taken 21 s after the click:

> - check "swap-units" (timing): evidence step 40 started 21.0 s after action step 38 ended,
>   later than its 2 s; a later observation cannot pass a timing check. Cite only observations
>   that started in time, or answer fail if they do not show the state

It re-read its own in-time effect read and posted **fail**: "Swap units button click changes
unit label immediately but result does not update in the immediate UI read (step 39 shows only
unit label change; result updates later by step 40)." 55 s, no new step. Class (a).

### 2. A check-level false pass caught: timing (r3 `todolist-late-add`, trial 2)

A fail verdict with `milk-instant` answered pass on a read 27 s after Return:

> - check "milk-instant" (timing): evidence step 13 started 27.0 s after action step 11 ended,
>   later than its 2 s; ...
> - check "milk-instant" (timing): no evidence step started within 2 s after action step 11
>   ended (the UI read right after it is step 12); ...

The model re-cited step 12 and changed `milk-instant` to fail. 20 s, no new step. Class (b1).
The mutant adds items late, so the pass was wrong.

### 3. A check-level false pass caught: rendered (r3 `unitconvert-result-invisible`, trial 1)

The result text exists in the tree but is not drawn. The model failed `result-visible` but
passed `result-correct` on the tree's value:

> - check "result-correct" (rendered): machine_ui step 6 marks [15] StaticText "10 km = 6.21 mi"
>   not drawn (its frame on screen holds no text), and the check rests on it; the UI tree's
>   text is not what a person sees there. Take a machine_screenshot, and answer fail if it does
>   not show it

It changed `result-correct` to fail citing the screenshot. 10 s. Class (b1). This pattern
repeats in 7 trials. With PR #168 the same answer would post as unchecked with this reason,
not as fail: still honest, never a pass.

### 4. Harmful, fixed: the bare-integer match (r1 `unitconvert-result-invisible`, trial 1, run `20260926-053629-cb581be3f2d64849`)

A correct fail was refused three times over a check about the input field:

> - check "input-value" (rendered): machine_ui step 6 marks [15] StaticText "10 km = 6.21 mi"
>   not drawn ..., and the check rests on it

The model resubmitted the same verdict twice, then the #125 repeat rule ended the turn:

> The verdict system seems to be confusing my checks - it says "check 'input-value'" but
> references element 15 (the result), not element 8 (the input field). Should I declare checks
> differently or report inconclusive?

No verdict. Class (d), three refusals. Fixed by the bare-integer rule in `mentionTerms`
(recorded in `apps/daemon/CLAUDE.md`); PR #168 would also have posted the fail.

### 5. The relaunch tax (r3 `tipsplit-correct-persist`, trial 1, run `20260926-204629-66ed3102ade4fe7f`)

A right pass on a correct build. The model quit with Cmd-Q (step 14), relaunched with
`machine_exec open ~/work/tipsplit/build/TipSplit.app` (step 16) and read the UI (step 17). It
cited both 14 and 16 as actions:

> - check "tip-25-persists" (kind): action step 16 is a machine_exec; actions must be inputs
>   (machine_click, machine_type, machine_key, machine_scroll or machine_input)

It dropped 16 and the same pass posted 12 s later. Class (c). This happens in 44 of the 45
persistence trials across the three runs: the task says "open it again", and a relaunch is a
command, not an input.

### 6. The quit ping-pong (r3 `tipsplit-tip-not-persisted`, trial 2)

A right fail: after relaunch the tip reads 18%, not 25%. Five refusals, 112 s, for the same
verdict. After the relaunch tax (case 5), the model cited the post-relaunch read, which is the
real evidence:

```
{"id":"tip-25-after-relaunch","status":"fail","actions":[14],"evidence":[17],
 "observed":"After quit and relaunch, tip percentage shows 18% selected (element 8), not 25%"}
```

> - check "tip-25-after-relaunch" (quit): action step 14 made the app quit, and its effect read
>   step 15 shows it: cite step 15 as evidence
> - fail: no check is fail with valid evidence; a fail needs at least one

Cmd-Q is what the task asked for, not a crash, yet the ADR 0028 advice treats it as the
failure. The model then cited 15 on every relaunch check, pass ones included:

> - check "people-4-after-relaunch" (quit): evidence step 15 is the UI read that found the app
>   had quit after action step 14; a quit is never evidence for a pass. ...

Two further refusals were `checks` sent as a JSON string. The fail finally posted with
`tip-25-after-relaunch` resting on `[15, 17]`, that is, held by the crash shortcut on the
intentional quit. The record's reason is wrong, and the same shortcut would hold a fail on a
build that did persist (a false fail). Class (c) for all five.

### 7. Harmful, fixed: a crash fail given up (r2 `wordcount-clear-crash`, trial 3, run `20260926-200954-d21f40c1007b43aa`)

Clear crashes the app. The model cited the click as evidence:

> - check "after-clear-words" (kind): evidence step 8 is a machine_input; evidence must be an
>   observation (machine_ui, machine_screenshot or machine_exec)
> - check "after-clear-words" (visual): ... a state that is gone cannot be shown, so answer that
>   check unchecked

It followed the visual advice, marked the three crash checks unchecked, and got only:

> - fail: no check is fail with valid evidence; a fail needs at least one

It posted **inconclusive**: "Clicking Clear crashes the app (step 8 effect, step 10 shows
Finder), so the after-clear state ... cannot be verified." Class (d), twice. #152 fixed it (the
quit rule now names the read to cite and suppresses the visual advice).

### 8. Harmful, fixed: setup steps as visual checks (r1 `todolist-correct-clear-done`, trial 3, run `20260926-065915-852b428361e934b2`)

A correct build. The model declared "three items appear" and "Milk and Eggs are ticked" as
visual checks, and its screenshot description failed with a 503:

> - check "add-items" (visual): it is a visual check, and no machine_screenshot after action
>   step 23 is among its evidence; ...

The states were gone, so it posted **inconclusive** with those two unchecked. Class (d). Fixed by
#143 (a check is an outcome the task claims, not a setup step).

### 9. The limit downgrade (live `20260926-231011-600cf88cfbdbcba8`, seq 33)

Four fail answers held on the verifier's own reads. Two others did not:

> [greenroom] Reported as fail; posted as inconclusive because its evidence does not hold: check
> "run-b-row" (cited step): evidence step 5 was recorded by the coder, not by you; check
> "run-b-continue" (timing): it is a timing check (within 2 s) and cites no action; ...

This is #153; PR #168 posts it as fail with those two unchecked.

### 10. The checklist lock after a correcting dispute (same run, seq 34 to 49)

The coder disputed: its own task had wrong expectations for Run B, and "NEEDS YOU" did not
apply. The model tried to declare the corrected checks:

> error: declare_checks refused; the declared checks stand. You have acted on this task, so a
> new declaration must keep every declared check with its criterion and kinds, and may only add
> checks: "run-a-card" is missing; ... "run-b-row-after" changed its criterion ...

It then answered the withdrawn criteria ("Two checks fail per original criteria") and was
refused on other rules (a late timing read, and the "steps" match of case 11); the turn ran out
of time with no verdict. The coder had to send a new task ("New task, replacing the checks of
the earlier one"). Class (x): the fail it tried to post was wrong because the checklist was
stale, which no evidence rule can see. The lock itself works as designed (it stops a model from
shedding a failing check); the gap is that nobody told either side that a changed task must
come as a new task.

### 11. Harmful, open: the "steps" match (same run, seq 83 to 91)

A right pass. The row check's observed text quoted the row, which includes "39 steps". The
newest UI read marks an unrelated label "Steps", covered by the runs list:

> - check "run-b-row-after" (rendered): machine_ui step 106 marks [62] Unknown "Steps" not drawn
>   (its frame on screen holds no text), and the check rests on it; ... Take a
>   machine_screenshot, and answer fail if it does not show it

The model had already taken and cited screenshot 107, whose description reads
"02:19 · 39 steps / No verdict". The refusal repeated five times, then the repeat rule asked:

> ... failed 3 times with the same error ... Tell me what to do instead, or fix what blocks it,
> then send a message and I will continue from here.

The coder answered ("The refusal is about your wording, not what you saw ... Report it again
with the row's words without the step count") and the same pass posted. 5 refusals, about
4 min, and a human in the loop. The earlier refusals at seq 45 and 64 carried the same false match. Class
(c) for three, (d) for the one that ended the turn.

### 12. Harmful, open: a timing kind read from quoted text (live `20260926-234503-3057f9fab7ae9133`, seq 5 to 43)

The criterion quoted the expected UI text:

> "not-checked-reason-2": The second not-checked check starts "Not verified: timing: it is a
> timing check (within 2 s) and cites no action"

The daemon applied a kind to it: "Kinds applied by greenroom: "not-checked-reason-2" is timing:
its criterion says "within 2 s"". After a dispute, the model scrolled, saw the text in a
screenshot and answered pass:

> - check "not-checked-reason-2" (timing): evidence step 55 started 6.7 s after action step 52
>   ended, later than its 2 s; a later observation cannot pass a timing check. ...

It turned the check into a **fail**: "Since the check was classified as timing (due to
criterion mentioning "within 2 s"), the observation within 2s does not show the expected state
- fail." A right pass became a wrong fail. Class (d). The coder had to reword the task without
the quote. The same declaration upgraded `summary-text` to visual on the quoted word "shown".

### 13. A describer outage turns a visual rule into inconclusive (live `20260926-231011-600cf88cfbdbcba8`, seq 64 to 67)

A right pass (confirmed by the next task). The refusal was fair: `run-b-continue` is visual and
cited only UI reads. Two screenshots then came back undescribed ("could not be described ...
context deadline exceeded"), and the verdict posted **inconclusive**: "Three checks are
unchecked because machine_screenshot calls timed out". The same refusal also carried the false
"steps" match. Class (d), shared between the describer and case 11.

## What the proposals would have saved

`simulate.py` replays each task's first refused call under a proposal set: a pass posts when no
rule remains; a fail posts when one fail answer has no remaining rule (PR #168). Then every
later refusal in that task disappears too, and the first attempt's verdict is compared with
the expected verdict.

| Proposal set | Refusals removed, r3 | Refusals removed, all bench | Seconds saved, all bench | Posted verdicts that would be wrong |
| --- | --- | --- | --- | --- |
| P1 (PR #168) alone | 14 of 35 | 39 of 107 | 866 | 0 |
| P2 alone | 6 of 35 | 34 of 107 | 344 | 0 |
| P1 + P2 | 30 of 35 | 85 of 107 | 1378 | 0 |
| P1 + P2 + P3 | 33 of 35 | 88 of 107 | 1400 | 0 |

No replayed verdict differs from the expected one: none of the removed refusals was a class
(a). The (a) refusal and the pass-side `timing` and `rendered` refusals are untouched by every
proposal. Cost of P1: the 10 (b1) check-level corrections would post as unchecked with the
rule's reason instead of being turned into fails.

## Proposals, ranked

Each is marked **safe** when it cannot let a pass through that today's review refuses, or has
its false-pass risk argued.

### P1. Land PR #168 (#153): a grounded fail stands. Safe: cannot enable a false pass.

A fail with one fail answer that holds posts as fail; the other broken answers post as
unchecked with their reasons. It only changes fail verdicts; a pass is reviewed as before.
Removes 39 of 107 bench refusals, 3 harmful ones (case 4) and the live downgrade (case 9).
The 10 (b1) pass answers that `rendered` or `timing` showed wrong would post as unchecked with
the rule's reason rather than as fail. That is honest, and a fail verdict cannot be a false
pass, so no change to the PR is needed.

### P2. Accept a relaunch in `actions`, for ordering only. Safe: cannot enable a false pass.

Today a `machine_exec` step in `actions` is refused, and the model resubmits without it, which
the review then checks with fewer constraints. Instead, accept a `machine_exec` step in
`actions` and use it only for freshness: the newest evidence step must come after it, and a
visual screenshot must come after it. The effect, timing and ADR 0028/0029 rules keep using the
last *input* only, so an exec can never move a timing window later or stand in for an input's
effect. Every verdict this accepts is at least as constrained as the resubmission posted today.
Removes 34 refusals alone, 85 with P1; the persistence tax goes from 44 of 45 trials to 0.
If a rule change is not wanted, the cheaper form is a refusal message change: "step 16 is a
machine_exec (a relaunch is not an input): keep it out of actions; the quit input and the read
after the relaunch are enough."

### P3. An asked-for quit is not a crash. Safe: cannot enable a false pass (it only narrows what a fail may rest on). It also closes a false-fail hole.

- The effect line of an input that is a quit command (`cmd+q`, or a click on a menu item named
  Quit) says the app quit as asked and drops "To fail a check that depends on this input, cite
  step N as its evidence".
- `checkEvidence`: the ADR 0028 shortcut (a fail holds on the quit read alone) and the "cite
  the quit read" pointers apply only to a quit the input did not ask for. For an asked-for quit,
  the fail is judged by the ordinary rules on the reads after the relaunch.
- The pass-side message (a quit read cited for a pass) keeps refusing, reworded: "step 15 only
  shows the app quitting, as step 14 asked; drop it and cite the read after the relaunch".
- Prompt line 56 ("If the app crashes or quits while you do what the task describes ... that is
  a fail") gains: "A quit the task asks for, such as Cmd-Q to test that something is remembered,
  is not a crash: relaunch as told and cite the read after the relaunch."

Today the shortcut holds a persistence fail on the Cmd-Q quit read whatever the relaunched app
shows, so a model that misreads a persisted value as lost gets its fail accepted on the crash
path (case 6). Removes the 19 `quit` refusals of r3; with P1, P2 and P3 together, r3 keeps 2 of its 35 refusals.

### P4. Give the `rendered` rule the way out its own message names. False-pass risk: low, argued.

The refusal says "Take a machine_screenshot, and answer fail if it does not show it", but a
cited screenshot never satisfies it (case 11: five refusals with screenshot 107 cited). Change:
the rule is satisfied when the check cites a `machine_screenshot` taken after that UI read
whose recorded description contains the text the check names of the element (the same
`mentionTerms` match, run on the description). This needs the description recorded on the
screenshot's step (today it lives only in the progress text, which the review must not read).

Risk: the check would then trust the describer, which is the trust the visual rule already
places in it. Data: in all 20 described screenshots of the invisible-text mutants, the
description never contained the hidden value (`6.21`, `49.56`). Re-measure this when the
default describer changes (PR #167, muse-glimmer). Keep the refusal when no such screenshot is
cited.

Separately, the "steps" match (a one-word element text matched by a word in the observed
text) could be narrowed, for example by treating a one-word text with no digits as shown when
a drawn element of the same read contains that word. That weakens the guard for one-word
labels; P4's screenshot path already resolves case 11, so do not do this unless it recurs.

### P5. Do not read kinds from quoted text. False-pass risk: low, argued.

`applyKinds` skips words inside quotes (straight and curly, single and double) when it adds
visual or timing. Case 12 shows the cost: a quoted "(within 2 s)" made a text check timing and
turned a right pass into a wrong fail. Risk: a real time limit written only inside quotes would
no longer be added by the daemon; the model's own declared kinds still apply, and ADR 0027's
keywords outside quotes still add kinds. Also say, in the declaration result, when a kind came
from the criterion's words: "if those words are expected text, not a claim about timing,
declare again without them before your first input" (declaring again is allowed until then).
That half is safe.

### P6. Refusal message changes. Safe: wording only.

- `fail:` consequence line (45 refusals): name the fail answers that broke rules and say how
  one can hold, instead of "a fail needs at least one". In case 7 the bare line was all the
  model saw after it made its crash checks unchecked, and it gave up to inconclusive.
- Observation steps in `actions` (live, 18 lines on one refusal): "step 16 is a machine_ui, an
  observation: move it to evidence".
- Repeat refusals: on the second identical `rendered` or `timing` refusal of the same check,
  add "answer this check unchecked with the reason, and the verdict can post" (with P1 a fail
  then posts; a pass becomes inconclusive, which is honest).

### P7. Decode `checks` sent as a JSON string. Safe: parsing only, the same review follows.

3 refusals (and 3 `answered` lines that were its side effect) came from the model sending
`"checks": "[{...}]"`. `looseString` already accepts a number for a string; do the same for an
array sent as a string.

### P8. A dispute that changes the task comes as a new task. Safe as a guidance change; not safe as a rule change.

Case 10 cost a whole turn. Change the `declare_checks` refusal to add "if the coder changed the
task, ask for it as a new task", and tell the coder the same in the `dispute` tool description
and `agent_wait`'s notes. Do **not** unlock redeclaration on a dispute: a lying coder's dispute
would let the model drop the check it is failing and pass (bench `lying` cases exist for this).

### Keep as they are

- Pass-side `visual`, `rendered` and `timing`: they made the one verdict-level catch and all 10
  check-level ones, and the pre-0024 false passes were exactly these families.
- Freshness, cited step (only the verifier's own steps count) and the handover rule: no cost
  measured (the one live `cited step` problem was a coder step, correctly refused).
- `declare_checks first`: 22 refusals, median 4 s, always followed by a declaration.
- Fail-side `timing` and `visual`: they only guard against false fails, but they cost little
  (fail-side `timing` is fixed with no new step in 12 of 13) and they make the posted evidence
  better. With P1 they no longer block a grounded fail.

## Next

1. Merge PR #168 (P1).
2. P2 and P3 together in one verifier change with an ADR (they touch ADR 0024's action rule and
   ADR 0028's shortcut); rerun the simple dev bench and compare refusals per trial with r3.
3. P5, P6, P7 as small changes; P4 with the describer switch (PR #167) and a re-measure of the
   hidden-value check above.
4. Keep logging dogfooding cases on #166; rerun these scripts after each bench run.
