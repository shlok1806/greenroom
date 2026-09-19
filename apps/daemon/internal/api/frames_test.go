package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// waitForFrame polls the run's frame list until it has at least one entry,
// the way the fake tart's fast boot lets other tests poll for readiness.
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

// TestRecordingAnswers404WithTheFfmpegMessageWhenAbsent covers the negative
// path from ADR 0008: a host with no ffmpeg on PATH gets a clear error
// pointing at /frames instead of a generic failure. The positive path (an
// actual mp4 built by ffmpeg) is not exercised here because this suite's
// host has no ffmpeg installed; if one is ever added to the CI image this
// test will skip itself rather than assert a codepath it can no longer see.
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
