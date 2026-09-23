# Design spec: Greenroom Companion

The companion is where a person watches an agent's run, judges its verdict and steps in.
Research behind this spec: `design-research.md`. The decisions: ADR 0001 and 0002 in
`adr/`. Round 2 (reviewer feedback) changed the colour table, the sidebar, the verdict
card and the status line; this spec is current.
Terms (run, verdict state, frame, lease, driving) are in `../CONTEXT.md`.

## Who and what for

| User | Top tasks |
| --- | --- |
| Engineer whose agent is running | Watch the live screen; see what the agent is doing right now; answer a question; take control to get past a dialog |
| Reviewer judging a verdict | Read the verdict and its reasons; open the cited evidence (step, screenshot, frame); accept or dispute |
| First-time user | Understand what a run is, what the verifier is, and what they are allowed to do |
| Anyone | Find a run again; compare today's runs by outcome at a glance |

The ordering of tasks shapes the layout: the screen and the verdict are always on screen
at once, the conversation explains both, and the steps are the evidence behind them.

## Information architecture

```
Window
├─ Sidebar: runs, grouped by day, newest first; search
└─ Run
   ├─ Header: the task (the run's name), status, time, facts
   ├─ Stage (one of):
   │   ├─ Screen: live stream or recording, player bar, take control
   │   └─ Steps: every tool call, expandable to its input, output and picture
   └─ Conversation (trailing column, can be hidden):
       ├─ Verdict card, pinned: outcome, state, summary, evidence, Accept / Dispute
       ├─ Transcript: task, replies, tool calls (grouped), questions, events
       └─ Composer: note or task to the verifier
```

- A run is named by its task (the first `task` message, `RunSummary.task` from the daemon).
  The id's hash is a secondary label; nobody recognises "9411d6".
- Evidence is linked both ways: a tool call, a step, an evidence chip or a verdict opens
  the Screen at that step. Nothing needs a copy-paste of a step number.
- The verdict lives in one place with its actions (the pinned card). The transcript keeps
  its history; older verdicts read as superseded.

## Layout

- `NavigationSplitView` (sidebar, detail) plus `.inspector` for the conversation, so the
  three columns resize, collapse and restore like any Mac app.
- Widths: sidebar 240 to 340 (ideal 280); stage at least 440; conversation 320 to 520
  (ideal 380). Window minimum 820 x 560; default 1320 x 840.
- Toolbar: sidebar toggle (leading), Screen / Steps (centre), Capture, Export recording,
  Destroy, then the conversation toggle (trailing edge, per HIG).
- The screen is a permanently dark well with rounded corners. Nothing is drawn on the
  picture except the driving badge. Position, step and live state sit under the track.
- Narrow windows lose in this order: sidebar collapses, the conversation becomes
  toggleable, the player bar's second line wraps under the first.

## Visual language

Only system text styles (macOS sizes). Nothing under 10 pt.

| Role | Style |
| --- | --- |
| Run title in header | Title 3 (15) semibold, up to 3 lines |
| Card titles, verdict outcome | Headline (13 bold) |
| Messages, list titles | Body (13) |
| Meta lines, chips | Callout (12) / Subheadline (11) |
| Timestamps, counts | Caption (10), monospaced digits |
| Ids, tools, code | SF Mono at the same sizes |

Spacing is a 4 pt grid: 4, 8, 12, 16, 20, 24. Content insets are 16; rows are 8 or 12
vertical. Radii: 4 (chips, code), 8 (cards, fields), 12 (bubbles, the screen well).

Colour. System colours only, so light, dark and Increase Contrast come for free. Each
colour means one thing; everything else is label, secondary and tertiary. Every state is
also a word, never colour alone.

| Colour | Meaning | Where |
| --- | --- | --- |
| green | pass | outcome seal, "Pass, accepted" |
| red | failure | fail seal, failed steps and their track dots, "Ended: machine lost" |
| orange | needs your attention | "Review Pass", "Needs you", "Idle 6m", proposed and contested cards |
| teal | live | broadcast mark, "Live", "Following live" |
| purple | you are driving | driving bar, frame, Take Control and Give Back |
| none | booting, offline, finished, inconclusive | words and neutral symbols |

A proposed verdict is outlined (dashed) and neutral; only a closed one is filled with its
outcome colour, lightly when the coding agent accepted it (no human review). Materials: system sidebar
and toolbar, `.bar` under the screen and composer; cards use `fill.quaternary` with a 1 pt
separator stroke. No custom glass on content.

## Components

`RunRow` (lifecycle dot, task title 2 lines, time and counts, verdict pill),
`LifecycleDot`, `VerdictPill`, `RunHeader`, `StagePicker`, `ScreenWell`, `PlayerBar`
(play, speed, track with step ticks and failure marks, position, live button, take
control), `DrivingBadge`, `StepRow` (tool symbol, title, summary, duration; expands to
picture, input, output), `VerdictCard`, `EvidenceChip`, `MessageBubble` (sender avatar,
name, kind, time), `ToolCallGroup`, `EventLine`, `QuestionCard`, `Composer`,
`WorkingIndicator`, `OfflineView`, `WelcomeView`.

## States

| State | Treatment |
| --- | --- |
| Connecting (first load) | Sidebar shows a small progress row; detail says "Connecting to greenroom" |
| Daemon offline, nothing loaded | Detail: "Greenroom is not running" with the URL, the command to start it, Retry |
| Daemon offline, runs loaded | Everything stays readable; a banner above the detail says offline and retrying |
| No runs | Welcome: what a run is and how an agent starts one |
| No selection | "Select a run" with the count of runs today |
| Run loading | Header from the list's copy, stage shows progress, never a blank |
| Booting machine | Stage says the machine is starting, with elapsed time |
| Live, stream down | Recording shows; a status line under the track says why |
| No frames | "No recording" with why (not ready yet, or none captured) |
| Long transcript | Tool calls fold into groups; lazy stacks; new messages follow only when at the bottom, with a "Jump to latest" button otherwise |
| Many runs | Day sections, search by task, id, status, verdict |
| Errors | Daemon's own words, inline where the action was, and in the sidebar footer |

## Keyboard

| Keys | Action |
| --- | --- |
| Cmd-1 / Cmd-2 | Screen / Steps |
| Opt-Cmd-0 | Show or hide the conversation |
| Ctrl-Cmd-S | Show or hide the sidebar (system) |
| Cmd-R | Refresh |
| Cmd-L | Follow live |
| Shift-Cmd-T | Take control or give it back |
| Cmd-' / Shift-Cmd-' | Next or previous failed step |
| Esc | Back to the verdict after opening its evidence |
| Shift-Cmd-S | Capture a screenshot |
| Shift-Cmd-E | Export recording |
| Space, Left, Right | Play or pause, previous or next frame (Screen focused) |
| Return / Shift-Return | Send / new line in the composer |
| Up, Down | Previous or next run (sidebar focused) |

While driving, every key goes to the machine while the screen has focus. Clicking the
composer moves the keys back to the app; the switch, clicked, gives the screen back.

## Accessibility

- Every control has a label; status and verdict read as words ("Finished, pass,
  accepted").
- Pulses and the working indicator stop under Reduce Motion.
- Targets are at least 20 x 20 pt; toolbar items 28 pt.
- Colours are system semantic colours, checked in light, dark and Increase Contrast.
