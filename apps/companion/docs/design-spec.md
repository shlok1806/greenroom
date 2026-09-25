# Design spec: Greenroom Companion

The companion is where a person watches an agent's run, judges its verdict and steps in.
It reads like a calm, mostly proportional product, Browserbase's session viewer more than
a terminal, with the terminal kept as an accent: a block cursor, mono chrome and data, key
hints, a glyph loader, a decoded verdict. Prose (verifier replies, notes, the task, the
verdict reason) sets in a proportional face so it is comfortable at length. The school is
still Charm's (Bubble Tea, Lip Gloss, Glamour) for the accent, not for the whole surface;
the app is not a terminal and does not pretend to be one.

Decisions: companion ADR 0004 (grid, type, colour, cursor, layout, rendering), 0005 (keys
and the action registry), 0006 (motion and signature moments), 0007 (dependency
allowlist), 0008 (readable type and the olive brand, amending 0004's type and colour
decisions), with 0002 and 0003 (run state, verdict trust, one vocabulary) still in force.
Data: `design/themes/*.json` and `design/tokens.json` at the repo root. Research:
`design-research.md`. Terms (run, verdict state, frame, lease, driving, cell, voice) are in
`../CONTEXT.md`.

This spec replaces the round-2 spec (system text styles, system colours, a
`NavigationSplitView` of three columns) and carries ADR 0008's readability revision. The
risky parts still open are marked **pending the prototype**. The prototype lives on branch
`prototype/glyph-ui` and is never merged; it exists to answer those questions and is then
thrown away. The "monospace prose at length" and "Radon or Argon" questions ADR 0004 left
pending are answered by ADR 0008 (proportional prose; the per-voice faces are dropped) and
no longer appear below as open questions.

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
Window (one layout, chrome on the mono grid, prose proportional)
├─ Runs: pinned "Needs you" and "Running", then days, newest first; / searches
├─ Run
│  ├─ Header row: the task (the run's name), state in words, time, hash
│  ├─ Stage
│  │  ├─ Screen: live stream or recording, cut into the layout as a panel
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

## Spacing, chrome and the accent (ADR 0008)

There is no strict character-cell grid over the whole window any more. In its place: a
4/8 pt spacing scale (`tokens.json` `spacing`), thin 1 px hairlines, quiet panels and
small radii (`tokens.json` `radii`: 4, 6, 8 pt). This is the Browserbase-like register:
generous whitespace, calm surfaces, chrome that gets out of the way of what is being read.

- **Spacing.** Every gap, inset and gutter is a multiple of the 4 pt unit
  (`tokens.json` `spacing.scale`: 4, 8, 12, 16, 24, 32, 48). A view asks for a step on the
  scale, never a bare point value.
- **Panels and cards.** A quiet panel is a flat fill with a 1 px hairline edge (dim, at
  rest) and a small radius (4 pt for a row or chip, 6 pt for a card, 8 pt for a sheet or
  the screen's frame). Box-drawing glyph borders (`┌─┐│└┘├┤`, rounded `╭╮╰╯`) are gone as
  the default frame for every pane; they no longer draw the whole window.
- **The mono grid still exists, narrower in scope.** Chrome and data (step and command
  rows, code, output, ids, times, the hint bar, small uppercase section labels) still
  measure from the base mono face (`tokens.json` `cell`: Monaspace Neon, 13 pt, line
  height 1.35), so those rows and the hint bar still line up cell to cell. It governs
  those elements and the layout breakpoints (Layout, below), not the whole window.
- **The terminal is the accent, not the surface.** What still reads as terminal: the
  block cursor, mono labels and key hints, the glyph loader and spinner, the verdict's
  decode-in, the boot reveal. Everything else (panels, the transcript's prose, the
  verdict card's body) is quiet chrome around that accent.
- **The one exception, as before,** is the live machine screen. It is real video at its
  own aspect ratio. It is framed as a panel cut into the layout: a hairline and an 8 pt
  radius run around the cut, and the gap between the picture and the frame is filled with
  background, never stretched video.

## Type: what is being read, not who is speaking (ADR 0008)

Two faces, both bundled with the app under the OFL: Monaspace Neon for chrome and data,
Mona Sans for anything read as sentences.

| Kind | Face | Used for |
| --- | --- | --- |
| Mono (`tokens.json` `type.mono`) | Monaspace Neon | Step and command rows, code, output, ids, times, key hints, small uppercase section labels, the hint bar, the verdict card's frame and headline |
| Reading (`tokens.json` `type.reading`) | Mona Sans, 14-15 pt, line height about 1.5 | Verifier replies, human notes, the task, the verdict reason, questions, empty-state copy |
| Reading heading (`tokens.json` `type.readingHeading`) | Mona Sans, a heavier and slightly wider cut | Section and card headings set in prose, at the same size as reading text |

- The five Monaspace voice faces (Xenon for the verifier, Radon with its Argon fallback
  for the human, Krypton for machine output) are gone. A line is no longer told apart by
  its typeface.
- **A speaker is a label and a colour, not a face.** Each message group is headed by the
  sender's name in words, and carries a thin coloured left edge (the sender's role
  colour) down its side. This is the only place a role colour is used for identity rather
  than status; it is not a pass/fail/attention colour and is not gated by the contrast
  test the same way, since it sits beside the text, not under it.
- A person may still override the mono face in settings (any monospace face; the mono
  grid re-measures). The reading face stays fixed.
- Markdown in messages renders in the reading face (Glamour style, proportionally):
  headings in the heavier reading-heading cut, emphasis italic, inline code and code
  blocks in the mono face, lists with glyph bullets, block quotes with a `│` rule.
- Reading text wraps at a measure that stays comfortable at the transcript's column width
  (340 to 560 pt beside the stage, at most 760 pt zoomed; a width, not a character count,
  since it is proportional).

## Colour: an ANSI theme plus a brand (ADR 0004, revised by ADR 0008)

A theme is a background, a foreground, 16 palette colours, a cursor and a selection, in
Ghostty's keys (`design/themes/*.json`), plus three Greenroom extensions beyond that
shape: `brand`, `brandText` and `chromeTint` (`tokens.json` `themeKeys` names the theme
keys, `greenroom-brand`, `greenroom-brand-text`, `greenroom-chrome-tint`). The first-party
themes are a neutral near-black and a warm paper, each with a high-contrast variant. Hue
is reserved for meaning and for the brand.

The Ghostty `cursor-color`, `cursor-text` and `selection-*` keys apply only inside the
terminal-screen well. The app's own cursor, selected run and active tab draw in `brand`
and `brandText`, never in those keys.

| Role | Slot | Meaning | Where |
| --- | --- | --- | --- |
| pass | 2, bluer emerald | a pass | block-letter outcome, "Pass, you accepted", small status marks (always with `✓` and the word "Pass") |
| failure | 1 red | a fail verdict, a failed step, a lost machine | FAIL, failed steps and their track marks, "Ended: machine lost" |
| attention | 3 yellow | needs you | "Needs review", "Contested", "Idle 6m", question cards |
| live | 6 cyan | live | "Live", the live mark on the track |
| driving | 5 magenta | you are driving | the hint bar while driving, the screen's border, the switch |
| dim | 8 bright black | secondary text | timestamps, hashes, counts, hairlines at rest |
| brand | `greenroom-brand`, olive | the product's identity, on large calm surfaces and actions | primary buttons, the selected run, the active tab, the block cursor, the wordmark `greenroom█`, a faint tint (`chromeTint`) on the sidebar and top bar |
| chromeTint | `greenroom-chrome-tint` | a faint brand tint behind chrome | the sidebar and top-bar backgrounds only; content areas (screen, steps, transcript) stay neutral so screenshots read true |
| none | foreground | everything else, booting, offline, finished, inconclusive | words |

- Roles use the normal ANSI slots 1 to 6, not the bright ones. In a light theme the
  bright slots are lighter and fall below the text threshold on paper; the normal slots
  hold in every theme, so one mapping serves all four. Bright slots are for imported
  shell output (SwiftTerm) and never carry meaning.
- **Pass moved to a bluer, brighter emerald** (hue about 158-160, versus the previous
  plain green around 126-134) so it cannot be mistaken for the olive brand. It is used
  only on small status marks, never on a large surface, and always with `✓` and the word
  "Pass".
- **Brand is olive**, about `#4B5320` in the light theme, lifted in dark so it reads on
  near-black, deeper again in light-hc for the button-label contrast that variant needs.
  It is deliberately kept off content areas: the screen, steps and transcript stay
  neutral so a screenshot of evidence reads true, not tinted by the brand.
- Olive and pass emerald are checked to be visibly different hues, not just different by
  the numbers: at least 25 degrees apart in HSL, per theme.
- Dim text is bright black. It is for secondary text only, never the only carrier of a
  state.
- Every state is also a word. Colour never stands alone.
- A proposed verdict's border is dim and its outcome is in the foreground; only a closed
  verdict takes its outcome colour. An agent-accepted outcome keeps its word but not its
  colour (ADR 0003).
- Emphasis is weight, inverse video or the foreground against dim, never a new hue.
- Contrast is measured, not judged by eye. `DesignDataTests` checks every theme: the
  foreground and every role, including dim, against the background at 4.5:1 (7:1 in a
  high-contrast theme; dim's own threshold was 3:1 before ADR 0008 raised it to match).
  It also checks `brand` against the background at 3:1 (a large-surface, non-text use),
  `brandText` against `brand` at the text threshold (4.5:1, 7:1 hc), `chromeTint` against
  the foreground at the text threshold (so chrome text stays readable on the tint), and
  the brand/pass hue distance. A theme that fails does not ship. The measured values are
  in ADR 0004 (the original palette) and ADR 0008 (this revision).
- Later: import a Ghostty or iTerm theme. It will not carry `brand`, `brandText` or
  `chromeTint` (Ghostty has no such concept); the app fills those three from a fixed
  default rather than refusing the import. The 16-colour palette still passes the same
  check or is refused with the failing role named.

## The cursor

The brand's mark is a block cursor in inverse video: `█`. The logo is `greenroom█`; the
icon is `█`. Since ADR 0008 the brand carries a colour (olive), and the cursor carries it:
its cell fills with `brand` and the glyph or row beneath draws in `brandText`, the same
pairing a primary button uses. The shape stays the signature; the colour now says which
product it is.

- The cursor marks keyboard focus. The focused row, card or step shows the cursor at its
  leading position, and the row is drawn in inverse video (brand-filled).
- It also marks the agent's attention. While the agent runs a step, a second cursor sits
  on that step in the timeline; while it writes, on the message being written. It
  **blinks** while the agent thinks and is **steady** while it acts.
- It moves by glide (a spring, `tokens.json` `motion.glide`).
- The cursor mirrors real focus. It never moves on its own where focus did not.

**Pending the prototype:** *cursor-follow.* A cursor that follows the agent may pull the
eye away from what the person is reading. If it does, the agent's cursor stays in the
timeline only (never in the transcript), and it stops following while the person is
scrolling or has focus in another pane.

## Layout: adaptive by width class (ADR 0004 decision 8, in points since ADR 0008)

The window's width picks one of three layouts. The breakpoints and pane sizes are points
in `tokens.json` `layout` (ADR 0008 lets widths be points rather than cells): wide at
1280 pt and more, medium from 960 pt, narrow under that. `Model/PaneLayout.swift` works
out what shows and where, from the width class, focus, zoom and what the person asked
for; three custom layouts (`Views/PaneLayouts.swift`) place the panes where it says.

**Every pane stays in the view tree in every arrangement.** A pane that is not shown is
placed under what shows, drawn at no opacity, takes no clicks and is hidden from
VoiceOver. Nothing is rebuilt by a resize or a zoom, which is what keeps the live screen,
the control lease, the player's place and a half-typed draft.

**Wide (1280 pt and more).** Closes issue #23.

```
  runs             |  screen                           |  conversation
 ------------------+------------------------------------+---------------------------
  Needs you        |                                    |   PASS   Needs review
  █ TipSplit       |          (picture, fitted)         |  ------------------------
  Running          |  ▶ ─────●────  Recording 4:17      |  transcript ...
  Today            | ----------------------------------- |
    ...            |  22 ✓ Clicked People +       39 ms |
                   | ▌23 ✓ Read TipSplit         648 ms |  > composer
 ------------------+------------------------------------+---------------------------
 ↑↓ runs  ⏎ open  z zoom  / search  tab pane  ? more
```

- Runs: a 280 pt column (dragged between 240 and 380). Screen and steps share the middle:
  Screen and Steps are no longer tabs. The screen takes the height its picture needs at
  the stage's width, up to what the steps list keeps (200 pt or 30% of the stage,
  whichever is more); the list takes the rest, so there is no empty band between them.
- The list follows the picture: the step at the playhead is marked (its number in the
  foreground, a quiet edge) and kept in view until the person moves through the list
  themselves. Clicking a step shows it on the screen.
- Conversation: 440 pt by default, dragged between 340 and 560, so the transcript keeps
  its measure at every width; every extra point goes to the stage, never to an empty
  margin. It gives way (the verdict card moves above the stage) only where it would take
  the stage under its 440 pt minimum.
- Hiding the runs (Ctrl-Cmd-S) folds them to the medium strip.

**Medium (960 to 1279 pt).**

- The runs fold to a 48 pt strip of status marks: one per run in the list's order, its
  state in the glyph, its title and state in the tooltip; a click opens that run. The
  strip's `»`, `g r`, esc and `/` open the whole list over the run (on a scrim); a click
  beside it, esc, or opening a run puts it away.
- The steps fold to a one-row track under the screen: a cell per step, errored ones in
  the failure role, the step at the screen's playhead full height in the foreground, and
  that step in words beside it (the hovered one while hovering). A click on a cell shows
  that step; "All Steps" or `g s` opens the list under the screen (half the stage), and
  "Fold" or `g v` folds it back.

**Narrow (under 960 pt, the window's minimum is 820).**

- One pane at a time: runs, stage or conversation. `tab` cycles them; the hint bar leads
  with the pane's name and says where `tab` goes (`tab → conversation`); a switch in the
  top bar names them for the mouse. The stage is the screen with the one-row track.
- The verdict card stays with the conversation, one pane away (above the stage it left
  the screen a thumbnail); `a` or `d` brings the conversation forward first.

**Zoom.** `z` zooms the focused pane to the whole window (tmux style): the runs, the
screen, the steps or the conversation (the stage's focused part, not the whole stage).
`z` again or esc restores it; esc restores before it backs out of anything else, and
focus moving to another pane restores too. The top bar (with Give Back) and the hint bar
stay. A zoomed conversation keeps a 760 pt reading measure, centred, with its rules
edge to edge. While zoomed the hint bar leads with `screen zoomed` and says `z restore`.

**Motion.** A change of width class, a zoom, the runs opening over the run and the help
move on the settle spring (`tokens.json` `motion.settle`, about 300 ms). Reduce Motion
makes each instant.

**Focus.** The pane with the keys shows it quietly, not only in the hint bar: a 2 pt
brand rule along its top edge (1 pt vanished into the hairline under the top bar), and
its label, where it has one ("Conversation", "26 STEPS"), in the brand as text
(`Theme.brandInk`). Drawn only where more than one pane shows.

**Help.** `?` opens the full help as a sheet over the panes, above the hint bar, at most
45% of the window's height (`layout.helpMaxShare`); it never pushes the panes up.

Rules:

- The thresholds and sizes are in `tokens.json`, not in views.
- A layout change never moves focus except to keep it on a pane that shows (the runs
  folded to their strip do not open over the run by themselves) and never drops a draft.
- The lease is never touched by layout: a covered screen keeps it, its input surface
  lets go of the keyboard, and its live stream stops until it shows again.
- Compare mode (two runs side by side) is later.

## Rendering

Approach C: real SwiftUI views, with small effect layers.

- Content is real SwiftUI text and views. Chrome and data snap to the mono grid through
  `GridMetrics`; reading text lays out at its own proportional metrics, on the spacing
  scale rather than in cells.
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
components. We port its components and interactions into SwiftUI, drawn in our spacing,
type and theme; its look (Inter, rounded cards, shadows, a blue accent) is not adopted.
Ported behaviour is credited under its MIT licence (ADR 0007). Plain words come first
throughout (ADR 0008): a row states what happened in a sentence a person would say, the
raw tool name, JSON or command is one expand (`⏎`) away, and a row carries at most two
secondary facts, so it stays scannable.

| Ours | Beautiful UI | Behaviour we port |
| --- | --- | --- |
| `StepsTimeline`, `StepTrack` | Thinking trace (`ThinkingState`, steps variant) | a step reads as a sentence ("Clicked Bill field", "Typed 120", "Took a screenshot") with at most two secondary facts (a duration, a state); a running step shows the tick spinner; done steps turn to muted checks (`✓` in dim); `⏎` expands a step to its raw tool name and JSON; the trace settles when the run stops working |
| `ToolCallGroup`, `ToolCallLine` | Tool Chips | one line per tool call in plain words ("Ran swift test"), state glyph, at most two secondary facts; the raw command is behind `⏎`; groups fold |
| `VerdictCard`, `QuestionCard` | Approval Card | the decision stated in plain words, the evidence under it, the actions named by what they do, the result replacing the actions after the choice |
| `ScreenWindow` | Agent Screen | a resting framed capture; open to a full viewer (our `z` zoom); "teach a task" becomes take control (`t`); a `REC` label while driving is recorded into the evidence; a connecting state inside the frame |
| `RunRow` | Task Rows | a row per run with running, failed and done states: tick, `✗`, `✓`, and the state in words |
| `Loader` | Loading State (Drive) | a 3 x 3 pixel grid with a chevron wavefront, drawn with block glyphs (`█` on `░`); a shimmering label; a live elapsed timer in tabular figures; Reduce Motion freezes the grid, the timer still ticks |
| `CommandPalette` | Search | a live filter as you type; an empty state that says what was searched and offers the nearest action |
| `MarkdownGrid` with `EvidenceChip` | Streaming Text | the reply streams at its real rate, in the reading face; inline citations become evidence chips that seek the step; follow-ups offered as registry actions |
| `ShellOutput`, `DiffView` | Code Block | code and output in the mono face; a unified diff with `+` and `-` in the pass and failure roles, never colour alone |

The rest, in our own terms:

- `GridMetrics` (environment, mono grid only), `SpacingScale` (the 4/8 pt steps),
  `Panel` (hairline edge, small radius, an optional title).
- `RunList`, `RunStrip` (medium: one glyph per run).
- `RunHeader` (task, state in words, time, hash).
- `SourceLabel` (live, connecting, recording 3:05 of 11:34, driving).
- `Transcript`, `MessageGroup` (sender in words, a coloured left edge, kind, time),
  `EventLine`.
- `Composer` (reading face), `Spinner` (tick), `Cursor` (brand-filled).
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
| `⏎` | Open the focused item (runs: into the run; steps: expand; transcript: show its step) | a pane |
| `esc` | Back out one level: help, evidence, then the pane, then the runs | anywhere but driving |
| `/` | Search runs | anywhere outside a text field |
| `tab`, `⇧tab` | Next or previous pane | a run open |
| `t` | Take control | a ready machine |
| `a` | Accept the verdict (`a` again answers "accept without opening the evidence?") | verdict open for review |
| `d` | Dispute the verdict (opens the reason) | verdict open for review |
| `u` | Undo the last accept or dispute | within 5 s |
| `space` | Play or pause | screen |
| `←` `→` | Previous or next frame | screen |
| `c` | Capture a screenshot | a ready machine |
| `e` | Export the recording | a run with frames |
| `n`, `N` | Next or previous step that errored | a run with errors |
| `g` then `v`, `s`, `t`, `r`, `c` | Go to the screen, the steps, the transcript, the runs, the composer | run open (`g r` anywhere) |
| `G` | Jump to latest (live on the screen) | screen, steps, transcript |
| `r`, Cmd-R | Refresh or retry | anywhere outside a text field |
| `?` | Expand the hint bar into full help, or collapse it | anywhere outside a text field |
| Cmd-K | Command palette: every action with its key | everywhere but driving |
| Cmd-L | Follow live | a ready machine, not live |
| Ctrl-Cmd-S | Show or hide the runs | anywhere |
| Cmd-Backspace | Destroy the machine (asked inline: `⏎` destroys, any other key keeps it) | a machine |
| `⏎`, Cmd-`⏎` / `⇧⏎`, `⌥⏎` | Send / new line | composer |
| `z` | Zoom the focused pane to the window; `z` again (or esc) restores | a run open |
| `m` | Click marks on or off | later layers |

The registry (`Model/ActionRegistry.swift`) is the source of truth since the registry PR
(ADR 0005, decision 6); this table follows it. Showing or hiding the conversation and the
themes have no key: they are in the palette and the View menu.

## Accessibility

We own accessibility; the system gives us nothing for custom chrome.

- **Final-state rule.** Assistive tech always sees the final state. A decoding or typing
  label is announced as its final text; effect layers are `accessibilityHidden`; the
  cursor mirrors real accessibility focus, never the other way round.
- **Words, not colour.** Every state and verdict reads as words ("Finished, pass, you
  accepted"). A sender's label is never the only sign of who spoke; the coloured left
  edge is a second cue, not the only one.
- **Reduce Motion.** Every change is instant, the spinner is a static `…`, the cursor
  does not blink, signature moments are skipped and their end state shown.
- **Contrast.** The high-contrast themes meet WCAG AAA 7:1 for the foreground and every
  role, dim included (ADR 0008 raised dim from 3:1 to match). `brandText` on `brand` and
  the foreground on `chromeTint` meet the same 7:1 in `-hc`, 4.5:1 otherwise; `brand`
  itself meets the 3:1 large-surface minimum against the background in every theme. The
  app follows Increase Contrast by switching to the high-contrast variant of the current
  theme. The contrast test gates every theme.
- **Targets.** Anything clickable is at least 44 x 20 pt, and at least as wide as three
  mono cells where it sits in the mono grid: a row that is one row tall takes the whole
  row's width as its target.
- **Labels.** Every control has an accessibility label and its registry key as the
  shortcut hint. Hairline panel edges and glyph bullets are hidden from VoiceOver.
- **Keyboard-only.** Everything the mouse does has a registry action, except giving back
  control while driving, which by design is a click.
- **Text size.** The mono cell size and the reading size both follow a text-size setting;
  the mono grid recomputes columns for chrome and data and may change layout, and reading
  text reflows at its own measure.
