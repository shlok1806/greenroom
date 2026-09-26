# UX audit: Greenroom Companion

Audited 2026-09-26 on `main` at 94ea94a (layers 1 to 6, ADR 0010 thumbnails, the ADR 0024
checklist card). Method: the `CompanionSnapshots` harness against a daemon serving clones of
the real runs in `~/.greenroom/runs`, `~/.greenroom/bench/runs` and
`~/.greenroom/bench-0027/runs` (179 runs, 110 of them verdicts with checklists), rendered in
`light`, `dark`, `light-hc` and `dark-hc` at 1440 x 900 (L), 1180 x 760 (M), 1024 x 660 (G,
the default window on a 1024 x 768 screen) and 900 x 600 (S). Every scenario 01 to 40, plus
five new ones built from bench verdicts exactly as recorded (41 to 45: 4 checks with 2
failed, 8 passed, inconclusive, an ADR 0027 visual check whose answer is "not drawn", a
timing check). Every picture was read at full size.

Research and the design point of view behind the fixes: `design-research-2.md`. Severity:

- **High**: gets in the way of the product's core job (deciding whether to trust a verdict)
  or shows something false, cut off or unreadable.
- **Medium**: costs time or attention on a frequent path, or breaks a stated rule.
- **Low**: polish.

Each finding names the scenario it shows in (`NN-name`, size, theme where it matters).

## 1. Reviewing a verdict (job 1)

**Its job.** A person decides, in seconds, whether each check the verifier answered is
shown by its evidence, and accepts or disputes. Questions to answer at a glance: what was
checked, what failed, what was not checked, what each result rests on, and whether the
evidence is something a person could see.

