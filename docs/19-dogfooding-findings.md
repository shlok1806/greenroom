# 19. Dogfooding findings: Greenroom built with Greenroom

Date: 2026-09-27. Status: findings, not a decision. Covers the dogfooding session of
2026-09-26/27, in which coding agents fixed ten Greenroom issues and checked each change on
a clean Greenroom machine, with Greenroom's verifier judging the on-screen ones.

Sources, all read-only: the per-issue logs in `~/.greenroom/dogfood/` (stop-card, 146, 153,
154, 155, 157, 159, 162, 103), the run records in `~/.greenroom/runs/<id>/`
(`manifest.json`, `steps.jsonl`, `conversation.jsonl`, `vm.log`), the PRs and their CI
check runs, the issues filed during the session (#153 to #169, #174, #175, #179, #183,
#185), `docs/17-scene-setup.md` and `docs/18-verifier-strictness-audit.md`. #179 was still
being verified when this was written (its first machine died; a second, run
`20260927-031436-085601dd1d982b05`, had just booted) and has no log yet; its numbers here
come from its run record only.

Times are UTC unless marked CDT (the #153 to #103 logs use CDT, UTC-5).

## 0. Headline

- **10 issues, 8 PRs, 8 machines** (7 finished, 1 still running). 5 PRs merged, 3 open
  with green CI. Every PR carries the run id that verified it.
- **Machines lived 316 min** in total over 7 finished runs. Boot is not the problem: median
  40 s, 5 min in total.
- **Waiting dominated wall time.** PRs sat **291 min in CI queues** in total (median 36 min
  per PR) for about 50 min of actual Fast checks. VM slot waits added 8 min.
- **The verifier ran 109 min of turns; 43 min (40%) produced the verdicts that were used.**
  The rest went to redos: 24 min to the verifier misreading the screen (clipped text, the
  wrong scroll view, a covered control that opened the wrong run), 17 min to evidence rules
  and check-kind heuristics, 14 min to infrastructure (describer timeouts, a GUI hang, a
  daemon restart) and 11 min to the coding agent's own bad scene data.
- **0 false pass verdicts.** All 7 accepted passes were right. But 3 checks passed against
  the wrong run inside an inconclusive verdict, and 2 of the 7 passes cited evidence that
  does not show the claim. Of 3 fail verdicts, 1 was right (a real Companion bug), 2 were
  wrong.
- **3 of 8 machines were lost mid-work**: two to a tart descriptor leak ("Too many open
  files", after 62 and 86 min, #186) and one to a guest WindowServer hang with no recovery
  tool (#187).
- **11 new issues filed**: #186 to #196.

## 1. What was built with Greenroom

| Issue | What changed | PR | Greenroom run(s) | Verifier | Outcome |
| --- | --- | --- | --- | --- | --- |
| #127 follow-up (stop card) | Companion: a verifier stopped at its limit is a card with Continue (companion ADR 0015) | #161 | `20260926-231011-600cf88cfbdbcba8` | 3 verdicts, 1 stop, 1 question; final pass (seq 91), accepted | Merged 00:13 |
| #153 | A grounded fail stands over answers that do not hold (ADR 0031) | #168 | `20260926-234503-3057f9fab7ae9133` | fail (seq 22), fail (seq 43), both wrong; pass (seq 58), accepted | Merged 03:15 |
| #154 | Record which models verified each run and bench result | #173 | `20260926-234503-3057f9fab7ae9133` (reused) | fail (right: a real layout bug), pass, pass, accepted | Merged 03:16 |
| #146 | Companion transcript is an eager stack, so it never draws nothing | #181 | `20260927-000233-acbc2b008dfc6a8f` (hung), `20260927-003333-7b483488f7e62556` | 4 inconclusive (2 on the wrong run), 1 cut off, pass (seq 69), pass (seq 84), accepted | Merged 03:15. Render loops: main 4 of 200 blank, branch 0 of 600 |
| #155 | bench stops cleanly on low disk; disk failures are setup errors | #178 | `20260927-005454-eb5b2373291da991` | not used (nothing on screen) | Open, CI green |
| #159 | install.sh checks local images against the helper and recipe | #180 | `20260927-005454-eb5b2373291da991` (reused) | not used | Merged 03:17 |
| #103 | serve sweeps orphaned run clones at start | #182 | `20260927-005454-eb5b2373291da991` (reused) | not used | Open, CI green |
| #157, #162 | Runs list: twins say what tells them apart; one selected row after a run changes section | #184 | `20260927-012918-a28cdeed6b759ec7` | inconclusive (4 pass, 1 unchecked), pass (seq 59), accepted | Open, CI green |
| #179 | More menu as a Greenroom dropdown | not yet | `20260927-011611-3e696117623d181b` (died), `20260927-031436-085601dd1d982b05` (running) | inconclusive x2: the app hung (#185), then steps ran out | In progress |

Bugs found along the way, filed by the coding agents: #169 (a key pair reached a guest),
#174, #175, #183, #185, plus the operating issues #163 to #166.

## 2. Where the time went

### 2.1 Per machine

| Run | Issues | Life | Boot | Ready to first task (setup) | Verifier turns | Coder guest calls (tool time) | Ended |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `231011` | stop card | 46.2 min | 34.3 s | 2 min 3 s | 5 turns, 38.8 min | 11 exec (138 s), 5 syncs (6 s) | destroyed |
| `234503` | #153, #154 | 62.0 min | 34.5 s | 11 min 14 s (#153); about 5.5 min (#154 re-sync) | 6 turns, 19.0 min | 21 exec (467 s), 11 syncs (11 s) | **died: tart "Too many open files"** |
| `000233` | #146 | 31.0 min | 44.6 s | 3 min 46 s | 2 turns, 11.7 min, plus 1 cut off (4 min) | 12 exec (159 s) | **GUI hung**, destroyed |
| `003333` | #146 | 42.2 min | 40.1 s | 1 min 38 s | 3 turns, 11.3 min | 18 exec (170 s) | destroyed |
| `005454` | #155, #159, #103 | 30.6 min | 41.6 s | n/a | none | 15 exec (626 s), 8 syncs (7 s) | destroyed |
| `012918` | #157, #162 | 17.9 min | 38.1 s | 5 min 40 s | 2 turns, 10.3 min | 11 exec (141 s), 4 syncs (8 s) | destroyed |
| `011611` | #179 | 86.1 min | 58.6 s | 21 min 57 s | 2 turns, 14.3 min | 34 exec (202 s), 59 UI calls | **died: tart "Too many open files"** |
| **Total** | | **316 min** | **4.9 min** (median 40.1 s) | **51.9 min** over 7 setups (median 5.5 min) | **20 turns, 105.3 min** (109.3 min with the cut-off turn) | | 3 of 7 lost |

### 2.2 Per issue

| Issue | Host work | Setup (machine side) | Verification | Waiting | Wall time |
| --- | --- | --- | --- | --- | --- |
| stop card | 12 min | boot 34 s, syncs 3 s, `swift build` 37 s, guest `swift test` 1 min (13 false failures until `design/` was synced) | 38.8 min of turns for one accepted pass; 31 min of it in 3 redone turns | CI queue 3 min, Fast checks 7.6 min | 75 min to merge |
| #153 | about 10 min | boot 35 s, Go install 5.9 s (+1 failed call, PATH), `go test` 1 min 55 s, `swift build` 53 s | 11.2 min of turns; 9.5 min on two wrong fails | 2 min daemon restart; CI first run cancelled after 72 min, second queued 38 min | about 30 min to PR |
| #154 | 8 min | re-sync + stray-file cleanup (3 calls), `go test` 2 min 19 s, `swift build` + `swift test` 54 s | 7.8 min of turns, all useful; 7 min of coder fix loops (3 rebuilds of 15, 5 and 4 s) | CI queue 34 min | about 37 min to PR |
| #146 | 53 min (a fork's reproduction and fix, then review) | 2 machines: 2 boots, 2 sets of builds (37 + 28 s, then 47 s), a Python venv (PEP 668), 3 render loops of 50 renders at 20 s | 23.0 min of turns + 4 min cut off; 16.9 min on the wrong run | 11 min diagnosing the hung GUI; CI queue 32 min | 2 h 20 min to PR |
| #155 | 7 min | boot 42 s, Go 5.3 s, `go test` 2 min 53 s (cold), guards end to end 50 s | none | VM slot 5 min; CI queue 6 min | about 21 min to PR |
| #159 | 12 min | stray-file cleanup (3 calls), `go test` 2 min 0 s, synthetic images 10.7 s | none | CI queue 62 min | about 20 min to PR |
| #103 | 12 min | stray-file cleanup (6 files, 3 calls), `go test` 3 min 4 s | none | CI queue 67 min (VM suite 46 min) | about 34 min to PR |
| #157, #162 | 32 min + 15 min reproduction | boot 38 s, 138 MB sync 3.6 s, both builds 87 s, reproduction on main's build (incl. about 6 min on #183) | 10.3 min of turns; 4.6 min of it a second task forced by a timing classification | VM slot 3 min 10 s; CI queue 49 min | about 62 min to PR |
| #179 | not logged | boot 59 s, 22 min from ready to task (93 coder steps, much of it driving the app itself) | 14.3 min of turns, both inconclusive | machine died at 86 min; next machine 32 min later | in progress |

Guest builds and tests were fast: guest `go test ./...` took 1 min 51 s to 3 min 4 s, while
the same package on the busy host took 3 min 28 s to 4 min 52 s (host CI and the demo ran at
the same time). The clean machine was the faster place to test.

### 2.3 Totals by kind

| Kind | Minutes | Notes |
| --- | --- | --- |
| **Waiting: CI queue** | **291** | Per PR, from workflow start to its Fast checks job start: #161 3, #178 6, #181 32, #173 34, #168 38, #184 49, #180 62, #182 67. Plus #168's first run, cancelled after 72 min. One self-hosted runner (#156). |
| Verification: verifier turns | 109 | 43 used, 66 redone (section 3.2) |
| Setup: ready to first task | 52 | Median 5.5 min, max 22 min (#179). Hand-built scenes (docs/17, #194) |
| Waiting: lost machines | about 45 | GUI hang 11 min diagnosis + a second machine; two descriptor-leak deaths with their guest state (Go, caches, the #179 session) |
| Setup: guest tests | 14 | Seven `go test ./...` runs, cold caches each new machine (#129) |
| CI: Fast checks run time | about 50 | 5.8 to 7.7 min each |
| Waiting: VM slot | 8 | #155 5 min, #157 3 min (#191) |
| Setup: boots | 5 | 7 boots, 34 to 59 s |
| Waiting: daemon restarts | about 6 | 2 restarts (00:01 and 00:24:47): 2 min of refused `agent_wait`, 1 turn cut off, 1 pty session dropped silently (#163) |
| Setup: toolchain installs | under 1 | Go twice (about 6 s each) plus one failed PATH call (#164) |

## 3. The verifier in real work

### 3.1 Verdicts

18 verdicts in 6 runs, plus one `stop: "time"` reply, one question and one turn cut off by
a daemon restart.

| Verdict | Count | Right | Wrong | Notes |
| --- | --- | --- | --- | --- |
| pass | 7 | 7 | 0 | 2 cited evidence that does not show the claim (#193) |
| fail | 3 | 1 | 2 | Right: #154's Details pushed the whole facts grid out of a 1024x768 window, a real pre-existing Companion bug. Wrong: both #153 fails (clipped label, folded card, quoted-text timing kind) |
| inconclusive | 8 | 6 honest | 2 misleading | Honest: bad scene data, describer timeouts, step budget, an app hang it could not get past, an unanswerable timing check. Misleading: the two #146 verdicts that judged the wrong run, one with 3 check-level false passes |

Accuracy where it mattered most held: no pass verdict was wrong, and the verifier never
claimed what it could not see (it marked checks unchecked when the describer timed out or its
steps ran out). The coding agents opened every cited screenshot before accepting, and in
three verdicts that habit is what caught a problem.

### 3.2 Where 109 minutes of verifier turns went

| Class | Minutes | Turns |
| --- | --- | --- |
| Produced the verdict that was used | 43.4 | stop card seq 91 turn (7.8, incl. 4 min of false refusals); #153 seq 58 (1.8); #154 all three (7.8); #146 seq 69 and 84 (6.0); #157/#162 seq 30 (5.7); #179 both (14.3, found #185 and passed 9 of 10 checks) |
| Verifier misread the screen | 23.6 | #153 seq 22 (6.7: 120-character clip, folded card); #146 first machine seq 44 and 86 (11.7: covered strip mark opened the wrong run); #146 second machine seq 44 (5.2: same) |
| Evidence rules and kind heuristics | 17.3 | stop card seq 48 (10.0: checklist locked after a correcting dispute, ran out of time); #153 seq 43, the dispute turn (2.7: "(within 2 s)" in quoted text made a timing check); #162 second task (4.6: "within a few seconds" made a timing check an API accept could not satisfy) |
| Infrastructure | 14.3 | stop card seq 67 (10.3: 3 checks unchecked, describer timeouts); #146 cut-off turn (4.0: GUI hang, then daemon restart) |
| Coding agent's own scene mistakes | 10.7 | stop card seq 33: the coder's script truncated Run B's recording; its task also wrongly expected Run B under "Needs you" |

### 3.3 What it caught

- **A real Companion bug** (#154, seq 101 at 00:34 UTC): "The Details grid described in the
  task does not appear to be rendered in the current view". On a run with a long task,
  Details pushed every fact out of a 768-point window. The fix shipped in #173.
- **The coding agent's own mistakes**: an empty copy of Run B (stop card; the coder's
  `open(f, 'w')` truncated it before reading) and a task that expected the wrong section for
  a machineless run. The verifier's honest "No conversation yet" is what exposed both.
- **A hang in the app** (#185): while checking #179 it toggled the conversation from the
  keyboard between UI reads and left the Companion at 98% CPU in layout. It reported the
  missing accessibility tree instead of guessing, and the coder traced it to main.
- **Setup and wording errors in tasks**: in #153 and #162 its classifications, though costly,
  pointed at criteria that quoted timing words. The coders reworded and got clean verdicts.

### 3.4 Where it was wrong or too strict

Too strict (tracked in #166, measured in docs/18):
- **Limit downgrade** (#153): a grounded fail posted as inconclusive over two bad answers.
  Fixed by #168 (merged).
- **"steps" rendered match** (stop card, seq 83 to 91): a right pass refused 3 times over a
  covered "Steps" label; 4 min and a question to the coder. docs/18 case 11.
- **Kinds read from quoted text** (#153): "(within 2 s)" inside the expected text made a timing
  check, and a right pass became a wrong fail. docs/18 P5.
- **Checklist lock after a correcting dispute** (stop card): the corrected checks could not
  replace the wrong ones; 10 min turn, no verdict. docs/18 P8.
- **Timing classification of an API action** (#162): "A's mark moves after the accept" could
  not be answered, because the accept was an API call, not an input step. New case for #166.

Wrong:
- **Clipped text read as missing** (#153): `machine_ui` clips strings at 120 characters with
  no marker (#190).
- **Nested scroll** (#153): it scrolled the conversation instead of the card body (#190).
- **Wrong run** (#146, twice, on two machines): it clicked a strip mark that the open runs list
  covered, never compared "17 messages" in the header with the task's 280, and passed 3 checks
  against TipSplit. App side #175, tool side #189.
- **Evidence not showing the claim** (#162 seq 59, #146 seq 84): conclusions right, cited
  steps wrong (#193).
- **Step budget** (#146, #179): 40 calls ran out before the last check three times (#192).

## 4. Greenroom friction, ranked by time cost

| Rank | Friction | Cost (measured) | Issue |
| --- | --- | --- | --- |
| 1 | One self-hosted CI runner: PRs queue behind each other and the VM suite; one run cancelled after 72 min | 291 min of queue over 8 PRs | #156 |
| 2 | Hand-built scenes: data, daemon, GUI launch, prose to the verifier; bad data cost a verifier turn | 52 min of setup over 7 machines, 10.7 min wasted turn | NEW **#194** (docs/17 phase 0) |
| 3 | tart descriptor leak kills machines after about an hour, taking guest state | 2 of 7 machines; #179 lost 32 min before a new machine; #155 re-paid boot, Go, cold tests and a slot wait | NEW **#186** |
| 4 | Verifier misreads the screen: controls under an in-window overlay look clickable | 16.9 min of turns, 3 check-level false passes | NEW **#189** (and #175 app side) |
| 5 | Evidence rules and kind heuristics refuse or reclassify right answers | 17.3 min of turns + 4 min of refusals inside a used turn | #166, #153 (PR #168 merged) |
| 6 | Guest GUI hang with no recovery: screenshots and UI reads block for 4 min, `screencapture` piles up, no reboot | 11 min diagnosis, 1 cut-off turn, a second machine | NEW **#187** |
| 7 | Screenshot describer timeouts left checks unchecked | 10.3 min turn | #154, #158 (fixed by #160 and #167: muse-glimmer default) |
| 8 | `machine_ui` clips labels at 120 characters unmarked; no scroll areas | 6.7 min turn, 1 wrong fail | NEW **#190** |
| 9 | VM slot at the 2-VM limit, no queue; no free-disk guard (host fell from 14 GB to 5.7 GB) | 8 min of polling | NEW **#191** |
| 10 | Step budget of 40 runs out on multi-check UI tasks | 3 turns ended short; 2 follow-up tasks (5 to 9 min each) | NEW **#192** |
| 11 | Every new machine rebuilds and re-tests from cold; Go installed by hand | 14 min of guest `go test`, 2 Go installs, 1 failed PATH call, Pillow via a venv | #164, #129 |
| 12 | Daemon restart cuts off turns and drops pty sessions silently | 2 min refused waits, 1 lost turn, 1 dropped session, 1 empty pull | #163 |
| 13 | `machine_sync` never deletes; stray files from the previous branch got compiled | 4 occurrences, about 12 calls of file-list diffing, 1 whole stray package | NEW **#188** |
| 14 | Cited evidence need not show the claim | caught only by the coder opening every screenshot | NEW **#193** |
| 15 | Held Accept in the Companion silently never sent | about 6 min (routed through the API) | #183 |
| 16 | A copied daemon root carried the daemon's key pair into a guest | about 5 min of cleanup, and a boundary crossed | #169 |
| 17 | `machine_exec_wait` waits at most about 45 s, so a 2 to 3 min `go test` needs 3 calls | a few extra round trips per issue | by design (MCP's 60 s first-byte limit); not filed |
| 18 | macOS "what's new" notification appears mid-run over the app | none measured, covers the top right | NEW **#195** |
| 19 | `agent_wait` says a note continues a stopped verifier; a coding agent's note never starts a turn | none measured (agents used a task) | NEW **#196** |
| 20 | tart cannot run in a guest (no nested virtualization) | tart-dependent checks moved to the host under a private `TART_HOME` | platform limit; covered by the fake-tart mode in #194 |

## 5. What worked well

- **Machines are quick and predictable to get.** Boot took 34 to 59 s (median 40 s) on every
  one of 8 machines, and `machine_wait` reported the toolchain (Xcode 27, XCTest,
  swift-testing) and a clean desktop at ready.
- **Sync is not a cost.** 6.8 MB in 0.8 s, 78 MB in 1.7 s, 138 MB in 3.6 s.
- **A clean machine was the faster test bed.** Guest `go test ./...` took 1 min 51 s to
  3 min 4 s, against 3 min 28 s to 4 min 52 s on the loaded host.
- **Reusing one machine across issues worked** once the agent diffed the tree: #155, #159
  and #103 shared one machine and one Go install; #153 and #154 shared another.
- **A/B on one screen.** Building main and the branch side by side in the same guest gave
  clean comparisons: #146's render loops (main 4 of 200 blank, branch 0 of 600) and #162's
  two selected rows on main against one on the branch.
- **No false pass verdict,** and honest "unchecked" answers when the verifier could not see
  (describer timeouts, steps out, an app hang). A coding agent could accept a pass after
  opening its cited screenshots, and every PR links a replayable run.
- **The question path unblocked a stuck verifier in 16 s.** After 3 refusals the verifier
  asked; the coder answered at 23:54:04 and the pass posted at 23:54:20.
- **The stop card (#161) showed up in its own verification.** The verifier hit its 10 min
  budget and posted `stop: "time"`, a live instance of the feature under test.
- **The daemon handled machine death cleanly:** a transcript event naming tart's exit reason,
  the clone deleted, no orphan (both descriptor-leak deaths and the #179 run).
- **Screenshots land in the host run directory**, so `machine_pull` was not needed for
  evidence, and the PR images came straight from the run.
- **The verifier found real bugs that were not the change under test:** the Details layout
  (#154's fix) and the keyboard hang (#185). The coding agents found #174, #175 and #183 on
  the same machines.
- **Guest-side tooling went further than expected:** `hdiutil` built and mounted APFS
  images in the guest (#159), and the host checks used APFS clones at no disk cost.

## 6. Recommendations, ranked

1. **Add CI capacity and per-PR concurrency (#156).** 291 min of queue against about
   50 min of checks is the largest single cost, and it scales with the number of agents.
   Per-PR `cancel-in-progress` alone would have removed #168's 72 min cancelled run.
2. **Stop losing machines (#186, #187).** 3 of 8 machines died or hung, costing about
   45 min and the guest state each time. Find the tart descriptor leak (both deaths at 62
   and 86 min with frame capture on); cap `machine_screenshot` and `machine_ui` at 50 s and
   add `machine_reboot`.
3. **Make the screen legible to the verifier (#189, #190, #193).** 23.6 min of turns and all
   3 check-level false passes came from what `machine_ui` shows: covered controls look
   clickable, clipped text looks complete, nested scroll areas are invisible. Fix the tool
   before tuning the prompt.
4. **Finish the strictness work (#166, docs/18 P5 and P8).** #168 is merged. Not reading
   kinds from quoted text and telling both sides that a changed task comes as a new task
   would have saved 17 min here. Add the #162 case (an API action cannot satisfy a timing
   check).
5. **Check in the Companion scene (#194, docs/17 phase 0).** Setup took 52 min over 7
   machines (up to 22 min for one), bad data wasted 10.7 min, and one copy carried a key pair
   (#169). A script with trimmed fixtures, a data check before the task, a GUI launch that
   works, and a fake-live mode for recorded runs covers all four.
6. **Give guests the project's toolchains and a warm build (#164, #129).** Every new
   machine repaid cold `go test` (about 2 min each, 14 min in total) and a manual Go install,
   and the descriptor-leak death made #155 pay it again.
7. **Queue `machine_create` and guard host disk (#191).** 8 min of polling today, and every
   agent writes its own retry loop. Disk fell to 5.7 GB free during the session.
8. **Let the verifier finish multi-check tasks (#192).** Three turns ran out of steps before
   the last check; each cost a follow-up task of 5 to 9 min.
9. **Make `machine_sync` able to mirror (#188).** Cheap, and it removes a class of wrong
   builds (a whole stray package reached #103's tree).
10. **Resume turns across daemon restarts (#163)** and say when a session was dropped.
    Restarts happen with every daemon update, and each one cost a turn or a session here.
11. **Small fixes:** #195 (notification over the app), #196 (`agent_wait` wording).
