# 0009. Transcript cards and the Markdown renderer

Date: 2026-09-25
Status: accepted. Builds on 0003, 0004, 0007 and 0008 and the spec's Components and
States; settles what they leave open for the transcript layer. Nothing in them changes.

## Context

Layer 5 of the redesign draws the conversation's cards: a spoken message, a question, a
tool call, the verdict pinned above the transcript and its one line in the history. The
spec names the behaviour to port (Beautiful UI's Approval Card, Tool Chips and Streaming
Text) and the type (Markdown in the reading face, Glamour style). Round 2 cut messages
into headings, code and prose by hand (`RichText`) and let `AttributedString` do inline
Markdown, so lists, quotes, tables and nesting read as raw text. A few things the spec
does not settle came up while building it.

## Decision

1. **swift-markdown parses; we draw.** `Model/Markdown.swift` (`MarkdownText`) is the only
   file that imports it. It turns a message into our own blocks (heading, paragraph, code,
   list, quote, table, rule) and styled spans; `Views/MarkdownView.swift` draws them in the
   two faces. Pinned `exact: "0.9.0"`, and `Package.resolved` is now checked in (ADR 0007's
   consequence; the package `.gitignore` had kept it out). swift-markdown declares Swift 5
   language mode for its own module. ADR 0007 names it in the starting set, so that stands;
   our code that uses it is Swift 6 with strict concurrency, and it is behind one file.
   Exit plan: `MarkdownText.blocks` is the seam; a hand parser could fill it again.
2. **A single line break stays a line break**, as a comment on GitHub reads. Agents write
   screen text and short lists one per line with no blank line between; CommonMark would
   run them into one paragraph. Smart punctuation is off: an agent's `--` stays `--`.
3. **Emphasis is Mona Sans's real italic.** `MonaSans-Italic` and `MonaSans-SemiBoldItalic`
   (v2.0.27 static, OFL, the same release as the faces already bundled) join the fonts.
   Headings stay at reading size in the wider cut (ADR 0008); inline code and code blocks
   are Monaspace Neon; tables are a mono grid; a picture shows its words (the app reads
   nothing but the daemon); raw HTML shows as code.
4. **JSON is laid out for reading.** A code block with no language or `json`, and a tool
   call's input and result, print one key per line with sorted keys and `"key": value`,
   numbers as their shortest form (`0.2`, never `0.20000000000000001`).
5. **Inline citations are evidence chips.** "step 16", and each number of "steps 4, 5 and
   6", in prose (never in code or a link) become a chip that seeks the step, only when the
   run's record holds that step: a chip that seeks nothing is worse than words. The chip
   is a link with the app's own `greenroom-step:` scheme, handled by `MarkdownView`'s
   `openURL`; it never leaves the app or reaches the daemon. In the verdict card it seeks
   the way the card's evidence does, so it counts as opening the evidence.
   Any other link opens only for `http`, `https` and `mailto`
   (`MarkdownText.openableURL`): messages quote untrusted screen and web text, so a link
   to a file or another app's scheme stays words.
6. **The verdict card's frame** (`VerdictAppearance`), reading the spec's "a proposed
   verdict's border is dim ... only a closed verdict takes its outcome colour" with ADR
   0003's "an agent-accepted outcome keeps its word but not its colour":
   - Proposed or contested (open for review): a dim edge, the outcome in the foreground.
   - Accepted by a person: the outcome and a 1 px edge in the outcome's role. A line, not
     a fill, so pass stays off large surfaces (ADR 0008).
   - Accepted by the coding agent, and **rejected**: the quiet hairline and the outcome in
     the foreground. The spec's States row says a closed verdict takes its colour; a
     rejected one is closed, but painting a rejected Pass emerald claims a result nobody
     stands behind, so it is read like the agent-accepted case.
   - An inconclusive outcome has no colour, so its edge stays a hairline.
7. **The outcome is set in mono bold capitals** ("✓ PASS"): the static end state of the
   verdict's decode-in (spec, Signature moments). Figlet-style block letters and the draw
   of the border belong to the motion layer, which may enlarge it.
8. **The Approval Card's result replaces the actions.** Once a person has closed the
   verdict the card says what they did where the buttons were ("You accepted this pass
   verdict at 20:12. It is closed."). A question card has the attention role on its edge
   while open; once answered it says who answered and when, and that the answer follows
   below, instead of repeating it.
9. **A tool call row** (`ToolCallRow`) is the sentence (`StepSummary.phrase`), a state
   glyph (a dim `✓`, or `✗` and the failure role), and at most two facts: the step number,
   then its duration. A call the daemon refused before it became a step ("error: ..." and
   no step) is a failure too. The raw call behind the caret is labelled parts: Tool,
   Input, Error, Result. Runs of more than four calls fold to the last three.

## Left for later layers

- **Keys.** In the transcript `⏎` keeps the registry's meaning (show the row's step);
  opening a tool call's raw call has no key yet. It should get one through the registry
  (ADR 0005), for example `space` or `→` on a focused tool row, with `←` to close.
- **Streaming.** The daemon sends a message whole, so there is nothing to stream yet; the
  `type` motion (ADR 0006) arrives with a daemon that streams and with the motion layer.
- **Follow-ups offered as registry actions** under a reply: none yet.
- **Acknowledgements.** The app has no acknowledgements page yet; when it gets one it
  lists swift-markdown (Apache 2.0) beside Beautiful UI (MIT) and the two faces (OFL).
