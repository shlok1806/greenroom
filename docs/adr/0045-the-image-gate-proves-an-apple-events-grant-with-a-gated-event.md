# 0045. The image gate proves an Apple Events grant with an event TCC gates

Date: 2026-10-01
Status: accepted

Supersedes ADR 0038 point 5's Calculator query (`tell application "Calculator" to get name`).
The rest of ADR 0038 stands. Fixes issue #282.

## Context

ADR 0038 point 5 added a `check-image` exercise that scripts Calculator, an app outside
`base.sh`'s old fixed list, to prove the build-time enumeration granted it. It asked for the
app's name. ADR 0044's "Measured" section found that AppleScript answers `get name` from the
bundle and sends no event, so the exercise passes whether or not the grant exists.

Measured on clones of `greenroom-base-v10-r4` through `tart exec`, with both Calculator rows
(tart-guest-agent and sshd-keygen-wrapper, user and system TCC.db) deleted and `tccd` restarted:

- `get name`, `get version`, `get frontmost`, `activate` and `quit` all succeed at once with no
  prompt, whichever of AppleScript or TCC lets each through.
- `count windows` launches Calculator and raises "tart-guest-agent wants access to control
  Calculator" (a `UserNotificationCenter` window at layer 8), blocking until killed.
- With the rows, `count windows` answers in about a second with Calculator's own error,
  "every window doesn't understand the count message" (-1708): Calculator has no scripting
  dictionary, so no gated event gets a plain answer from it.
- The System Events exercise (`get name of first process`) is sound: without its rows it raised
  the same prompt for System Events on a clean desktop and blocked.

## Decision

The Calculator exercise sends `count windows` and counts it answered when `osascript` exits 0
or its whole output is Calculator's own error, "Calculator got an error: ..." ending in (-1708).
That -1708 is the app's own reply, sent only after TCC let the event through; a -1708 that
AppleScript raises itself, or one followed by other output, fails.
Anything else fails the image, including a denial (-1743) and the watchdog's exit 124 on a
prompt, which the window check then also reports. `quit-calculator` still follows it and works
without the grant, so Calculator's window never outlives the exercise.

A unit test fails any `appleevent-` exercise that tells an app to `get name`, `get version`,
`get frontmost`, `activate` or `quit`, the answers measured to need no grant.

## Consequences

- `check-image` fails an image that lacks the Calculator rows, as ADR 0038 point 5 intended.
  An image built by the current recipe has them, so no rebuild and no `imageRecipeVersion`
  bump is needed.
- Accepting -1708 ties the exercise to Calculator staying unscriptable. If a later Calculator
  gains a dictionary, `count windows` answers with a number and still passes.