| # | Finding | Where | Severity | Fix |
| --- | --- | --- | --- | --- |
| V1 | **The checklist is below the fold of its own card.** The card leads with the state line, a "who can accept" line, the outcome, three lines of the verifier's prose, then a bold scope sentence; checks start at about 200 pt down. At 1024 x 660 one check shows before the body scrolls (41 G, 37 G); at 1440 x 900 two to three. The checklist is the verdict's scope (root ADR 0024, point 6) and comes last. | 37, 39, 41, 44 | High | Checks first. Headline, outcome with a tally, then the checks; the verifier's summary folds under them as one line ("Verifier's summary"), opened on demand. |
| V2 | **No glanceable tally.** "Verified: 2 of 4 checks; failed: 1; not checked: Each pays becomes $50.00 at 25%" is a 2-line bold sentence that repeats the rows below it and is the heaviest text in the card. A reviewer counts marks instead. | 37, 39, 43 | High | One mono line beside the outcome: `2 failed · 1 not checked · 5 passed`, each count with its mark and role colour, failed first. Drop the scope sentence; the rows name what was not checked. |
| V3 | **Evidence is a step number with no kind.** Chips read `Step 11 ↗`: nothing says whether step 11 is a screenshot a person can look at, a UI-tree read (text the app reports, not what is drawn) or a command. ADR 0027 exists because a tree read passed a "visible" claim that was false; the card hides that distinction. | all checklist scenarios | High | Name each piece of evidence by what it is: `Screenshot 7`, `UI read 6`, `Command 4`, from the step's tool. |
| V4 | **"Not drawn" is invisible.** In 44 the UI read marks `Each pays: $49.56` `[not drawn]`; the card shows it only because the verifier happened to write it in `observed`. A pass that rested on such a read would show nothing. | 44 | High | A check whose UI-read evidence marks elements `blank`, `offscreen` or `covered` says so under the check in the attention role: `! UI read 6: "Each pays: $49.56" is not drawn`. |
| V5 | **Check kinds are not shown.** ADR 0027 posts the applied kind "so reviewers see it"; the app drops `kinds` and `within` on decode, so a visual or timing check looks like any other and a reviewer cannot tell a screenshot was required. | 44, 45 | High | Decode `kinds` (and the early single `kind`) and `within`; show `visual` or `timing, within 2 s` as a small mono tag after the criterion. |
| V6 | **Claim and proof are never on screen together.** Opening evidence moves the screen in another column to the step; the check's words stay in the card 600 pt away, and the stage's caption says only "Step 11, cited by the verdict". Comparing "Each pays read $8.00" with the picture means reading across the window and back. | 13, 37, 41 | High | Selecting a check (a click on its row, or its evidence) pins it over the stage: the stage caption becomes the check's mark, criterion and what was observed, above the picture of its evidence. |
| V7 | **A check row is not a target.** Only its small chips are clickable (about 60 x 20 pt each); the row itself does nothing, and nothing shows which check the screen is showing. | all checklist scenarios | Medium | The whole row selects the check (and shows its first evidence); the selected row carries a 2 pt brand edge. |
| V8 | **The card's cap starves the checklist.** `verdictCardShare` is 0.6 of the column in every state, so an open verdict with 8 checks scrolls inside a card while the transcript below it (history, not a decision) keeps 40%. | 39 L, 42 | Medium | While a verdict is open for review the card may take 0.8 of the column; once closed it goes back to 0.6. |
| V9 | **Cut-off sentence under the actions.** "The verifier stopped with the machine, so a re-check gets" ends mid-sentence above a disabled button (38 M, light and dark). The explanation's container clips instead of wrapping when the body is capped. | 38 M | High | The explanation belongs to the actions (it says what they do): lay it out with them, whole, never inside the capped scroll. |
| V10 | **Card in a card.** The verdict card is a bordered panel inside a bordered column, with chips (bordered) inside rows inside it; the expanded evidence adds bordered thumbnails. Four nested frames around one decision. | 37b, 06 | Medium | One frame per decision: the card keeps its edge (it carries the verdict's state colour, ADR 0009), and nothing inside it is boxed. Check rows are separated by space; step chips become mono text links named by what they are. |
| V11 | **Two outcome words, one light.** "✗ FAIL" in mono bold at title size beside "proposed by the verifier" in reading face; the state line above says "! NEEDS REVIEW" in small caps and "you or the coding agent can accept it" wraps under it at 1024. Three type styles in 40 pt of height before any content. | 37, 41 | Medium | One headline row: state (attention) and the decider, then the outcome and tally on the next. "Who can accept" moves to the actions' explanation, where it matters. |
| V12 | **Refused verdict attempts read as raw JSON.** A `report_verdict` the daemon refused shows as a red row `✗ Used report_verdict: {"checks":[{"actions":[]...` (41, 44). The refusal is trustworthy information (the review caught something) presented as noise. | 41, 44 | Medium | Name it: "Verdict refused by greenroom" with the daemon's first reason, the raw call behind the caret as before. |
| V13 | **The plan repeats the checklist.** After the verdict lands, the transcript still shows "Plan: 4 checks" with the same four criteria, a second copy of the card's rows. | 37, 39 | Low | Once a verdict answers a plan, the plan block folds to one line: "Plan: 4 checks, answered in the verdict above". |
| V14 | **Evidence diamonds use the failure colour on a fail verdict.** The scrubber's cited-step diamonds and the `◆ evidence` legend draw in red for a fail verdict (37, 44) and green for a pass, while "red" also means an errored step on the same track. | 37, 44 | Low | Cited steps draw in the foreground (hollow for superseded), never an outcome colour; the failure role stays for errored steps. |
| V15 | **A dispute's history is cut to two lines** and the third dispute item is clipped by the card fade (07 M). | 07 | Low | Folds with the summary: one line each, the whole text on expand. |

## 2. Watching live and taking control (job 2)

**Its job.** Show what the agent and verifier are doing now on the machine, and let a person
take the screen and give it back without doubt about who holds it.

| # | Finding | Where | Severity | Fix |
| --- | --- | --- | --- | --- |
| L1 | **Top-bar button row.** A live run shows five bordered text buttons of equal weight: Screenshot, Export, Destroy..., Hide Conversation, Take Control (08, 10). The one primary action competes with four occasional ones; at 1024 they crowd the wordmark. | 08, 10, 30 | Medium | The top bar keeps Take Control / Give Back (primary) and one "More" menu (Screenshot, Export Recording, Show or Hide Conversation, Destroy Machine), each also in the registry, the palette and the menu bar. |
| L2 | **The driving banner points elsewhere.** "You have control ... Give Back is in the top bar" (10, 30): the one control that ends the mode is described, not offered, 150 pt away from where the person is looking. | 10, 30 | Medium | Give Back sits in the driving bar, where the eye is. The top bar's Take Control becomes Give Back as today (one registry action, two places to click; ADR 0003 decision 6 is superseded for this one control). |
| L3 | **"connecting" with no picture age.** A live run whose stream has not connected shows the recording's last frame with `connecting` (08); nothing says how old the picture is. | 08 | Low | The source label says the frame's age while connecting: `connecting, picture 12 s old`. |
| L4 | **Verifier working is two lines in two places.** The steps list ends in "The verifier is working 00:05" and the transcript ends in "Verifier is working" (08). Consistent words, but two tickers. | 08 | Low | Keep both (each pane may be alone in a narrow window); same words: "Verifier is working". |
| L5 | **Booting stage is a black box with small text.** The boot log sits in a 640 x 480 well at 12 pt dim mono (28); it is honest and pleasant, but the well is black in the light theme's paper window. | 28 | Low | Kept: the well is a picture of a monitor (ADR 0008 decision 19). |

## 3. Finding a run and jumping to the moment (job 3)

**Its job.** Find the run that needs you, see how it ended, and land on the step that matters.

| # | Finding | Where | Severity | Fix |
| --- | --- | --- | --- | --- |
| R1 | **"Needs review" hides the proposed outcome.** 136 rows say `! Needs review`; none says whether the verifier proposes a pass or a fail, the one fact that sets how carefully to review. The strip in a medium window is 20 identical `!` (07, 10, 15). | all | High | The row says the proposed outcome and the state: `✗ Fail, needs review`, `✓ Pass, needs review`, `? Inconclusive, needs review` (attention role; the mark carries the outcome shape, not its colour, since nobody closed it). The strip draws that mark. |
| R2 | **No check tally in the list.** The run list's verdict carries its checks (the daemon sends them in `/api/runs`), but a row cannot say "2 of 4 failed". | all | Medium | A row with a checklist verdict shows `2/4` failed or `8/8` passed beside its steps count. |
| R3 | **Thumbnails carry no information at row size.** Every row's braille dither of the last frame is a grey rectangle with a lighter centre (a desktop with one window): 136 identical tiles in `14`, `15`, 56 x 44 pt each, pushing titles into two truncated lines. | 01, 14, 15 | Medium | Replace the tile with the verdict mark column (outcome shape, 16 pt), titles get the width back; the last frame shows on hover as a real picture. ADR 0010's thumbnails move to the hover card. |
| R4 | **Titles repeat the time.** `RunTitle.distinct` appends ", 02:29" to tell same-titled runs apart, and the line under it starts "02:29 · 12 steps" (01 S, 14). | 01, 14 | Low | Tell twins apart by what differs in their task (the next words), else by the run's hash; never by the time already shown. |
| R5 | **Needs you has no end.** With 136 runs needing review the pinned section fills the column; Running and the days are never reached without scrolling past all of them (15). | 15 | Medium | Pin the 5 newest, then "Show all 136"; `/` search keeps finding any. |
| R6 | **The header repeats the title.** The run's title (its task's first sentence) and, under it, the task starting with the same sentence (41, 44). | 41, 44 | Low | The line under the title is the rest of the task. |
| R7 | **Opening a finished fail lands on the right step, but says only "Step 11, cited by the verdict".** Fixed by V6: the stage says which check that step answers. | 36, 37 | (V6) | |

