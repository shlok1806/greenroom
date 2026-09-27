package machine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// eventLog keeps every lifecycle event of one run, in order.
type eventLog struct {
	mu     sync.Mutex
	events []LifecycleEvent
}

func recordEvents(t *testing.T, mgr *Manager, runID string) *eventLog {
	t.Helper()
	l := &eventLog{}
	t.Cleanup(mgr.Listen(func(ev LifecycleEvent) {
		if ev.RunID != runID || ev.Kind == "frame" || ev.Kind == "boot" || ev.Kind == "step" {
			return
		}
		l.mu.Lock()
		l.events = append(l.events, ev)
		l.mu.Unlock()
	}))
	return l
}

// kinds lists the events from the first "rebooting" on as "kind" or "kind+reboot". The boot's
// "ready" can land after the test's Wait returned, so it is not counted.
func (l *eventLog) kinds() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, ev := range l.events {
		if len(out) == 0 && ev.Kind != "rebooting" {
			continue
		}
		k := ev.Kind
		if ev.Reboot {
			k += "+reboot"
		}
		out = append(out, k)
	}
	return out
}

// waitFor blocks until the machine leaves Rebooting and returns it.
func waitRebooted(t *testing.T, mgr *Manager, runID string) *Machine {
	t.Helper()
	got, err := mgr.Wait(context.Background(), runID, 30*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status == Rebooting {
		t.Fatal("the machine is still rebooting after 30 s")
	}
	return got
}

func rebootStep(t *testing.T, dir string, seq int) Step {
	t.Helper()
	for _, s := range readSteps(t, dir) {
		if s.Seq == seq {
			if s.Tool != "machine_reboot" {
				t.Fatalf("step %d is %s, want machine_reboot", seq, s.Tool)
			}
			return s
		}
	}
	t.Fatalf("no step %d in the record", seq)
	return Step{}
}

func countLines(log, prefix string) int {
	n := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// Issue #187: a reboot stops the VM and boots the same clone again. The run, its record and
// its disk stay; nothing is deleted and the watcher does not take the expected stop for a VM
// that went away.
func TestRebootKeepsTheCloneAndTheRun(t *testing.T) {
	mgr, root, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	events := recordEvents(t, mgr, mc.RunID)

	snap, seq, err := mgr.Reboot(context.Background(), mc.RunID)
	if err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if snap.Status != Rebooting || snap.RunID != mc.RunID || seq == 0 {
		t.Fatalf("Reboot returned %s for %s at step %d, want rebooting, the same run and a step", snap.Status, snap.RunID, seq)
	}
	got := waitRebooted(t, mgr, mc.RunID)
	if got.Status != Ready || got.IP == "" || got.Error != "" {
		t.Fatalf("after the reboot the machine is %s (ip %q, error %q), want ready", got.Status, got.IP, got.Error)
	}

	calls := testsupport.Calls(t, control)
	if !strings.Contains(calls, "stop "+mc.Name+" --timeout 20") {
		t.Errorf("the reboot did not stop the VM with a grace\ncalls:\n%s", calls)
	}
	// tart stop powers the VM off at once: the guest's disk is flushed first, or what it
	// wrote in its last seconds is lost.
	if sync, stop := strings.Index(calls, "exec "+mc.Name+" /bin/sync"), strings.Index(calls, "stop "+mc.Name); sync < 0 || sync > stop {
		t.Errorf("the reboot did not sync the guest before stopping it\ncalls:\n%s", calls)
	}
	if n := countLines(calls, "run "+mc.Name); n != 2 {
		t.Errorf("tart run %s ran %d times, want 2 (boot and reboot)", mc.Name, n)
	}
	if strings.Contains(calls, "delete "+mc.Name) || countLines(calls, "clone ") != 1 {
		t.Errorf("the reboot deleted or recloned the VM\ncalls:\n%s", calls)
	}
	// The old tart run has exited by now (the reboot waits for it); its watcher must stand down.
	time.Sleep(300 * time.Millisecond)
	if l := mgr.List(); len(l) != 1 || l[0].Status != Ready {
		t.Fatalf("after the reboot the daemon lists %+v, want the one machine ready", l)
	}
	if k := strings.Join(events.kinds(), ","); k != "rebooting,ready+reboot" {
		t.Errorf("events = %s, want rebooting,ready+reboot", k)
	}

	dir := filepath.Join(root, "runs", mc.RunID)
	step := rebootStep(t, dir, seq)
	out, _ := step.Output.(map[string]any)
	if step.Error != "" || out["status"] != string(Ready) || out["stopSeconds"] == nil || out["syncSeconds"] == nil || out["agentSeconds"] == nil || out["sshSeconds"] == nil {
		t.Errorf("the reboot step is %+v, want status ready with its phases timed", step)
	}
	if man := readManifest(t, dir); man.DestroyedAt != nil {
		t.Error("a reboot ended the run")
	}
	if phases := mustLive(t, mgr, mc.RunID).BootPhases(); len(phases) == 0 || phases[0].Phase != PhaseStop || phases[1].Phase != PhaseStart {
		t.Errorf("the reboot's phases are %+v, want stop, start, then the boot's", phases)
	}

	// The machine works, and the new tart run is watched: a VM that stops on its own now is noticed.
	if _, err := mgr.Exec(context.Background(), mc.RunID, "true", "", time.Minute); err != nil {
		t.Fatalf("Exec after the reboot: %v", err)
	}
	testsupport.Flag(t, control, "stopped")
	deadline := time.Now().Add(20 * time.Second)
	for mgr.Live(mc.RunID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if mgr.Live(mc.RunID) {
		t.Fatal("a rebooted machine whose VM then stopped on its own is still listed")
	}
	// machineGone leaves the map first and marks the run ended in its directory after; wait for
	// its "stopped" event, sent last, or that write races t.TempDir's cleanup.
	for !strings.HasSuffix(strings.Join(events.kinds(), ","), ",stopped") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

func mustLive(t *testing.T, mgr *Manager, runID string) *Machine {
	t.Helper()
	mc, err := mgr.get(runID)
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mc.publicLocked()
}

// While a reboot runs, every guest call and a second reboot are refused at once with words
// that say what to do; machine_list and Wait report rebooting.
func TestCallsDuringARebootAreRefusedAtOnce(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "agent-down") // the guest does not answer yet, so the reboot waits

	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); !errors.Is(err, ErrRebooting) || !strings.Contains(err.Error(), "machine_wait") {
		t.Errorf("a second reboot: %v, want ErrRebooting pointing at machine_wait", err)
	}
	at := time.Now()
	if _, err := mgr.Exec(context.Background(), mc.RunID, "true", "", time.Minute); !errors.Is(err, ErrRebooting) {
		t.Errorf("Exec during a reboot: %v, want ErrRebooting", err)
	}
	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); !errors.Is(err, ErrRebooting) {
		t.Errorf("Screenshot during a reboot: %v, want ErrRebooting", err)
	}
	if _, err := mgr.SessionStart(context.Background(), mc.RunID, ""); !errors.Is(err, ErrRebooting) {
		t.Errorf("SessionStart during a reboot: %v, want ErrRebooting", err)
	}
	if _, err := mgr.WatchScreen(context.Background(), mc.RunID); !errors.Is(err, ErrNotReady) {
		t.Errorf("WatchScreen during a reboot: %v, want ErrNotReady", err)
	}
	if d := time.Since(at); d > 5*time.Second {
		t.Errorf("the refusals took %s; they must not wait for the reboot", d)
	}
	if l := mgr.List(); len(l) != 1 || l[0].Status != Rebooting {
		t.Errorf("machine_list shows %+v, want the machine rebooting", l)
	}
	if got, _ := mgr.Wait(context.Background(), mc.RunID, 50*time.Millisecond); got.Status != Rebooting {
		t.Errorf("Wait returned %s, want rebooting until the guest answers", got.Status)
	}

	if err := os.Remove(filepath.Join(control, "agent-down")); err != nil {
		t.Fatal(err)
	}
	if got := waitRebooted(t, mgr, mc.RunID); got.Status != Ready {
		t.Fatalf("the reboot ended %s (%s), want ready", got.Status, got.Error)
	}
}

// What lived only in the running guest ends: sessions, running commands (with an error
// saying why), the lease and the live screen. Finished results stay collectable.
func TestRebootEndsSessionsCommandsTheLeaseAndTheScreen(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	ctx := context.Background()

	done, err := mgr.ExecStart(ctx, mc.RunID, "echo done", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := mgr.ExecWait(ctx, mc.RunID, done.ExecID, 10*time.Second); err != nil || st.Running {
		t.Fatalf("ExecWait: %+v, %v", st, err)
	}
	start, err := mgr.SessionStart(ctx, mc.RunID, "")
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	s := liveSession(t, mgr, mc.RunID, start.SessionID)
	if err := os.WriteFile(filepath.Join(control, "exec-sleep"), []byte("30"), 0o644); err != nil {
		t.Fatal(err)
	}
	running, err := mgr.ExecStart(ctx, mc.RunID, "sleep 30", "", time.Minute)
	if err != nil || !running.Running {
		t.Fatalf("ExecStart: %+v, %v", running, err)
	}
	if _, _, err := mgr.TakeControl(mc.RunID, HolderCoder, time.Minute); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	w := watchScreen(t, mgr, mc.RunID)
	live := mustRaw(t, mgr, mc.RunID)
	live.input.screen.Store(&Screen{Width: 1, Height: 1})
	live.input.uiMu.Lock()
	live.input.ui = map[string]*UITree{HolderCoder: {}}
	live.input.looks = map[string]uint64{HolderVerifier: 3}
	live.input.uiMu.Unlock()

	if _, _, err := mgr.Reboot(ctx, mc.RunID); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	waitNotRunning(t, s, "a session outlived the reboot of its machine")
	st, err := mgr.ExecWait(ctx, mc.RunID, running.ExecID, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "machine rebooted") || st.Running {
		t.Errorf("a command running across a reboot: %+v, %v; want it ended with an error naming the reboot", st, err)
	}
	if st, err := mgr.ExecWait(ctx, mc.RunID, done.ExecID, time.Second); err != nil || st.ExitCode == nil {
		t.Errorf("a command finished before the reboot lost its result: %+v, %v", st, err)
	}
	select {
	case _, ok := <-w.C:
		for ok {
			_, ok = <-w.C
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the live screen outlived the reboot")
	}
	if werr := w.Err(); werr == nil || !strings.Contains(werr.Error(), "rebooting") {
		t.Errorf("the live screen ended with %v, want a reason naming the reboot", werr)
	}

	if got := waitRebooted(t, mgr, mc.RunID); got.Status != Ready {
		t.Fatalf("the reboot ended %s (%s)", got.Status, got.Error)
	}
	if _, held := mgr.ControlState(mc.RunID); held {
		t.Error("the control lease outlived the reboot")
	}
	if _, err := mgr.SessionRead(ctx, mc.RunID, start.SessionID, 0); err == nil {
		t.Error("a session from before the reboot can still be read")
	}
	if live.input.screen.Load() != nil {
		t.Error("the helper's screen size from before the reboot is still cached")
	}
	live.input.uiMu.Lock()
	ui, looks := live.input.ui, live.input.looks
	live.input.uiMu.Unlock()
	if ui != nil || looks != nil {
		t.Errorf("UI trees %v and looks %v from before the reboot are still cached", ui, looks)
	}
}

// mustRaw returns the live machine itself, with its handles.
func mustRaw(t *testing.T, mgr *Manager, runID string) *Machine {
	t.Helper()
	mc, err := mgr.get(runID)
	if err != nil {
		t.Fatal(err)
	}
	return mc
}

// A guest that does not come back within the reboot's bound leaves the machine failed with
// the reason and its disk: nothing is deleted, the run goes on, and machine_destroy deletes it.
func TestARebootThatTimesOutKeepsTheDisk(t *testing.T) {
	mgr, root, control := newTestManager(t, WithRebootTimeout(time.Second))
	mc := readyMachine(t, mgr)
	events := recordEvents(t, mgr, mc.RunID)
	testsupport.Flag(t, control, "agent-down")

	_, seq, err := mgr.Reboot(context.Background(), mc.RunID)
	if err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	got := waitRebooted(t, mgr, mc.RunID)
	if got.Status != Failed || !strings.Contains(got.Error, "did not come back within 1s") || !strings.Contains(got.Error, "disk is kept") {
		t.Fatalf("a reboot whose guest never answered ended %s: %q", got.Status, got.Error)
	}
	calls := testsupport.Calls(t, control)
	if strings.Contains(calls, "delete "+mc.Name) {
		t.Fatalf("a failed reboot deleted the VM\ncalls:\n%s", calls)
	}
	if n := countLines(calls, "stop "+mc.Name); n != 2 {
		t.Errorf("tart stop %s ran %d times, want 2 (the reboot, then the failed guest)", mc.Name, n)
	}
	if !mgr.Live(mc.RunID) {
		t.Fatal("a failed reboot dropped the machine")
	}
	dir := filepath.Join(root, "runs", mc.RunID)
	if man := readManifest(t, dir); man.DestroyedAt != nil {
		t.Error("a failed reboot ended the run")
	}
	if step := rebootStep(t, dir, seq); !strings.Contains(step.Error, "did not come back") {
		t.Errorf("the reboot step's error is %q", step.Error)
	}
	if k := strings.Join(events.kinds(), ","); k != "rebooting,failed+reboot" {
		t.Errorf("events = %s, want rebooting,failed+reboot", k)
	}
	if _, err := mgr.Exec(context.Background(), mc.RunID, "true", "", time.Minute); err == nil || !strings.Contains(err.Error(), "reboot failed") {
		t.Errorf("Exec on a failed reboot: %v, want the reason", err)
	}

	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if calls := testsupport.Calls(t, control); !strings.Contains(calls, "delete "+mc.Name) {
		t.Errorf("destroying a failed reboot left its VM behind\ncalls:\n%s", calls)
	}
}

// A failed reboot can be tried again: the disk it kept boots.
func TestAFailedRebootCanBeRetried(t *testing.T) {
	mgr, _, control := newTestManager(t, WithRebootTimeout(time.Second))
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "agent-down")
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if got := waitRebooted(t, mgr, mc.RunID); got.Status != Failed {
		t.Fatalf("the reboot ended %s, want failed", got.Status)
	}
	if err := os.Remove(filepath.Join(control, "agent-down")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatalf("retrying a failed reboot: %v", err)
	}
	if got := waitRebooted(t, mgr, mc.RunID); got.Status != Ready || got.Error != "" {
		t.Fatalf("the retry ended %s (%q), want ready", got.Status, got.Error)
	}
}

// A machine still booting, or one whose failed boot deleted its VM, is not rebooted.
func TestRebootRefusesABootingOrDeletedMachine(t *testing.T) {
	mgr, _, control := newTestManager(t, WithReadyTimeout(time.Second))
	testsupport.Flag(t, control, "agent-down")
	mc, err := mgr.Create(context.Background(), testImage)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err == nil || !strings.Contains(err.Error(), "still booting") {
		t.Errorf("rebooting a booting machine: %v, want a refusal", err)
	}
	if got, _ := mgr.Wait(context.Background(), mc.RunID, 20*time.Second); got.Status != Failed {
		t.Fatalf("the boot ended %s, want failed", got.Status)
	}
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err == nil || !strings.Contains(err.Error(), "nothing to reboot") {
		t.Errorf("rebooting a machine whose boot failed: %v, want a refusal", err)
	}
	if _, _, err := mgr.Reboot(context.Background(), "no-such-run"); err == nil || !strings.Contains(err.Error(), "no machine for run") {
		t.Errorf("rebooting an unknown run: %v", err)
	}
}

// A machine reattached after a daemon restart has no tart run of this daemon's: the reboot
// waits for tart to list it stopped, and its polling watcher does not take that for a VM
// that went away. After the reboot this daemon owns its tart run.
func TestRebootingAReattachedMachine(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	mc := &Machine{RunID: "old", Name: "greenroom-old", Image: testImage, Status: Ready,
		CreatedAt: time.Now().UTC(), Dir: filepath.Join(root, "runs", "old")}
	writeState(t, root, []*Machine{mc})
	if err := os.WriteFile(filepath.Join(control, "vmname"), []byte(mc.Name), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(root, discardLog(), WithTartBin(bin), WithSSHProbe(sshAnswers),
		WithReadyTimeout(10*time.Second), WithFrameInterval(0), WithVMPollInterval(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)
	events := recordEvents(t, mgr, mc.RunID)

	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if got := waitRebooted(t, mgr, mc.RunID); got.Status != Ready {
		t.Fatalf("the reboot ended %s (%s)", got.Status, got.Error)
	}
	time.Sleep(200 * time.Millisecond) // ten polls of the old watcher
	if k := strings.Join(events.kinds(), ","); k != "rebooting,ready+reboot" {
		t.Errorf("events = %s, want rebooting,ready+reboot", k)
	}
	calls := testsupport.Calls(t, control)
	if !strings.Contains(calls, "stop greenroom-old --timeout") || !strings.Contains(calls, "run greenroom-old") || strings.Contains(calls, "delete greenroom-old") {
		t.Errorf("the reattached reboot did not stop and start the VM, or deleted it\ncalls:\n%s", calls)
	}
	raw := mustRaw(t, mgr, mc.RunID)
	mgr.mu.Lock()
	owned := raw.proc != nil
	mgr.mu.Unlock()
	if !owned {
		t.Error("after the reboot the daemon does not own the machine's tart run")
	}
}

// A daemon that died during a reboot leaves the machine rebooting in state.json. The next one
// keeps it as failed with its disk, so it can be rebooted again or destroyed, never dropped.
func TestLoadStateKeepsAMachineWhoseRebootTheDaemonDiedIn(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	mc := &Machine{RunID: "mid", Name: "greenroom-mid", Image: testImage, Status: Rebooting,
		CreatedAt: time.Now().UTC(), Dir: filepath.Join(root, "runs", "mid")}
	writeState(t, root, []*Machine{mc})
	if err := os.WriteFile(filepath.Join(control, "list.json"),
		[]byte(`[{"Source":"local","Name":"greenroom-mid","State":"stopped"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(root, discardLog(), WithTartBin(bin), WithSSHProbe(sshAnswers),
		WithReadyTimeout(10*time.Second), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)
	l := mgr.List()
	if len(l) != 1 || l[0].Status != Failed || !strings.Contains(l[0].Error, "machine_reboot") {
		t.Fatalf("reattached %+v, want the machine failed with advice", l)
	}
	if man := readManifest(t, mc.Dir); man.DestroyedAt != nil {
		t.Error("the run was ended")
	}
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if got := waitRebooted(t, mgr, mc.RunID); got.Status != Ready {
		t.Fatalf("the reboot ended %s (%s)", got.Status, got.Error)
	}
}

// Destroy during a reboot takes the machine: the reboot records nothing more and the VM is deleted.
func TestDestroyDuringARebootDeletesTheVM(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "agent-down")
	events := recordEvents(t, mgr, mc.RunID)
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if calls := testsupport.Calls(t, control); !strings.Contains(calls, "delete "+mc.Name) {
		t.Errorf("destroy during a reboot left the VM\ncalls:\n%s", calls)
	}
	if k := strings.Join(events.kinds(), ","); k != "rebooting,destroyed" {
		t.Errorf("events = %s, want rebooting,destroyed", k)
	}
}

// Frames stop for the reboot and start again once it is ready.
func TestFramesResumeAfterAReboot(t *testing.T) {
	mgr, _, control := newTestManager(t, WithFrameInterval(20*time.Millisecond))
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	frames := countFrameEvents(mgr)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if got := waitRebooted(t, mgr, mc.RunID); got.Status != Ready {
		t.Fatalf("the reboot ended %s", got.Status)
	}
	before := frames()
	deadline := time.Now().Add(10 * time.Second)
	for frames() <= before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if frames() <= before {
		t.Error("no frame was recorded after the reboot")
	}
}
