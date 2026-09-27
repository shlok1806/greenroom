# 0036. The daemon owns the run summary

Date: 2026-09-27
Status: accepted. Phase 1 of the Companion redesign (docs/20 section 10). Amends companion
ADR 0002: the one derived run state moves from the app into the daemon, and the app reads it.
Root ADR 0035 is left to docs/20's phase 0 decision (the web UI in a native shell).

## Context

docs/20 audited the Companion: a user cannot tell in five seconds whether a run passed, what
it is doing or what to do next. The facts exist (companion ADR 0002's `RunFacts`, the verdict
and its checks, root ADRs 0024 and 0034), but the app derives them itself, in Swift, and words
them in up to seven phrases across three regions. Two findings drive this change:

- **"Needs you" means nothing.** 136 of 179 runs sat under it. The app's rule is "a question,
  a proposed or contested verdict, or a stopped verifier", with no regard to whether the run
  is still open, so every run whose coding agent moved on without accepting its verdict stayed
  there for good.
- **Titles are instructions.** A run is named by the first sentence of its task, so rows are
  long, truncated and near duplicates.

A web UI is coming (docs/20 section 9) next to the SwiftUI app. If each derives the state, the
two will word one run two ways, which is the contradiction companion ADR 0002 was written to
end inside one app.

## Decision

1. **The daemon derives one summary per run** (`internal/summary`, `Derive`): a pure function
   of what it already records (manifest, live machine, conversation, steps, frames). No file
   read and no model call inside it. Every UI renders the summary as it comes and derives
   nothing of its own.

2. **Fields** (JSON, `summary.Summary`):

   | Field | What it is |
   | --- | --- |
   | `runId` | the run |
   | `name` | five words or fewer (point 3) |
   | `source` | who started the run, in words ("Claude Code"); absent when unknown |
   | `state`, `status` | the status id and its word (point 4) |
   | `tone` | `pass`, `fail`, `live`, `wait` or `quiet` (point 6) |
   | `group` | `needs-you`, `running` or `done` (point 5) |
   | `detail` | the one sentence under the status, if the state has one |
   | `now` | what an open run is doing, in plain words ("Clicking 25% in TipSplit") |
   | `since` | when the run entered its status |
   | `startedAt`, `endedAt`, `elapsedSeconds` | the run's span; `endedAt` absent while open |
   | `checks` | `{total, passed, failed, pending, text, current: {text, state}}` |
   | `failing` | the first failed check: `{text, expected, saw, observed, step, picture, mark}` |
   | `primaryAction`, `secondaryActions` | `{id, label}`; the one prominent action, and the rest |
   | `machine` | `{status, warning}` in words |
   | `outcome` | `Verified`, `Unverified` or `Abandoned` once the coding agent finished the run |
   | `lastFrame` | the newest frame, `{kind, file, url, at, step}` |
   | `updatedAt` | the newest record the summary was made from |

