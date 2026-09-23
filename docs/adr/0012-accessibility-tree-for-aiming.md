# 0012. Agents aim clicks from the accessibility tree, not from a described picture

Date: 2026-09-22
Status: accepted

## Context

The verifier's reasoning model (ADR 0005) reads text only. `machine_screenshot` reaches
it as prose from a separate vision model, whose prompt never asked for positions. So
every click the verifier made was a guess at a fraction of a picture it had never seen.

A live demo showed the cost. Asked to click the "25%" segment of a SwiftUI segmented
picker at about (0.60, 0.47), it clicked between (0.80, 0.45) and (0.98, 0.45) for ten
minutes. The misses landed on the wallpaper, where macOS's "Click wallpaper to reveal
desktop" hid every window, which the verifier then could not tell from a crash. It also
read the app's source and started second copies of the app, both of which the coder had
told it not to do.

The guest already has what a precise answer needs. The image grants Accessibility to
tart-guest-agent (`images/scripts/greenroom-tcc.sh`, `docs/02-spike.md`), the input
helper inherits it because the agent starts it, and every macOS control a person can
click is in its application's AX tree with a frame in global points: the same points
CGEvent posts in.

## Decision

1. The input helper gains a third mode, `greenroom-input --ui-base64 <{"app","limit"}>`.
   It walks the frontmost application's windows (or a named application's, matched by
   name or bundle id), focused window first, and prints each on-screen element: role,
   subrole, title, description (label), value, help, identifier, enabled, selected,
   focused, and its frame clipped to its window and scroll areas. It skips the menu bar,
   anything with no visible area, and pure layout (groups, rows, cells) that carries no
   text of its own, while still descending into it. A radio button's or checkbox's 0/1
   value becomes `selected`. Output stops at `limit` elements (default 250, max 1000),
   or at the walk's own caps (5000 elements visited, depth 40), and says `truncated`
   with `truncatedBy` (`limit`, `visited` or `depth`); the outline says which. It sets `AXManualAccessibility` so Chromium and Electron build
   their trees.
2. `Manager.UI` runs it through the same one-shot exec as input, records a `machine_ui`
   step with the whole tree, and converts each frame to fractions of the screen the
   helper reported: center `x`, `y` and size `w`, `h`. Only the manager converts between
   points and fractions, as for input (ADR 0009). No lease: it only reads.
3. `machine_ui` is a tool for both the verifier and the MCP server. Both return an
   indented outline, one element a line, with an id and its center:
   `[10] RadioButton/Segment label="25%" center (0.596, 0.467) size 0.047x0.031`.
   The MCP result also carries the structured tree.
4. `machine_click` also takes `element`, an id from the caller's own latest
   `machine_ui`, and clicks its center. Trees are kept per machine and reader (the
   coder over MCP, the verifier), so one reader's read never retargets the other's ids
   (issue #35). An optional `uiStep`, the step of the read the id came from, refuses a
   click when that is not the caller's latest read. The result names the tree's app and
   step. The tree is not re-read; the id is only as fresh as the read, and the tool says
   so. x and y still work for content with no AX tree.
5. The verifier's prompt makes machine_ui the way to aim: read the tree before a click,
   click element centers, read it again after, never click where it lists nothing. The
   vision model is asked for approximate centers of interactive elements, as a fallback
   for canvases, games and web views with no accessibility. A coder's explicit
   constraints ("use the UI only", "do not rebuild or relaunch") are hard rules.
6. Boot and `prepare-image` turn off "Click wallpaper to reveal desktop"
   (`com.apple.WindowManager EnableStandardClickToShowDesktop`), window restore at
   login, display and system sleep (`pmset -a displaysleep 0 sleep 0`, via passwordless
   sudo), the screensaver (`idleTime 0`) and the screen lock (`machine/desktopprefs.go`),
   beside the screen-capture approvals. A sleeping guest display makes every capture
   black with no error. The lock is changed only if it is on, because `sysadminctl`
   needs the admin password; the script passes the Cirrus base image's default,
   `admin`, which a different base would have to match.

Alternatives rejected: sending the screenshot to a multimodal reasoning model (not
available on the NIM models ADR 0005 chose, and still imprecise for small controls);
set-of-mark overlays drawn on the screenshot (the vision model still has to read the
marks, and the result is prose again); AX actions (`AXPress`) instead of clicks (they
skip the event path a user takes, so they would verify less).

## Consequences

- `inputHelperVersion` 4, then 5 for `truncatedBy`. An image built before it compiles the helper once on first
  use (~28 s); `scripts/build-image.sh` bakes it.
- Apps with no or poor accessibility (custom-drawn views, games) still need screenshot
  positions. The verifier is told to fall back to them only then.
- A tree is a snapshot. A click by element after the window moved lands where the
  element was; the verifier re-reads after every action.
- The tree is evidence too: every read is a step, so a verdict can cite the exact value
  a label had.
