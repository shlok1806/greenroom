package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// logBuffer collects slog output from many goroutines.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *logBuffer) logger() *slog.Logger { return slog.New(slog.NewTextHandler(l, nil)) }

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func stepTools(t *testing.T, dir string) []string {
	t.Helper()
	var tools []string
	for _, s := range readSteps(t, dir) {
		tools = append(tools, s.Tool)
	}
	return tools
}

// writeState stands in for a previous daemon that left these machines in state.json.
func writeState(t *testing.T, root string, machines []*Machine) {
	t.Helper()
	data, err := json.Marshal(machines)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// readOnly makes dir unwritable for the rest of the test.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func TestDestroyDuringBootEndsTheBootSilently(t *testing.T) {
	probed := make(chan struct{}, 1)
	sshNeverAnswers := func(context.Context, string, string) error {
		select {
		case probed <- struct{}{}:
		default:
		}
		return errors.New("connection refused")
	}
	const readyTimeout = 2 * time.Second
	mgr, root, control := newTestManager(t, WithSSHProbe(sshNeverAnswers), WithReadyTimeout(readyTimeout))
	var mu sync.Mutex
	var kinds []string
	stop := mgr.Listen(func(ev LifecycleEvent) {
		mu.Lock()
		kinds = append(kinds, ev.Kind)
		mu.Unlock()
	})
	defer stop()

	mc, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-probed:
	case <-time.After(20 * time.Second):
		t.Fatal("the boot never reached the ssh wait")
	}
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// Past the point where an uncancelled boot would time out and record its failure.
	time.Sleep(readyTimeout + time.Second)

	dir := filepath.Join(root, "runs", mc.RunID)
	if got := strings.Join(stepTools(t, dir), ","); got != "machine_create,machine_destroy" {
		t.Errorf("steps are %s, want machine_create,machine_destroy", got)
	}
	if n := strings.Count(testsupport.Calls(t, control), "delete "+mc.Name); n != 1 {
		t.Errorf("the VM was deleted %d times, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, k := range kinds {
		if k == "ready" || k == "failed" {
			t.Errorf("a destroyed machine emitted %q; events: %v", k, kinds)
		}
	}
}

func TestDestroyIgnoresACancelledCallerContext(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := mgr.Destroy(ctx, mc.RunID); err != nil {
		t.Fatalf("Destroy with a cancelled context: %v", err)
	}
	if calls := testsupport.Calls(t, control); !strings.Contains(calls, "delete "+mc.Name) {
		t.Errorf("the VM was never deleted\ncalls:\n%s", calls)
	}
}

func TestDestroyLogsAFailedDelete(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	var logs logBuffer
	mgr, err := NewManager(t.TempDir(), logs.logger(), WithTartBin(bin), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "fail-delete")

	if err := mgr.Destroy(context.Background(), mc.RunID); err == nil {
		t.Error("Destroy reported success although the delete failed")
	}
	if !strings.Contains(logs.String(), "cannot delete the VM") {
		t.Errorf("the failed delete was not logged:\n%s", logs.String())
	}
	if n := len(mgr.List()); n != 0 {
		t.Errorf("%d machines listed after destroy, want 0", n)
	}
}

func TestCreateCleansUpWhenStateCannotBeSaved(t *testing.T) {
	mgr, root, control := newTestManager(t)
	readOnly(t, root) // runs/ stays writable; state.json cannot be replaced

	if _, err := mgr.Create(context.Background(), testImage, false); err == nil {
		t.Fatal("Create succeeded although state.json could not be written")
	}
	if n := len(mgr.List()); n != 0 {
		t.Errorf("a failed create left %d machines listed", n)
	}
	if calls := testsupport.Calls(t, control); !strings.Contains(calls, "delete greenroom-") {
		t.Errorf("a failed create left its VM behind\ncalls:\n%s", calls)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "runs"))
	if len(entries) != 1 {
		t.Fatalf("found %d run directories, want 1", len(entries))
	}
	if man := readManifest(t, filepath.Join(root, "runs", entries[0].Name())); man.DestroyedAt == nil {
		t.Error("a failed create left the run with no end")
	}
}

