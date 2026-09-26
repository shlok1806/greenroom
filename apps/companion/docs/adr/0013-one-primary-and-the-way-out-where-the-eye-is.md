# 0013. One primary in the top bar, and the way out where the eye is

Date: 2026-09-26
Status: accepted. Amends 0003 decision 6 and 0004's note on it (Take Control / Give Back
exists once, in the top bar).

## Context

The audit (`ux-audit.md`, L1, L2, P3, P5) found:

- A live run's top bar held five bordered buttons of equal weight (Screenshot, Export,
  Destroy..., Hide Conversation, Take Control). The HIG asks for one prominent action per
  view; Raycast and Linear keep occasional actions behind one menu and a palette.
- While driving, the magenta bar over the screen said "Give Back is in the top bar": a
  signpost to the one control that ends the mode, about 150 pt from where the person is
  looking (Fitts; NN/g on weak signifiers).
- "Only errors" showed disabled on runs where nothing errored.
- The welcome's command was cut by a hidden horizontal scroll with no sign more followed.

## Decision

1. **The top bar keeps one primary**: Take Control (or Give Back while driving). Capture
   Screenshot, Export Recording..., Show or Hide Conversation and Destroy Machine... move
   into one "More" menu beside it, each named as the menu bar names it with its key.
   Every one of them is still a registry action with its key, in Cmd-K and in the menu
   bar (ADR 0005); only the button row goes. Destroy still asks inline in the top bar and
   the hint bar, never in a dialog. While a recording exports, "Exporting" with a spinner
   shows beside the menu.
2. **Give Back is on the driving bar too.** The bar says "You have control", where keys go,
   and offers Give Back as a button. The top bar's Give Back stays, because the driving bar
   is covered whenever the screen is (a zoom, a narrow window's other pane) and the lease
   must always have a way back. Both perform the same registry action; there is still one
   control, in two places a person looks.
3. **A filter that can do nothing is not offered.** "Only errors" shows only when a step
   errored, or while it is on.
4. **A command block shows the whole command**, wrapped, with Copy beside it.

## Consequences

- The top bar reads as the product's chrome, not a toolbar: the wordmark, one action and a
  menu.
- Hide Conversation is one more click with the mouse; it is also in the View menu and
  Cmd-K.
