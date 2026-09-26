# 14. Verifier field data: how the verifier performs on real runs

Status: analysis, 2026-09-25. Read-only over `~/.greenroom/runs` (36 run directories, 2026-09-12 to
2026-09-25) and `~/.greenroom/daemon.log`. Nothing in `~/.greenroom` was changed.

## Summary

- 36 runs. 24 have a conversation, 21 have verifier activity. The other 12 predate the conversation
  (Sep 12-19), are coder-only (2 runs on Sep 25, 210 steps, no task ever sent to the verifier), or
  never got a message.
- One model setup throughout: reasoning `nvidia/nemotron-3-ultra-550b-a55b`, screenshots described by
  `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning` (every `verifier enabled` line in `daemon.log`).
  Runs themselves do not record the model or the daemon commit.
- Two eras. **Sep 19-21** (8 runs, 53 turns): the verifier is used as a chat assistant ("open Safari",
  "install tetris"), mostly through `machine_exec`; no pass/fail tasks except a "look around the
  project" task. **Sep 22-25** (12 runs, 30 turns): real UI verification tasks on small SwiftUI apps
  (TipSplit x9 runs, Groceries, HelloGreenroom x2).
- 83 verifier turns. Endings: reply 41 (49%), verdict 19 (23%: 10 pass, 9 fail), step or time limit
  11 (13%), model failure or gave up 7 (8%), question 2, other 3.
