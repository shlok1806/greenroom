# 0009. Input goes to the app it aims at: focus first, and never click what covers an element

Date: 2026-09-28
Status: accepted.

## Context

Issue #220, and the demo run `20260928-203834-5538bd4dba20639d`. Input is posted at the HID
tap, so keys go to the frontmost app and a click goes to whatever is on top at its point.
Neither is always the app the caller means:

- An app started through `machine_exec` (`./App &`, a bare binary) opens its window behind the
  frontmost app. macOS 14 and later ignore `activate(ignoringOtherApps:)` from an app the user
  did not start. Reproduced on :7777 in run `20260928-212146-43fa22b15717b63b`: Calculator
  started as a bare binary showed its window, Finder stayed frontmost, `machine_key 7` was
  acknowledged (`actions: 1`) and Calculator's display stayed `0`, while `machine_ui` called
  its keypad focused.
- A control can lie under something that is always on top. In the demo run the verifier clicked
  Calculator's Equals by element at (0.252, 0.934): that point is under the Dock, so the click
  opened Finder from the Dock (steps 37, 38: "the frontmost app is now Finder (was
  Calculator)"). Finder's window then covered Calculator, and the next clicks by element
  (All Clear, steps 40 to 47) went to Finder too. Each read of `machine_ui` with `app
  Calculator` still gave the same centers, so the verifier repeated the same clicks.

The desktop toolkit already brings an element's app to the front and hit-tests before it acts
(`bringToFront`, `hitTest` in the helper). The input path every daemon without
`-desktop-toolkit` uses, including :7777, did neither.

## Decision

1. **A `focus` input action.** `{type: focus, app | pid}`: the helper unhides the app, sets it
   frontmost through accessibility (`AXFrontmost`, which works for a background caller where
   `NSRunningApplication.activate` is declined), un-minimizes and raises its window (the one
   under the action's point when it has one, else its main window) and waits up to 1 s for the
   window server to put it in front. An app it cannot find or bring forward fails the batch
   before anything else in it is posted, with the app that stayed in front.
2. **A click by element aims at the element, not only at its center.** `machine_click` with
   `element` (the coder's and the verifier's) posts `focus` with the element's app, then the
   click with that app's pid and the element's size. The helper hit-tests the center; if another
   process owns that point, it clicks the visible point of the element nearest the center (a
   5 by 5 grid over the frame), and if none is visible the batch fails before the click with
   what covers the element ("Equals is covered by Dock"). A click by x and y is posted as
   before: the caller aimed at a point, not an element.
3. **Keys and text can name their app.** `machine_type`, `machine_key` and `machine_scroll` take
   an optional `app` (a name or bundle id, as `machine_ui` does) and focus it first; without it
   they go to the frontmost app as before. `machine_input` takes `focus` as an action type.
4. **The descriptions say it.** `machine_exec` says an app it starts is not frontmost; the input
   tools say to pass `app` or click an element first.
5. `inputHelperVersion` is 10: the helper has a new action. Images from before it compile the
   helper at boot until they are rebuilt, as for every helper change.

## Consequences

- Keys sent with `app` reach an app started by `machine_exec`; a click by element on a window
  behind another app raises it first instead of clicking the other app.
- A control under the Dock or another always-on-top surface is clicked where it shows, or the
  click is refused and says what covers it, instead of acting on the cover. Calculator's bottom
  row under the Dock reads as covered; the caller can press its key or move the window.
- Focus costs a few milliseconds when the app is already in front, and up to 1 s when it is not.
- The verifier's own key and type tools do not take `app` yet: it clicks a field by element
  first, which focuses it.