## 4. Every screen: pixels, type, colour

| # | Finding | Where | Severity | Fix |
| --- | --- | --- | --- | --- |
| P1 | **Uniform weight in chrome.** Section labels, tool rows, step rows, times and hint keys are all mono at 12 to 13 pt; the tool-call rows in the transcript are as loud as a verifier's reply. | 08, 03 | Low | Tool rows and step rows at `monoSmall` with dim durations (already); no change beyond V1's hierarchy. |
| P2 | **Low-contrast "Send".** The disabled Send button in light (`38`, `37b`) is brand at reduced opacity on paper: about 2.4:1. Disabled controls are exempt from 1.4.3, but it reads as a smudge. | 37b, 38 | Low | Disabled buttons draw dim text on no fill with a hairline, like other quiet buttons. |
| P3 | **Checkbox "Only errors" disabled in grey** on a run with no errors (37): a control that can do nothing. | 37 | Low | Hide it when the run has no errored step. |
| P4 | **High contrast holds.** Every role, dim included, reads at 7:1 in `-hc` (41, 43 dark-hc and light-hc); focus rules and the brand edge stay visible. No finding. | 41, 43 | none | |
| P6 | **A scrim over an empty page.** With no run open in a medium window, the runs open over the detail with a drop shadow and a dark scrim, over nothing (15, 17, 18 at 1180). | 15, 17, 18 | Low | Lift and dim only over a run. |
| P5 | **Welcome's claude command is clipped** in its box (`claude mcp add --transport http greenroom http://127.0.0.(`, 18 M): the copy button covers the tail. | 18 | Low | Let the command wrap in its box; Copy sits beside it. |

