# 0016. A finished run says Done, and only Verified is green

Date: 2026-09-27
Status: accepted. Implements root ADR 0031 point 3. Amends 0002 (the colour of a pass
nobody reviewed) and 0012 (the row's words for a finished run).

## Context

Root ADR 0031 lets the coding agent end a run with `run_finish`: an outcome (`verified`,
`unverified`, `abandoned`), a summary and a ref. The daemon puts it on the run list, on
the run detail (the manifest's `finish`) and on the system event that recorded it. Before
it, the window could only say "Ended" once the machine was gone, for shipped and abandoned
runs alike. Three things were not settled by the root ADR:

- Companion ADR 0002 keeps the pass colour for a pass a person accepted; one the coding
  agent accepted reads "Pass, unreviewed" with no colour. `verified` needs only an accepted
  pass, by the coder or a person.
- ADR 0012 leads a row with what waits on the person (`Fail, needs review`). An unverified
  finish can leave a proposed verdict open.
- The glyph for a neutral Done. `✓` is pass, `✗` failure, `●` live, `!` needs you.

## Decision

1. **Done is part of `RunFacts`** (`finish`), not a parallel state. The transcript's finish
   event wins (it comes over the event stream before the run is re-read), then the detail,
   then the list. The phase is unchanged: a machine the agent kept is still live or idle,
   but every state word says Done, and an idle finished run offers no idle actions.
2. **The words**: `Done, verified`, `Done, unverified`, `Done, abandoned` in the row and in
   the header's status line; an outcome this app does not know reads as sent
   (`Done, shipped`). Under the status line: the summary in the reading face (three lines
   at most), then the ref in mono, `branch x · commit y · PR #12 ↗`. Only an http(s) PR is
   a link, and it opens in the browser; a GitHub pull URL reads as its number.
3. **Colour**: `Done, verified` takes the pass role with `✓`, whoever accepted the pass. The
   daemon checked it (an accepted pass on this run), so it is a fact, not the agent's claim.
   The verdict card still says who accepted the pass. `unverified` and `abandoned` take the
   foreground with `■` (tone `done`), never the failure colour: an abandoned run is not a
   failed check.
4. **Needs you still leads the row.** A question, a proposed or a contested verdict keeps
   the run in "Needs you" with `Fail, needs review`; the header says Done. A finished run
   with nothing waiting on the person reads Done everywhere.

## Consequences

- `RunFacts.Tone` gains `done`; every switch over it (the status text, the runs strip)
  draws `■`.
- A finish on a lost machine hides "machine lost" from the status line; the transcript
  still has the line.
