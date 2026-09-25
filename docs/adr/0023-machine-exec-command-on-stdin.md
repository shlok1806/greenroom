# 0023. machine_exec sends its wrapper and command on stdin, never in argv

Date: 2026-09-25
Status: accepted. Supersedes the invocation in ADR 0014 decision 1; the rest of 0014 stands.

## Context

ADR 0014 ran `tart exec <vm> /bin/sh -c <execWrapper> greenroom-exec <command> <secs>`,
and the wrapper ran `/bin/zsh -lc "disable log 2>/dev/null; $1"`. Both the whole wrapper
script and the user's command were in argv, twice (issue #128). Every `ps` and `pgrep -f`
in the guest showed them, so:

- One `pgrep -fl` through `machine_exec` in an agent session returned about 58 KB, mostly
  Greenroom's own plumbing.
- `pgrep -f <pattern>` matched the wrapper that ran it, because the pattern was in the
  wrapper's argv. "Is my app still running?" was always yes.

Two ways out were weighed:

1. **Install the wrapper as a versioned file at boot**, like the input helper
   (`~/.greenroom/bin/greenroom-exec-<ver>`), and pass the command through a temp file.
   The command still has to reach that file without argv, so this needs stdin anyway,
   plus a boot phase, a version check, an image-staleness story for old images and
   reattached machines, and a second `tart exec` per call to write the file.
2. **Send everything on stdin.** `tart exec -i` attaches the host's stdin; the guest agent
   closes it at EOF. Sessions already rely on it (ADR 0017).

## Decision

1. `machine_exec` runs `tart exec -i <vm> /bin/sh -s greenroom-exec <secs>` and writes
   `execScript(command)` to its stdin (`machine/guest.go`, `tart.ExecInputTo`).
   `greenroom-exec` is only `$1`, a name in the listing; the timeout is `$2`.
2. `execScript` is the wrapper with the command in a quoted heredoc
   (`<<'GREENROOM_CMD_<random>'`), which the shell copies byte for byte into
   `$d/cmd` under the wrapper's temp dir. The delimiter is 26 random base32 characters,
   drawn again in the unlikely case the command contains it.
3. zsh runs the file as `/bin/zsh -lc "disable log 2>/dev/null; eval \"\$(<$d/cmd)\""`,
   not as a script file: `eval` keeps `zsh -c`'s semantics (the whole command is parsed
   before any of it runs, `$0` and an empty `$@` as before, `return` ends it), and the
   issue #40 fix stays outside the command, so its line numbers are unchanged.
   Running `zsh -l $d/cmd` was rejected: it runs line by line, so a parse error on
   line 5 leaves lines 1 to 4 already run, and `$0` and every error carry the temp path.
4. Nothing but the wrapper's `sh` reads stdin. zsh and the watchdog get `/dev/null`, as
   before.

Everything else in ADR 0014 is unchanged: output through files, fd 3 for the command's
stderr, `set -m` and the watchdog, exit 124 with the note, the temp dir removed.

## Consequences

- The guest shows `/bin/sh -s greenroom-exec 600` (twice while a timeout watchdog is up)
  and `/bin/zsh -lc disable log 2>/dev/null; eval "$(</tmp/greenroom-exec.XXXXXX/cmd)"`.
  A `pgrep -f` through `machine_exec` finds only processes that really carry the
  pattern.
- zsh's own errors say `(eval):N:` where they said `zsh:N:`. N is the command's line.
- The fake tart reads the stdin of `exec -i ... greenroom-exec`, logs it to
  `exec-stdin` (`testsupport.ExecStdin`) and appends it to its arguments, so its pattern
  cases still see the command. Tests that look for a command read `ExecStdin`, not
  `Calls`.
- Sessions (`greenroom-session`, ADR 0017) still pass their command in argv. They are
  long-lived and fewer; moving them is a separate change.
- The optional `maxOutput` from issue #128 is not part of this decision; the per-stream
  caps of ADR 0015 are unchanged.
