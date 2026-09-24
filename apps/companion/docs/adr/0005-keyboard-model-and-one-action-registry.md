# 0005. A keyboard model with bare keys, and one action registry

Date: 2026-09-23
Status: accepted. Builds on ADR 0004. Replaces the keyboard table of the round-2 spec.

## Context

The round-2 window put every command behind Command chords (Cmd-1, Opt-Cmd-0,
Shift-Cmd-T, Command-') and the system toolbar and menu bar. That is the Mac default, and
it hides the product's keys: nobody finds Shift-Cmd-T without reading the menu. With
custom chrome (ADR 0004) there is no toolbar at all, so discovery has to be designed.

The people who use greenroom live in terminal tools where single keys act and a footer
says which ones work here (Bubble Tea's `help`, lazygit, k9s), and in apps like Raycast
and Linear where a palette lists every action with its key. Two risks come with bare
keys: a stray key doing something that cannot be taken back, and a key reaching the app
when the person meant to type it.

Shortcuts are also easy to scatter: a `.keyboardShortcut` on a button here, an
`onKeyPress` there, a menu item somewhere else. Then the help, the palette and the real
keys drift apart.

## Decision

1. **One action registry.** Every action is one entry: name, key (or key sequence),
   context, and whether it is enabled now (from `RunFacts` and focus). The registry lives
   in Swift, not in `design/`. The hint bar, the `?` help, the Cmd-K palette and the menu
   bar all read it. A test fails if any shortcut is declared outside it
   (`.keyboardShortcut`, `onKeyPress` or a menu key equivalent not built from an entry).
2. **The hint bar.** One row at the bottom of the grid shows the keys that work in the
   current context, Bubble Tea `help` style: `↑↓ runs  ⏎ open  / search  t take control
   ␣ play  ? more`. `?` expands it in place into the full grouped help; `?` or `esc`
   collapses it.
3. **The palette.** Cmd-K lists every action with its key, filtered live as you type,
   disabled ones shown dim with why. Choosing one runs it; the key shown teaches it.
4. **Bare keys when focus is not in a text field:** `j` `k` or `↑` `↓` move, `⏎` opens,
   `/` searches, `t` takes control, `a` accepts, `d` disputes, `space` plays, `c`
   captures, `g` then `s` goes to the steps, `z` zooms, `m` toggles click marks, `?`
   helps. Command chords are for global or destructive actions: Cmd-K for the palette,
   Cmd-Backspace to destroy (with an inline confirm). `esc` backs out one level: composer,
   then run, then runs.
5. **Three rules.**
   - **Nothing destructive has a bare key.** Destroy needs Cmd-Backspace and a confirm.
   - **Accept and dispute act only on a verdict open for review** (proposed or contested,
     ADR 0003), and each shows a 5 s undo in the hint bar. The message is sent when the
     undo ends, because a message the daemon has recorded cannot be taken back.
   - **Modes own the keyboard.** A typing context (composer, search, a dispute reason)
     swallows bare keys, and the hint bar shows `⏎ send  ⇧⏎ newline  esc leave`. Driving
     is a hard mode: every key, Cmd-Q included, goes to the guest, and only clicking the
     switch exits. The hint bar turns magenta: `all keys → machine · click switch to
     return`.
6. **The spec's key table is the starting registry.** Keys the spec adds beyond this list
   (`u` undo, `G` latest, `r` retry, `g c`, `←` `→` frames, next and previous failure)
   are settled in the registry PR; after that the registry is the source of truth, and
   the spec's table follows it.

## Consequences

- Every existing `.keyboardShortcut` and menu key moves into the registry. `RunCommands`
  and `ScreenCommands` build their menu items from registry entries.
- The Command-chord shortcuts of ADR 0001 and 0002 (Cmd-1, Cmd-2, Opt-Cmd-0, Shift-Cmd-T,
  Command-', Shift-Cmd-S, Shift-Cmd-E) go. The actions stay, under the new keys or in the
  palette.
- Bare keys need a real focus model: the app must know at all times whether focus is in
  a text field, a pane, or the screen. The cursor (ADR 0004) draws that focus, so a wrong
  focus is visible.
- Accept and dispute feel instant (optimistic, as Linear does) but are sent 5 s late. The
  card shows the pending choice and the undo; a relaunch inside the window drops it.
- The driving rule is unchanged in substance from ADR 0009 and the companion rules: Esc
  does not give back control. The way out is a click, so a mouse is needed to leave
  driving. That is deliberate: the guest may need every key.
- `HostedViewTests` gain key-routing tests: a bare key in the composer types, a bare key
  on a pane acts, a key while driving reaches the guest.