3. **The name.** `machine_create` takes an optional `name` ("What this run checks, in five
   words or fewer, e.g. 'TipSplit: split the bill'"), cut to five words, and the daemon records
   the MCP client that sent it (its `clientInfo` name, else the HTTP User-Agent's first product
   unless that is an HTTP library's) as `source`. Both go into the manifest (`name`, `source`).
   With no name, one is made from the first task, deterministically: the first app-like name
   (`TipSplit`, `HelloGreenroom`), else the sentence's first capitalised words that do not lead
   it ("Greenroom Companion"), then a short quoted phrase or an issue number from the task
   ("HelloGreenroom: Your name", "Greenroom Companion: issue 146"), else the subject alone;
   with no subject, the task's first five words.

4. **The status vocabulary**, nine words. The first rule that holds wins. "Open" means the
   machine is up or coming up (booting, ready, rebooting) and the coding agent has not
   finished the run (ADR 0034).

   | Status | When | Primary action | Secondary |
   | --- | --- | --- | --- |
   | Starting | the machine is booting | none | none |
   | Restarting | the machine is `rebooting` (machine_reboot, daemon ADR 0004) | none | none |
   | Not answering | open, and every look (screenshot, UI read) since the machine was last ready timed out with the screen not answering (daemon ADR 0003) | Restart the Mac | Keep waiting |
   | Paused | open, and the verifier's last word is an unanswered question, or a reply stopped at its limit with nothing after it (issue #127) | Answer, or Continue | none |
   | Checking | open, and a coder or human message owes a verifier turn (a system event saying nobody or nothing will answer closes it) | Take control (Give control back while you hold it) | Restart the Mac, when the machine is low on resources |
   | Passed, Failed, Inconclusive | the current verdict's outcome, unless a person rejected it | while the verdict is proposed or contested: Accept pass, Accept fail, or Accept | Reject; or Ask for a re-check when the coding agent accepted it and the run is open |
   | Checking | open, with no outcome (no verdict yet, or a rejected one) | Take control | as above |
   | Stopped | not open, with no outcome | none | none |

   A verdict followed by a new task is Checking again (a re-check), not the old outcome. A
   rejected verdict gives no outcome: the person's word outranks it.

5. **Groups, narrowly.**
   - **Needs you**: the run is open **and** cannot go on without a person: Paused, Not
     answering, a verdict that is proposed or contested with no turn owed, or a machine
     warning (point 7).
   - **Running**: open and not Needs you.
   - **Done**: not open. A proposed verdict on a run whose machine is gone, or that the coding
     agent finished, is Done: it still offers Accept and Reject (the daemon still accepts a
     human's), but nothing is blocked on it. This is the change that empties the audit's
     "136 of 179".

6. **Tone.** Colour only for a real state (docs/20 principle 2; companion ADRs 0002, 0003,
   0016): `live` for Starting, Checking and Restarting; `wait` for Paused and Not answering;
   `pass` for a pass waiting on review, accepted by a person, or finished as verified; `fail`
   for a fail waiting on review or accepted by a person; `quiet` for everything else, which
   includes an outcome only the coding agent accepted ("unreviewed keeps its word but not its
   colour") and Inconclusive.

7. **Machine words.** `machine.status` is `starting`, `on`, `restarting`, `not running` or
   `off`. `machine.warning` is a problem a person should act on: for issue #186's files warning,
   "The Mac is running low on resources; save what you need."

8. **Plain words only.** No string in a summary names a tool (`machine_ui`), an image
   (`greenroom-base`), a timing in milliseconds, a step or element number, an issue number or
   an internal term ("superseded", "contested", "unreviewed"). The verifier's prose that a
   summary quotes (a check's criterion and observation) is cleaned: its record citations
   ("(steps 19, 20)") and greenroom's review note are dropped and tool names are said in words.
   A verifier's question is not quoted at all: the detail says it has one. A test lists the
   forbidden terms and checks every string of every fixture.

9. **Checks.** An outcome's tally is the verdict's checks; otherwise the newest plan's
   (`declare_checks`), all pending: "2 of 4 checks failed", "4 of 4 checks passed", "4 checks
   planned". The verifier does not report per-check progress during a turn, so a live run
   shows its plan, not a running count. `failing.expected` and `failing.saw` are the values
   the check's criterion and observation disagree on (money, numbers, percentages, quoted
   text), when they name them. The picture is the check's first screenshot, else the frame of
   its first evidence step; the mark is the element of a cited UI read that shows `saw`, when
   no input came between that read and the picture (companion ADR 0014's rule).

10. **HTTP.** `GET /api/summary` answers `{groups: [{id, title, count, runs}], macs: {free,
    total, text}, updatedAt}`, every group present, the run that entered its status last first.
    `macs` counts the manager's machines against `-max-machines` ("2 of 3 Macs free"); other
    VMs on the host are not counted. `GET /api/runs/{id}/summary` answers one run. The event
    stream sends `event: summary` with `{runId, summary, macs}` when a run's summary changes,
    checked at most every 250 ms per stream, ignoring `elapsedSeconds` and `updatedAt`.

## Consequences

- The Companion and the web UI render one derivation. The Companion reads `name`, `status`,
  `group` and `primaryAction` next (phase 1's app side); `RunFacts` shrinks to what the daemon
  cannot know (the player position, a draft).
- A new run state is a new daemon rule and a new word here, never a UI's own rule. A client
  treats an unknown `state` as data and shows its `status` word.
- Summaries of runs with no live machine are cached until their conversation grows or their
  manifest is rewritten, so the board does not reread every old run's steps.
- `summary.LiveMachine.LowOnFiles` is set from the machine's files count once issue #186's
  `Machine.Files` lands; until then no machine warns. The Not answering rule reads the
  not-answering text daemon ADR 0003's looks return, and Restarting the `rebooting` status of
  daemon ADR 0004; both are inert until those land, and neither needs code from them.
- A derived name can still be poor for a task with no app-like subject. Coding agents that
  pass `name` avoid that; the tool description asks for it.
