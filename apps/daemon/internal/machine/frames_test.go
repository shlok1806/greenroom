package machine

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// syncBuffer lets a test read a *slog.Logger's output while the frame
// recorder and the boot goroutine may both be writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// countFrameEvents subscribes to a manager and reports how many "frame"
// lifecycle events it has seen so far, safe for a test goroutine to poll
// while capture keeps running concurrently.
func countFrameEvents(mgr *Manager) func() int {
	var mu sync.Mutex
	n := 0
	mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind != "frame" {
			return
		}
		mu.Lock()
		n++
		mu.Unlock()
	})
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// waitFor polls fn until it returns true or the deadline passes, the way
// readyMachine's own tests wait on the fake tart's fast but async boot.
func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition was not met within %s", timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestFrameRecorderCapturesFrames is ADR 0008's core promise: a ready machine
// is recorded, every frame.jsonl line cites the step current at that moment,
// and a "frame" lifecycle event fires for each one.
func TestFrameRecorderCapturesFrames(t *testing.T) {
	mgr, _, control := newTestManager(t, WithFrameInterval(50*time.Millisecond))
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	frameEvents := countFrameEvents(mgr)

	mc := readyMachine(t, mgr)
	dir := mgr.RunDir(mc.RunID)

	var frames []Frame
	waitFor(t, time.Second, func() bool {
		var err error
		frames, err = ReadFrames(dir)
		if err != nil {
			t.Fatalf("ReadFrames: %v", err)
		}
		return len(frames) >= 3
	})

	entries, err := os.ReadDir(filepath.Join(dir, "frames"))
	if err != nil {
		t.Fatalf("frames directory: %v", err)
	}
	if len(entries) < len(frames) {
		t.Errorf("frames/ has %d files, frames.jsonl has %d lines", len(entries), len(frames))
	}
	for _, fr := range frames {
		if fr.Step < 2 {
			t.Errorf("frame %+v cites step %d, want at least 2 (boot itself is steps 1 and 2)", fr, fr.Step)
		}
		if fr.Bytes <= 0 {
			t.Errorf("frame %+v has no bytes recorded", fr)
		}
		if _, err := os.Stat(filepath.Join(dir, "frames", fr.File)); err != nil {
			t.Errorf("frame file missing on disk: %v", err)
		}
	}

	if got := frameEvents(); got < 3 {
		t.Errorf("saw %d \"frame\" lifecycle events, want at least 3", got)
	}
}

// TestFrameRecorderStopsAfterDestroy proves Destroy actually cancels the
// recorder rather than letting it keep writing into a run directory whose
// machine is gone.
func TestFrameRecorderStopsAfterDestroy(t *testing.T) {
	mgr, _, control := newTestManager(t, WithFrameInterval(30*time.Millisecond))
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}

	mc := readyMachine(t, mgr)
	dir := mgr.RunDir(mc.RunID)

	waitFor(t, time.Second, func() bool {
		frames, err := ReadFrames(dir)
		if err != nil {
			t.Fatalf("ReadFrames: %v", err)
		}
		return len(frames) >= 1
	})

	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	after, err := ReadFrames(dir)
	if err != nil {
		t.Fatalf("ReadFrames after destroy: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	stillAfter, err := ReadFrames(dir)
	if err != nil {
		t.Fatalf("ReadFrames after the wait: %v", err)
	}
	if len(stillAfter) != len(after) {
		t.Errorf("frame count grew from %d to %d after Destroy; the recorder outlived the machine", len(after), len(stillAfter))
	}
}

// TestFrameRecorderDisabledWhenIntervalIsZero is the "-frame-interval 0"
// escape hatch: no frames, no frames directory, nothing to clean up.
func TestFrameRecorderDisabledWhenIntervalIsZero(t *testing.T) {
	// newTestManager's default is WithFrameInterval(0) precisely so an
	// ordinary readyMachine test is not also a recorder test; this test
	// just makes that default an explicit assertion.
	mgr, _, control := newTestManager(t)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}

	mc := readyMachine(t, mgr)
	time.Sleep(150 * time.Millisecond)

	frames, err := ReadFrames(mgr.RunDir(mc.RunID))
	if err != nil {
		t.Fatalf("ReadFrames: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("interval 0 produced %d frames, want 0", len(frames))
	}
	if _, err := os.Stat(filepath.Join(mgr.RunDir(mc.RunID), "frames")); !os.IsNotExist(err) {
		t.Errorf("interval 0 still created a frames directory (err=%v)", err)
	}
}

// TestFrameCaptureFailureDoesNotFailTheRunAndLogsOnce covers the failure path
// ADR 0008 asks for: with no shot.b64, the guest's base64 output is empty, so
// decoding it as a PNG fails every interval. That must never fail the run,
// and it must be logged once for the run rather than once per interval.
func TestFrameCaptureFailureDoesNotFailTheRunAndLogsOnce(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	var logBuf syncBuffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	mgr, err := NewManager(t.TempDir(), log,
		WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers),
		WithFrameInterval(30*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	mc := readyMachine(t, mgr)
	if mc.Status != Ready {
		t.Fatalf("machine is %s, want ready despite the frame capture failure", mc.Status)
	}

	// Several intervals' worth of failed captures.
	time.Sleep(200 * time.Millisecond)

	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	frames, err := ReadFrames(mgr.RunDir(mc.RunID))
	if err != nil {
		t.Fatalf("ReadFrames: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("a failing capture still recorded %d frames", len(frames))
	}

	got := logBuf.String()
	if n := strings.Count(got, "frame capture failed"); n != 1 {
		t.Errorf("\"frame capture failed\" was logged %d times, want exactly 1\nlog:\n%s", n, got)
	}
}
