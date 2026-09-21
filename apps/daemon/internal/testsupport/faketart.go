// Package testsupport holds helpers shared by the daemon test suites.
package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// FakeTart writes a shell script that answers every tart subcommand the
// daemon uses, so that tests reach every path without a real VM.
//
// Behavior is driven by files in a control directory:
//
//	fail-clone, fail-run, fail-ip, fail-exec, fail-stop, fail-delete
//	    the matching subcommand exits 1 with a message
//	exec-exit-<n>   `tart exec` returns exit code n
//	exec-codes      `tart exec` returns the next newline-separated code in
//	                this file on each call, consuming it, then returns 0
//	                once the file is empty; for a test that runs several
//	                commands in one turn and needs their exit codes to
//	                differ
//	agent-down      `tart exec` fails as if the guest agent is unreachable
//	ssh-down        the in-guest `nc -z 127.0.0.1 22` probe exits 1 with
//	                "Connection refused", as it does while sshd is starting
//	fail-input-install  compiling the guest input helper fails, as it does on
//	                a machine with no Swift toolchain
//	input-down      the input helper refuses every event
//	screen          "<width>x<height>" the input helper reports as the guest
//	                display size; without it, 1024x768
//	list-empty      `tart list` returns an empty JSON array
//	vmnames         one VM name per line; `tart list` reports each as running
//	vmname          one VM name; `tart list` reports it as the only running VM
//
// With neither file, `tart list` reports one unrelated running VM, the way a
// host with other tart usage would.
//
// Every invocation is appended to <control>/calls.log so a test can assert
// the exact argument list the daemon built.
func FakeTart(t *testing.T) (bin string, control string) {
	t.Helper()
	dir := t.TempDir()
	control = filepath.Join(dir, "control")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "tart")

	script := `#!/bin/sh
C="` + control + `"
printf '%s\n' "$*" >> "$C/calls.log"
sub="$1"; shift
case "$sub" in
  clone)
    [ -f "$C/fail-clone" ] && { echo "Error: image not found" >&2; exit 1; }
    exit 0 ;;
  run)
    [ -f "$C/fail-run" ] && { echo "The number of VMs exceeds the system limit" >&2; exit 1; }
    # A real "tart run" stays in the foreground for the life of the VM, so
    # this waits too. It must never outlive the test: it stops when the VM is
    # stopped, when the control directory goes away with the test's temporary
    # directory, and in any case after the cap below. Without those exits a
    # test that does not destroy its machine leaks a process that spins
    # forever, and enough of them will bring a host to its knees.
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
    if [ -f "$C/agent-down" ]; then echo "Error: is the Tart Guest Agent running?" >&2; exit 1; fi
    [ -f "$C/fail-exec" ] && { echo "Error: VM is not running" >&2; exit 1; }
    case "$*" in
      *authorized_keys*) [ -f "$C/fail-keyinstall" ] && { echo "Error: cannot write" >&2; exit 1; } ;;
    esac
    # The ssh readiness probe runs inside the guest over vsock, so it arrives
    # here rather than as a host-side dial. Answer it before the generic
    # exec-exit-<n> hook, which speaks for machine_exec and not for boot.
    case "$*" in
      *"nc -z 127.0.0.1 22"*)
        [ -f "$C/ssh-down" ] && { echo "Connection refused" >&2; exit 1; }
        exit 0 ;;
    esac
    # Computer use (ADR 0009). The helper is compiled in the guest once, then
    # called with a base64 payload; both arrive here as a /bin/sh script, so
    # the compile is matched before the call.
    case "$*" in
      *swiftc*)
        [ -f "$C/fail-input-install" ] && { echo "swiftc: command not found" >&2; exit 1; }
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
    for f in "$C"/exec-exit-*; do
      [ -e "$f" ] || continue
      code=$(basename "$f" | sed 's/exec-exit-//')
      echo "fake stdout"; echo "fake stderr" >&2
      exit "$code"
    done
    # Screenshot support: the daemon base64s a PNG out of the guest. This
    # comes before exec-codes because the frame recorder screenshots on a
    # timer, and a capture must not eat an exit code queued for a command.
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
