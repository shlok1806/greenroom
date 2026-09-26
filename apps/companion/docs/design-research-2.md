# Design research, round 2: evidence review, live watching, keyboard flow

Gathered 2026-09-26 for the second design pass on the Greenroom Companion. Round 1
(`design-research.md`, 2026-09-22) covered the Mac platform and the first layout. This
round asks one question the checklist verdict (root ADR 0024, 0027) makes urgent: **what
does the best software do when a person must decide, fast, whether a machine's claim is
true?** It ends with a point of view for Greenroom (section 5), which the audit
(`ux-audit.md`) and companion ADRs 0011 onward apply.

Sources are marked [read] when the page itself was read for this round, and [known] when
the point comes from the source's well-known text but the page could not be fetched this
time (a 404, a 403, or a JavaScript-only page). Apple's HIG pages were read through their
JSON (`developer.apple.com/tutorials/data/design/human-interface-guidelines/<page>.json`).

## 1. Craft sources

### Apple Human Interface Guidelines (macOS)

- **Less modality on a big display.** macOS apps should "show more content with fewer
  nested levels and reduced modality" [read, designing-for-macos]. A verdict, its evidence
  and the recording belong on one screen together, not behind sheets.
- **Selection stays visible in every pane that leads to the detail** [read, split-views].
  A selected run, a selected check and the evidence it shows are three highlights at once.
- **One prominent action** per view, on the trailing side; every toolbar item also in the
  menu bar [read, toolbars; buttons]. A live run's top bar has one primary (Take Control);
  a verdict under review has one (Accept).
- **A show/hide or take/give command is one menu item whose title follows the state**
  [read, the-menu-bar]. Take Control and Give Back are one action.