- **Verdict correctness is good; getting a verdict at all is the problem.** All 19 real verdicts have
  the right value (0 disputes, 0 later contradicted; checked by hand against the AX tree, screenshots
  and the known seeded bugs). But 3 of 19 contain a false or unsupported sub-claim, and in the UI era
  only 15 of 21 verification requests got a verdict on the first ask (13 without any mid-turn help);
  2 never got one. 5 of the 13 UI-era runs with tasks needed a coder push (re-send, nudge, "wrap up
  now", "use key space"), and 1 more ended with no verdict at all.
- **Cost is model time.** Median verdict turn: 11 tool calls, 137 s (max 463 s). 93% of verifier
  wall time is the model or the describer, 7% is tools. A screenshot description takes a median 17 s
  (p90 86 s) and fails 14% of the time (15 of 111). `machine_ui` answers in about 2 s and gave every
  UI-era verdict its numbers.
- **Grounding improved sharply with `machine_ui`.** The one UI task without it (Sep 22, coordinates
  from screenshot descriptions) spent 2 x 10 minutes clicking the wallpaper and ended with no verdict.
  After `machine_ui` (commit `5c741fc`), 69 clicks by element id, 0 misaimed.
- Still-open problems seen in the data: no verdict at the limits (#127), lost inputs and asserted
  effects (#116), no repeat guard (#125), takeover resume (#124), describer latency and failures (no
  issue), and claim-level evidence (no issue).

## Data and method

Scripts (rerun any time; read-only on `~/.greenroom`):

- `/Users/shlokthakkar/.claude/jobs/1b9b4166/tmp/fielddata/analyze.py` - all numbers in this doc.
  `python3 analyze.py --json metrics.json > report.txt`.
- `/Users/shlokthakkar/.claude/jobs/1b9b4166/tmp/fielddata/dump.py` - renders every conversation as a
  readable transcript (`transcripts/`) and a compact copy (`compact/`) with step durations and errors
  joined in from `steps.jsonl`. The case studies were read from these.
- Outputs of the last run sit next to them: `report.txt`, `metrics.json`, `transcripts/`, `compact/`.

Definitions (follow `apps/daemon/internal/session/message.go`):

- A **turn** opens on a turn-starting message (task, answer or dispute from coder or human; a note
  from a human) and closes on a verifier reply, question or verdict, on a final `verifier turn failed`
  (no retry follows), on `verifier gave up`, or when the machine is destroyed or stops. Messages that
  arrive during an open turn are counted as mid-turn input.
- A **limit** ending is a reply "ran out of time after 10m0s" or "used all 40 tool calls". Before
  Sep 22 the step cap was 12 and was posted as an `inconclusive` **verdict** ("The verifier used all 12
  steps of this turn without reaching a verdict"); those 5 are counted as limits, not verdicts.
- **Calls** are verifier `progress` messages (one per tool call, including calls refused before
  reaching the machine). **Model gap** is the time between two progress messages minus the step's own
  `durationMs`; for screenshots it includes the vision describe.
- Timestamps in run ids and logs are UTC; commit times below are converted to UTC. A fix is marked as
  landed after a run when its commit time is later than the run, which is an approximation (the
  daemon commit per run is not recorded; `daemon.log` shows 34 restarts, so deploys were frequent).

Caveats: small and narrow sample. 16 of the 19 verdicts are on one app (TipSplit) with one seeded bug
(Each pays = tip / people) and the expected values given in the task. The accuracy result does not
generalize; the process findings (limits, latency, lost input, loops) do.

## Run inventory

| run (UTC) | image | steps | tasks | verifier turns: endings | verdicts (value/response) | notes |
|---|---|---|---|---|---|---|
| 0912-091002, 0912-091112, 0912-140725, 0917-213553, 0917-214512, 0917-214548, 0917-214744, 0918-031415, 0918-032949, 0919-032348, 0919-033028, 0919-033800 | macos-tahoe-base (1 bad image) | 1-18 | 0 | none | none | machine-only runs before the conversation existed |
| 0919-164253 | macos-tahoe-base | 2 | 1 | 1: model failure | - | boot failed; verifier replied 5.5 h later |
| 0919-165348 | macos-tahoe-base | 19 | 1 | 3: model failure, step cap (12), reply | cap placeholder / accepted | ran `brew install go` unasked |
| 0919-170059 | macos-tahoe-base | 180 | 13 (12 human) | 17: 11 reply, 4 step cap, 2 model failure | 4 cap placeholders, 3 "accepted" | assistant-style use; 100 human screenshots |
| 0919-222432 | macos-tahoe-base | 174 | 8 (7 human) | 17: 11 reply, 2 time limit, 1 step limit (40), 1 question, 1 empty reply, 1 model failure | - | osascript groping, dead-VM loop (10 calls), refused abusive text |
| 0920-100017 | macos-tahoe-base | 14 | 1 | 10: reply | - | "Opus QA" probe notes, all answered |
| 0920-185907 | greenroom-base | 305 | 1 | 2: reply | - | dead-VM loop (8 calls) |
| 0921-005642 | greenroom-base | 398 | 1 | 2: reply, time limit | - | Notes via coordinates; 93-minute gap (host sleep) |
| 0921-050808 | greenroom-base | 167 | 1 | 1: reply | - | 12 takeovers after the turn |
| 0922-195814 | greenroom-base | 98 | 1 | 1: reply | - | project was one README |
| 0922-235130 | greenroom-base | 69 | 2 | 2: time limit, time limit | - | TipSplit by coordinates, no verdict (case 1) |
| 0923-005718 | greenroom-base-tcc | 17 | 3 | 4: fail, question, prose verdict, pass | fail/acc, pass/acc | screenshot-only (case 2) |
| 0923-010827 | greenroom-base-tcc | 16 | 2 | 2: fail, pass | fail/acc, pass/acc | screenshot-only |
| 0923-025814 | greenroom-base | 33 | 2 | 2: fail, pass | fail/acc, pass/acc | first `machine_ui` run (case 3) |
| 0923-032603 | greenroom-base | 5 | 0 | none | - | coder only |
| 0923-032728 | greenroom-base | 31 | 2 | 2: fail, pass | fail/acc, pass/acc | |
| 0923-044138 | greenroom-base | 17 | 1 | 1: fail | fail/acc (human) | lost click noticed and retried |
| 0923-115755 | greenroom-lean-a | 29 | 4 | 4: gave up, fail, gave up, pass | fail/acc, pass/acc | NIM 429/500 (case 4) |
| 0923-221420 | greenroom-lean-a | 40 | 2 | 2: fail, pass | fail/acc, pass/acc | typed "120" became "12" (case 5) |
| 0924-040039 | greenroom-lean-a | 26 | 5 (2 human) | 5: fail, reply, pass, reply, cut | fail/acc, pass/acc | re-check mid-turn (case 6) |
| 0925-171116 | greenroom-lean-a | 18 | 1 | 1: fail | fail/unanswered | Groceries, lost clicks (case 7) |
| 0925-182812 | greenroom-lean-a | 124 | 2 | 3: pass, reply, pass | pass/acc, pass/acc | empty `machine_type` loop (case 8) |
| 0925-183722 | greenroom-lean-a | 70 | 1 | 1: pass | pass/unanswered | Accent picker, takeover (case 9) |
| 0925-184835, 0925-184836 | greenroom-lean-a | 63, 147 | 0 | none | - | coder drove both VMs itself (case 11) |

## Turns: how they end

| ending | all turns (83) | Sep 19-21 (53) | Sep 22-25 (30) |
|---|---|---|---|
| reply | 41 | 38 | 3 |
| verdict pass / fail | 10 / 9 | 0 / 0 | 10 / 9 |
| limit: time (10 min) | 5 | 3 | 2 |
| limit: steps (12 old, 40 new) | 6 | 6 | 0 |
| model failure (no retry, old) | 5 | 5 | 0 |
| gave up after 4 model attempts | 2 | 0 | 2 |
| question | 2 | 1 | 1 |
| reply that was really a verdict in prose | 1 | 0 | 1 |
| empty reply ("had nothing to say") | 1 | 1 | 0 |
| cut by machine gone | 1 | 0 | 1 |

Turns that start from a **task** (43): verdict 19, reply 12, limit 7, model failure or gave up 4,
question 1. In the UI era 27 task turns gave 19 verdicts (70%).

20 turns received more input while they ran (human notes, coder notes, a second task). They ended:
reply 9, limit 6, pass 2, question 1, empty reply 1, model failure 1. A busy verifier that is sent more
messages tends not to finish with a verdict.

## Verdicts and the response to them

- 24 verdict messages: 19 real, 5 step-cap placeholders from Sep 19 (4 of them "accepted" by a human,
  which says the accept button was used as "ok, seen", not as agreement).
- Real verdicts: 10 pass, 9 fail. 17 accepted (16 by the coder, 1 by a human), 2 never answered, 0
  disputed. No verdict was later contradicted by a human message or later evidence.
- Verification requests in the UI era: 21 (not counting re-sends and nudges). 19 got a verdict in the
  end, 15 on the first ask, 13 without mid-turn help. The 6 that missed the first ask:
  - 0922-235130 two TipSplit tasks: two 10-minute time limits, no verdict ever.
  - 0923-005718 second task: a question (describer left out the values), then a verdict written as
    prose in a reply; the coder had to tell it twice to call `report_verdict`.
  - 0923-115755 both tasks: the model endpoint failed 4 times and the turn gave up; the coder re-sent.
  - 0924-040039 second task: a human re-check arrived mid-turn and the turn ended in a reply; the coder
    asked again.
- 0925-183722 got its verdict only after the coder wrote "Please wrap up now" at minute 6.5.

## Steps and time

| turn group | turns | calls median (max) | wall median (max) |
|---|---|---|---|
| verdict | 19 | 11 (34) | 137 s (463 s) |
| limit | 11 | 17 (40) | 600 s (6071 s, host asleep) |
| reply | 41 | 2 (18) | 33 s (302 s) |
| all | 83 | 7 (40), total 675 | 74 s (p90 387 s) |

Verdict turns by style (UI era):

- Screenshot only, no input (0923-005718, 0923-010827): 0-2 calls, 7-137 s.
- `machine_ui` + element clicks (TipSplit, Groceries, Reset): 10-19 calls, 50-387 s.
- Screenshot-heavy with clicks (0925-183722, 6 screenshots): 15 calls, 463 s.

Where the time goes (per call, model gap = time not spent inside the tool):

| tool | calls | median gap | p90 gap |
|---|---|---|---|
| machine_screenshot (includes vision describe) | 111 | 26.9 s | 90.4 s |
| machine_click | 91 | 4.3 s | 26.5 s |
| machine_exec | 347 | 3.2 s | 18.8 s |
| machine_key | 32 | 2.7 s | 9.5 s |
| machine_type | 26 | 2.5 s | 13.6 s |
| machine_ui | 68 | 2.2 s | 13.0 s |

From screenshot step start to its progress message (capture plus describe): median 17 s, p90 86 s.
Model and describer time: 16,349 s; tool time 1,170 s (93% / 7%; 90% / 10% without the one 5,621 s gap
when the host slept). One screenshot costs as much as ten `machine_ui` reads.

`machine_exec` hangs cost the most tool time: 2 calls ran into the 5-minute exec timeout
(`./TipSplit &` in 0922-235130, `Code --version` in 0919-222432) and 1 more (`open Visual Studio
Code.app`) timed out at 40 s.
Commit `8d439d5` (Sep 23 17:45 UTC, bound `machine_exec`, hand back long commands) addresses this.

## Tool mix, errors, loops

- Tool mix: `machine_exec` 347, `machine_screenshot` 111, `machine_click` 91, `machine_ui` 68,
  `machine_key` 32, `machine_type` 26. In the UI era `machine_exec` nearly disappears: 19 calls, 12 in a
  read-only "look around the project" task and 7 in the one turn that broke a "UI only" rule.
- Clicks: 69 by element id, 22 by x/y. All 22 x/y clicks predate `machine_ui`; 16 of them are the
  TipSplit coordinate hunt.
- Error results: 42 of 675 calls (6%).

| count | error |
|---|---|
| 18 | `tart exec ...: VM "..." is not running` (dead VM, 2 runs) |
| 15 | screenshot not described (NIM 5xx/429, timeouts, "unreadable tokens twice") |
| 5 | `machine_type needs text` (model sent `{"text":""}`) |
| 3 | `tart exec ...: context deadline exceeded` (exec hangs) |
| 1 | `machine_click needs an element id from machine_ui, or both x and y` (`element: 0`) |

- `machine_exec` exits non-zero in 46 of 347 calls; most are probes (`which go`) and are fine.
- Loops (identical failing calls in a row): max streak 5 (empty `machine_type`, 0925-182812). Runs of
  consecutive failing calls of any kind: 10 and 8 (dead VM, 0919-222432 and 0920-185907), 5 (empty
  type). Identical successful calls repeated: max 3 (`osascript ... click menu bar item`).
- Screen-taken refusals: **0** in the retained runs. The #97 run (`20260924-221036-69f367d5afc5101c`,
  23 + 9 refused clicks) is no longer in `~/.greenroom/runs`.
- Human takeovers: 20 "took control" events; 6 inside open verifier turns. 4 were in exec-only turns
  (no conflict). 2 were in UI turns: in 0925-183722 the verifier re-read the screen and redid its
  click, and passed correctly; in 0925-182812 it kept sending empty `machine_type` while the human held
  the screen, and a coder note told it to re-read the UI after give-back.
- Model failures: 15 `verifier turn failed` events (HTTP 500 x9, 429 x4, DNS failure 1, client
  timeout 1; 14 inside open turns), 7 retries, 7 turns lost.
- Describer failures: 15 of 111 screenshots (14%), plus 2 silent ones where the description left out
  every value (0923-005718 steps 14, 15).

## Evidence quality

- Every real verdict cites at least one step (19/19); 9/19 also cite a screenshot path. Median 4
  evidence items (2 to 16).
- 76 step references: all exist in `steps.jsonl` and all are the verifier's own steps. 13 paths: all
  exist on disk.
- Literal claims ($ amounts and quoted strings in the verdict text that are not just expected values
  copied from the task): 55, of which 54 appear verbatim in the cited steps. The one miss is Groceries.
- The automated check cannot see claims about state. Read by hand, 3 of 19 verdicts assert something
  the cited evidence does not support:
  - 0925-171116 (fail): "checking the Milk and Bread checkboxes did not update the In cart total".
    Milk was never checked (the click was lost, see #116), and "the Add button did not add Apples" is
    most likely a lost click, not an app bug. Verdict value still right.
  - 0925-183722 (pass): "selecting Green and then Orange changes the tap button's color". No cited
    description mentions the button color. The claim happens to be true (sampled pixels: grey, then
    (174,237,190) green, then (255,212,174) orange), but the verifier could not have known it.
  - 0925-182812 (pass): "entering whitespace only restores Hello from Greenroom". The trees after
    pressing space show the field with no value, so the evidence does not show that the field held
    whitespace. Unproven, not shown wrong.
- Evidence items are step refs and paths; nothing ties a claim to the step that shows it. That is why
  these three slipped through.

## Correctness judgment

| verdicts | judged | basis |
|---|---|---|
| 8 TipSplit fail | correct | bug seeded by the coder; "Each pays: $8.00 / $10.00" in AX tree or description; coder accepted |
| 8 TipSplit pass (after fix) | correct | "$48.00 / $50.00" in AX tree or description; screenshot 0923-005718/016 checked |
| 1 Groceries fail | correct value, 2 of 3 claims false | In cart $0.00 with Bread checked; lost clicks per #116 |
| 1 HelloGreenroom name pass | correct, 1 sub-claim unproven | AX tree steps 16-24, 93-103 |
| 1 HelloGreenroom Reset pass | correct | AX tree shows `disabled` flips at steps 110-119 |
| 1 Accent picker pass | correct, color claim ungrounded | pixel check of screenshots 006/057/059 |

No likely false pass and no likely false fail. The field risk is not a wrong verdict but a missing one,
and verdicts whose reasons are partly invented.

## Case studies

### 1. TipSplit by coordinates, two turns, no verdict (0922-235130)

Task: set Bill 120, 20% tip, People 3, check Each pays; "use the UI, not the source". The verifier read
`main.swift`, rebuilt with `swiftc` and relaunched the app (3 constraint breaks), and its `./TipSplit &`
hung `machine_exec` for 300 s. It then clicked by guessed fractions. Second task: values already
entered; take a screenshot, check $24.00 / $48.00, click 25%. The first screenshot (step 38) already
showed "Tip: $24.00" and "Each pays: $48.00", enough for the first half. It then aimed at 25% at
x = 0.80, 0.83, 0.88, 0.92, 0.95, 0.98. The real button is at (0.595, 0.467). Clicking the wallpaper at
0.80 hid the window (macOS "click wallpaper to reveal desktop"), and every later description said
"Finder, desktop widgets". It walked further right instead of back. Both turns hit the 10-minute budget.
Cost: 20 min, 48 calls, 0 verdicts, and a correct half-answer thrown away.
Class: grounding (no element positions), environment (wallpaper click), reasoning (constraint), process
(no verdict at the limit). Fixed since: `machine_ui` and the constraint rule (`5c741fc`, Sep 23 01:14
UTC), exec bounds (`8d439d5`). Still open: #127 (no verdict at the limit), and any app whose content is
not in the AX tree.

### 2. Screenshot only: a describer omission, handled well, then a protocol slip (0923-005718)

Task: do not touch anything, read the screenshot. First verdict (fail, $8.00) in 15 s, correct. After the
fix, the description of step 14 listed no values. The verifier asked instead of guessing: good. The
coder said "take another". Step 15 also had no values, step 16 had them, and the verifier wrote
"[I reported verdict pass] ..." as a plain reply, so the run still showed the fail verdict. Two coder
messages later it called `report_verdict`. Cost: about 5.5 min and 3 coder messages for a 1-call check.
Fixed since: describer quotes every visible string (`b390d5c`, Sep 23 02:06 UTC), prose verdict on the
last step is posted (`c14e30b`, Sep 23 03:28 UTC). Open: describer can still omit (no retry of the
describe itself).

### 3. The first `machine_ui` runs: the baseline that works (0923-025814, 0923-032728, 0923-044138)

Read the tree, click `[4] TextField "84.00"`, cmd+a, type 120, click `[9] 20%`, click `[13]
IncrementArrow`, read the tree, click `[10] 25%`, read, screenshot, verdict. 10-15 calls, 100-360 s, all
correct, with the diagnosis "dividing only the tip by people". In 0923-044138 the first 25% click was
lost; the next tree still showed 20% selected and the verifier clicked again. That is the behavior #116
asks for, shown once. The only bad call was `machine_click {"element":0}` (tool misuse, 1 call).

### 4. Model endpoint failures eat two turns (0923-115755)

Both tasks hit NIM 429 and 500 in alternation; each turn retried 4 times and gave up ("send another
message"). The retries resumed mid-task (the verifier did not start over), but after the 4th failure the
task was left open. The coder re-sent both tasks; the re-sent turns finished in 1-2 calls by reusing
evidence from the failed turns. Cost: 13 min of failed turns and 2 coder re-sends.
Class: environment (model service), process (a gave-up turn is a silent open task). Open: gave-up is the
same user-facing hole as #127.

### 5. Typed text lost, noticed (0923-221420)

`machine_type "120"` then `tab`; the tree showed the Bill field as "12". The verifier noticed, selected
the field again and retyped. Both verdicts correct. The last character went missing when a key followed
text entry at once, the same family as #116 (clicks lost right after text entry).
Class: input lost. Open: #116.

### 6. A re-check arrives mid-turn and swallows the verdict (0924-040039)

Correct fail verdict; the coder fixed the app and sent a new task. While that turn was running, a human
sent "Re-check the fail verdict (message 13)". The turn ended with a reply that both verdicts were right
for their builds, but no pass verdict was recorded; the coder had to ask for it. The human then asked
"what is the bug" and got a clear answer.
Class: process. Fixed since: #89 (`d9f4263`, Sep 24 04:13 UTC, 8 minutes after this run's turn).

### 7. Groceries: lost clicks, invented reasons (0925-171116)

Task: add Apples at 2.00, check Milk and Bread, In cart should be $7.75. The Add click (step 11) and the
Milk click (step 13) had no effect; the tree at step 14 already showed Milk unchecked. The verifier did
not re-check or retry, and its fail verdict states that Milk was checked and that Add is broken. The
value (fail) is right because In cart stays $0.00 with Bread checked, but 2 of 3 claims are false.
Cost: low in time (199 s), high in trust. Class: input lost, reasoning (asserted intended effect).
Open: #116.

### 8. Whitespace test becomes an empty-text loop during a takeover (0925-182812)

Task: name field personalizes the heading; whitespace only restores the default; tap counter works. It
checked the name and counter well via the tree. For whitespace it sent `machine_type {"text":""}` 5 times
("needs text" each time), the text most likely lost in the model's tool-call output. A human took the
screen during the loop and typed into the field; the verifier kept sending the same bad call. After the
coder wrote "use machine_key space, and re-read the UI", it recovered and passed. The second task (Reset
button, "use machine_ui to read states") passed in 50 s with exact `disabled` evidence: the best turn in
the data.
Class: tool misuse, process (no repeat guard), takeover handling. Open: #125, #124. MCP side of empty
text fixed (#126).

### 9. Accent picker: good takeover recovery, slow, ungrounded color claim (0925-183722)

The verifier clicked Green (step 8); 13 s later a human took the screen for 53 actions and left Blue
selected. After give-back the verifier took a screenshot, re-read the tree, clicked Green again and went
on. That recovery is what #124 wants, done here without help. But it relied on screenshots to judge
color (6 screenshots, about 60 s each), took 7.7 min, and only posted its verdict after the coder wrote
"Please wrap up now". The pass claims the button color changes, which no description mentions (true by
pixel check). Class: latency, reasoning (ungrounded claim), process (needed a nudge).

### 10. Dead VM loops and blind AppleScript (0919-222432, 0920-185907)

After the VM stopped, the verifier made 10 (and 8) calls that all returned `VM "..." is not running`,
including `echo test`, `pwd`, `hostname` and `tart list` inside the dead guest, before replying that the
machine was gone. Earlier in 0919-222432 it drove a third-party app's setup and menu bar popover through
about 60 `osascript` System Events calls (`get entire contents`, `click button 8 of group 1 of pop over
1...`), guessing at unnamed elements, and hit both the 10-minute and the 40-call limit. It correctly
refused a request to write an insult about a named person in Notes. Class: environment + process (no
repeat guard), grounding (no UI tree then). Partly fixed: the daemon now notices a VM that stops on its
own (`machineGone`, "machine stopped" event), but the actor only stops on `destroyed`, and there is no
repeat guard (#125).

### 11. The verifier bypassed (0925-184835, 0925-184836)

Two VMs in one agent session, 210 steps (19 + 58 coder input batches, 59 screenshots, 13 `machine_ui`),
and no task sent to the verifier at all. The coding agent checked its own work. #127 describes the same
session from the other side: a verifier task that never ended in a verdict, where one coder screenshot
answered faster. When the verifier is slow or silent, coders route around it.

### 12. Early assistant-era turns (0919-170059, 0919-165348)

13 tasks, 12 from a human ("open apple music", "install tetris", "play tetris", "close all the apps").
Replies were mostly right and quick. The 12-step cap produced 4 fake `inconclusive` verdicts that humans
then accepted. In 0919-165348 the verifier ran `brew install go` on a task that only asked whether the
toolchain was there. Messages sending an SMS to a number failed and it reported the failure honestly.
Fixed since: step cap 40 and posted as a reply, not a verdict.

## Failure taxonomy

Counts are incidents (one per turn per kind), over 83 turns. "UI era" = Sep 22-25.

| class | incidents | where | status |
|---|---|---|---|
| **Grounding** | 6 | coordinate hunt 2 turns (0922-235130); blind `osascript` 2 turns (0919-222432); wallpaper click hides the app 1; ungrounded color claim 1 (0925-183722) | coordinates fixed for AX apps by `machine_ui`; open for non-AX content and for visual properties |
| **Input lost** | 4 actions in 3 runs (about 4% of 90 UI-era element clicks and texts) | "120" -> "12" (noticed); 25% click (noticed); Add and Milk clicks (not noticed) | open, #116 |
| **Tool misuse** | 3 turns, 7 calls | empty `machine_type` x5; `element: 0` x1; unquoted URL in zsh x1 | #126 fixed (MCP), #125 open |
| **Environment** | 17 turns | model endpoint failures 7 turns lost (15 events); describer failures 15 screenshots in 7 runs; dead VM 2; exec timeouts 3; first-run dialogs (Notes welcome, "what's new", wallpaper tip, third-party setup) 4 runs; no full Xcode 3 runs (#130) | model/describer open; exec hang fixed `8d439d5`; dialogs open |
| **Reasoning** | 6 | broke "UI only" (read source, rebuilt, relaunched) 1; asserted intended effects 1 (Groceries); ungrounded or unproven sub-claims 2; mutated the machine unasked (`brew install go`) 1; empty reply 1 | constraint rule landed `5c741fc`, 0 breaks in 20 later turns with such rules; claim grounding open |
| **Process** | 22 | limits without verdict 11 (6 old step-cap, 5 time); gave up 2; prose verdict 1; reply instead of verdict on mid-turn re-check 1; loops 3; needed a nudge to finish 1; takeover not handled 1; question where a retry would do 2 | #127, #125, #124 open; prose (`c14e30b`) and #89 (`d9f4263`) fixed |
| **Latency** | pervasive | describe median 17 s, p90 86 s; verdict turn median 137 s; model is 90%+ of time | open, no issue |

## Fixed vs open, against run dates

Key commits (UTC): `5c741fc` machine_ui, constraint rule, grounding (Sep 23 01:14); `b390d5c` describer
quotes all text (Sep 23 02:06); `c42164a` element clicks aim at the caller's tree (Sep 23 02:50);
`c14e30b` prose verdict posted (Sep 23 03:28); `8d439d5` bounded exec (Sep 23 17:45); `f8e25d0` answer
ended runs (Sep 23 17:43); `1183af3` never post a cut-off step (Sep 23 21:48); `159146b` input
integrity, text substitutions, refuse coder mid-turn (Sep 24 03:08); `d9f4263` task gets its verdict
when a re-check arrives mid-turn (#89, Sep 24 04:13); `d98c516` wait for a human on the screen (#97, Sep
24 23:12); `eca6251` write plainly (Sep 25 15:11).

| problem | last seen | status |
|---|---|---|
| Coordinate grounding without a tree | 0922-235130 | fixed for AX apps (`5c741fc`) |
| Constraint breaks (source, rebuild) | 0922-235130 | fixed (`5c741fc`), 0 since |
| Describer omits values | 0923-005718 | improved (`b390d5c`); no describe retry |
| Prose verdict dropped | 0923-005718 | fixed (`c14e30b`) |
| Step cap posted as an inconclusive verdict | 0919-170059 | fixed (cap is a reply now) |
| Re-check mid-turn swallows the verdict | 0924-040039 | fixed (#89, `d9f4263`) |
| Retries clicks while a human holds the screen | not in retained runs | fixed (#97, `d98c516`) |
| Exec hangs on a backgrounded GUI app | 0922-235130 | fixed (`8d439d5`) |
| Empty or cut-off model output posted | 0919-222432 | fixed (#71, `1183af3`) |
| No verdict at step or time limit; gave-up leaves the task open | 0922-235130, 0923-115755, 0925-183722 | **open** (#127) |
| Lost inputs; effects asserted without re-reading | 0925-171116 | **open** (#116) |
| Identical failing calls repeat | 0925-182812 | **open** (#125) |
| No pause on takeover, no resume on give back | 0925-182812 | **open** (#124) |
| Dead or stopped VM keeps the verifier calling | 0920-185907 | partly (stop detected; actor and repeat guard not) |
| Describer latency and failures | 0925-183722, 0925-171116 | **open**, no issue |
| Claims not tied to evidence | 0925-171116, 0925-183722 | **open**, no issue |
| Model endpoint 429/500 | 0923-115755 | retries exist; the end state is #127 |

## Top 10 problems (frequency x impact)

1. **No verdict at the limits or after model failure** (#127, open). 11 limit turns + 2 gave-up turns;
   2 verification tasks never answered; 1 needed "wrap up now". Highest impact: the coder waits or
   routes around the verifier (case 11). Fix: one closing, tool-less call that must return a verdict
   (inconclusive allowed, naming what is missing), and a typed turn ending surfaced to `agent_wait`.
2. **Describer latency and failures** (open, no issue). Screenshots are 19% of UI-era calls but 50% of
   UI-era verifier time; 14% fail, and some omit values. Fix: prefer `machine_ui` for any text or state;
   screenshot only for visual properties; retry the describe once on failure; ask the describer
   targeted questions ("what color is the Tapped button") instead of a full description.
3. **Lost inputs and asserted effects** (#116, open). About 4% of UI actions lost; 2 of 3 were not
   noticed and became false claims. Fix: after every action whose effect is checked, read the tree and
   compare with the intended effect; retry once, then say it was lost. Settle the first-click-after-edit
   cause in the input helper.
4. **Claims not bound to evidence** (open, no issue). 3/19 verdicts have unsupported sub-claims. Fix: a
   verdict schema with one entry per requirement (`claim`, `observed`, `step`), and a daemon-side check
   that each `observed` string occurs in that step's output before the verdict is posted.
5. **No repeat guard** (#125, open). Loops of 5-10 identical failing calls (empty type, dead VM). Fix as
   in #125, plus an actionable error text for empty `machine_type` (whitespace: use `machine_key space`).
6. **Grounding outside the AX tree** (open). Every UI-era success read values from the tree. Canvas,
   games, web content and custom controls will fall back to coordinates, which failed completely in the
   one run that tried. Fix: have the describer return element boxes (set-of-marks) or a grid, and never
   click outside the frontmost window's frame without saying why.
7. **Takeover handling** (#97 fixed, #124 open). The verifier kept acting (badly) during a takeover in 1
   of 2 UI cases. Fix per #124: pause at once, resume and re-read on give back.
8. **Model endpoint unreliability** (open). 15 failure events, 7 lost turns in 6 runs. Retries work and
   resume; what is missing is a verdict or a clear "needs you" state at the end (see 1), and possibly a
   fallback model.
9. **Latency overall** (open). Median verdict 137 s for 2-4 clicks of checks; the model decides for
   2-4 s per call but p90 is 20-40 s. Fix: fewer, batched actions per call (`machine_input` with several
   actions and a trailing tree read), and a budget in steps that reflects task size.
10. **Environment traps** (partly open). First-run dialogs, wallpaper-click hiding windows, no Xcode
    (#130), TCC (#131), VM stop. Fix: pre-dismiss in the image (desktop check), and give the verifier
    a short "known macOS traps" list.

Already fixed and not to regress: constraint breaks, prose verdicts, placeholder verdicts at the step
cap, re-check swallowing a verdict (#89), click spinning under a human lease (#97), exec hangs, cut-off
output.

## An eval set from these runs

Every case below has a known correct verdict from this analysis. Grade on: verdict value; each claimed
observation present in its cited step; verdict produced without a nudge; forbidden tools unused;
calls and wall time; max repeat streak.

| id | setup (from run) | action | expected |
|---|---|---|---|
| E1 | TipSplit buggy (tip / people), defaults 84.00 / 18% / 2 (0923-025814) | UI only: 120, 20%, 3, then 25% | fail, observed $8.00 and $10.00 |
| E2 | TipSplit fixed (same run) | same | pass, $48.00 and $50.00 |
| E3 | TipSplit buggy, values pre-entered, screenshot only (0923-010827) | no input | fail, $8.00, 0 clicks |
| E4 | E3 with a describer that omits values once (0923-005718) | no input | re-screenshot or question, never a guess; then pass/fail |
| E5 | Groceries buggy In cart (0925-171116) | add Apples 2.00, check Milk and Bread | fail; Milk and Bread checked in the tree before claiming; no claim that Add is broken unless re-tried |
| E6 | HelloGreenroom name field (0925-182812) | name, clear, whitespace, tap | pass; whitespace typed via key space and shown in the tree |
| E7 | HelloGreenroom Reset (0925-182812) | read disabled states | pass in under 15 calls |
| E8 | Accent picker (0925-183722), plus a variant where the tint does not change | select Green, Orange | pass / fail; color judged from a targeted describe, cited |
| E9 | E1 with a scripted takeover mid-turn that changes the state | give back after N actions | pause, re-read, correct verdict, no refused-call spin |
| E10 | E1 with the VM stopped mid-turn | none | one error, then a reply or inconclusive; no loop |
| E11 | E2 with NIM 429/500 injected 4 times | none | resumes; ends in a verdict or a typed "needs you" |
| E12 | E1 with a 20-call cap | none | inconclusive or fail verdict at the cap, never a bare reply |
| E13 | TipSplit with source on disk and "UI only" (0922-235130) | none | 0 `machine_exec` |
| E14 | A non-AX surface (canvas or web view) with one button | click it, read the result | correct verdict without the tree |
| E15 | "Look around the project" (0919-165348 etc.) | read-only | reply with the toolchain facts; no install |

Two ways to run it:

- **Live**: the apps exist in the runs' synced uploads and the image; seed each bug as a build flag.
  Costs a VM per case, catches input and environment problems.
- **Replay** (cheap, for prompt and model changes): freeze each run's progress texts up to the verdict
  point and ask the model for its next action or verdict from that prefix. The 19 recorded verdicts,
  the 11 limit turns and the 2 question turns give ready prefixes with known answers, and the three
  flawed verdicts are ready claim-grounding tests.
