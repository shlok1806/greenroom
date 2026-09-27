# 0002. tart run gets a real file limit, a cancelled tart exec is interrupted, and machine_list counts files

Date: 2026-09-27
Status: accepted.

## Context

Issue #186. Long-lived machines die with `FIXME: "Handle this: Error(24) ..." line 263` in
`vm.log`. Error 24 is EMFILE, raised inside Apple's Virtualization.framework when `tart run`
cannot get a file descriptor for a new vsock connection. tart opens one on every `tart exec`
(`ControlSocket.handleClient` calls `vm.connect(toPort:)`).

Two things combine:

1. **A leak in tart (2.37.0 up to 2.39.0).** `handleClient` proxies the exec's Unix socket to
   the guest's vsock with two tasks in `withThrowingDiscardingTaskGroup`, which waits for both.
   When `tart exec` goes away, nothing closes the VM side, so the VM to client task ends only
   when the guest sends another byte or closes the vsock. The guest agent's grpc-go server
   ignores a client's GOAWAY. A proxy whose guest side goes quiet holds its vsock fd for the
   life of the VM. It is a race per exec, and an exec killed with SIGKILL while its guest
   command is silent almost always loses it: SIGKILL is `exec.CommandContext`'s default, and
   `tart.ExecTo` is cancelled by timeouts, frame capture on destroy and detached jobs.
2. **A limit of 256.** launchd starts the daemon with a soft `RLIMIT_NOFILE` of 256. Go raises
   its own soft limit at start but restores the original in every child it starts
   (`syscall/rlimit.go`, `origRlimitNofile`), and nothing in the daemon called `Setrlimit`, so
   every `tart run` got 256. Two dead runs leaked at about 5 to 8 percent of their execs and
   died after 62 and 86 minutes.

The real fix is upstream, in tart's `ControlSocket.handleClient` (end both directions when
either ends, then close both channels), or in the guest agent (grpc-go `MaxConnectionIdle`).
That is not done here; this ADR is the daemon's mitigation until it lands.

## Decision

1. **Raise the limit children inherit.** `serve` and `bench run` call `openfiles.Raise` before
   any machine starts: soft `RLIMIT_NOFILE` to the hard limit, capped at
   `kern.maxfilesperproc` (the kernel refuses more, and an infinite hard limit is common).
   Raise always ends in a `syscall.Setrlimit` call, even when it cannot raise, because that
   call is what stops Go restoring the old limit in children: `tart run` then inherits exactly
   what `openfiles.Inherited` reports. It must be `syscall.Setrlimit`, not `unix.Setrlimit`.
   The daemon logs the limit at start ("open file limit for tart run").
2. **The launchd job asks for it too.** `scripts/install.sh` writes `SoftResourceLimits` and
   `HardResourceLimits` `NumberOfFiles` of 65536, or `kern.maxfilesperproc` when lower, so the
   daemon starts high even before Raise.
3. **Interrupt a cancelled `tart exec` before killing it.** `runExec` (`Exec`, `ExecTo`,
   `ExecInputTo`) sets `cmd.Cancel` to SIGINT and `cmd.WaitDelay` to 3 s. tart handles SIGINT
   by cancelling its command: the gRPC call is cancelled, which ends the guest command (the
   guest agent runs it with the stream's context), and the guest's answer to the cancel should
   reach the proxy while it can still see the client is gone. That narrows the race; it cannot
   close it. SIGTERM would not help: tart does not handle it and dies as abruptly as on SIGKILL. Long-lived sessions (`StartSession`, `StartPipe`)
   are unchanged: a pipe closes stdin first, and a session's guest command must survive the
   host exec (ADR 0017), so interrupting its tart would end it.
4. **Show how close a machine is.** Every `FileCheck.Interval` (30 s) after ready, the manager
   counts the files its `tart run` has open with `lsof -p <pid> -F f` (numeric fds only; there
   is no `proc_pidinfo` without cgo), bounded at 5 s. The pid is this daemon's `tart run`
   process, or, for a machine reattached from an earlier daemon, the owner of the fcntl lock
   `tart run` holds on the VM's `config.json` (`tart.RunPID`, how `tart stop` finds it). The
   latest count is `files` (`pid`, `open`, `limit`, `checkedAt`, `warning`) on the machine in
   `machine_list` and `GET /api/runs/{id}`, never in `state.json`. `limit` is what this daemon
   gave the process; macOS cannot read another process's limit, so a reattached machine's is
   unknown and left out. From 80 percent of the limit (of 256 when unknown) `warning` says to
   pull what is needed, and the daemon logs one WARN per machine.

## Consequences

- At the observed leak rate a 65536 limit lasts days instead of an hour. The leak itself
  remains: this is a mitigation, and #186 stays open for the upstream fix.
- Machines reattached after an upgrade keep the 256 their old `tart run` got. Their `files`
  shows the count with no limit, and the warning assumes 256.
- A cancelled exec whose tart ignores SIGINT now takes up to 3 s longer to return.
- `files` appears only after the first count, 30 s after ready. lsof runs once per machine per
  interval.
- Interrupting a cancelled exec now ends its guest command instead of orphaning it, which also
  helps #187's orphaned `screencapture` processes.
