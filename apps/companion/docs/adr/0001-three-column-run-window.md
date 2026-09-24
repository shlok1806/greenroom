# 0001. One run window: the stage, the conversation beside it, the verdict pinned on top

Date: 2026-09-22
Status: superseded by 0004

ADR 0004 replaces the three-column `NavigationSplitView` and the system-colours-only
style with a glyph-native window on a character grid.

## Context

The first companion (system ADR 0007) put a run behind three tabs: Transcript, Screen,
Steps. Each tab was right on its own, and together they fought the person's actual tasks:

- Watching a live run means looking at the screen and reading what the verifier says
  about it at the same moment. Tabs make that a choice.
- Judging a verdict means reading the verdict and opening what it cites. The verdict was
  a message somewhere in the transcript and its evidence was plain text ("step 16", an
  absolute path).
- Runs were named by clock time and a hash. Nobody remembers "20:08:27 9411d6"; they
  remember "the TipSplit check".
- A daemon that was not running looked like an empty list with "Reconnecting" in a
  footer.

Research (`../design-research.md`) and the spec (`../design-spec.md`) cover the HIG,
comparable Mac apps and run viewers (GitHub Checks, Buildkite, PostHog, Devin).

## Decision

1. **Three columns.** `NavigationSplitView` for runs and the run, and `.inspector` for the
   conversation. The run's column is a stage that shows the Screen or the Steps (toolbar
   segmented control, Cmd-1 and Cmd-2). The conversation stays beside either and can be
   hidden (Opt-Cmd-0). The Transcript tab is gone.
2. **The verdict is a card pinned above the conversation**, with its outcome, state,
   summary, evidence chips and the only Accept and Dispute buttons. Evidence chips and
   tool calls seek the Screen to their step.
3. **A run is named by its task.** The daemon adds `task` (the first task message, clipped
   to 280 runes) to each `RunSummary`, computed where it already reads the conversation
   for the message count. No new route.
4. **One window.** The scene is a `Window`, not a `WindowGroup`: selection lives in
   `RunStore`, so two windows mirrored each other.
5. **Menu commands reach the open run through focused scene values** (`RunCommands`,
   `ScreenCommands`), so shortcuts need no second source of truth.
6. **Connection is a state the app shows**, derived in `RunStore.connection`: connecting,
   online, offline. Offline with nothing loaded fills the window with how to start the
   daemon.
7. **Style is tokens in `Views/Theme.swift`**: system text styles, a 4 pt grid, system
   colours for three separate axes (lifecycle, outcome, agreement), each paired with a
   symbol or word. No custom colours, fonts or dependencies.

## Consequences

- Driving no longer owns the whole detail: the conversation is still visible. Keys follow
  focus, so a person can click the composer mid-drive and type to the verifier; Command
  shortcuts still go to the guest while the screen has focus. The lease is given back when
  the stage leaves the Screen, as it was when the tab changed.
- The live stream runs while the Screen is the stage, whether or not the conversation is
  open, which is what a person watching expects.
- `RunSummary.task` is optional in the app, so an older daemon still decodes; its runs fall
  back to "Run <hash>".
- The UI rules in the companion's CLAUDE.md change from "one dense row" to a two line row
  with a title. "Status is a dot" and "only state gets strong colour" stay.
- Screenshots for design review come from `SnapshotHarness` in the test target, gated by
  `GREENROOM_SNAPSHOTS`, against a daemon serving copied runs. It never runs in CI.
