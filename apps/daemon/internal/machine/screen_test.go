package machine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

func watchScreen(t *testing.T, mgr *Manager, runID string) *ScreenWatch {
	t.Helper()
	w, err := mgr.WatchScreen(context.Background(), runID)
	if err != nil {
		t.Fatalf("WatchScreen: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

func next(t *testing.T, w *ScreenWatch) ScreenMsg {
	t.Helper()
	select {
	case msg, ok := <-w.C:
		if !ok {
			t.Fatalf("the live screen closed: %v", w.Err())
		}
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("no screen message within 10s")
	}
	return ScreenMsg{}
}

// nextVideo skips anything but VIDEO.
func nextVideo(t *testing.T, w *ScreenWatch) ScreenMsg {
	t.Helper()
	for {
		if msg := next(t, w); msg.Type == ScreenVideo {
			return msg
		}
	}
}

// seqOf reads the fake helper's frame counter.
func seqOf(msg ScreenMsg) uint32 { return binary.BigEndian.Uint32(msg.Payload[9:]) }

// expectOpening checks what every new viewer sees first: HELLO, FORMAT, then a keyframe.
func expectOpening(t *testing.T, w *ScreenWatch) {
	t.Helper()
	hello := next(t, w)
	if hello.Type != ScreenHello {
		t.Fatalf("first message is type 0x%02x, want HELLO", hello.Type)
	}
	var h struct {
		Version string `json:"version"`
		Screen  Screen `json:"screen"`
	}
	if err := json.Unmarshal(hello.Payload, &h); err != nil || h.Screen.Width != 1024 {
		t.Fatalf("HELLO is %s (%v)", hello.Payload, err)
	}
	if f := next(t, w); f.Type != ScreenFormat || !bytes.Equal(f.Payload, testsupport.FakeScreenFormat) {
		t.Fatalf("second message is type 0x%02x %q, want FORMAT", f.Type, f.Payload)
	}
	// The keyframe this viewer asked for may repeat the FORMAT.
	if v := nextVideo(t, w); !v.keyframe() {
		t.Fatalf("the first frame has flags %v, want a keyframe", v.Payload[:1])
	}
}

func streamOf(mgr *Manager, runID string) *screenStream {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.machines[runID].screen
}

func waitEnded(t *testing.T, s *screenStream) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the live screen never ended")
	}
	select {
	case <-s.pipe.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the screen helper outlived its stream")
	}
}

func waitClosed(t *testing.T, w *ScreenWatch) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-w.C:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the viewer was never closed")
		}
	}
}

func TestWatchScreenOpensWithHelloFormatAndAKeyframe(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	w := watchScreen(t, mgr, mc.RunID)
	expectOpening(t, w)

	prev := uint32(0)
	for range 10 {
		v := nextVideo(t, w)
		if s := seqOf(v); s <= prev {
			t.Fatalf("frame %d came after %d", s, prev)
		} else {
			prev = s
		}
	}
	if !strings.Contains(testsupport.Calls(t, control), `pkill -f '[g]reenroom-input-5 --serve'; exec "$HOME/.greenroom/bin/greenroom-input-5" --serve`) {
		t.Errorf("the stream did not run helper version 5 with --serve:\n%s", testsupport.Calls(t, control))
	}
}

// A still screen sends nothing on its own, so a new viewer needs the KEYFRAME it asks for.
func TestANewViewerAsksForAKeyframe(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "serve-still")
	mc := readyMachine(t, mgr)
	expectOpening(t, watchScreen(t, mgr, mc.RunID))
	expectOpening(t, watchScreen(t, mgr, mc.RunID))
	if n := testsupport.ServeStarts(t, control); n != 1 {
		t.Errorf("two viewers started %d helpers, want 1", n)
	}
}

// A viewer that stops reading loses its backlog and resumes at a keyframe;
// the other viewer keeps getting frames the whole time.
func TestASlowViewerSkipsToTheNextKeyframe(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mgr.screenBuffer = 16
	mc := readyMachine(t, mgr)
	slow := watchScreen(t, mgr, mc.RunID)
	fast := watchScreen(t, mgr, mc.RunID)
	expectOpening(t, slow)
	prev := seqOf(nextVideo(t, slow))

	stall := time.After(500 * time.Millisecond)
	got := 0
	for waiting := true; waiting; {
		select {
		case msg := <-fast.C:
			if msg.Type == ScreenVideo {
				got++
			}
		case <-stall:
			waiting = false
		}
	}
	if got < 50 {
		t.Errorf("the fast viewer got %d frames while the other stalled, want at least 50", got)
	}

	gaps := 0
	for range 200 {
		v := nextVideo(t, slow)
		s := seqOf(v)
		if s != prev+1 {
			gaps++
			if !v.keyframe() {
				t.Fatalf("after frame %d the slow viewer got frame %d, which is not a keyframe", prev, s)
			}
		}
		prev = s
	}
	if gaps == 0 {
		t.Error("the slow viewer never dropped anything; the test did not overflow it")
	}
}

func TestTheScreenStopsWhenNobodyWatches(t *testing.T) {
	mgr, _, control := newTestManager(t, WithScreenIdle(200*time.Millisecond))
	mc := readyMachine(t, mgr)

	w := watchScreen(t, mgr, mc.RunID)
	s := streamOf(mgr, mc.RunID)
	w.Close()
	// A viewer back within the idle time keeps the same helper.
	w = watchScreen(t, mgr, mc.RunID)
	time.Sleep(400 * time.Millisecond)
	if !s.running() || streamOf(mgr, mc.RunID) != s {
		t.Fatal("the screen stopped while someone was watching")
	}
	w.Close()
	waitEnded(t, s)

	expectOpening(t, watchScreen(t, mgr, mc.RunID))
	if n := testsupport.ServeStarts(t, control); n != 2 {
		t.Errorf("started the helper %d times, want 2", n)
	}
}

