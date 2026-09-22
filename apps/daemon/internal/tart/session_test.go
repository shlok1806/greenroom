package tart

import (
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
	killGroup = func(pgid int) {
		mu.Lock()
		got = append(got, pgid)
		mu.Unlock()
		orig(pgid)
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

// tart dying of a signal is a failure whatever it prints. The Swift trap is
// what `tart exec -t` hits without a terminal.
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
