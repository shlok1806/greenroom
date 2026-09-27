# 0019. A native SwiftUI redesign: verdict first, a checklist beside the picture

Date: 2026-09-27
Status: accepted. Supersedes 0004 (the glyph-native interface), 0005's hint bar (the keys
stay, shown in menus and Cmd-K), 0006 (signature moments), 0007 item 6 (code is now copied,
with notices), 0008 (type and the olive brand), 0009 (transcript cards as the main surface),
0010, 0012's row layout, 0017 (in-window menus drawn by the app). Amends 0002 (the derived
run state is the daemon's summary, root ADR 0036) and 0011 (the ledger becomes the page).
Keeps 0003 (one vocabulary, now the daemon's nine status words), 0013 (one primary), 0014
(evidence marks), 0015 (Paused with Continue), 0016 (only Verified is green) and 0018.

## Context

`docs/20-companion-ux-research.md` audited the window: 250 to 450 words on a screen, the
outcome in seven phrases across three regions, one timeline drawn four times, a terminal
costume. It recommended direction 2 (the verdict first, a checklist beside the picture) and
drew it in Figma (file "Greenroom Companion redesign", 041UmqtdMYVCufxO8g9Ius: Principles,
Wireframes, Mockups, Components, Tokens). Its section 9 recommended building the window as a
web UI in a thin native shell. The person who uses the Companion every day approved the
design and chose, explicitly, **pure native SwiftUI**: no web technology, no `WKWebView`, no
web package. This record states what is built and how, and overrules docs/20 section 9.

The brief: minimal but functional. The developer has everything important at once, never
scouring or clicking through buttons for a detail that matters; quick, easy to move through,
good to look at.

## Decision

1. **Pure native SwiftUI**, in the existing SwiftPM package. AppKit only where SwiftUI has
   no equivalent (the live H.264 layer, the guest's input surface, the window). No web view,
   no JavaScript, no web package, no TypeScript for the Companion. Root ADR 0035 (a web UI in
   a native shell) is not written.

2. **Direction 2**, as the Figma Mockups page draws it:
   - A runs sidebar grouped **Needs you, Running, Done** (the daemon's groups), each row one
     glyph, a name of five words or fewer and one short meta; "Show N more" folds Done;
     the footer says "2 of 3 Macs free".
   - A run pane: a toolbar (name, source and age; Activity, Message, More), a **header**
     (status glyph, status word, the tally or time, one line under it, one primary action
     top right and at most two secondary), then the **checks** column beside the **stage**
     (the evidence frame with its mark, the caption "Expected $50.00, saw $10.00", the
     filmstrip or key frames, Recording). The first failed check is selected by itself.
   - Activity (the transcript as task rows with tool chips) and the composer are one click
     or one key away, never on the default view.
   - Header states, not messages: Starting, Checking, Paused (Continue), Not answering
     (Restart the Mac, Keep waiting), Restarting, a resource warning banner, Passed, Failed,
     Stopped. Take control is a full-bleed screen with "Give control back".

3. **The daemon's summary is the state** (root ADR 0036). The status word, tone, group,
   the sentence under the status, "now", the tally, every check's row (`checks.items`, added
   for this ADR: text, state, expected, saw, observed, picture, mark) and the actions come
   from `GET /api/summary`, `GET /api/runs/{id}/summary` and the `summary` event. The app
   lays them out (`UI/Presentation.swift`: which glyph a state draws, how a count or a time
   reads) and derives no run state of its own where the summary covers it. Restart the Mac
   is `POST /api/runs/{id}/reboot`.

4. **Tokens are the Figma file's**, in `UI/Tokens.swift`: 23 colour tokens with Light and
   Dark values (contrast-tested: every text token 4.5:1 on every surface in both
   appearances, `text-tertiary` for disabled text only), the type scale (the system font,
   SF Pro, at 22/15/13/11 pt in three weights; the file draws Inter only because Figma lacks
   SF Pro), the 4/8 pt spacing scale, five radii, flat elevation with one raised level, and
   motion (120 ms press, 180 ms settle, 240 ms landing, ease-out cubic-bezier(0.23, 1, 0.32,
   1); only state changes move; Reduce Motion makes them instant). `design/tokens.json` and
   the themes stay for the old views until they go; nothing new reads them.

5. **Components are the Figma Components page**, each with every state on its states board:
   status glyph (8 shapes), buttons (primary, secondary, plain x default, hover, pressed,
   focused, disabled, loading), toolbar button, icon button, keycap, run row, check row,
   palette row, tool chip, task row, thinking, evidence frame with mark, filmstrip thumb,
   composer. Focus is a 2 pt ring 2 pt outside the control, keyboard only, never removed.

6. **Keys**: Up and Down move between runs, J and K between checks, A opens Activity, M the
   composer, E the evidence viewer, Cmd-K the palette, Return the primary action where it is
   safe; every action is also a labeled button, and the menu bar lists them. No always-on
   hint bar. Nothing destructive has a bare key.

7. **Code is reused, not re-invented, where the licence allows** (MIT, Apache-2.0, BSD, ISC,
   with the notice in `apps/companion/ACKNOWLEDGEMENTS.md`; never GPL, AGPL or BSL):
   Ghostty's command palette (MIT) for Cmd-K; Beautiful UI's Task Rows, Thinking state, Tool
   Chips and Shimmer (MIT), ported line by line into SwiftUI with the Figma file's sizes;
   Emil Kowalski's motion rules for the durations and easing. Packages are still an allowlist
   (0007 items 1 to 5 stand); copying a file is not a package. Pow and Inferno were
   considered: Inferno's effects are Metal shaders, which the Command Line Tools (the
   self-hosted runner and the guest) cannot compile, and nothing in the design needs Pow's
   effects, so neither is added.

8. **Measured, not asserted**: the snapshot harness renders every state at 1280 x 800 (and
   1024 x 680 and dark for the key ones) and reads each render back with Apple's text
   recogniser; a test holds each state to the docs/20 word budget (live 60, verdict waiting
   70, finished 50, booting 20, no run open 15, first launch 35). The sidebar stays smooth
   with 2,000 runs (a lazy, `NSTableView`-backed `List`), measured in the PR that builds it.

## Checklist

- [ ] Foundation: this ADR, tokens with contrast tests, the component set with every state,
      the summary client model (PR 1).
- [ ] Runs sidebar, 2,000 runs measured (PR 2).
- [ ] Run window: header, checklist, evidence, live screen, keys, palette (PR 3).
- [ ] States: paused, starting, not answering, restarting, resource warning, take control,
      Activity and composer, evidence viewer, empty, daemon offline, settings (PR 4).
- [ ] The old views, the glyph layer, the hint bar and their ADRs' code removed;
      `CLAUDE.md` and `CONTEXT.md` rewritten (PR 5).

## Consequences

- The window drops to about 60 words per screen, and every one of docs/20's five questions
  is answered in the header or the selected check.
- The bundled faces (Mona Sans, Monaspace Neon) and the ANSI themes go with the old views.
- A state the daemon adds is a new word and glyph mapping here, never a new rule.
- `checks.items` makes each check's proof the daemon's too, so a web dashboard later (if it
  comes) renders the same rows.
