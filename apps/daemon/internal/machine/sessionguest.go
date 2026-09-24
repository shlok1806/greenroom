package machine

// Guest side of an interactive session (ADR 0016). The pty lives in the guest,
// made by `script`; its output goes to a guest file that the daemon reads with
// short non-tty `tart exec` calls. Output never streams through tart: a pty's
// output streaming through tart 2.37.0 stalls and wedges the guest agent
// (issue #30). All three scripts run under /bin/sh and find the file from the
// session id alone, so they agree on it without the daemon knowing guest paths.

// sessionWrapper starts a session: `/bin/sh -c sessionWrapper greenroom-session
// <id> <command>`. It runs as the long-lived `tart exec -i`, whose stdin is the
// session's input. `exec` keeps the pid, so the pid file names `script`.
// `script -F` flushes the file on every write; its own stdout, the same bytes,
// goes nowhere. It exits with the command's status, which tart forwards.
const sessionWrapper = `f="${TMPDIR:-/tmp}/greenroom-session.$1"
umask 077
: > "$f" || exit 125
echo $$ > "$f.pid"
exec /usr/bin/script -q -F "$f" /bin/sh -c '
stty rows ` + sessionRowsCols + ` 2>/dev/null
case "$TERM" in ""|dumb) TERM=xterm-256color ;; esac
export TERM
exec /bin/zsh -lc "$1"' greenroom-session-pty "$2" > /dev/null`

// sessionRowsCols is the terminal size a session's command sees. 120 columns
// because a model reads the output, and 80 wraps diagnostics.
const sessionRowsCols = "40 cols 120"

// sessionReadScript prints where its data starts, a newline, then at most
// <limit> bytes of the file from that offset: `/bin/sh -c sessionReadScript
// greenroom-session-read <id> <offset> <limit>`. When the file is more than
// <limit> past <offset> it starts at the last <limit> bytes instead, so a flood
// never crosses vsock whole. It exits 3 while the file does not exist.
const sessionReadScript = `f="${TMPDIR:-/tmp}/greenroom-session.$1"
size=$(stat -f %z "$f" 2>/dev/null) || exit 3
off=$2
[ $((size - off)) -gt "$3" ] && off=$((size - $3))
echo "$off"
[ "$size" -gt "$off" ] && tail -c +$((off + 1)) "$f" | head -c "$3"
exit 0`

// sessionReadMissing is sessionReadScript's exit code for a file not there yet.
const sessionReadMissing = 3

// sessionCloseScript ends a session and removes its files: `/bin/sh -c
// sessionCloseScript greenroom-session-close <id>`. It hangs up script and
// every process whose controlling terminal is the session's pty (named by
// script's child), then KILLs what is left after 3 s. macOS pgrep has no -s
// and its -t matches nothing for a ttys name, so `ps -t` lists them. The pid
// is checked to still be script, so a reused pid is never signalled. Killing
// the host `tart exec` alone reaches none of this.
const sessionCloseScript = `f="${TMPDIR:-/tmp}/greenroom-session.$1"
p=$(cat "$f.pid" 2>/dev/null)
case "$(ps -o comm= -p "${p:-0}" 2>/dev/null)" in
*script)
  c=$(pgrep -P "$p" | head -n 1)
  t=
  [ -n "$c" ] && t=$(ps -o tty= -p "$c" | tr -d ' ')
  case "$t" in ""|"??") t= ;; esac
  on_tty() { [ -n "$t" ] && ps -o pid= -t "$t" 2>/dev/null; }
  kill -HUP $(on_tty) "$p" 2>/dev/null
  i=0
  while [ "$i" -lt 30 ]; do
    kill -0 "$p" 2>/dev/null || [ -n "$(on_tty)" ] || break
    sleep 0.1
    i=$((i + 1))
  done
  kill -KILL $(on_tty) "$p" 2>/dev/null ;;
esac
rm -f "$f" "$f.pid"
exit 0`
