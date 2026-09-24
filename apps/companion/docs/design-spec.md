# Design spec: Greenroom Companion

The companion is where a person watches an agent's run, judges its verdict and steps in.
It is glyph-native: a character-cell grid, monospace type, box-drawing borders and an ANSI
theme, in a native app with real springs, real video and a real mouse. The school is
Charm's (Bubble Tea, Lip Gloss, Glamour); the app is not a terminal and does not pretend
to be one.

Decisions: companion ADR 0004 (the grid, type, colour, cursor, layout, rendering), 0005
(keys and the action registry), 0006 (motion and signature moments), 0007 (dependency
allowlist), with 0002 and 0003 (run state, verdict trust, one vocabulary) still in force.
Data: `design/themes/*.json` and `design/tokens.json` at the repo root. Research:
`design-research.md`. Terms (run, verdict state, frame, lease, driving, cell, voice) are in
`../CONTEXT.md`.

This spec replaces the round-2 spec (system text styles, system colours, a
`NavigationSplitView` of three columns). The risky parts are marked **pending the
prototype**. The prototype lives on branch `prototype/glyph-ui` and is never merged; it
exists to answer those questions and is then thrown away.

## Who and what for

| User | Top tasks |
| --- | --- |
| Engineer whose agent is running | Watch the live screen; see what the agent is doing right now; answer a question; take control to get past a dialog |
| Reviewer judging a verdict | Read the verdict and its reasons; open the cited evidence (step, screenshot, frame); accept or dispute |
| First-time user | Understand what a run is, what the verifier is, and what they are allowed to do |
| Anyone | Find a run again; compare today's runs by outcome at a glance |

The order of tasks shapes the layout: the screen and the verdict are on screen together,
the conversation explains both, and the steps are the evidence behind them. On a wide
window the steps sit under the screen as a timeline, so all four are visible at once.

## Information architecture

```
Window (one grid)
├─ Runs: pinned "Needs you" and "Running", then days, newest first; / searches
├─ Run
│  ├─ Header row: the task (the run's name), state in words, time, hash
│  ├─ Stage
│  │  ├─ Screen: live stream or recording, cut into the grid as a window
│  │  └─ Steps: a timeline under the screen (wide), a one-row track (medium)
│  └─ Conversation
│     ├─ Verdict card, pinned: outcome, state and who decided, evidence, accept / dispute
│     ├─ Transcript: task, replies, tool calls (grouped), questions, events
│     └─ Composer: note or task to the verifier
└─ Hint bar: the keys that work here, from the action registry
```

- A run is named by its task (`RunSummary.task`). The id's hash is a secondary label.
- Evidence is linked both ways: a tool call, a step, an evidence chip or a verdict moves
  the screen and the timeline to that step. Nothing needs a copy-paste of a step number.
- The verdict lives in one place with its actions (the pinned card). The transcript keeps
  its history; older verdicts read as superseded.
- Screen and Steps are no longer tabs. They share the stage and move together.

## The grid

Everything sits on one character-cell grid.

- The cell is measured from the base face (`tokens.json` `cell`: Monaspace Neon, 13 pt,
  line height 1.35). A `GridMetrics` environment value holds the cell size, rounded to
  whole backing pixels so rows never drift. At 13 pt a cell is about 7.8 x 18 pt.
- Sizes, gutters, insets and pane edges are whole cells. There are no point values in a
  view: a view asks `GridMetrics` for `n` columns or rows.
- Panes resize in cell steps. A divider drag snaps to the next column.
- All text is monospace, prose included. Headings are bold or coloured, never larger:
  one size on the whole grid.
- Borders are real box-drawing glyphs (`┌─┐│└┘├┤`, rounded `╭╮╰╯` for cards) drawn in cells.
  A border takes a cell; a pane's inner width is its width minus two.
- **The one exception** is the live machine screen. It is real video at its own aspect
  ratio and breaks the grid. It is framed as a window cut into the grid: the grid's
  border runs around the cut, and the gap between the picture and the border is filled
  with background, never stretched video.

## Type: one face per voice

Monaspace (OFL, bundled with the app). Its five faces share metrics, so a line can change
voice without moving the grid.

| Voice | Face | Used for |
| --- | --- | --- |
| Chrome | Neon | Runs, headers, hint bar, labels, verdict card frame |
| Coder | Neon | The coding agent's messages |
| Verifier | Xenon (slab) | The verifier's messages and verdict text |
| Human | Radon (handwriting) | The person's messages and drafts. Fallback: Argon |
| Machine | Krypton | Tool output, shell output, step input and output |

