# 0017. In-window menus are drawn by the app

Date: 2026-09-26
Update 2026-09-27: Superseded by 0019 (menus are the system's).
Status: accepted. Builds on 0005 (keys and the one registry), 0008 (type and the olive
brand) and 0013 (one primary and the More menu); issue #179.

## Context

ADR 0013 moved the top bar's occasional actions into one "More" menu. It was built as a
SwiftUI `Menu`, which AppKit draws as a native `NSMenu`: the system font, the grey system
highlight, the key hints squeezed into the shortcut column, the "rebuild" news of root ADR
0033 sitting where a shortcut goes, and Destroy Machine drawn like everything else. It was
the one place inside the window that did not speak the app's design language (ADR 0004,
0008): every other surface is Mona Sans and Monaspace Neon on the theme, with the olive
selection and keys shown as in the hint bar.

A native menu also lives outside the registry's keyboard model. Its keys are AppKit's, it
knows nothing of the hint bar, and its items were a second list in `RunView`, parallel to
the registry's.

The macOS menu bar is different: it is system chrome, outside the window, where a Mac
person looks for every command. It is already built from the registry (ADR 0005).

## Decision

1. **A menu inside the window is drawn by the app.** The More menu is `MoreMenuView`, a
   dropdown under its button: the theme's background with a hairline and a shadow, like the
   Cmd-K palette; titles in Mona Sans; keys through `KeyLabel`, as the hint bar draws them;
   the selected row filled with the brand, as the palette's and the selected run's.
2. **Its items come from the registry.** An entry says whether it is in the menu and in which
   section (`ActionSpec.more`: run, view, app, destructive). `MoreMenu.items` lists the
   entries that work now, in section order with a line between sections, each titled as the
   menu bar titles it (`MenuTitles`, shared with the menu bar, so "Hide Conversation" follows
   the state in both). Nothing in the views lists actions.
3. **Builds and Updates carries a badge**, from root ADR 0033's state (`MoreBadge`): "N new",
   "rebuild" or "mismatch", in the attention role, in words.
4. **Destroy Machine is set apart**: its own section after a line, its title and key in the
   failure role, and its selection filled with the failure colour instead of the brand. It
   still asks inline (ADR 0005), never in a dialog.
5. **Keys.** `.` opens it on a run (a registry entry, `more`), and so does a click. While
   open it owns the keyboard (`ActionContext.more`): the arrows (and Ctrl-N, Ctrl-P) move,
   Return or space runs the selection, esc or `.` closes, and an item's own key runs it
   (`c`, `e`, Cmd-Backspace), as a native menu's key equivalents would. Other bare keys are
   swallowed so nothing under it acts; other chords reach the menu bar. Opened by a key the
   first item is selected; opened by a click nothing is, until the pointer or a key moves.
   The hint bar shows its keys.
6. **It closes** on esc, on a click anywhere outside it (that click does nothing else), when
   an item runs, when the app goes to the background, and when the run changes.
7. **Accessibility.** The menu is one modal group labelled "More"; each row is a button
   named by its title and badge, with its key as the hint; the More button says whether it
   is open.
8. **What stays native**: the menu bar (system chrome), `NSSavePanel` for Export Recording
   (a system file dialog), and tooltips. The evidence previews on hover (`.popover` in
   `ChecklistViews` and `VerdictCard`) are pictures, not menus; they stay popovers for now,
   and redrawing them is a separate change. There are no context menus in the app.

## Consequences

- One list: adding an action to the menu is one field on its registry entry, and it shows
  in the menu, the palette, the help and the menu bar with the same title and key.
- The menu looks right in all four themes and is checked by the snapshot harness
  (scenarios 55 to 55f), which could never draw a native menu.
- We own what AppKit gave for free: outside clicks, type-ahead, VoiceOver's menu role. The
  dropdown does outside clicks and keys; it reads to VoiceOver as a group of buttons rather
  than a menu.
