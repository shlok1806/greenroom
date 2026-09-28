package machine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

func TestDescribeIdle(t *testing.T) {
	for idle, want := range map[time.Duration]string{
		0:                               "active 0s ago",
		42 * time.Second:                "active 42s ago",
		3*time.Minute + 59*time.Second:  "idle 3m",
		4*time.Hour + 7*time.Minute + 5: "idle 4h07m",
		26*time.Hour + 30*time.Minute:   "idle 26h30m",
	} {
		if got := describeIdle(idle); got != want {
			t.Errorf("describeIdle(%v) = %q, want %q", idle, got, want)
		}
	}
}

// ageRun moves everything a run has done back by d, as if it had sat idle that long.
func ageRun(t *testing.T, mgr *Manager, runID string, d time.Duration) {
	t.Helper()
	dir := mgr.RunDir(runID)
	var man map[string]any
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &man); err != nil {
		t.Fatal(err)
	}
	created, _ := time.Parse(time.RFC3339Nano, man["createdAt"].(string))
	man["createdAt"] = created.Add(-d)
	if data, err = json.Marshal(man); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	steps, err := os.ReadFile(filepath.Join(dir, "steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(steps))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var s map[string]any
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		at, _ := time.Parse(time.RFC3339Nano, s["at"].(string))
		s["at"] = at.Add(-d)
		line, _ := json.Marshal(s)
		out.Write(append(line, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, "steps.jsonl"), out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr.mu.Lock()
	mgr.machines[runID].CreatedAt = mgr.machines[runID].CreatedAt.Add(-d)
	mgr.mu.Unlock()
}

// The frame recorder keeps capturing an idle machine; only steps and messages count.
func TestLastActivityIsStepsAndMessagesNotFrames(t *testing.T) {
	mgr, _, control := newTestManager(t, WithFrameInterval(20*time.Millisecond))
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, mgr)
	ageRun(t, mgr, mc.RunID, 4*time.Hour)

	waitFor(t, 2*time.Second, func() bool { frames, _ := ReadFrames(mc.Dir); return len(frames) >= 2 })
	if idle := mgr.IdleFor(mc.RunID); idle < 4*time.Hour-time.Minute {
		t.Errorf("IdleFor = %v after four idle hours with frames still landing, want about 4h", idle)
	}

	said := time.Now()
	mgr.SetMessageActivity(func(runID string) time.Time {
		if runID == mc.RunID {
			return said
		}
		return time.Time{}
	})
	if got := mgr.LastActivity(mc.RunID); !got.Equal(said) {
		t.Errorf("LastActivity = %v, want the last message at %v", got, said)
	}
}

// When the host is full the error describes each of our machines with how long it has
// been idle, by a short runId, and calls none of them the caller's (daemon ADR 0008). Nothing
// is destroyed.
func TestCapacityErrorSaysHowLongEachMachineHasBeenIdle(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	mgr, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithTartBin(bin), WithMaxMachines(2), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)
	mc := readyMachine(t, mgr)
	ageRun(t, mgr, mc.RunID, 3*time.Hour+12*time.Minute)

	_, err = mgr.Create(context.Background(), testImage)
	if err == nil {
		t.Fatal("a create past the host limit succeeded")
	}
	want := "run ..." + mc.RunID[len(mc.RunID)-8:] + " (started 3h12m ago, idle 3h12m)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	if strings.Contains(err.Error(), mc.RunID) || strings.Contains(err.Error(), "yours are") {
		t.Errorf("error = %q, which hands out a full runId or calls a run the caller's", err)
	}
	if len(mgr.List()) != 1 {
		t.Errorf("the manager holds %d machines after a refused create, want the idle one untouched", len(mgr.List()))
	}
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
}
