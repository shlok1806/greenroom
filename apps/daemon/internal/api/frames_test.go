package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// waitForFrame polls the run's frame list until it has an entry.
func waitForFrame(t *testing.T, h *harness, runID string) machine.Frame {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var frames []machine.Frame
		h.get("/api/runs/"+runID+"/frames", &frames)
		if len(frames) > 0 {
			return frames[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("no frame appeared within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- frames and the recording (ADR 0008) ---

func TestRunFramesListsAndServesAFrame(t *testing.T) {
	h := newHarness(t, machine.WithFrameInterval(30*time.Millisecond))
	runID := h.ready()
	h.putShot()

	fr := waitForFrame(t, h, runID)
	if fr.File == "" || fr.Bytes <= 0 {
		t.Fatalf("frame = %+v, want a file name and some bytes", fr)
	}
	if fr.Step < 2 {
		t.Errorf("frame cites step %d, want at least 2 (boot itself is steps 1 and 2)", fr.Step)
	}

	res, body := h.do(http.MethodGet, "/api/runs/"+runID+"/frames/"+fr.File, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET the frame: status %d: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg", ct)
	}
	if len(body) != fr.Bytes {
		t.Errorf("served %d bytes, frames.jsonl says %d", len(body), fr.Bytes)
	}

	var runs []RunSummary
	h.get("/api/runs", &runs)
	if len(runs) != 1 || runs[0].Frames == 0 {
		t.Fatalf("runs = %+v, want the run summary to carry a nonzero frame count", runs)
	}
}

func TestFrameFileRefusesPathTraversal(t *testing.T) {
	h := newHarness(t, machine.WithFrameInterval(30*time.Millisecond))
	runID := h.ready()
	h.putShot()
	waitForFrame(t, h, runID)

	for _, name := range []string{"..%2Fx", "a/b", "..%2f..%2fstate.json", "a%5Cb"} {
		code, body := h.status(http.MethodGet, "/api/runs/"+runID+"/frames/"+name, nil)
		if code != http.StatusBadRequest {
			t.Errorf("frame %q: status %d: %s, want 400", name, code, body)
		}
	}
	if code, _ := h.status(http.MethodGet, "/api/runs/"+runID+"/frames/nope.jpg", nil); code != http.StatusNotFound {
		t.Errorf("a missing frame gave %d, want 404", code)
	}
}

// Without ffmpeg the recording is a 404 pointing at /frames (ADR 0008). The ffmpeg path itself is untested.
func TestRecordingAnswers404WithTheFfmpegMessageWhenAbsent(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg is installed on this host; the 404-without-ffmpeg path cannot be exercised here")
	}

	h := newHarness(t, machine.WithFrameInterval(30*time.Millisecond))
	runID := h.ready()
	h.putShot()
	waitForFrame(t, h, runID)

	code, body := h.status(http.MethodGet, "/api/runs/"+runID+"/recording.mp4", nil)
	if code != http.StatusNotFound {
		t.Fatalf("status %d: %s, want 404", code, body)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("body = %s, want a JSON error object: %v", body, err)
	}
	want := "ffmpeg is not installed on this host; frames are available at /api/runs/" + runID + "/frames"
	if e.Error != want {
		t.Errorf("error = %q, want %q", e.Error, want)
	}
}

func TestEventStreamDeliversFrameEvents(t *testing.T) {
	h := newHarness(t, machine.WithFrameInterval(30*time.Millisecond))
	runID := h.ready()
	h.putShot()
	_ = h.store(runID) // subscribe the registry to this run before listening

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/api/events?runId="+runID, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	type frameData struct {
		RunID string `json:"runId"`
		At    string `json:"at"`
		File  string `json:"file"`
		Step  int    `json:"step"`
	}
	names := make(chan string, 16)
	datas := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		var lastName string
		for sc.Scan() {
			line := sc.Text()
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				lastName = name
				names <- name
				continue
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && lastName == "frame" {
				datas <- data
			}
		}
	}()

	deadline := time.After(5 * time.Second)
	sawFrame := false
	var data string
	for !sawFrame {
		select {
		case name := <-names:
			if name == "frame" {
				sawFrame = true
				select {
				case data = <-datas:
				case <-time.After(time.Second):
					t.Fatal("a frame event arrived with no data line")
				}
			}
		case <-deadline:
			t.Fatal("the stream never delivered a frame event")
		}
	}

	var fd frameData
	if err := json.Unmarshal([]byte(data), &fd); err != nil {
		t.Fatalf("frame event data = %s, not valid JSON: %v", data, err)
	}
	if fd.RunID != runID || fd.File == "" {
		t.Errorf("frame event = %+v, want runId %q and a file name", fd, runID)
	}
}

// --- the run list's last frame (the companion's thumbnail, companion ADR 0006) ---

// listed returns the run list's entry for runID, and the raw JSON object it came from.
func listed(t *testing.T, h *harness, runID string) (RunSummary, map[string]json.RawMessage) {
	t.Helper()
	res, body := h.do(http.MethodGet, "/api/runs", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/runs: status %d: %s", res.StatusCode, body)
	}
	var runs []RunSummary
	var raws []map[string]json.RawMessage
	if err := json.Unmarshal(body, &runs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &raws); err != nil {
		t.Fatal(err)
	}
	for i, r := range runs {
		if r.RunID == runID {
			return r, raws[i]
		}
	}
	t.Fatalf("run %s is not listed: %s", runID, body)
	return RunSummary{}, nil
}

func TestRunListCarriesTheNewestFrameAndItServes(t *testing.T) {
	h := newHarness(t, machine.WithFrameInterval(30*time.Millisecond))
	runID := h.ready()
	h.putShot()
	waitForFrame(t, h, runID)
	// Destroyed, the run is finished and its frame log stops growing, so the list and
	// /frames can be compared.
	if err := h.mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	var frames []machine.Frame
	h.get("/api/runs/"+runID+"/frames", &frames)
	s, _ := listed(t, h, runID)
	if s.DestroyedAt == nil {
		t.Fatalf("summary = %+v, want a finished run", s)
	}
	if s.LastFrame == nil {
		t.Fatalf("a finished run with %d frames lists no lastFrame", len(frames))
	}
	if newest := frames[len(frames)-1]; *s.LastFrame != newest {
		t.Errorf("lastFrame = %+v, want the newest frame %+v", *s.LastFrame, newest)
	}
	res, body := h.do(http.MethodGet, "/api/runs/"+runID+"/frames/"+s.LastFrame.File, nil)
	if res.StatusCode != http.StatusOK || len(body) != s.LastFrame.Bytes {
		t.Errorf("GET lastFrame: status %d, %d bytes, want 200 and %d bytes", res.StatusCode, len(body), s.LastFrame.Bytes)
	}
}

func TestRunListSaysNullForARunWithNoFrames(t *testing.T) {
	h := newHarness(t) // frames off
	runID := h.ready()

	s, raw := listed(t, h, runID)
	if s.LastFrame != nil {
		t.Errorf("lastFrame = %+v, want none", *s.LastFrame)
	}
	if got, ok := raw["lastFrame"]; !ok || string(got) != "null" {
		t.Errorf("lastFrame is %q (present %v), want an explicit null like verdict", got, ok)
	}
}

func TestRunListSkipsATornLastFrameLine(t *testing.T) {
	h := newHarness(t, machine.WithFrameInterval(30*time.Millisecond))
	runID := h.ready()
	h.putShot()
	waitForFrame(t, h, runID)
	if err := h.mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	before, _ := listed(t, h, runID)

	// A crash mid-append leaves a line with no newline; it is not a frame yet.
	f, err := os.OpenFile(filepath.Join(h.mgr.RunDir(runID), "frames.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"at":"2030-01-01T00:00:00Z","file":"999`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	after, _ := listed(t, h, runID)
	if after.LastFrame == nil || before.LastFrame == nil || *after.LastFrame != *before.LastFrame {
		t.Errorf("lastFrame after a torn line = %+v, want it unchanged at %+v", after.LastFrame, before.LastFrame)
	}
	if after.Frames != before.Frames {
		t.Errorf("frames = %d, want %d", after.Frames, before.Frames)
	}
}
