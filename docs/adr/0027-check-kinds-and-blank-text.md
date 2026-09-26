# 0027. Checks have kinds, and the UI tree marks text that is not drawn

Date: 2026-09-26
Status: accepted. Extends ADR 0024.

## Context

ADR 0024 makes every verdict answer declared checks with fresh evidence. It says nothing
about what kind of evidence a check needs. The first bench baseline (ADR 0025, dev split,
before 0024) has exactly two wrong results so far, and both are false passes that 0024
alone would not stop:

- `tipsplit-each-pays-invisible` (visual_only): "Each pays" is drawn in the window's
  background colour. The accessibility tree still reports `Each pays: $49.56`. The verifier
  read the tree and passed in 3 steps. Any evidence that is only a tree read passes a
  "visible" claim that is false.
- `todolist-late-add` (late_result): an added item appears 8 s after Add; the task says
  "at once". The verifier saw the item and passed. Under 0024 the item seen after 8 s is
  fresh evidence, so the pass would still be posted. Nothing checks when the observation
  happened relative to the action.

The research names both: grounding in the tree alone misses what is rendered (Screen2AX,
`docs/13` section 3), and "grade outcomes, not claims" needs the outcome as claimed,
including when (`docs/13` section 9, P0 item 2).

## Decision

1. **Every declared check has a kind.** `declare_checks` takes `kind` per check:
   - `value` (default): a text, number or state; any observation can show it.
   - `visual`: appearance on screen (visible, readable, shown, colour, size, position,
     layout). Needs at least one `machine_screenshot` step among its evidence, after its
     actions.
   - `timing`: something happens within a time (`at once`, `immediately`, `within 3 s`).
     Takes `within` in seconds (default 2). Needs an evidence observation that started no
     later than `within` seconds after the end of the check's last action and shows the
     expected state. A later observation cannot pass it. An observation inside the window
     that does not show the state is evidence for `fail`.
2. **The daemon upgrades a kind, never downgrades it.** A criterion whose words claim
   appearance (visible, shown, displayed, readable, see, hidden, colour, bold, large) is
   treated as `visual`; one whose words claim speed (at once, immediately, instantly,
   right away, straight away, the moment, as you type, within N s) as `timing`, with N if
   given. The verifier may declare a stricter kind; it cannot declare a weaker one. The
   applied kind is posted with the checklist and on the verdict, so reviewers see it.
3. **The UI tree marks text that is not drawn.** When `machine_ui` reads a window, the
   daemon captures the screen once and, for each element that carries text (static text,
   labels, values, titles) and has an on-screen frame, measures the contrast inside the
   frame. An element whose frame holds no ink (pixel spread below a threshold) is reported
   with `rendered: "blank"`, and the outline shows it (`[not drawn]`). Elements outside the
   screen or covered by another window are reported as `offscreen` or `covered`. The tree
   then stops asserting text that a person cannot see, for every check, not only visual
   ones.
4. **The review rejects evidence that contradicts itself.** A `visual` or `value` check
   cannot pass on an element the latest read marks `blank`, `offscreen` or `covered`.

## Consequences

- A visual check costs one screenshot, and every UI read one capture plus a contrast pass
  over its text elements (tens of milliseconds for the pass; the capture is the cost).
- Timing checks depend on the effect read (0024) taken right after each input, which is
  about 1 to 2 s later. A `within` below about 2 s cannot be met reliably and is raised to
  2 s, with a note.
- Keyword upgrades can over-classify ("see that the total is right"). Over-classifying
  only asks for more evidence; it never lets a false claim through.
- Contrast is a heuristic: text drawn in a colour very close to its background, or on a
  busy image, may be marked blank or missed. The bench's visual cases and new unit tests
  on real captures set the threshold.
- Persistence claims ("kept between launches") get no kind yet: the baseline got every
  persistence case right. Revisit if the bench shows a miss.
