# 0016. Sessions run their pty in the guest; output comes back through a guest file

Date: 2026-09-24
Status: accepted

## Context

**A session with fast output wedged the whole machine** (issue #30). A
`machine_session_*` session was one `tart exec -i -t` child behind a host pty. A command
that printed quickly (`yes | head -c 3000000; echo DONE`) stalled after 100 to 250 KB;
the rest came a few hundred bytes every ~12 s and `DONE` never arrived. While it stalled,
every other guest-touching call on that machine failed with tart's control socket error
or a deadline: `machine_exec`, screenshots, the input tools, the frame recorder. Only
closing the session freed the machine.

Measured again on `main` (c3cade4) through MCP, own daemon, `greenroom-base`: 144 KB in
92 s, no `DONE`, and three concurrent `machine_exec echo alive` calls each still running
after their 30 s wait. The same stall happens with raw `tart exec -i -t` from a host pty
read as fast as possible, so the fault is in tart 2.37.0 and its guest agent, not in the
daemon, which drains the pty at once.

The first fix tried was the obvious one: keep a single long-lived process, but move the
pty into the guest and give tart plain pipes, `tart exec -i <vm> script -q /dev/null
/bin/sh -c <command>`. Raw trials of the 3 MB command on one VM (60 s cap each):

| Transport | Trials | Stalled |
| --- | --- | --- |
| `tart exec` / `tart exec -i`, no pty (`yes \| head`, some through `dd obs=128`), up to 100 MB | 12 | 0 |
| no pty, 1 KB writes, paced or unpaced (perl), up to 30 MB | 12 | 0 |
| `tart exec -i`, guest `script` writing straight to tart's stdout | 17 | 6 |
| `tart exec`, guest `script` writing straight to tart's stdout | 6 | 1 |
| `tart exec -i`, guest `script` piped through `dd bs=65536` | 16 | 0 |
| `tart exec -i`, guest `script` writing to a guest file, stdout `/dev/null` | 8 | 0 |

A stall on the non-tty path also wedged the guest agent: a concurrent `tart exec` failed
with `GRPCConnectionPoolError`. So moving the pty is not enough while a pty's output
streams through tart. Piping through `dd` never stalled, but nothing explains why, and a
rare stall takes the whole machine down. It was not taken.

## Decision

1. A session's command runs under a pty the guest makes: `script -q -F <file>`, running
   `/bin/sh` that sets the terminal to 40x120 and `TERM` (if unset) to `xterm-256color`,
   then execs `/bin/zsh -lc <command>`. The program sees a real terminal as before
   (`isatty`, `tty`, `stty size`, Ctrl-C through the line discipline).
2. Output never streams through tart. `script` writes the typescript to
   `${TMPDIR:-/tmp}/greenroom-session.<id>` and its own stdout goes to `/dev/null`.
3. Input still goes through one long-lived non-tty `tart exec -i`, whose stdin is
   `script`'s stdin. It carries only what `machine_session_send` writes. The process
   ends when `script` ends and forwards the command's exit code, as before.
4. A follower goroutine per session copies the file into the session's 1 MiB window
   with short non-tty `tart exec` reads (`tail -c +N | head -c 1 MiB`), the path
   `machine_exec` has always used without stalling. It polls again at once after a full
   chunk, soon after any data, and backs off to 2 s when idle; a send or a read wakes it.
   When the guest is more than 1 MiB ahead it skips to the last 1 MiB and the skipped
   bytes are reported as `dropped`, so a flood never has to cross vsock. A session is
   reported ended only after the file has been read to its end.
5. Closing a session runs one guest command that hangs up `script`'s session (HUP, then
   KILL after 3 s, to every process in it) and removes the file, then ends the host
   `tart exec`. Before, killing the host process left the guest command running.
   Destroying the machine only ends the host processes.
6. The tool surface and its semantics do not change. The description no longer warns
   that fast output stalls the machine.

## Consequences

- No window resize from the host: the size is fixed at start (it already was).
- Output reaches a reader up to the poll interval late (at most 2 s when idle, much less
  while output flows or right after a send).
- Each running session costs up to a few `tart exec` calls a second while busy and one
  every 2 s while idle.
- The guest file grows with everything the command prints until the session is closed
  or the machine goes. A command that prints gigabytes fills guest disk the same way
  `> file` would.
- The fake tart models a session with the real `script` on the host (running `cat`) and
  runs the real read and close commands, so the follower and cleanup are tested without
  a VM.
- If a later tart fixes tty streaming, returning to `tart exec -i -t` is a transport
  swap inside `machine/ptysession.go`; nothing above it changes.