- A person may override the chrome and coder font in settings (any monospace face; the
  grid re-measures). The verifier, human and machine voices stay fixed, because the voice
  is how a line says who spoke.
- A voice is never the only sign of the speaker: each message group is headed by the
  sender's name in words.
- Markdown in messages renders onto the grid (Glamour style): headings bold and coloured,
  emphasis italic, code in the machine voice, lists with glyph bullets, block quotes with a
  `│` rule. Nothing changes size.

**Pending the prototype:**

- *Monospace prose at length.* Long verifier replies and task text may read worse in a
  monospace face than in a proportional one. If they do, the fallback is a 72-column
  measure, a looser line height for prose rows (whole rows only, 1.5 instead of 1.35) and
  more paragraph spacing. Monospace stays; a proportional face would break the grid and
  is not a fallback.
- *Radon or Argon for the human.* Radon may read as too cute beside the others. If it
  does, the human voice is Argon, and `tokens.json` `type.human` changes. Nothing else
  moves.

## Colour: the palette is an ANSI theme

A theme is a background, a foreground, 16 palette colours, a cursor and a selection, in
Ghostty's keys (`design/themes/*.json`). The first-party themes are monochrome: a neutral
near-black and a warm paper, each with a high-contrast variant. Hue is reserved for
meaning.

| Role | Slot | Meaning | Where |
| --- | --- | --- | --- |
| pass | 2 green | a pass | block-letter outcome, "Pass, you accepted" |
| failure | 1 red | a fail verdict, a failed step, a lost machine | FAIL, failed steps and their track marks, "Ended: machine lost" |
| attention | 3 yellow | needs you | "Needs review", "Contested", "Idle 6m", question cards |
| live | 6 cyan | live | "Live", the live mark on the track |
| driving | 5 magenta | you are driving | the hint bar while driving, the screen's border, the switch |
| dim | 8 bright black | secondary text | timestamps, hashes, counts, borders at rest |
| none | foreground | everything else, booting, offline, finished, inconclusive | words |

- Roles use the normal slots 1 to 6, not the bright ones. In a light theme the bright
  slots are lighter and fall below 4.5:1 on paper; the normal slots hold in every theme,
  so one mapping serves all four. Bright slots are for imported shell output (SwiftTerm)
  and never carry meaning.
- Dim text is bright black. It is for secondary text only, never the only carrier of a
  state.
- Every state is also a word. Colour never stands alone.
- A proposed verdict's border is dim and its outcome is in the foreground; only a closed
  verdict takes its outcome colour. An agent-accepted outcome keeps its word but not its
  colour (ADR 0003).
- Emphasis is weight, inverse video or the foreground against dim, never a new hue.
- Contrast is measured, not judged by eye. `DesignDataTests` checks every theme: the
  foreground and every role against the background at 4.5:1 (7:1 in a high-contrast
  theme), dim at 3:1 (7:1). A theme that fails does not ship. The measured values are in
  ADR 0004.
- Later: import a Ghostty or iTerm theme. An imported theme passes the same check or is
  refused with the failing role named.

## The cursor

The brand is monochrome, and its mark is a block cursor in inverse video: `█`. The logo
is `greenroom█`; the icon is `█`.

- The cursor marks keyboard focus. The focused row, card or step shows the cursor at its
  leading cell, and the row is drawn in inverse video.
- It also marks the agent's attention. While the agent runs a step, a second cursor sits
  on that step in the timeline; while it writes, on the message being written. It
  **blinks** while the agent thinks and is **steady** while it acts.
- It moves by glide (a spring, `tokens.json` `motion.glide`).
- The cursor mirrors real focus. It never moves on its own where focus did not.

**Pending the prototype:** *cursor-follow.* A cursor that follows the agent may pull the
eye away from what the person is reading. If it does, the agent's cursor stays in the
timeline only (never in the transcript), and it stops following while the person is
scrolling or has focus in another pane.

## Layout: adaptive by cell columns

The window's width in columns picks one of three layouts (`tokens.json` `layout`).

**Wide (200 columns and more).** Closes issue #23.

