# 0006. Desktop toolkit wave 1: refs, actionability, effects, waits

Date: 2026-09-27
Status: accepted. Implements root ADR 0037 decisions 3 to 5 and 7 (docs/21 sections 5.1 to 5.3,
5.5, 6) for wave 1 (#212), over the channel of daemon ADR 0005. Leaves the evidence-contract
changes of docs/21 section 7.4 to wave 4, except the minimum a verifier needs to cite the new
steps (point 12).

## Context

Root ADR 0037 fixed the shape: a snapshot with stable refs and visibility from real
hit-testing, actions by ref with actionability checks and auto-wait whose result is their own
effect, and waits and assertions as tools. docs/21 section 5 sketches the arguments. This
record fixes what that sketch leaves open: where each part runs, the op and tool contracts,
how refs, effects and the lease work, and how the wave-1 tools sit beside today's.

## Decision

### Who does what

1. **The agent does mechanics, the daemon does meaning.** The agent (Swift, in the guest) walks
   AX, keeps the ref table, computes visible rects and hit tests, runs the actionability loop,
   posts input, waits for the UI to settle and returns structured before and after trees. The
   daemon (`internal/desktop`, pure Go; `internal/machine` for I/O) turns them into what a
   model reads: the outline, the diff, the effect line, the refusal text, the step record. So
   wording and diffing change without a helper version bump (which costs every image a
   rebuild), and are unit-tested in Go. `logic/` in the helper is the part of the mechanics that
   needs no AX and is tested on the host.
2. **Package layout.** `internal/desktop` has no I/O and imports nothing of the daemon's: the
   wire types of the ops below, `Outline`, `Diff`, `Effect`, argument validation shared by MCP
   and the verifier, and the ink test over a captured PNG (moved from `machine/render.go`'s
   logic). `internal/machine` (`desktop*.go`) owns the calls: `Manager.Snapshot`, `Find`,
   `Press`, `Type`, `SetValue`, `Key`, `Scroll`, `WaitFor`, `Expect`, `ScreenshotOf`, each
   bounded, lease-checked and recorded. `mcpserver` and `verifier` only map arguments and
   results.

### Refs

3. **A ref is `e<n>`, per machine, per reader, per connection.** The agent keeps a table per
   reader (`coder`, `verifier`, `human`): ref to live `AXUIElement` plus a fingerprint (pid,
   the process's start time, window number, the role path with indices, `AXIdentifier`, role,
   name). Walking finds an element's existing ref by `CFHash` then `CFEqual`, so an element
   keeps its ref across snapshots, actions and waits. Using a ref:
   - live element: used;
   - dead (`kAXErrorInvalidUIElement`, or its pid now has another start time): re-resolved by
     fingerprint only when exactly one element of the same window (or app) matches, and the
     result says `reResolved`;
   - otherwise `stale_ref`, naming what happened: its window closed, its app quit, or it is gone
     from its window.
   A ref from an earlier connection is refused by the daemon (daemon ADR 0005 point 10). A
   table holds at most 20000 refs per reader; the least recently seen go first.

### Ops and results

All frames are guest points, top-left origin. `node` is one element:

```json
{"ref": "e41", "role": "Button", "subrole": "", "name": "Open run", "value": "", "desc": "",
 "help": "", "id": "", "states": ["disabled", "selected", "focused", "expanded", "editable", "busy", "secret"],
 "frame": [x, y, w, h], "vis": [x, y, w, h], "depth": 2, "window": "e1", "scroller": "e20",
 "covered": {"by": "e70", "role": "List", "name": "Runs", "where": "window|app|other", "app": "TipSplit"},
 "offscreen": "above|below|left|right", "clipped": "e20|window|screen",
 "scroll": {"x": null, "y": 0.62, "up": true, "down": true, "left": false, "right": false},
 "chars": 412, "cut": ["value"]}
```

Empty fields are left out. `role` has no `AX` prefix. `name` is the title, else the
description, else the placeholder. `vis` is the frame clipped by every ancestor scroll area,
the window and the screen, and is absent when nothing of it shows. `covered` comes from
`AXUIElementCopyElementAtPosition` at the visible center: a hit that is the element, a
descendant or an ancestor means nothing covers it; anything else covers it, named by its
nearest ref'd ancestor, `where` saying whether it is in the same window, another window of the
app, or another app. Strings longer than 240 characters are cut, `cut` names the field and
`chars` the full length, unless the ref is in the request's `fullText`. A secure text field's
value is never sent: `secret` in states and `chars` only.

A `tree` is `{"nodes": [node...], "attention": [...], "windows": ["e1", ...], "app": {...}}`,
the target app's windows in interactive mode, used as the before and after of actions and waits.

| Op | Args | Result |
| --- | --- | --- |
| `snapshot` | `app?`, `window?` (ref or title), `ref?` (subtree), `mode` (`interactive` default, `all`, `text`), `limit` (250, max 1000), `fullText: [ref]` | `screen`, `frontmost {name, bundleId, pid}`, `app {name, bundleId, pid, started}`, `focused?`, `attention: [{ref, kind: sheet\|alert\|dialog\|menu\|popover\|window, role, name, app, pid}]`, `nodes`, `truncatedBy?` (`limit`, `visited`, `depth`, `time`), `responding` |
| `find` | `text` (substring, or `/regex/`), `role?`, `app?`, `includeOffscreen` (default true), `limit` (50) | `matches: [node]`, `searched` |
| `press` | `ref` or `point [x, y]`, `button`, `count`, `mods`, `via` (`pointer` default, `ax`), `timeoutMs` (5000, max 30000) | `target` (node), `point`, `tried` (points tried), `via`, `checks: [{check, ms, detail?}]`, `waitedMs`, `notes: [text]`, `overlay?` (node hit after the input, when not the target), `before`, `after` (trees), `settled`, `settledMs`, `appGone` |
| `type` | `ref?`, `text`, `replace`, `submit` (`return`, `tab` or none), `timeoutMs` | as `press`, plus `typed`, `readBack?`, `readBackOK`, `secret` |
| `setValue` | `ref`, `value`, `timeoutMs` | as `press`, plus `readBack`, `readBackOK` |
| `key` | `key`, `mods`, `ref?`, `timeoutMs` | as `press` (focusing the ref by `AXFocused` when settable, else a press) |
| `scroll` | `ref` (a scroll container or anything inside one), `to`: `{"ref": e}`, `"top"`, `"bottom"`, `{"pages": n}` or `{"by": dy}`, `timeoutMs` | `container` (node), `from {x, y}`, `to {x, y}` (fractions or null), `atEnd`, `steps`, `via` (`axScrollToVisible`, `wheel`, `scrollBar`), `target?` (node after), `visible`, `before`, `after` |
| `waitFor` | `target`: `{"ref"}`, `{"text", "role?"}`, `{"app"}`, `{"window"}` or `{"idle": true}`; `state`: `appears`, `disappears`, `enabled`, `disabled`, `focused`, `changes` or `value`; `value?: {op: equals\|contains\|matches, expected}`; `timeoutMs` (max 40000) | `satisfied`, `elapsedMs`, `node?`, `value?`, `before`, `after` |
| `expect` | `target` (as waitFor), `property` (`value`, `name`, `exists`, `visible`, `enabled`, `selected`, `count`), `op` (`equals`, `contains`, `matches`, `atLeast`, `atMost`), `expected`, `timeoutMs` (default 2000, max 40000) | `passed`, `observed`, `elapsedMs`, `node?` |
| `capture` | `format` (`png`, `jpeg`), `quality`, `maxWidth`, `rect?`, or `ref?` with `margin` (24) | `width`, `height`, `scale`, `rect`; the image as a BLOB |

`ui`, `desktop`, `screen`, `input` and `sh` keep their old shapes (daemon ADR 0005 point 12).

Additions from the control catalog (docs/21a, its comment on #212):

- **W16.** A window node carries `document` (the `AXDocument` file URL, as a path) and
  `edited: true` (`AXEdited`) when the app reports them; the outline shows both on the window
  line.
- **M15.** A toolbar item that sits behind the overflow chevron (its toolbar has an
  `AXOverflowButton` and the item has no visible rect inside the toolbar) has the state
  `overflow`, and the outline says `[in overflow: press the toolbar's >> button first]`. It is
  not actionable until it shows.
- **I2.** `press {count}` is 1, 2 or 3. The pairs of one press carry
  `kCGMouseEventClickState` 1, 2, 3 in turn, so an app sees a double or triple click and not
  separate single clicks.
- **I9.** `type {via}` is `unicode` (default: each character as the event's Unicode string,
  exact whatever the keyboard layout, but no key equivalents and no input method) or `keys`
  (virtual key codes with shift, as a US keyboard sends them; a character with no key is
  `bad_request` naming it, before anything is typed).
- **I15.** Typing into a secure text field works while secure input is on (a trusted poster may
  post; taps cannot read). Its result and step never carry the typed text or the value: `typed`
  is `<secret, N chars>`, and the read-back compares lengths only.

Fixes from the first live run (navlab in a real VM, run `20260928-094238-2b407db80be568e5`):

- **Refs never repeat (B10).** A new agent connection starts its tables at `e1`, so a ref the
  caller kept from before a reconnect could name another element once a new snapshot passed
  the connection check. The daemon keeps, per machine and reader, the highest ref it has seen in
  any result, and before a reader's first toolkit call on a new connection sends the op `refs
  {reader, next}`, which raises that reader's counter (never lowers it; at most `e999999999`).
  An old ref is then one this reader does not hold. An agent without `refs` in its caps is
  refused as an older helper.
- **Before is read after the checks (B5).** An action's `before` tree is read once the checks
  pass, right before the input, so what changed during the auto-wait (a button becoming
  enabled) is not its effect.
- **Into view means where the screen shows it (B3).** Scrolling an element into view (the
  `scroll` op's `to: {ref}` and the visible check's) aims at the part of the container's view
  inside the screen's visible frame (no menu bar, no Dock), with the usual margin. When the
  container cannot bring it there (its window runs under the Dock), `visible` is false and
  `notes` says why; the press's hit test still decides.
- **Points on content are not refused (B8).** Point 6's "use e41" applies only when the hit is a
  control a press by ref reaches the same way (button, checkbox, radio, link, menu item,
  pop-up, tab, text field, slider, stepper, and a label inside one). A point on a line of a text
  area, a web area, a group or an image is pressed.
- **A rebuilt target is named by its current ref (B9).** When the agent re-resolves a ref to an
  element the walks know by another ref, the target node carries that ref, the lead line names
  it and a note says `e101 was re-resolved to e143`. When the old element still answers but the
  before tree lists the one element of its window with its role, name and frame under another
  ref, the note says to use that ref.
- **Scroll bars are the scroll area's (B4).** `AXScrollBar` is listed only in `all` mode, and its
  value never counts as a change: the area's `scroll` carries the position. A disabled scroll bar
  means its axis does not scroll, and a position within half a percent of an end cannot move on
  that way (B2, B6).

### Actionability

4. **Checks, in order, retried until the timeout**: attached (4.3's resolution), not behind a
   modal (the target app has a sheet, alert or open menu and the element is not in it),
   visible (an element out of view in a scroll area is scrolled into view first, as `scroll
   {to: ref}` does, and `notes` says so), enabled, stable (the same frame in two reads 50 ms
   apart), receives events (the visible center, then up to 8 more points of the visible rect
   on a 3x3 grid, hit-tested; the first that hits the element or a descendant is the point;
   static text and images also accept an ancestor), frontmost (a background app is activated
   and `notes` says so). Backoff 0, 20, 100, 100, 500 ms, then every 500 ms. A check that still
   fails at the timeout is `refused` with its reason and whatever names the cause (the
   coverer, the modal).
5. **The input is never repeated.** Only the checks retry. After the input the agent hit-tests
   the point again and reports an `overlay` that appeared, waits for the target app to settle
   (no AX notification and no change of the tree's signature for 300 ms, at most 2 s; AXObserver
   notifications wake a 150 ms poll) and reads the after tree.
6. **Points need a reason.** `press {point}` needs `reason`; a point whose hit element has a
   ref in the caller's table is refused with "use e41" (the coder may pass `force` to skip that
   rule, root ADR 0037's open question 6: coders are not held to the verifier's rule).
7. **Typing is paced and reads back.** `type` presses the ref to focus it (skipped when it
   already has focus), selects all first when `replace`, posts each character, waits for the
   window server to apply it and leaves at least `paceMs` (default 20, 0 to 100) between
   characters: an app that is busy drops keys that arrive faster than any hand types them
   (docs/14 case 5), and 20 ms a key is still 50 characters a second. It then presses `submit`
   and reads the element's value (waiting up to 1 s for
   it to settle) and compares: with `replace` the whole value, else that it contains the text.
   A difference is reported as `typed "120" but e4 shows "12"`, never retyped. A secure field
   compares lengths only.

### Effects

8. **Every action's result is its effect**, computed by the daemon from `before` and `after`:
   `changed` with the diff (elements that appeared, went away or changed name, value, states or
   visibility flags, by ref; windows, sheets and attention items that opened or closed),
   `no change` ("no change in <app> after <settle>"), `unverifiable` (no after tree: the app
   stopped answering, or has no AX content) or `refused` (an actionability check failed, with
   its reason). `appGone` makes it `changed` with "<App> is no longer running (it quit or
   crashed)". "changed" is never inferred from pixels. A step records it as
   `effect: {of: <its own step>, kind: changed|none|unknown}`; a refusal is the step's error.

### Tools

9. **Behind `serve -desktop-toolkit`**, MCP gains `machine_snapshot`, `machine_find`,
   `machine_press`, `machine_set_value`, `machine_wait_for` and `machine_expect`, and
   `machine_type`, `machine_key`, `machine_scroll` and `machine_screenshot` gain their new
   arguments while their old calls keep working: a call with only the arguments the tool took
   before (`machine_type` with `text`, `machine_key` with `key` and `mods`, `machine_scroll` with
   `x`, `y`, `deltaX`, `deltaY`, `machine_screenshot` with none) is the old call and behaves
   exactly as before, with no read-back and no effect; any new argument makes it a toolkit call
   (`desktop.ToolkitCall`). So `machine_type` without `ref` but with a new argument (`replace`,
   `submit`, `via`, `paceMs`, `timeoutMs`) types into the focus with read-back of the focused
   element, and `machine_key` without `ref` but with `timeoutMs` presses into the frontmost app
   with its effect; `machine_screenshot` with `ref`, `window` or `region` is a crop.
   `machine_ui`, `machine_click` and `machine_input` are unchanged. Without the flag nothing
   changes. The verifier gets the same set in its tool list (plus `question` on
   `machine_screenshot`, for its describer) and a prompt section on using it; wave 4 removes
   the old ones from it.
10. **Bounds.** Actions default to 5 s of checks and at most 30 s; waits and expects at most
    40 s; every call is capped at 45 s in the daemon, under the MCP client's 60 s timer with the
    headroom of `maxWait` (50 s).
11. **The lease.** Actions take the lease per call as `InputAs` does (root ADR 0009): refused
    while a human holds the screen (`ScreenTakenError`), the coder refused during a verifier
    turn (#82), the verifier refused after a handover until it looks again (#124): a
    `machine_snapshot`, `machine_find` or `machine_screenshot` is a look. Reads take no lease.
    **Takeover** (#212): an action holds its per-call lease for up to 30 s of auto-wait, so on a
    machine with a guest agent a seat that pauses the agent (a human) may take the screen from
    the coder or the verifier while they hold it. It is a fresh take for the human (a handover,
    the "control" event, PAUSE); the agent ends the action in flight with `paused`, which the
    action returns as the screen taken by that human, recorded as its step's error; the next
    action is refused while the human holds the screen and works after the give back. The
    preempted call's release never releases the human's lease. Without an agent a held screen
    is refused to everyone, as before.
12. **Recording.** Every call is a step, recorded by its reader (`by`), under its tool's name:
    `machine_snapshot` (the whole structured snapshot), `machine_find`, `machine_press`,
    `machine_type`, `machine_set_value`, `machine_key`, `machine_scroll`, `machine_wait_for`,
    `machine_expect` (expected, observed, elapsed, passed) and `machine_screenshot`. For ADR 0024
    the verifier's review counts `machine_snapshot`, `machine_find`, `machine_wait_for` and
    `machine_expect` as observations, the action tools as inputs, and reads an action's effect
    from its own step rather than from a UI read after it; the toolkit's actions have no
    separate effect read. The rest of docs/21 section 7.4 is wave 4's ADR.
13. **Guest text is data.** Outlines quote every guest string (`%q`); nothing in a result comes
    from anywhere but the guest and this repository.

## Consequences

- A verified action costs one call for the action and its effect, and a wait one call, instead
  of look, act, look.
- An actionability failure names its class (`covered`, `offscreen`, `disabled`, `unstable`,
  `modal`), so the bench and the live classifier count navigation waste directly instead of
  inferring it.
- Wave 1 does not have `attention` for system prompts from other processes beyond windows
  hit-testing names, menus, dialogs, windows, apps, drag, OCR, marks or before and after crops;
  those are waves 2 and 3.
- Some text clipping cannot be seen through AX (a SwiftUI label truncated by its own layout
  reports its whole string); `clipped` covers a frame cut by a container, the window or the
  screen, and `chars` with `cut` covers our own limit. `[not drawn]` stays the ink test.
