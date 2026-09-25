# 0014. machine_exec runs behind a guest wrapper: output through files, timeout enforced in the guest

Date: 2026-09-22
Status: accepted. Decision 1's invocation (the wrapper and the command in argv) is superseded by
ADR 0023, which sends both on stdin.

## Context

`machine_exec` ran `tart exec <vm> /bin/zsh -lc <command>` and returned what tart
printed. Two things went wrong with that.

**A background child held the call open.** `tart exec` returns only when every holder of
the guest's stdout and stderr pipes has closed them. `./App &`, the normal way to start
the app under test, left the app holding both, so the call hung until its timeout.
Neither `cmd.WaitDelay` on the host nor redirecting inside the command helped: tart
itself stays up while the guest pipes are open.

**The timeout killed nothing** (issue #28). The timeout was a context on the host's
`tart exec` process. Killing it does not reach the guest: the guest agent never signals
the command, which ran on to completion, side effects included. The call returned
"context deadline exceeded" and lost everything printed so far.

A first version of the wrapper (this PR) fixed the first problem and made the second
worse: output went to files that were printed only after zsh exited, so a timeout lost
all of it, and the files leaked in `/tmp`.

## Decision

1. `machine_exec` runs `/bin/sh -c execWrapper greenroom-exec <command> <timeoutSeconds>`
   (`machine/guest.go`). The wrapper runs the command in a login zsh with stdout and
   stderr in temp files under `/tmp/greenroom-exec.*` and stdin from `/dev/null`, then
   prints the files and removes them. A child the command leaves running inherits files,
   not the pipes, so the call returns when the shell exits, and the child keeps running.
2. The guest enforces the timeout. `set -m` gives zsh its own process group. A watchdog
   sends that group TERM at the timeout and KILL 5 s later, or at once once zsh has gone,
   so a child that ignores TERM still dies. The wrapper then prints the output so far,
   adds `greenroom: timed out after N s; the command and its children were killed`,
   exits 124, and removes the files. `ExecResult.TimedOut` is set, and the call returns
   a result, not an error.
3. The host waits the timeout plus 20 s (`execHostGrace`), so the guest's watchdog, not
   the host, ends the command. A host deadline still firing means tart or the guest
   agent is stuck.
4. The wrapper's own stderr goes to `/dev/null`, so the shell's job notices never reach
   the caller. The command's stderr is written through fd 3, which zsh and the watchdog
   do not inherit: a child holding it would hold the call open again.

## Consequences

- Output streams nowhere while a command runs: it arrives all at once when the shell
  exits or is killed. Nothing a background child writes after the shell exits is
  returned.
- A command's children that are still running when it times out are killed with it;
  children of a command that finished normally are not.
- Exit 124 with `timedOut` is a timeout. A command that exits 124 itself is told apart by
  the absence of the note.
- The wrapper assumes the guest's `/bin/sh` supports `set -m` and `kill -<pgid>`, true
  of macOS's bash-based `/bin/sh`. The real-shell tests in `guest_test.go` run it on the
  host.