```
╭ runs ─────────╮╭ screen ─────────────────────────╮╭ conversation ───────────╮
│ Needs you     ││                                  ││ ╭ verdict ────────────╮ │
│ █ TipSplit    ││         (live video)             ││ │ PASS  Needs review  │ │
│ Running       ││                                  ││ ╰─────────────────────╯ │
│   Login flow  │╰──────────────────────────────────╯│ transcript ...          │
│ Today         │╭ steps ───────────────────────────╮│                         │
│   ...         ││ 14 ✓ click "Split"          0.4s ││                         │
│               ││ 15 ✗ type "48.00"           1.2s ││ > composer              │
╰───────────────╯╰──────────────────────────────────╯╰─────────────────────────╯
 ↑↓ runs  ⏎ open  / search  t take control  ␣ play  ? more
```

- Runs: 32 columns. Screen and steps share the middle column; the timeline takes the rows
  under the screen. Conversation: 56 to 80 columns, so the transcript keeps its measure at
  every width and the extra width goes to the screen.

**Medium (140 to 199 columns).**

- Runs collapse to a strip of status glyphs, 3 columns wide: one glyph per run with its
  state in the glyph and the title on focus or hover.
- Steps shrink to a one-row track under the screen: one cell per step, failures in red,
  the playhead as the cursor.

**Narrow (under 140 columns).**

- One pane at a time: runs, stage or conversation. `tab` cycles. The hint bar names the
  current pane and the next one (`tab → conversation`).

**Zoom.** `z` zooms the focused pane to the whole window (tmux style); `z` again restores
the layout. Zoom settles on a spring. While zoomed the hint bar says `z restore`.

Rules:

- Widths are whole columns. The thresholds are in `tokens.json`, not in views.
- A layout change never moves focus and never drops a draft.
- Compare mode (two runs side by side) is later.

## Rendering

Approach C: real SwiftUI views snapped to the grid, with small effect layers.

- Content is real SwiftUI text and views laid out in cells through `GridMetrics`.
- Each effect is a small Canvas or Metal layer owned by one component: decode, draw,
  spinner, dither, cursor. An effect never owns content. The text exists as real text
  first; the effect draws over it and then gets out of the way.
- Accessibility sees the final state. A decoding title is announced as its final text,
  the dither is `accessibilityHidden`, the cursor mirrors real focus.
- Live video stays on `AVSampleBufferDisplayLayer` (ADR 0011).
- Own window chrome: no system toolbar or sidebar. The hint bar and Cmd-K replace the
  toolbar and most of the menu bar's discovery. The standard menu bar stays, and every
  item in it comes from the action registry.

## Motion

Text moves in steps; space moves in springs. Timings are in `tokens.json` `motion`.

| Motion | What it does | Budget | Used for |
| --- | --- | --- | --- |
| decode | characters scramble, then settle left to right | 300 ms at most | new status text, titles, the verdict outcome |
| draw | a box border traces itself | 250 ms at most | a card or pane appears |
| type | characters arrive at the stream's real rate | the stream's own rate | streaming messages, never faked |
| tick | braille spinner `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`, 80 ms a frame | while it lasts | inline working or thinking: one step, one message |
| load | 3 x 3 block-glyph grid (`█` on `░`), chevron wavefront, 650 ms cycle; a shimmering label; an elapsed timer | while it lasts | a pane waiting on long work: boot, "verifier is working", connecting |
| glide | spring, response 0.25 s | about 250 ms | the cursor |
| settle | spring, response 0.3 s | about 300 ms | resize, zoom, expand |

- Motion never blocks. Navigation is instant; the animation plays over the new state.
  A key pressed mid-animation acts on the final state.
- Under Reduce Motion every change is instant, the spinner is a static `…`, the loader's
  grid freezes dim and its label stops shimmering. The elapsed timer still ticks: it is
  information, not decoration.
- Nothing loops except the spinner and the thinking cursor's blink.

## Signature moments

These may exceed the motion budget. Each is short and plays once per event.

1. **Boot.** The screen window shows the daemon's real boot events as terminal lines
   (clone, boot, ssh, ready). The first frame resolves out of an ASCII and dither rendering
   into real pixels over about 600 ms. **Pending the prototype:** if the reveal looks slow
   or cheap, the first frame appears directly and the boot lines stay.
2. **Take control.** Everything but the screen dims ("house lights"), the cursor slides
   into the screen and becomes the pointer, and the hint bar turns magenta. Give back
   reverses it.
3. **Verdict lands.** The outcome decodes in as big block letters (PASS or FAIL, figlet
   style on the grid) and the card's border draws around it. On FAIL the cursor glides to
   the first failing step.