func TestDestroyEndsTheScreen(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	w := watchScreen(t, mgr, mc.RunID)
	s := streamOf(mgr, mc.RunID)
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	waitClosed(t, w)
	if err := w.Err(); err == nil || !strings.Contains(err.Error(), "machine is gone") {
		t.Errorf("the viewer ended with %v", err)
	}
	waitEnded(t, s)
}

func TestAHelperCrashEndsViewersAndTheNextViewerRestartsIt(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	w := watchScreen(t, mgr, mc.RunID)
	expectOpening(t, w)

	testsupport.Flag(t, control, "serve-exit")
	waitClosed(t, w)
	if err := w.Err(); err == nil || !strings.Contains(err.Error(), "fake helper crashed") {
		t.Errorf("the viewer ended with %v, want the helper's stderr", err)
	}
	if err := os.Remove(filepath.Join(control, "serve-exit")); err != nil {
		t.Fatal(err)
	}
	expectOpening(t, watchScreen(t, mgr, mc.RunID))
	if n := testsupport.ServeStarts(t, control); n != 2 {
		t.Errorf("started the helper %d times, want 2", n)
	}
}

func TestWatchScreenRefusesAMachineThatIsNotReady(t *testing.T) {
	mgr, _, control := newTestManager(t, WithReadyTimeout(300*time.Millisecond))
	testsupport.Flag(t, control, "agent-down")
	mc, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := mgr.WatchScreen(context.Background(), mc.RunID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("watching a booting machine got %v, want ErrNotReady", err)
	}
}

func TestWatchScreenReportsAHelperThatCannotStart(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-serve")
	mc := readyMachine(t, mgr)
	_, err := mgr.WatchScreen(context.Background(), mc.RunID)
	if err == nil || !strings.Contains(err.Error(), "VM is not running") {
		t.Fatalf("WatchScreen got %v, want tart's error", err)
	}
}

// servedInputs decodes the INPUT payloads the fake helper received.
func servedInputs(t *testing.T, control string) []struct {
	ID      int64         `json:"id"`
	Actions []InputAction `json:"actions"`
} {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(control, "serve-input"))
	var out []struct {
		ID      int64         `json:"id"`
		Actions []InputAction `json:"actions"`
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var in struct {
			ID      int64         `json:"id"`
			Actions []InputAction `json:"actions"`
		}
		if err := json.Unmarshal([]byte(line), &in); err != nil {
			t.Fatalf("INPUT %q: %v", line, err)
		}
		out = append(out, in)
	}
	return out
}

func TestInputGoesThroughTheRunningScreen(t *testing.T) {
	mgr, root, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	watchScreen(t, mgr, mc.RunID)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	res, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{{Type: "click", X: frac(0.5), Y: frac(0.25)}})
	if err != nil {
		t.Fatalf("Input: %v", err)
	}
	inputs := servedInputs(t, control)
	if len(inputs) != 1 || inputs[0].ID == 0 || len(inputs[0].Actions) != 1 {
		t.Fatalf("the helper got %+v, want one INPUT with an id", inputs)
	}
	if a := inputs[0].Actions[0]; *a.X != 512 || *a.Y != 192 {
		t.Errorf("0.5,0.25 went out as %v,%v, want 512,192", *a.X, *a.Y)
	}
	if posted := postedActions(t, control); posted != nil {
		t.Errorf("input also went through a one-shot exec: %+v", posted)
	}
	steps, err := ReadSteps(filepath.Join(root, "runs", mc.RunID))
	if err != nil {
		t.Fatal(err)
	}
	if last := steps[len(steps)-1]; last.Tool != "machine_input" || last.Seq != res.Step {
		t.Errorf("last step is %+v, want machine_input %d", last, res.Step)
	}

	testsupport.Flag(t, control, "input-down")
	_, err = mgr.Input(context.Background(), mc.RunID, "human", []InputAction{{Type: "key", Key: "a"}})
	if err == nil || !strings.Contains(err.Error(), "refused the event") {
		t.Errorf("a refused INPUT got %v, want the helper's error", err)
	}
}

func TestSlowInputBatchesGetTimeForTheirActions(t *testing.T) {
	// Each batch takes 1 s to post and the slack is 0.7 s: the second batch
	// needs 2 s, which only the backlog covers, and the 0.7 s margin absorbs
	// a loaded -race runner.
	mgr, _, control := newTestManager(t, WithScreenInputSlack(700*time.Millisecond))
	mc := readyMachine(t, mgr)
	watchScreen(t, mgr, mc.RunID)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	batch := []InputAction{{Type: "sleep", MS: 500}, {Type: "sleep", MS: 500}, {Type: "key", Key: "a"}}
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := mgr.Input(context.Background(), mc.RunID, "human", batch)
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("a batch that sleeps longer than the slack, queued behind another, got %v", err)
		}
	}
	if inputs := servedInputs(t, control); len(inputs) != 2 {
		t.Errorf("the helper got %d INPUTs, want 2", len(inputs))
	}
}

func TestInputUsesExecOnceTheScreenStops(t *testing.T) {
	mgr, _, control := newTestManager(t, WithScreenIdle(50*time.Millisecond))
	mc := readyMachine(t, mgr)
	w := watchScreen(t, mgr, mc.RunID)
	s := streamOf(mgr, mc.RunID)
	w.Close()
	waitEnded(t, s)

	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if _, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if posted := postedActions(t, control); len(posted) != 1 {
		t.Errorf("the exec path posted %+v, want the click", posted)
	}
	if inputs := servedInputs(t, control); len(inputs) != 0 {
		t.Errorf("a stopped screen took input: %+v", inputs)
	}
}
