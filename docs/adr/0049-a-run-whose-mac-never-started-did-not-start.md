# 0049. A run whose Mac never started did not start

Date: 2026-10-01
Status: accepted

Amends ADR 0036 (the summary's status vocabulary, point 4, and its tone, point 6) and ADR 0034
(the report's outcome). Fixes issue #286.

## Context

`machine_create` writes a run directory before it clones, so a create that fails (a missing
image, tart refusing to clone or start) still leaves a run: a manifest stamped `destroyedAt` a
few milliseconds after `createdAt`, and one `machine_create` step carrying the error. A boot that
fails after the clone started (the guest agent, the IP, the key or ssh never answering) leaves a
`machine_boot` step with the error and a live machine in status `failed` until it is destroyed
or the daemon restarts.

Three surfaces read such a run three ways:

- `GET /api/runs` said `status: "finished"`, the word it gives every run with a readable
  manifest and no live machine. It already says `failed` for a live machine whose boot failed
  (`machine.Failed`), and lost that word the moment the machine went.
- The summary said **Stopped**, tone `quiet`, in Done: "the run is over with no outcome", the
  same row as a run whose agent worked for an hour and shut its Mac down. A failed create had no
  sentence at all; a failed boot read "The Mac did not start." only while its "machine failed"
  event was the last word.
- The report said `Not finished`: "the coding agent has not called run_finish", which is true
  but blames the coding agent for a run it never got to use.

The Companion renders the summary, so a person could not tell a failed create from a finished run
in the runs list. A failed create is also never named: the name `machine_create` was given was
written after the create returned, and a failed create returns no run.

## Decision

1. **One rule, read from the step log.** A run's Mac **did not start** when its `machine_create`
   step or its `machine_boot` step carries an error (`machine.StartFailureOf`, also on
   `machine.StepLog.StartFailure`). Every run since 2026-09-12 records both steps, and a run
   from before that recorded a failed synchronous create as an errored `machine_create`, so the
   rule classifies runs already on disk when they are read, with nothing rewritten. A boot that
   a destroy cut short records no `machine_boot` step and is not a failure; neither is a failed
   `machine_reboot` (the Mac started once, and its files are kept).

2. **`/api/runs` says `failed`.** A run with no live machine whose Mac did not start lists as
   `status: "failed"`, the word the list already gives the live machine and a run whose manifest
   cannot be read. `finished` stays for every other run with no live machine. The list also
   carries `startError`, the failed step's error, for a run whose Mac did not start.

3. **The summary's eleventh status, `did-not-start`, "Did not start".** It is not `failed`:
   that id already means a verdict that failed its checks, and a client keys its tally, its
   failing check and its verdict page on it. The word reuses the sentence the summary already
   said for a failed boot ("The Mac did not start."). The rule sits after Starting and
   Restarting and before every other, and holds only while the run is not open (a live machine
   in `failed` is not open). Group: Done. Tone: `fail`, the one exception to ADR 0036's "fail
   only for a fail verdict a person can still review or accepted": nothing else will tell a
   person that a run they started never ran. No action. The detail says which step failed, in
   plain words: "The Mac could not be created." for `machine_create`, "The Mac did not finish
   starting." for `machine_boot`. `machine.ended` is "The Mac did not start." for both.

4. **The report says it.** A run whose Mac did not start and that the coding agent did not
   finish is headed `Did not start`, its outcome line is "did not start: the machine never became
   ready: <the error>", and its JSON carries `startError`. A finish still outranks it in the
   heading and outcome (the agent's word on the run), and the report adds a Machine line with the
   error. The summary does not defer to a finish: it never words the finish's outcome as its
   status (a finish with no verdict is Stopped), so a run whose Mac did not start reads Did not
   start there whether or not the agent finished it, and the list says `failed` either way.

5. **A failed create keeps its name.** `machine_create` hands its name and client to
   `Manager.CreateLabeled`, which writes them into the first manifest before the clone, so a run that never started is listed by the name
   its agent gave it instead of "Untitled run".

## Consequences

- The list, the summary and the report agree: a run whose Mac never started is a failure,
  never a finished run, and none of them needs the run's conversation to say so (a failed create
  has none).
- A client that does not know `did-not-start` shows its word and tone, as ADR 0036 asks. The
  Companion learns the id and draws it with the failed glyph.
- `did-not-start` is a new row in `TestEveryStatusHasItsGroupActionAndWords`, as ADR 0036's
  Consequences require of any new state.
- Not decided here: deleting the run directory of a create that failed before the clone. The
  record is the evidence that the create was tried, which is what this issue needed to show.