4. **Click marks.** Where the agent clicked or typed, a cell-shaped ripple shows for about
   800 ms. `m` turns them off and on. When the recording is paused on a step, that step's
   click shows as a static mark. This amends the old rule "nothing is drawn over the
   screen" (ADR 0006): click marks are the only thing drawn over the picture.
5. **Power-down.** On destroy the video dissolves into glyphs and freezes as a dithered
   still. The still becomes the run's thumbnail.
6. **Welcome.** The empty state types the `greenroom█` wordmark and the one command that
   starts the daemon.

## Components

Beautiful UI (research section 5) is the reference inventory for agent-interface
components. We port its components and interactions into SwiftUI, drawn in our grid,
type and theme; its look (Inter, rounded cards, shadows, a blue accent) is not adopted.
Ported behaviour is credited under its MIT licence (ADR 0007).

| Ours | Beautiful UI | Behaviour we port |
| --- | --- | --- |
| `StepsTimeline`, `StepTrack` | Thinking trace (`ThinkingState`, steps variant) | a running step shows the tick spinner; done steps turn to muted checks (`✓` in dim); the trace settles when the run stops working and stays expandable per step |
| `ToolCallGroup`, `ToolCallLine` | Tool Chips | one line per tool call: name, short summary, state glyph; groups fold |
| `VerdictCard`, `QuestionCard` | Approval Card | the decision stated in plain words, the evidence under it, the actions named by what they do, the result replacing the actions after the choice |
| `ScreenWindow` | Agent Screen | a resting framed capture; open to a full viewer (our `z` zoom); "teach a task" becomes take control (`t`); a `REC` label while driving is recorded into the evidence; a connecting state inside the frame |
| `RunRow` | Task Rows | a row per run with running, failed and done states: tick, `✗`, `✓`, and the state in words |
| `Loader` | Loading State (Drive) | a 3 x 3 pixel grid with a chevron wavefront, drawn with block glyphs (`█` on `░`); a shimmering label; a live elapsed timer in tabular figures; Reduce Motion freezes the grid, the timer still ticks |
| `CommandPalette` | Search | a live filter as you type; an empty state that says what was searched and offers the nearest action |
| `MarkdownGrid` with `EvidenceChip` | Streaming Text | the reply streams at its real rate; inline citations become evidence chips that seek the step; follow-ups offered as registry actions |
| `ShellOutput`, `DiffView` | Code Block | code in the machine voice; a unified diff with `+` and `-` in the pass and failure roles, never colour alone |

The rest, in our own terms:

- `GridMetrics` (environment), `CellFrame` (a view sized in cells), `BoxBorder` (glyph
  border with an optional title in the top edge).
- `RunList`, `RunStrip` (medium: one glyph per run).
- `RunHeader` (task, state in words, time, hash).
- `SourceLabel` (live, connecting, recording 3:05 of 11:34, driving).
- `Transcript`, `MessageGroup` (sender in words, voice face, kind, time), `EventLine`.
- `Composer` (human voice), `Spinner` (tick), `Cursor`.
- `HintBar`, `HelpOverlay` (`?`, the hint bar expanded in place).
- `OfflineView`, `WelcomeView`.

## States

| State | Treatment |
| --- | --- |
| Connecting (first load) | Runs pane shows `⠋ connecting`; the stage shows the loader, "Connecting to greenroom" |
| Daemon offline, nothing loaded | Stage: "greenroom is not running", the URL, the command to start it; `r` retries |
| Daemon offline, runs loaded | Everything stays readable; a dim line at the top of the stage says offline and retrying, with a tick |
| Daemon answers with an error | Not "not running": the daemon's words (status and text), advice when needed, `r` try again |
| Open run gone from the daemon | Selection clears; "No run open" says the run is no longer on the daemon; nothing of it stays actionable |
| No runs | Welcome: the wordmark types itself, then what a run is and the command an agent uses to start one |
| No selection | "Select a run" with the count of runs today |
| Run loading | Header from the list's copy; the stage ticks; never blank |
| Booting machine | The screen window shows the loader ("booting machine", elapsed), then the boot lines |
| Live | `live` in cyan beside the source label; the track's live mark in cyan |
| Live, stream down | The recording shows; a dim line under the track says why |
| No frames | "No recording" with why (not ready yet, or none captured) |
| Agent working | The loader under the transcript ("verifier is working", elapsed); a tick on the running step; the agent's cursor blinks |
| Needs you | The run pins under "Needs you"; its state word in yellow; the hint bar offers the action |
| Verdict proposed | Card border dim, outcome in the foreground, "Needs review" in yellow; `a` and `d` active |
| Verdict closed | Outcome in its colour (unless agent-accepted), who decided in words; `a` and `d` absent |
| Undo window | After accept or dispute, the hint bar shows `u undo 5s` counting down; nothing is sent until it ends |
| Driving | House lights down, the screen's border and the hint bar magenta: `all keys → machine · click switch to return` |
| Typing | The hint bar shows `⏎ send  ⇧⏎ newline  esc leave`; bare keys type |
| Zoomed | One pane fills the window; the hint bar shows `z restore` |
| Destroyed | Power-down still as the thumbnail; "Ended: destroyed by you" in words |
| Long transcript | Tool calls fold into groups; lazy stacks; new messages follow only at the bottom, `G` jumps to latest otherwise |
| Many runs | Day sections; `/` searches by task, id, state, verdict |
| Errors | The daemon's own words, inline where the action was |