## 5. Keys, focus, VoiceOver, motion

| # | Finding | Where | Severity | Fix |
| --- | --- | --- | --- | --- |
| K1 | **No key moves through checks.** `a` and `d` decide; nothing walks the checks and their evidence, so a keyboard reviewer opens evidence with the mouse. | all checklist | Medium | `]` and `[` select the next and previous check (failed first, the card's order) and show its evidence; registry actions, in the hint bar while a verdict with checks shows. |
| K2 | **VoiceOver reads a check as three unrelated strings** (mark, criterion with its state word, observed) and the chips as "Step 11". | code: `ChecklistViews.swift` | Medium | One element per check: "Failed. Each pays shows $48.00 with 3 people. Visual. Observed: Each pays read $8.00. Evidence: screenshot 12, UI read 11." with a "Show evidence" action. |
| K3 | Motion stays inside ADR 0006; Reduce Motion shows end states (verified by the moment scenarios 28 to 36). | 28 to 36 | none | |

## What is good and stays

- The two faces: Mona Sans for reading, Monaspace Neon for data. They read well at every
  size and give the app its voice without a single decoration.
- The olive brand used sparingly, and content kept neutral so evidence reads true.
- One vocabulary and every state a word (ADR 0003). The hint bar and Cmd-K.
- The stage: screen with the steps as a trace under it, the scrubber's ticks, the player.
- The four themes and their measured contrast.
- No sheets or alerts in the verdict flow; the 5 s undo.

## Priorities

1. **Layer A, the review ledger** (V1 to V13, K1, K2): the checklist leads the card, a tally,
   evidence named by kind, kinds and "not drawn" shown, the selected check pinned over the
   stage, the re-check explanation whole, refused verdicts named.
2. **Layer B, the runs list** (R1 to R6): the proposed outcome and tally in every row, the
   pinned section capped, thumbnails to hover, titles without the time.
3. **Layer C, the top bar and driving** (L1, L2, P2, P3, P5).

## Resolution (2026-09-26)

| Findings | Fixed in |
| --- | --- |
| V1 to V13, K1, K2 | #147, the verdict as a ledger (ADR 0011) |
| R1 to R5 | #148, a run's row says its verdict (ADR 0012) |
| L1, L2, P3, P5 | #149, one primary in the top bar (ADR 0013) |
| V4, V6 on the picture itself | #150, evidence marks (ADR 0014) |
| V10 (the card's other evidence links), P6 | the polish PR after #150 |
| Open, low | V15, L3, L4, P1, P2, R6 |
| Found while auditing, not fixed | #146: the transcript sometimes draws nothing after its rows change (harness, about 1 in 16) |