func TestSaveStateLeavesTheOldFileOnAFailedWrite(t *testing.T) {
	mgr, root, _ := newTestManager(t)
	readyMachine(t, mgr)
	path := filepath.Join(root, "state.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if tmps, _ := filepath.Glob(filepath.Join(root, ".state.json-*")); len(tmps) != 0 {
		t.Errorf("a successful save left temp files: %v", tmps)
	}

	readOnly(t, root)
	mgr.mu.Lock()
	mgr.machines["ghost"] = &Machine{RunID: "ghost"}
	mgr.mu.Unlock()
	defer func() {
		mgr.mu.Lock()
		delete(mgr.machines, "ghost")
		mgr.mu.Unlock()
	}()
	if err := mgr.saveState(); err == nil {
		t.Fatal("saveState succeeded in a read-only directory")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("a failed save changed state.json")
	}
}

func TestLoadStateSkipsARunItCannotReopen(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	good := &Machine{RunID: "good", Name: "greenroom-good", Image: testImage, Status: Ready,
		CreatedAt: time.Now().UTC(), Dir: filepath.Join(root, "runs", "good")}
	bad := &Machine{RunID: "bad", Name: "greenroom-bad", Image: testImage, Status: Ready,
		CreatedAt: time.Now().UTC(), Dir: filepath.Join(blocker, "run")}
	writeState(t, root, []*Machine{bad, good})
	if err := os.WriteFile(filepath.Join(control, "vmnames"), []byte("greenroom-good\ngreenroom-bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(root, discardLog(), WithTartBin(bin), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatalf("one bad run stopped the daemon: %v", err)
	}
	got := mgr.List()
	if len(got) != 1 || got[0].RunID != "good" {
		t.Errorf("reattached %+v, want only the good run", got)
	}
}

func TestAReattachedMachineWhoseVMStopsIsNoticed(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	mc := &Machine{RunID: "old", Name: "greenroom-old", Image: testImage, Status: Ready,
		CreatedAt: time.Now().UTC(), Dir: filepath.Join(root, "runs", "old")}
	writeState(t, root, []*Machine{mc})
	if err := os.WriteFile(filepath.Join(control, "vmname"), []byte(mc.Name), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(root, discardLog(), WithTartBin(bin), WithSSHProbe(sshAnswers),
		WithFrameInterval(0), WithVMPollInterval(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	var once sync.Once
	defer mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind == "stopped" && ev.RunID == mc.RunID {
			once.Do(func() { close(stopped) })
		}
	})()
	if n := len(mgr.List()); n != 1 {
		t.Fatalf("reattached %d machines, want 1", n)
	}

	testsupport.Flag(t, control, "list-empty")
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("no stopped event for a reattached machine whose VM went away")
	}
	if n := len(mgr.List()); n != 0 {
		t.Errorf("%d machines still listed, want 0", n)
	}
	if calls := testsupport.Calls(t, control); !strings.Contains(calls, "delete "+mc.Name) {
		t.Errorf("the stopped VM was not deleted\ncalls:\n%s", calls)
	}
	if man := readManifest(t, mc.Dir); man.DestroyedAt == nil {
		t.Error("the run has no end")
	}
}

func TestEnsureSSHKeyRestoresAMissingPublicKey(t *testing.T) {
	root := t.TempDir()
	key, pub, err := EnsureSSHKey(root)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := os.ReadFile(key)
	if err := os.Remove(key + ".pub"); err != nil {
		t.Fatal(err)
	}

	_, again, err := EnsureSSHKey(root)
	if err != nil {
		t.Fatalf("EnsureSSHKey without the .pub: %v", err)
	}
	if f, g := strings.Fields(pub), strings.Fields(again); len(g) < 2 || f[0] != g[0] || f[1] != g[1] {
		t.Errorf("public key is %q, want the one derived from the existing key %q", again, pub)
	}
	if after, _ := os.ReadFile(key); !bytes.Equal(priv, after) {
		t.Error("the private key was replaced")
	}
	if _, err := os.Stat(key + ".pub"); err != nil {
		t.Errorf("the .pub was not written back: %v", err)
	}
}