## Keyboard

All keys come from one action registry (ADR 0005). Each action has a name, a key, the
context it works in and whether it is enabled now. The hint bar, the `?` help, the Cmd-K
palette and the menu bar all read the registry. A test fails if a shortcut exists outside
it.

**The three key rules:**

1. **Nothing destructive has a bare key.** Destroy is Cmd-Backspace and asks to confirm
   inline.
2. **Accept and dispute act only on a verdict open for review**, and each shows a 5 s undo
   in the hint bar before it is sent.
3. **Modes own the keyboard.** A typing context swallows bare keys. Driving is a hard
   mode: every key, Cmd-Q included, goes to the guest, and only clicking the switch
   exits.

| Keys | Action | Context |
| --- | --- | --- |
| `j` `k`, `↑` `↓` | Previous or next item | runs, steps, transcript |
| `⏎` | Open the focused item | anywhere outside a text field |
| `esc` | Back out one level: composer, then run, then runs | anywhere but driving |
| `/` | Search runs | anywhere outside a text field |
| `tab` | Next pane (narrow: cycles the one visible pane) | anywhere outside a text field |
| `t` | Take control or give it back | a ready machine |
| `a` | Accept the verdict | verdict open for review |
| `d` | Dispute the verdict | verdict open for review |
| `u` | Undo the last accept or dispute | within 5 s |
| `space` | Play or pause | stage |
| `←` `→` | Previous or next frame | stage, paused |
| `c` | Capture a screenshot | a ready machine |
| `g` then `s` | Go to steps | run open |
| `g` then `c` | Go to the conversation (composer) | run open |
| `G` | Jump to latest | transcript, steps |
| `z` | Zoom the focused pane, or restore | run open |
| `m` | Click marks on or off | stage |
| `r` | Refresh or retry | anywhere outside a text field |
| `?` | Expand the hint bar into full help, or collapse it | anywhere outside a text field |
| Cmd-K | Command palette: every action with its key | everywhere but driving |
| Cmd-Backspace | Destroy the machine (confirm inline) | a live machine |
| `⏎` / `⇧⏎` | Send / new line | composer |

The keys in the decision record are fixed. `u`, `G`, `r`, `g c`, `←` `→` and `tab` outside
narrow are this spec's choices; the registry PR may change them, and the registry is then
the source of truth.

## Accessibility

We own accessibility; the system gives us nothing for custom chrome.

- **Final-state rule.** Assistive tech always sees the final state. A decoding or typing
  label is announced as its final text; effect layers are `accessibilityHidden`; the
  cursor mirrors real accessibility focus, never the other way round.
- **Words, not colour.** Every state and verdict reads as words ("Finished, pass, you
  accepted"). A voice face is never the only sign of the speaker.
- **Reduce Motion.** Every change is instant, the spinner is a static `…`, the cursor
  does not blink, signature moments are skipped and their end state shown.
- **Contrast.** The high-contrast themes meet WCAG AAA 7:1 for text and every role. The
  app follows Increase Contrast by switching to the high-contrast variant of the current
  theme. The contrast test gates every theme.
- **Targets.** Anything clickable is at least 3 cells wide and one row tall, and at least
  20 x 20 pt: a row that is one cell tall takes the whole row's width as its target.
- **Labels.** Every control has an accessibility label and its registry key as the
  shortcut hint. Box-drawing borders and glyph bullets are hidden from VoiceOver.
- **Keyboard-only.** Everything the mouse does has a registry action, except giving back
  control while driving, which by design is a click.
- **Text size.** The cell size follows a text-size setting (11 to 18 pt); the layout
  recomputes columns and may change layout.
