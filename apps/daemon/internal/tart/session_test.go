package tart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sessionBin writes a stand-in tart whose `exec` runs body.
func sessionBin(t *testing.T, body string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tart")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Client{Bin: path}
}

// recordKills records which groups Close signals, still killing them.
func recordKills(t *testing.T) func() []int {
	t.Helper()
	var mu sync.Mutex
	var got []int
	orig := killGroup
	killGroup = func(pgid int) error {
		mu.Lock()
		got = append(got, pgid)
		mu.Unlock()
		return orig(pgid)
	}
	t.Cleanup(func() { killGroup = orig })
	return func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), got...)
	}
}

func waitSessionEnd(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.Running() {
		if time.Now().After(deadline) {
			t.Fatal("the session never ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A reaped pid (and so its process group) may be reused, so closing a
// finished session must signal nothing.
func TestClosingAFinishedSessionSignalsNothing(t *testing.T) {
	kills := recordKills(t)
	s, err := sessionBin(t, "exit 0").StartSession("vm", "true")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	waitSessionEnd(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := kills(); len(got) != 0 {
		t.Errorf("closing a finished session signalled process groups %v", got)
	}
}

// A session that is still running is ended with its whole process group.
func TestClosingARunningSessionKillsItsGroup(t *testing.T) {
	kills := recordKills(t)
	s, err := sessionBin(t, "exec sleep 60").StartSession("vm", "true")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	pid := s.cmd.Process.Pid
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := kills(); len(got) != 1 || got[0] != pid {
		t.Errorf("Close signalled %v, want the session's own group %d", got, pid)
	}
	if s.Running() {
		t.Error("the session is still running after Close")
	}
}

// Without an exit watch Close must still kill a running session.
func TestClosingKillsASessionWhoseExitCannotBeWatched(t *testing.T) {
	orig := watchExit
	watchExit = func(int) error { return errors.New("kqueue: too many open files") }
	t.Cleanup(func() { watchExit = orig })
	kills := recordKills(t)

	s, err := sessionBin(t, "exec sleep 60").StartSession("vm", "true")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	pid := s.cmd.Process.Pid
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := kills(); len(got) != 1 || got[0] != pid {
		t.Errorf("Close signalled %v, want the session's own group %d", got, pid)
	}
	if s.Running() {
		t.Error("the session is still running after Close")
	}
}

// A session that outlives its kill is reported, not closed silently.
func TestClosingReportsASessionThatWouldNotDie(t *testing.T) {
	orig, origWait := killGroup, closeWait
	killGroup = func(int) error { return nil }
	closeWait = 100 * time.Millisecond
	t.Cleanup(func() { killGroup, closeWait = orig, origWait })

	s, err := sessionBin(t, "exec sleep 60").StartSession("vm", "true")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_ = orig(s.cmd.Process.Pid)
		waitSessionEnd(t, s)
	})
	if err := s.Close(); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("Close = %v, want it to say the session is still running", err)
	}
}

// A tart failure is an error; a command exiting non-zero is not.
func TestAFinishedSessionExplainsATartFailureOnly(t *testing.T) {
	failed, err := sessionBin(t, `echo "Error: VM is not running" >&2; exit 1`).StartSession("vm", "true")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	waitSessionEnd(t, failed)
	defer func() { _ = failed.Close() }()
	if err := failed.Err(); err == nil {
		t.Error("a session tart refused reported no error")
	}

	exited, err := sessionBin(t, "exit 3").StartSession("vm", "false")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	waitSessionEnd(t, exited)
	defer func() { _ = exited.Close() }()
	if err := exited.Err(); err != nil {
		t.Errorf("a command that exited non-zero was reported as an error: %v", err)
	}
}

// Issue #30, ADR 0016: a session reaches tart as `exec -i <vm> <command>` on a
// plain pipe, never `-t` and never a terminal, and what is written arrives on
// the command's stdin.
func TestASessionIsANonTTYExecOnAPipe(t *testing.T) {
	dir := t.TempDir()
	args, got := filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	s, err := sessionBin(t, `printf '%s\n' "$*" > `+args+`; [ -t 0 ] && exit 9; [ -t 1 ] && exit 8; exec cat > `+got).
		StartSession("vm", "/bin/sh", "-c", "wrapper")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := s.Write([]byte("typed\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(got)
		if string(b) == "typed\n" {
			break
		}
		if !s.Running() {
			code, _ := s.ExitCode()
			t.Fatalf("the stand-in tart exited %d (9: stdin was a terminal, 8: stdout was)", code)
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdin got %q, want what was written", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("a second Close failed: %v", err)
	}
	a, _ := os.ReadFile(args)
	if strings.TrimSpace(string(a)) != "exec -i vm /bin/sh -c wrapper" {
		t.Errorf("tart got %q", a)
	}
}

// tart dying of a signal is a failure whatever it prints. The Swift trap is
// what tart 2.37.0's `exec -t` hit without a terminal.
func TestASessionWhoseTartCrashesExplainsWhy(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"swift trap",
			`echo "Fatal error: 'try!' expression unexpectedly raised an error: failed to get terminal size" >&2; kill -TRAP $$`,
			"failed to get terminal size"},
		{"killed with nothing printed", `kill -KILL $$`, "killed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := sessionBin(t, c.body).StartSession("vm", "true")
			if err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			waitSessionEnd(t, s)
			defer func() { _ = s.Close() }()
			err = s.Err()
			if err == nil {
				t.Fatal("a session whose tart crashed reported no error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not say %q", err, c.want)
			}
		})
	}
}
