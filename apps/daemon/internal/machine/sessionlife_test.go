package machine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// liveSession returns the process behind a session, for tests that need to
// see whether it is still running after its handle is gone.
func liveSession(t *testing.T, mgr *Manager, runID, sessionID string) *PTYSession {
	t.Helper()
	_, s, err := mgr.session(runID, sessionID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return s
}

func waitNotRunning(t *testing.T, s *PTYSession, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.proc.Running() {
		if time.Now().After(deadline) {
			t.Fatal(why)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A machine whose tart process exits on its own leaves the map the same way a
// destroyed one does, and takes its sessions with it.
func TestAMachineThatStopsOnItsOwnEndsItsSessions(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	start, err := mgr.SessionStart(context.Background(), mc.RunID, "")
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	s := liveSession(t, mgr, mc.RunID, start.SessionID)

	stopped := make(chan struct{})
	var once sync.Once
	stop := mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind == "stopped" && ev.RunID == mc.RunID {
			once.Do(func() { close(stopped) })
		}
	})
	defer stop()
	testsupport.Flag(t, control, "stopped")
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("no stopped event for a machine whose tart process exited")
	}
	waitNotRunning(t, s, "a session outlived the machine that stopped under it")
}

// A start that found the machine before a concurrent Destroy must not leave a
// session on the machine that was destroyed: nothing could reach it again.
func TestASessionCannotStartOnADestroyedMachine(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ready := readyMachine(t, mgr)
	mc, err := mgr.get(ready.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Destroy(context.Background(), ready.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	err = mgr.reserveSession(mc, &PTYSession{ID: "late", out: newStream(16)})
	if err == nil || !strings.Contains(err.Error(), "no machine for run") {
		t.Errorf("a session was reserved on a destroyed machine (err %v)", err)
	}
	if len(mc.sessions) != 0 {
		t.Errorf("the destroyed machine holds %d sessions", len(mc.sessions))
	}
}

// If the machine goes while tart is starting a session, the process that did
// start is ended rather than left with no handle.
func TestASessionStartedAsItsMachineGoesIsEnded(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ready := readyMachine(t, mgr)
	mc, err := mgr.get(ready.RunID)
	if err != nil {
		t.Fatal(err)
	}
	s := &PTYSession{ID: "late", out: newStream(16)}
	if err := mgr.reserveSession(mc, s); err != nil {
		t.Fatalf("reserveSession: %v", err)
	}
	proc, err := mgr.tart.StartSession(mc.Name, "/bin/zsh", "-lc", "cat")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = proc.Close() }()
	if err := mgr.Destroy(context.Background(), ready.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if mgr.attachSession(mc, s, proc) {
		t.Error("a session attached to a machine that was destroyed while it started")
	}
}
