package machine

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// fileLog is a log buffer safe to read while the manager writes to it.
type fileLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *fileLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *fileLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// fakeCount answers every count with open and records the pids it was asked about.
type fakeCount struct {
	open atomic.Int64
	mu   sync.Mutex
	pids []int
}

func (f *fakeCount) count(_ context.Context, pid int) (int, error) {
	f.mu.Lock()
	f.pids = append(f.pids, pid)
	f.mu.Unlock()
	return int(f.open.Load()), nil
}

func (f *fakeCount) calls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.pids...)
}

// filesOf waits until List shows a count for runID that satisfies ok.
func filesOf(t *testing.T, mgr *Manager, runID string, ok func(*FileUse) bool) *FileUse {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, mc := range mgr.List() {
			if mc.RunID == runID && mc.Files != nil && ok(mc.Files) {
				return mc.Files
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("machine_list never showed the expected file count for %s", runID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAMachineShowsItsTartRunFilesAndWarnsOnceNearTheLimit(t *testing.T) {
	counter := &fakeCount{}
	counter.open.Store(40)
	mgr, root, _ := newTestManager(t, WithFileCheck(FileCheck{
		Interval: 10 * time.Millisecond,
		Count:    counter.count,
		Limit:    func() (uint64, bool) { return 256, true },
	}))
	log := &fileLog{}
	mgr.Log = slog.New(slog.NewTextHandler(log, nil))
	mc := readyMachine(t, mgr)

	use := filesOf(t, mgr, mc.RunID, func(u *FileUse) bool { return u.Open == 40 })
	if use.Limit != 256 || use.Warning != "" || use.PID <= 0 || use.CheckedAt.IsZero() {
		t.Fatalf("a machine far from its limit shows %+v", use)
	}
	for _, pid := range counter.calls() {
		if pid != use.PID {
			t.Fatalf("counted pid %d, but the machine's tart run is %d", pid, use.PID)
		}
	}

	counter.open.Store(210)
	use = filesOf(t, mgr, mc.RunID, func(u *FileUse) bool { return u.Open == 210 })
	if want := "tart run has 210 of 256 files open; pull what you need, the machine may die (#186)"; use.Warning != want {
		t.Errorf("warning = %q, want %q", use.Warning, want)
	}
	// More counts over the line log nothing more.
	n := len(counter.calls())
	for len(counter.calls()) < n+5 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := strings.Count(log.String(), "near its open file limit"); got != 1 {
		t.Errorf("logged the near-limit warning %d times, want once:\n%s", got, log.String())
	}

	// A count is never persisted: a restarted daemon must not show an old one.
	data, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"files"`) {
		t.Errorf("state.json holds a file count:\n%s", data)
	}
}

// A machine reattached from an earlier daemon has no process of ours: its `tart run` is found
// by VM name, and the limit it was given is unknown, so the warning assumes 256.
func TestAReattachedMachineIsCountedThroughItsVMLock(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := []Option{WithTartBin(bin), WithReadyTimeout(10 * time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0)}
	first, err := NewManager(root, quiet, append(opts, WithFileCheck(FileCheck{Interval: 0}))...)
	if err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, first)
	if err := os.WriteFile(filepath.Join(control, "vmname"), []byte(mc.Name), 0o644); err != nil {
		t.Fatal(err)
	}

	counter := &fakeCount{}
	counter.open.Store(220)
	var asked atomic.Value
	second, err := NewManager(root, quiet, append(opts, WithFileCheck(FileCheck{
		Interval: 10 * time.Millisecond,
		Count:    counter.count,
		Limit:    func() (uint64, bool) { t.Error("a reattached machine used this daemon's limit"); return 0, false },
		RunPID:   func(name string) (int, error) { asked.Store(name); return 4242, nil },
	}))...)
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, second)
	use := filesOf(t, second, mc.RunID, func(u *FileUse) bool { return u.Open == 220 })
	if use.PID != 4242 || use.Limit != 0 {
		t.Errorf("reattached count = %+v, want pid 4242 and an unknown limit", use)
	}
	if name, _ := asked.Load().(string); name != mc.Name {
		t.Errorf("looked up tart run for %q, want %q", name, mc.Name)
	}
	if !strings.Contains(use.Warning, "220 files open and its limit is unknown") {
		t.Errorf("warning = %q", use.Warning)
	}
}

func TestNewFileUseWarnsFromEightyPercent(t *testing.T) {
	at := time.Now()
	for _, c := range []struct {
		open  int
		limit uint64
		warn  bool
	}{
		{204, 256, false},
		{205, 256, true},
		{52428, 65536, false},
		{52429, 65536, true},
		{204, 0, false}, // unknown: against 256
		{205, 0, true},
	} {
		if got := newFileUse(1, c.open, c.limit, at).Warning != ""; got != c.warn {
			t.Errorf("%d of %d: warns %v, want %v", c.open, c.limit, got, c.warn)
		}
	}
}