- **Colour keeps one meaning and is never alone** [read, color; accessibility]. Status is
  a legitimate fixed colour in a sidebar (Mail's VIP yellow) [read, sidebars].
- **Hierarchy by weight and colour at 11 to 13 pt**, not by size: body 13, callout 12,
  subheadline 11, minimum 10 [read, typography]. Aim for 7:1 on small custom text in dark
  mode [read, dark-mode].
- **Motion: purposeful, brief, never on frequent interactions** (moving between checks
  must not animate) [read, motion]. No auto-dismiss of important information [read,
  accessibility].
- **Alerts only for essential, actionable information; not for undoable actions**; button
  titles are verbs of the result [read, alerts].

### WCAG 2.2

- 1.4.3: 4.5:1 for text, 3:1 large; 1.4.6: 7:1 [read]. Text the verifier read but a
  person cannot see is content when we show it, so it meets contrast too; its difference
  is a word, not a fainter grey.
- 1.4.11: 3:1 for state indicators and graphical objects needed to understand content:
  status glyphs, scrubber marks, outlines drawn over a screenshot [read]. 2.999:1 fails.
- 2.4.11 Focus Not Obscured, 2.4.13 Focus Appearance (a 2 px perimeter at 3:1 change)
  [read]. A floating player or banner must not cover the focused check.
- 2.5.8: targets at least 24 x 24 px, or spaced so a 24 px circle does not touch
  another [read]. A 60 x 18 pt evidence chip is under it; a whole row is not.

### Refactoring UI (Wathan and Schoger)

- "Hierarchy is everything": de-emphasize to emphasize; labels are a last resort; balance
  weight and contrast [read, table of contents]. "12 left in stock", not "In stock: 12"
  [read, labels-are-a-last-resort]: "Each pays read $8.00", not "Observed: $8.00".
- "Use fewer borders": spacing and background contrast instead [read].
- Two or three text colours, two weights; no grey text on coloured grounds [known].

### Laws of UX

- **Hick**: the decision is binary (accept or dispute); everything else supports it.
- **Fitts**: the action belongs near what is judged; keys beat both [read].
- **Doherty threshold**: under 400 ms keeps flow [read]; Nielsen's 0.1 s feels
  instant [read, nngroup response-times]. Selecting a check and seeing its evidence must
  not wait on the network when the frame is cached.
- **Von Restorff**: only the item that differs is remembered. If every row is coloured,
  none is. Failures and "not checked" earn a mark; a pass stays quiet [read].
- **Serial position, peak-end**: the verdict's outcome first, the decision last; the end
  of a review (the undo window, the closed card) and the peak (finding the failing
  moment) deserve the most care [read].
- **Tesler**: irreducible complexity is absorbed by the system [read]. The app, not the
  reviewer, works out which step shows which check and whether its text was drawn.
- **Aesthetic-usability**: attractive designs are forgiven [read]. In a trust tool that
  is a risk: polish must never make a pass look more certain than its evidence.

### Nielsen's heuristics

- #1 visibility of status: who has the screen, what the verifier is doing, how fresh the
  picture is. #3 an emergency exit and undo. #6 recognition over recall: show the claim
  next to the evidence so nobody holds the criterion in memory while looking at the
  picture. #8 every extra unit of information competes with the relevant ones [read].

### Automation bias (the research that matters most for a verdict)

- People follow an automated suggestion over contrary evidence (commission) and miss
  what the automation missed (omission); training reduces the first and not the second
  (Parasuraman and Manzey 2010; Mosier and Skitka) [read, summary on Wikipedia with its
  citations].
- LLM explanations are often unfaithful; sources belong next to the specific claim,
  because people "rarely click citation links"; confident, human-sounding language earns
  undeserved trust (NN/g, explainable AI) [read].
- For Greenroom: evidence beside each check, "not checked" as loud as a fail (omission is
  the dangerous error), plain observations instead of "I verified that", and friction in
  proportion to risk (accepting without opening any evidence asks first; the app already
  does this).

## 2. What the best tools do, and what applies here

| Product | What it does exceptionally well | What Greenroom takes |
| --- | --- | --- |
| **Xcode test report** (WWDC23 10175) [read] | UI test activities, a video of the run and a scrubber in one view; clicking an activity moves the video and the scrubber to that frame; a failure icon above the scrubber; bounding boxes over the frame that name each element | The check, its evidence and the recording move together; the failing moment is marked on the track; outline the element a UI read reports, dashed when it is not drawn |
| **Playwright trace viewer** [read] | Actions list beside a filmstrip; Before, Action (with the click point) and After snapshots; errors as red timeline marks; pick an element and see its locator | Evidence is a picture of the moment, with where the agent acted; the step list, picture and scrubber are one selection |
| **Chromatic** [read] | Per-change Accept and Deny with `a` and `d`; next and previous with Option-arrows; 1up, 2up and diff views; comments pinned to a spot | Our keys already match (`a`, `d`); add next and previous check; visual checks deserve picture-first review |
| **GitHub PR checks** [read] | Status and conclusion as separate axes; annotations at the line; "re-run failed jobs"; a skipped job reads as success, a known trap | Lifecycle, outcome and review state never share a word or a colour; "not checked" must never read as a pass |
| **Sentry Session Replay** [read] | Breadcrumbs that scroll with playback; the issue page embeds the replay at the error; share a moment | The run opens at the failing check's evidence; the steps follow the playhead (the app does) |
| **Cypress Test Replay** [read] | The same command log as local; time travel straight to the failure | Opening a failed run lands on the failing check, not the last frame |
| **Datadog Synthetics** [read] | Every step with its screenshot, duration and a link into the replay; the failed step highlighted | Evidence named by what it is, with its picture one hover away |
| **Linear** [read] | "Structure should be felt, not seen": fewer dividers, dimmed sidebar, fewer and smaller icons, no coloured icon chips; themes from three variables in LCH so high contrast is a setting, not a redesign; triage by single keys, decline with an optional reason; code review ordered "core of the change first" | Chips become text; the verdict leads with its core (the failures); single keys for review; contrast by theme data (the app already gates it) |
| **Raycast** [read] | A bottom action bar: where you are on the left, the actions with their keys on the right; primary on Return | The hint bar is this; it earns its place |
| **Superhuman** [read] | One palette everywhere, fuzzy with synonyms, every command shows its key; under 100 ms per action, preload the next item | Cmd-K stays; the next check's frame should already be decoded |
| **Things 3** [read] | Calm at any length; every animation keeps your place; Type Travel (start typing to go anywhere) | Motion only where it keeps place; `/` to find a run |
| **NetNewsWire** [read] | Fast, stable, accessible; single-key shortcuts; three panes | Runs, checks, evidence: the Mac's three-pane model, kept accessible |
| **Ghostty** [read] | Native components for native jobs; secure input awareness; window restoration | A driving mode that says plainly where keys go |
| **Warp** [read] | A command and its output as one block; failed blocks marked | A check and its evidence as one row |
| **Tower** [read] | Undo instead of confirmation | The 5 s undo on accept and dispute stays |
| **Browserbase** [read] | Live view read-only by default; takeover is explicit; a disconnect is its own message, never a frozen frame | Take Control stays explicit; a stream that is down says so under the picture (the app does) |
| **Vercel Geist** [read] | Colour scales by role (backgrounds 1 to 3, borders 4 to 6, text 9 and 10); tabular figures; loading states with a show delay; deep links to everything | Roles over hex (the app's themes do this); tabular times |
| **Stripe** [read] | Palettes in CIELAB where any two colours five steps apart pass text contrast | Contrast as a rule in data, tested (DesignDataTests) |
| **Figma Dev Mode** [read] | Focus view: one item at a time with its history | Review one check at a time over the screen |
| **Grafana** [read] | Annotations as marks and regions on a time series | Takeovers and cited steps as marks on the scrubber |

Arc and Craft were read only from their product pages, which say little about design
decisions; nothing here rests on them.

## 3. Slop: what makes an interface feel generated, and why it hurts

Each item is something to remove on sight.

1. **Card in card.** Cards are less scannable than lists and blur ranking (NN/g, cards
   [read]); borders add busyness (Refactoring UI [read]); "structure should be felt"
   (Linear [read]). A checklist is ranked and compared: rows.
2. **Chips and pills for everything.** A bordered chip per step number is a label
   dressed as a control. Indicators should be conditional (NN/g, indicators [read]).
   Only a state that needs the eye earns a mark.
3. **Gradients, glow, glass on content.** Translucency over varied content gives
   unpredictable contrast (NN/g, glassmorphism [read]); glow reads as alert and dilutes
   real status (Von Restorff).
4. **Emoji as UI.** They draw differently per OS version, carry arbitrary colour that
   breaks one-colour-one-meaning (HIG color [read]) and have no fixed meaning.
5. **Vague or magic copy.** "Something went wrong", sparkles, "Magic": no participant in
   NN/g's study read sparkles as AI (NN/g, sparkles [read]); errors must say what failed
   and what to do (NN/g, error messages [read]; HIG writing [read]).
6. **Confident machine voice.** "I verified that ..." earns trust the evidence did not
   (NN/g, explainable AI [read]). Say what was observed.
7. **Decorative motion.** Motion is for feedback, state change and navigation (NN/g,
   animation [read]); not on frequent actions (HIG motion [read]).
8. **Low-contrast grey text.** It hurts older readers and low vision, reads as disabled,
   and readers blame themselves (NN/g, low contrast [read]).
9. **Uniform weight.** When everything is bold or everything is grey, nothing leads
   (NN/g, visual hierarchy [read]).
10. **Unlabeled icons and icon soup.** Few icons are universal; hover-only labels fail
    (NN/g, icon usability [read]).
11. **Rows of equal buttons.** One primary per view (HIG buttons [read]); a row of five
    bordered buttons is a toolbar that forgot to choose.
12. **Modals and confirmation dialogs.** They hide the content needed to decide (a
    dispute dialog hides the evidence being disputed) and train people to click through
    (NN/g, modal and confirmation dialogs [read]). Undo instead.
13. **Placeholder as label** (NN/g, placeholders [read]).
14. **Skeletons that lie.** A skeleton for a frame that may never arrive, or one that does
    not match the final layout (NN/g, skeleton screens [read]; Vercel guidelines [read]).
15. **Colour-only status** (WCAG 1.4.1 [known], 1.4.11 [read]).
16. **Dead zones.** If it looks interactive it is interactive; a row that highlights but
    does nothing, or a small chip inside a large dead row (Vercel guidelines [read]).
17. **Signposts instead of controls.** "X is in the top bar" where X should be.
18. **Aesthetic over trust.** A pass drawn more beautifully than a fail inflates trust
    (aesthetic-usability plus automation bias).

## 4. What round 1 got right

The audit confirms these, and they stay: two faces chosen by what is read (Mona Sans for
sentences, Monaspace Neon for data); a neutral content area so evidence reads true; one
brand colour used sparingly; every state a word and a glyph; one vocabulary; the hint bar,
Cmd-K and single keys from one registry; the 5 s undo instead of confirmation; no sheets in
the verdict flow; four measured themes. These are exactly what the sources above
recommend, and several (the hint bar, undo, single keys) are what Raycast, Tower,
Chromatic and Linear converged on independently.

## 5. A point of view for Greenroom

**Greenroom is a lightbox with a ledger.** The screen is a lightbox: a neutral, dark well
where the only thing drawn over the machine's picture is what points at evidence. The
verdict is a ledger: ruled rows of claims, each with its mark, what was seen and where it
was seen. Everything else in the window is quiet so these two can be trusted.

### Principles

1. **Claim beside proof.** A check and the picture that shows it are on screen together.
   Selecting a check puts its words over its evidence on the stage; nobody holds a
   criterion in their head while looking across the window (Nielsen #6, NN/g on
   citations, Xcode test report).
2. **Scope before story.** The checklist leads; the verifier's prose follows, folded. A
   pass certifies the listed checks, nothing more (root ADR 0024), so what was not checked
   is as visible as what failed (automation bias: omission is the dangerous error).
3. **Say what the evidence is.** A screenshot is something a person saw; a UI read is the
   app's report of itself; text in a read that is not drawn is marked in words (root ADR
   0027). Evidence is named, never numbered alone.
4. **Colour is a decision, not decoration.** Hue only where a person must act or a state
   differs: failed, not checked, needs you, live, driving. A pass is a quiet mark. The
   brand is for the chosen thing (the selected run, the selected check, the primary
   action).
5. **One primary per place.** Accept in the verdict, Take Control in the top bar, Give
   Back where the driving is. Everything occasional is in the palette, the menu bar and
   one More menu.
6. **Keys for flow, words for state.** Every action is a key in the registry; every state
   is a word. Moving through checks is `[` and `]`, like Chromatic's next and previous.
7. **Plain, not clever.** Observations in the verifier's words, errors in the daemon's,
   our own copy in short sentences with a named actor (the Copy rules in `CLAUDE.md`).
8. **Motion keeps place or marks an arrival.** Nothing loops but a spinner; nothing
   animates on a frequent action.

### Visual direction

Kept from ADR 0004 to 0010, because the research supports it: the two faces, the olive
brand, the ANSI themes and their contrast gates, the hint bar, the glyph accents (the
block cursor, the loader, the decoded outcome). A new identity would cost recognition and
buy nothing for the three jobs.

Sharpened:

- **Rows, not boxes.** Inside the verdict, checks are ledger rows separated by space; the
  selected one carries a 2 pt brand edge, as the selected run does. Step chips become mono
  text links ("Screenshot 7 ↗"). One frame per decision, none inside it.
- **Figures in mono, claims in the reading face.** A check's criterion and observation are
  sentences (Mona Sans); its tally, kind and evidence names are data (Monaspace Neon). The
  eye reads the claim, then the numbers.
- **The tally is the glance.** `✗ 2 failed  ○ 1 not checked  ✓ 5 passed` beside the
  outcome answers "can I trust it" before a single row is read.
- **The stage speaks the claim.** Over the picture, the selected check's mark, criterion,
  kind and observation; under it, the player as before.
- **Quiet chrome.** One primary button in the top bar; the rest in a More menu. The
  sidebar row says the proposed outcome, not only that review is needed.

### What makes it unmistakably Greenroom

- The block cursor wordmark and olive on the chosen thing; everything else neutral.
- Data in Monaspace Neon beside prose in Mona Sans: a reviewer's notebook, half ledger,
  half sentence.
- The verdict that decodes in once, the machine that powers down into glyphs: the only
  two moments allowed to perform.
- A checklist whose every row can put its proof on the screen, with a key.
