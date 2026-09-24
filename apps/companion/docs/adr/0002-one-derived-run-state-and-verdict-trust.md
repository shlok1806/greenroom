# 0002. One derived run state, colours with one meaning, verdicts headed by who decided

Date: 2026-09-22
Status: accepted. Refines ADR 0001. Amended by 0004 (colour table) and 0005 (failure
navigation keys).

## Context

Three reviewers judged the first redesign from screenshots: an engineer who checks runs
several times an hour, a tech lead judging verdicts, and a first-time user. What they found:

- **The window contradicted itself.** One screen said Live and offered Take control
  while its transcript said the machine was destroyed. Another said Booting next to a
  transcript saying ready. The header said 26:45 and the player said 26:08. Each view
  worked its answer out from a different field.
- **Colours had two meanings.** Green meant pass and live. Orange meant booting and
  stale. Red meant fail, a command error, the control frame and offline.
- **Verdicts were hard to trust.** "Accepted" did not say who accepted. A coding agent
  accepting its own verifier's verdict looked the same as a person's sign-off. The verdict
  was drawn twice, and the evidence was a list of file names.
- **The screenshots did not match the app.** The test host (Xcode's `xctest`) links a
  newer SDK than SwiftPM stamps on the app, so the harness drew the macOS 26 look and the
  app drew the older one. The harness also put windows on the person's screen and an icon
  in the Dock while it ran.

## Decision

1. **`RunFacts` is the one derived state for a run** (`Model/RunFacts.swift`), built by
   `RunStore.facts(_:)` from held records, and from the list's copy when nothing is held.
   Every indicator reads it: sidebar row, header status line, player, toolbar, verdict
   card and menu commands.
   - It gives exactly one phase: booting, live, idle (nothing for 5 minutes), ended
     (destroyed by you, destroyed by the agent, or machine lost) or failed.
   - It gives exactly one duration (start to machine gone, or to now). The player
     measures from the same start.
   - `machineReady` needs a machine in the detail, so a finished run can never offer the
     live screen or control.
   - Last activity is the newest step or message. Frames do not count: the recorder
     captures an idle machine too.
2. **Each colour means one thing** (`Palette`):

   | Colour | Meaning |
   | --- | --- |
   | green | pass |
   | red | failure (a fail verdict, a failed step, a lost machine) |
   | orange | needs your attention (review, contested, idle) |
   | teal | live, with its own broadcast mark |
   | purple | you are driving |

   Booting and offline carry no colour. Every state is also a word.
3. **A verdict's headline is its state and who decided**, from `VerdictReview`:
   - "PROPOSED awaiting review".
   - "ACCEPTED by the coding agent at 20:12", followed by "No human has reviewed this
     verdict".
   - "CONTESTED only you can close it".

   A proposed verdict is drawn outlined and neutral; only a closed verdict is filled
   with its outcome colour. The buttons say what they do ("Accept Pass", "Dispute...",
   "Reject..."), with the primary action on the right. Accepting without having opened
   any evidence asks first.
4. **Checks against the record**, never guessed from prose (`VerdictCheck`): no evidence
   cited, a cited step that is not in the record, a cited step that failed, an outcome
   that disagrees with the verdict message.

   Linking each acceptance criterion in the task to evidence needs the verifier to report
   criteria, so it waits for a daemon change.
5. **Evidence is a round trip.** A cited step is a link with a hover preview. Opening it
   seeks the Screen to that step, and "Back to Verdict" returns to where the person was.
   In the transcript the live verdict is one line, "Verdict: Pass, in full above", so it
   is never drawn twice.
6. **Failures are navigable:** a header navigator, Next and Previous Failure (Command-'
   and Shift-Command-'), and "Failed only" in Steps. A tool error is shown as "the
   command's result is unknown", and its output is labelled "not a result", so an
   `exitCode` of 0 beside an error is not read as success.
7. **The package splits** into a `Companion` library (everything), `CompanionApp` (the
   `main.swift` entry point, the only product) and `CompanionSnapshots` (the design
   harness).
   - The harness is a SwiftPM executable, so it links the same SDK as the app and draws
     what the app draws.
   - It runs as `.prohibited` (no Dock icon, never frontmost) and puts its windows far
     off every display.
   - It captures with `cacheDisplay`, so it needs no screen-recording permission.
   - It never launches the app or a bundle.
   - It replaces the round-1 `SnapshotHarness` test.

## Consequences

- `swift build -c release` for the whole package fails: the harness uses `@testable
  import`, which needs a debug build. `scripts/bundle.sh` builds `--product Companion`
  only.
- Harness windows are never key, so selection and prominent buttons draw in their
  inactive colours. Reviewers are told.
- The idle threshold (5 minutes) lives in the app until the daemon reports idleness
  itself (open PR #27 changes `lastActivity` to steps and messages only).
- Esc does not give back control: while driving, every key belongs to the machine (ADR
  0009). The way out is the "Give Back" button, which is in the bar above the picture and
  in the toolbar.
