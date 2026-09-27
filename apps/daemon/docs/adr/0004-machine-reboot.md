# 0004. machine_reboot restarts a wedged guest and keeps the run and its disk

Date: 2026-09-27
Status: accepted.

## Context

Issue #187. In run `20260927-000233-acbc2b008dfc6a8f` the guest's WindowServer hung
(`coreanimation timed out fence`) while the app under test ran two render loops. From then on
every screenshot and UI read waited on WindowServer, the guest agent stalled for a while, and
`screencapture` processes piled up in the guest. `tart exec` recovered; the GUI never did. The
only way out an agent had was `machine_destroy`, which deletes the clone: the synced project,
its build products and caches, and the run went with it, and the agent had to start over on a
new machine.

Restarting only the GUI session (`killall WindowServer`, `launchctl kickstart -k`) is not
reliable: a process in uninterruptible wait (`U`) takes no signal, and a logout kills the
user-level guest agent and the app under test with it. `sudo shutdown -r` from inside the guest
can stall on the same hung WindowServer. A reboot from the host is the one recovery that always
ends: `tart stop`, then `tart run` on the same clone, whose disk persists. `tart stop` is no
guest shutdown: it ends `tart run`, which powers the VM off at once (0.2 s live), so the guest's
unwritten data is lost unless it was flushed first. Only `cleanupVM` (destroy, a failed boot, a VM that stopped on its
own) deletes a clone.

## Decision

1. **`machine_reboot {runId, waitSeconds?}`** (`Manager.Reboot`, `machine/reboot.go`) stops the
   VM with `tart stop --timeout 20` after a guest `sync` through the agent (at most 15 s; a
   file written seconds before a reboot was lost without it), waits for it to be gone (this daemon's `tart run` exits; if
   it does not within 30 s the daemon kills it, which ends the VM it hosts; a reattached
   machine's is polled in `tart list` instead), starts the same clone with a new `tart run`,
   and runs every boot phase again (`bootGuest`: agent, ip, key, settings with the capture
   approvals and desktop preferences, helper, checks, ssh). The IP may change. The frame
   recorder starts again once it is ready.
2. **A status, `rebooting`**, from the call until ready or failed, in `machine_list`,
   `machine_wait`, `/api/runs` and every lifecycle event. `machine_wait` waits through it.
   Reboot's boot phases replace the first boot's: `stop`, `start`, then the boot's.
3. **Kept**: the disk (the guest home, `~/work` and every synced file, build products, the
   input helper, `authorized_keys`, TCC and replayd capture-approval rows), the run, its
   record, its conversation and its runId. **Lost**: `machine_session_*` sessions, running
   `machine_exec` commands (they end with an error saying the machine rebooted; finished
   results stay collectable), apps and background processes, `/tmp`, the live screen (viewers
   are told why and reconnect), the control lease, and the daemon's memory of the old guest:
   the helper's screen size and every reader's UI tree and looks, so element ids from before
   aim at nothing and the verifier must look again.
4. **The watcher rule.** `Machine.gen` counts boots of one clone and a reboot increments it.
   `watchProcess`, `watchVM`, `machineGone` and `startFrames` carry the gen they were started
   for and stand down when it moved on. So the exit of the old `tart run`, or `tart list`
   showing the VM stopped, during or after a reboot never fails the machine, deletes its clone
   or ends its run. A VM that stops on its own after the reboot is still noticed, by the new
   boot's watcher.
5. **Concurrency.** Every guest call on a rebooting machine fails at once with `ErrRebooting`
   ("the machine is rebooting ... call machine_wait"): a reboot takes longer than any call may
   wait. A second reboot while one runs is refused the same way. A booting machine, or one
   whose failed boot deleted its VM, is refused. `Destroy` during a reboot cancels it, waits
   for it and deletes the VM; the reboot records nothing more.
6. **Bounded, and never silently destructive.** The whole reboot has 5 minutes
   (`WithRebootTimeout`). A guest that does not come back leaves the machine `failed` with the
   reason and "the disk is kept"; the VM is stopped so it holds no host slot, nothing is
   deleted, and the run does not end. `machine_reboot` on it tries again; `machine_destroy`
   deletes it (`Machine.vmDeleted`, not the status, tells Destroy whether a VM is left).
7. **The 50 s call limit.** The tool starts the reboot and waits at most `waitSeconds`
   (default 45, max 50), like `machine_exec`, then returns the machine, `rebooting` if it is not
   back, and the reboot's step; `machine_wait` does the rest.
8. **Record.** One `machine_reboot` step, claimed when the call starts and written when the
   reboot ends, with `stopSeconds`, `startSeconds`, every boot phase key `machine_boot` has,
   `status`, `ip` and `rebootSeconds`, and the error of a failed one. The transcript says
   "machine is rebooting ..." when it starts and "machine rebooted and is ready" or "machine
   failed to reboot: ..." when it ends (the lifecycle bridge; the events are `rebooting`, then
   `ready` or `failed` with `reboot: true`).
9. **After a daemon restart** a machine in `state.json` as `rebooting` (the daemon died in a
   reboot) or `failed` whose clone tart still lists is reattached as `failed`, with its disk,
   rather than dropped: `machine_reboot` boots it, `machine_destroy` deletes it.
10. **Companion.** `POST /api/runs/{id}/reboot` does the same for a person (202, the event
    "human rebooted the machine (step N)", 409 for any refusal); guest routes answer 409 while a
    machine reboots. The app decodes `rebooting` and shows the run as coming up again
    ("Rebooting"); it has no Reboot button yet.

## Consequences

- An agent whose screen stops answering keeps its machine: one call and a `machine_wait`
  instead of destroy, create, sync and rebuild.
- A reboot ends everything in flight on the guest without asking; its description says so.
- The fake tart (`testsupport/faketart.go`) now boots a VM again after `tart stop <name>`, and
  lists a VM stopped by name as stopped, so reboots are tested without a VM. The real
  behaviour (a hung WindowServer, the IP after a reboot, the time it takes) is checked on a
  real machine.
