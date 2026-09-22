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

// An agent must be able to answer a prompt while its own long read waits, and
// the read must return the answer's output when it arrives.
func TestASendCompletesWhileAReadWaits(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	start, err := mgr.SessionStart(context.Background(), mc.RunID, "cat")
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}

	type result struct {
		out SessionReadResult
		err error
	}
	read := make(chan result, 1)
	go func() {
		out, err := mgr.SessionRead(context.Background(), mc.RunID, start.SessionID, 30*time.Second)
		read <- result{out, err}
	}()
	time.Sleep(300 * time.Millisecond) // let the read start waiting

	sendCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sent := time.Now()
	if _, err := mgr.SessionSend(sendCtx, mc.RunID, start.SessionID, "yes\n"); err != nil {
		t.Fatalf("SessionSend while a read waits: %v", err)
	}
	if took := time.Since(sent); took > 2*time.Second {
		t.Errorf("SessionSend took %s while a read waited", took)
	}
	select {
	case r := <-read:
		if r.err != nil || !strings.Contains(r.out.Output, "yes") {
			t.Errorf("the waiting read returned %+v, %v; want the answer", r.out, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting read did not wake on output")
	}
}

// Output that arrives in quick pieces, like a command's echo and then its
// answer, comes back as one read.
func TestAReadReturnsABurstTogether(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	start, err := mgr.SessionStart(context.Background(), mc.RunID, "cat")
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	go func() {
		ctx := context.Background()
		_, _ = mgr.SessionSend(ctx, mc.RunID, start.SessionID, "first\n")
		time.Sleep(sessionSettle / 3)
		_, _ = mgr.SessionSend(ctx, mc.RunID, start.SessionID, "second\n")
	}()
	out, err := mgr.SessionRead(context.Background(), mc.RunID, start.SessionID, 10*time.Second)
	if err != nil || !strings.Contains(out.Output, "first") || !strings.Contains(out.Output, "second") {
		t.Fatalf("read returned %q, %v; want both pieces", out.Output, err)
	}
}

// A session whose command has ended does not hold one of the machine's slots.
func TestEndedSessionsDoNotCountTowardTheCap(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "session-exits")

	for i := range maxSessionsPerMachine + 2 {
		start, err := mgr.SessionStart(context.Background(), mc.RunID, "true")
		if err != nil {
			t.Fatalf("SessionStart %d with every earlier command ended: %v", i+1, err)
		}
		waitNotRunning(t, liveSession(t, mgr, mc.RunID, start.SessionID), "a command that exits kept running")
	}
	live, err := mgr.get(mc.RunID)
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.Lock()
	n := len(live.sessions)
	mgr.mu.Unlock()
	if n > maxSessionsPerMachine {
		t.Errorf("the machine keeps %d sessions, more than the cap of %d", n, maxSessionsPerMachine)
	}
}
