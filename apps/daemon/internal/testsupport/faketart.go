// Package testsupport holds helpers shared by the daemon test suites.
package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// FakeTart writes a shell script answering every tart subcommand the daemon uses, so tests need no VM.
// Every invocation is appended to <control>/calls.log. Files in the control directory switch behavior:
//
//	fail-clone, fail-run, fail-ip, fail-exec, fail-stop, fail-delete, fail-keyinstall,
//	fail-capture-approval, fail-desktop-prefs, fail-lean, fail-base, fail-toolchain, fail-softwareupdate
//	                    (capture-approval-stale: the approval check reports a stale record)
//	                    the matching operation exits 1 with a message
//	toolchain.json      what the image's toolchain manifest holds; the manifest script writes
//	                    {"known":true,...} here, and boot reads it (absent: the image has none)
//	desktop.json        what the input helper's --desktop prints (default: a clean desktop, Finder only)
//	softwareupdate      lines the image check's Software Update probe prints (default: none, it is off)
//	fail-check-<name>   the image check's exercise <name> exits 1 (e.g. fail-check-appleevent-safari)
//	boottime            the guest's boot time; the image check's in-guest reboot bumps it
//	exec-exit-<n>       `tart exec` exits n
//	exec-codes          `tart exec` exits with the next line of this file (consumed), then 0
//	exec-sleep          machine_exec's command takes this many seconds
//	exec-stdout         machine_exec's command prints this file and exits 0
//	agent-down          `tart exec` fails as if the guest agent is unreachable
//	ssh-down            the in-guest sshd probe is refused
//	fail-input-install  compiling the guest input helper fails
//	input-stale         the boot check finds only an old helper (greenroom-input-2)
//	input-down          the input helper refuses every event
//	screen              "<width>x<height>" the input helper reports (default 1024x768)
//	shot.b64            base64 PNG a screenshot returns
//	ui.json             what the input helper's --ui-base64 prints (default: Finder, no elements)
//	fail-session        an interactive session (`exec -i ... greenroom-session`) exits 1 at once
//	fail-serve          the live screen helper (`exec -i ... --serve`) fails to start
//	session-exits       a session's output file holds session-output, if present, and it exits at once,
//	                    with the code in session-exit-code (default 0)
//	tart-version        what `tart --version` prints (default tart.PinnedVersion)
//	list-empty          `tart list` returns []
//	vmnames, vmname     `tart list` reports these VMs running (default: one unrelated VM)
//
// The script writes session-stdin ("tty <rows> <cols>" or "pipe"), a session's files
// (greenroom-session.<id> and .pid, ADR 0017) and stopped (after stop or delete).
// `--serve` runs the fake live screen helper; its own control files are listed in fakescreen.go.
func FakeTart(t *testing.T) (bin string, control string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	control = filepath.Join(dir, "control")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "tart")

	script := `#!/bin/sh
C="` + control + `"
# The fake guest's temp dir, where session files land (the real guest uses its own TMPDIR or /tmp).
export TMPDIR="$C"
printf '%s\n' "$*" >> "$C/calls.log"
sub="$1"; shift
case "$sub" in
  --version)
    if [ -f "$C/tart-version" ]; then cat "$C/tart-version"; else echo "` + tart.PinnedVersion + `"; fi
    exit 0 ;;
  clone)
    [ -f "$C/fail-clone" ] && { echo "Error: image not found" >&2; exit 1; }
    exit 0 ;;
  run)
    [ -f "$C/fail-run" ] && { echo "The number of VMs exceeds the system limit" >&2; exit 1; }
    # Stays in the foreground like a real VM, but never outlives the test: it exits on stop,
    # when the control directory is removed, or after 5 minutes.
    i=0
    while [ ! -f "$C/stopped" ] && [ -d "$C" ] && [ "$i" -lt 600 ]; do
      sleep 0.5
      i=$((i + 1))
    done
    exit 0 ;;
  ip)
    [ -f "$C/fail-ip" ] && { echo "Error: no IP" >&2; exit 1; }
    echo "192.168.64.9"
    exit 0 ;;
  exec)
    case "$*" in
      *"-i "*"--serve"*)
        [ -f "$C/fail-serve" ] && { echo "Error: VM is not running" >&2; exit 1; }
        exec env ` + fakeScreenEnv + `="$C" "` + self + `" ;;
    esac
    # A session is "exec -i <name> /bin/sh -c <wrapper> greenroom-session <id> <command>" (ADR 0017).
    # It is modelled by the real script(1) running cat behind a host pty, writing the file the real
    # read and close commands below use, so it echoes like the guest. The command is never run.
    # Record whether the daemon handed tart a pipe or a terminal: it must be a pipe now.
    if [ "$1" = "-i" ] || [ "$1" = "-t" ]; then
      if [ -t 0 ]; then
        printf 'tty %s\n' "$(stty size < /dev/tty 2>/dev/null || stty size 2>/dev/null)" > "$C/session-stdin"
      else
        echo "pipe" > "$C/session-stdin"
      fi
      [ -f "$C/fail-session" ] && { echo "Error: VM is not running" >&2; exit 1; }
      f="$TMPDIR/greenroom-session.$7"
      : > "$f"
      echo $$ > "$f.pid"
      if [ -f "$C/session-exits" ]; then
        cat "$C/session-output" > "$f" 2>/dev/null
        code=$(cat "$C/session-exit-code" 2>/dev/null)
        exit "${code:-0}"
      fi
      exec /usr/bin/script -q -F "$f" /bin/cat > /dev/null
    fi
    if [ -f "$C/agent-down" ]; then echo "Error: is the Tart Guest Agent running?" >&2; exit 1; fi
    [ -f "$C/fail-exec" ] && { echo "Error: VM is not running" >&2; exit 1; }
    # A session's output reads and its close run for real on the host (machine/sessionguest.go).
    case "$*" in
      *greenroom-session-read*|*greenroom-session-close*) shift; exec "$@" ;;
    esac
    case "$*" in
      *authorized_keys*) [ -f "$C/fail-keyinstall" ] && { echo "Error: cannot write" >&2; exit 1; } ;;
    esac
    # Image build and image check (machine/base.go, imagecheck.go). Before the cases below:
    # these scripts are long and would match their patterns.
    case "$*" in
      *greenroom-base-profile*)
        [ -f "$C/fail-base" ] && { echo "base: check failed: appleevents-user-tart-guest-agent-com.apple.Safari" >&2; exit 1; }
        echo "base: ok"; exit 0 ;;
      *greenroom-toolchain-manifest*)
        [ -f "$C/fail-toolchain" ] && { echo "swift: command not found" >&2; exit 1; }
        echo '{"known":true,"xcode":false,"xctest":false,"swiftTesting":true,"swiftVersion":"Apple Swift version 6.3.3"}' > "$C/toolchain.json"
        cat "$C/toolchain.json"; exit 0 ;;
      *greenroom-toolchain-read*)
        cat "$C/toolchain.json" 2>/dev/null; exit 0 ;;
      *greenroom-softwareupdate-off*)
        [ -f "$C/fail-softwareupdate" ] && { echo "softwareupdate: not disabled: com.apple.mobile.softwareupdated" >&2; exit 1; }
        echo "softwareupdate: off"; exit 0 ;;
      *greenroom-check-softwareupdate*)
        cat "$C/softwareupdate" 2>/dev/null; exit 0 ;;
      *greenroom-check-boottime*)
        cat "$C/boottime" 2>/dev/null || echo "{ sec = 1 }"; exit 0 ;;
      *greenroom-check-reboot*)
        echo "{ sec = $(date +%s)$$ }" > "$C/boottime"; exit 0 ;;
      *greenroom-check-login*) exit 0 ;;
      *": greenroom-check-"*)
        for f in "$C"/fail-check-*; do
          [ -e "$f" ] || continue
          case "$*" in *"greenroom-check-${f##*/fail-check-}"*) echo "execution error: AppleEvent timed out. (-1712)" >&2; exit 1 ;; esac
        done ;;
    esac
    case "$*" in
      *"greenroom-input"*"--desktop"*)
        if [ -f "$C/desktop.json" ]; then cat "$C/desktop.json"; else
          echo '{"windows":[{"owner":"Window Server","name":"Menubar","layer":24,"alpha":1,"x":0,"y":0,"width":1024,"height":30},{"owner":"Dock","name":"Dock","layer":20,"alpha":1,"x":0,"y":0,"width":1024,"height":768}],"apps":[{"name":"Finder","bundleId":"com.apple.finder","pid":391}]}'
        fi
        exit 0 ;;
    esac
    # replayd's screen-capture approvals; answered before exec-exit-<n> and exec-codes, which are for machine_exec.
    case "$*" in
      *ScreenCaptureApprovals*)
        [ -f "$C/fail-capture-approval" ] && { echo "defaults: cannot write" >&2; exit 1; }
        # The check mode exits 3 for a stale record.
        case "$*" in *" sh check") [ -f "$C/capture-approval-stale" ] && exit 3 ;; esac
        # "app <path>" prints the bundle URL replayd keys the record by.
        case "$*" in *" sh app "*) for last; do :; done; echo "file:///Users/admin/${last#/}/" ;; esac
        exit 0 ;;
    esac
    # The lean image profile (machine/lean.go) confirms its read-back with "lean: ok".
    case "$*" in
      *StandardHideWidgets*)
        [ -f "$C/fail-lean" ] && { echo "lean: check failed: gamecenter" >&2; exit 1; }
        echo "lean: ok"
        exit 0 ;;
    esac
    case "$*" in
      *com.apple.Terminal.savedState*)
        : > "$C/terminal-quit-ran"
        [ -f "$C/fail-terminal" ] && { echo "Terminal is still running" >&2; exit 1; }
        echo "quit"
        exit 0 ;;
    esac
    case "$*" in
      *settimezone*)
        [ -f "$C/fail-timezone" ] && { echo "systemsetup: not permitted" >&2; exit 1; }
        exit 0 ;;
    esac
    case "$*" in
      *EnableStandardClickToShowDesktop*)
        [ -f "$C/fail-desktop-prefs" ] && { echo "defaults: cannot write" >&2; exit 1; }
        exit 0 ;;
    esac
    # The sshd readiness probe; answered before exec-exit-<n>, which is for machine_exec.
    case "$*" in
      *"nc -z 127.0.0.1 22"*)
        [ -f "$C/ssh-down" ] && { echo "Connection refused" >&2; exit 1; }
        exit 0 ;;
    esac
    # Input helper (ADR 0009): the boot check and the compile must match before the call.
    case "$*" in
      *greenroom-helper-check*)
        if [ -f "$C/input-stale" ]; then echo "stale greenroom-input-2"; else echo current; fi
        exit 0 ;;
      *swiftc*)
        [ -f "$C/fail-input-install" ] && { echo "swiftc: command not found" >&2; exit 1; }
        exit 0 ;;
      *"greenroom-input"*"--ui-base64"*)
        [ -f "$C/input-down" ] && { echo '{"error":"this machine has not granted Accessibility"}' >&2; exit 1; }
        if [ -f "$C/ui.json" ]; then cat "$C/ui.json"; else
          echo '{"app":{"name":"Finder","bundleId":"com.apple.finder","pid":1},"apps":["Finder"],"screen":{"width":1024,"height":768},"elements":[],"truncated":false}'
        fi
        exit 0 ;;
      *greenroom-input*)
        [ -f "$C/input-down" ] && { echo '{"error":"this machine refused the event"}' >&2; exit 1; }
        w=1024; h=768
        if [ -f "$C/screen" ]; then
          w=$(cut -d x -f1 "$C/screen"); h=$(cut -d x -f2 "$C/screen")
        fi
        printf '{"ok":true,"screen":{"width":%s,"height":%s}}\n' "$w" "$h"
        exit 0 ;;
    esac
    # machine_exec's own command (the greenroom-exec wrapper).
    case "$*" in
      *greenroom-exec*)
        [ -f "$C/exec-sleep" ] && sleep "$(cat "$C/exec-sleep")"
        [ -f "$C/exec-stdout" ] && { cat "$C/exec-stdout"; exit 0; } ;;
    esac
    for f in "$C"/exec-exit-*; do
      [ -e "$f" ] || continue
      code=$(basename "$f" | sed 's/exec-exit-//')
      echo "fake stdout"; echo "fake stderr" >&2
      exit "$code"
    done
    # Screenshots come before exec-codes so a timed frame capture cannot consume a queued code.
    case "$*" in
      *base64*) cat "$C/shot.b64" 2>/dev/null; exit 0 ;;
      *screencapture*) exit 0 ;;
    esac
    if [ -f "$C/exec-codes" ]; then
      code=$(head -n 1 "$C/exec-codes")
      tail -n +2 "$C/exec-codes" > "$C/exec-codes.next"
      mv "$C/exec-codes.next" "$C/exec-codes"
      [ -n "$code" ] || code=0
      echo "fake stdout"; echo "fake stderr" >&2
      exit "$code"
    fi
    echo "fake stdout"
    exit 0 ;;
  list)
    if [ -f "$C/list-empty" ]; then echo '[]'
    elif [ ! -f "$C/vmname" ] && [ ! -f "$C/vmnames" ]; then
      echo '[{"Source":"local","Name":"unrelated-vm","State":"running"}]'
    elif [ -f "$C/vmnames" ]; then
      out=""
      while IFS= read -r n; do
        [ -n "$n" ] || continue
        out="$out{\"Source\":\"local\",\"Name\":\"$n\",\"State\":\"running\"},"
      done < "$C/vmnames"
      printf '[%s]\n' "${out%,}"
    else
      printf '[{"Source":"local","Name":"%s","State":"running"}]\n' "$(cat "$C/vmname" 2>/dev/null || true)"
    fi
    exit 0 ;;
  stop)
    [ -f "$C/fail-stop" ] && { echo "Error: cannot stop" >&2; exit 1; }
    touch "$C/stopped"; exit 0 ;;
  delete)
    [ -f "$C/fail-delete" ] && { echo "Error: cannot delete" >&2; exit 1; }
    touch "$C/stopped"; exit 0 ;;
  *) echo "fake tart: unknown subcommand $sub" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, control
}

// ServeStarts counts how many times the live screen helper was started.
func ServeStarts(t *testing.T, control string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(Calls(t, control), "\n") {
		if strings.HasPrefix(line, "exec -i ") && strings.HasSuffix(line, " --serve") {
			n++
		}
	}
	return n
}

// Flag turns on one fake-tart behavior by creating its control file.
func Flag(t *testing.T, control, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Calls returns every argument list the daemon passed to the fake tart.
func Calls(t *testing.T, control string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(control, "calls.log"))
	if err != nil {
		return ""
	}
	return string(data)
}
