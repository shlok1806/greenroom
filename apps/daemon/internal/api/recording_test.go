package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// fakeFFmpeg puts an ffmpeg first on PATH. Each build writes "build <n>" to its output; files in the
// returned directory switch behaviour: slow (sleeps first), fail (exits 1 after a partial write), hang.
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
C="` + dir + `"
echo build >> "$C/calls"
[ -f "$C/hang" ] && exec sleep 30
[ -f "$C/slow" ] && sleep 0.3
for a; do out="$a"; done
printf partial > "$out"
[ -f "$C/fail" ] && { echo "encoder exploded" >&2; exit 1; }
printf 'build %s' "$(wc -l < "$C/calls" | tr -d ' ')" > "$out"
`
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func ffmpegBuilds(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "calls"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// logFrame appends a frame to the run's frames.jsonl, as the recorder does.
func logFrame(t *testing.T, runDir string) {
	t.Helper()
	at := time.Now().UTC()
	line, _ := json.Marshal(machine.Frame{At: at, File: fmt.Sprintf("%d.jpg", at.UnixMilli()), Step: 1, Bytes: 1})
	f, err := os.OpenFile(filepath.Join(runDir, "frames.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

// leftovers lists the run directory's recording files other than the finished mp4.
func leftovers(t *testing.T, runDir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(runDir, "recording*"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range matches {
		if filepath.Base(m) != "recording.mp4" {
			out = append(out, filepath.Base(m))
		}
	}
	return out
}

func TestTheRecordingIsCachedUntilANewFrameArrives(t *testing.T) {
	ff := fakeFFmpeg(t)
	h := newHarness(t)
	runID := h.ready()
	dir := h.mgr.RunDir(runID)
	logFrame(t, dir)
	path := "/api/runs/" + runID + "/recording.mp4"

	for range 2 {
		code, body := h.status(http.MethodGet, path, nil)
		if code != http.StatusOK || body != "build 1" {
			t.Fatalf("GET recording: %d %q, want 200 and the first build", code, body)
		}
	}
	if n := ffmpegBuilds(t, ff); n != 1 {
		t.Fatalf("ffmpeg ran %d times for an unchanged frame log, want 1", n)
	}

	logFrame(t, dir)
	if code, body := h.status(http.MethodGet, path, nil); code != http.StatusOK || body != "build 2" {
		t.Fatalf("GET after a new frame: %d %q, want a rebuild", code, body)
	}
	if left := leftovers(t, dir); len(left) != 0 {
		t.Errorf("temp files left in the run directory: %v", left)
	}
}

func TestConcurrentRecordingRequestsShareOneBuild(t *testing.T) {
	ff := fakeFFmpeg(t)
	if err := os.WriteFile(filepath.Join(ff, "slow"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	runID := h.ready()
	logFrame(t, h.mgr.RunDir(runID))

	var wg sync.WaitGroup
	bodies := make([]string, 6)
	codes := make([]int, 6)
	for i := range bodies {
		wg.Go(func() { codes[i], bodies[i] = h.status(http.MethodGet, "/api/runs/"+runID+"/recording.mp4", nil) })
	}
	wg.Wait()
	for i := range bodies {
		if codes[i] != http.StatusOK || bodies[i] != "build 1" {
			t.Errorf("request %d: %d %q, want 200 and the one build", i, codes[i], bodies[i])
		}
	}
	if n := ffmpegBuilds(t, ff); n != 1 {
		t.Errorf("ffmpeg ran %d times for concurrent requests, want 1", n)
	}
}

func TestAFailedBuildLeavesNoRecording(t *testing.T) {
	ff := fakeFFmpeg(t)
	if err := os.WriteFile(filepath.Join(ff, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	runID := h.ready()
	dir := h.mgr.RunDir(runID)
	logFrame(t, dir)

	code, body := h.status(http.MethodGet, "/api/runs/"+runID+"/recording.mp4", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "encoder exploded") {
		t.Fatalf("GET recording: %d %s, want 500 with ffmpeg's words", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "recording.mp4")); !os.IsNotExist(err) {
		t.Errorf("a partial recording.mp4 was cached: %v", err)
	}
	if left := leftovers(t, dir); len(left) != 0 {
		t.Errorf("temp files left in the run directory: %v", left)
	}

	// The next request builds again rather than serving the failure.
	if err := os.Remove(filepath.Join(ff, "fail")); err != nil {
		t.Fatal(err)
	}
	if code, body := h.status(http.MethodGet, "/api/runs/"+runID+"/recording.mp4", nil); code != http.StatusOK || body != "build 2" {
		t.Errorf("GET after the failure: %d %q, want a fresh build", code, body)
	}
}

func TestAHungBuildTimesOut(t *testing.T) {
	ff := fakeFFmpeg(t)
	if err := os.WriteFile(filepath.Join(ff, "hang"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := recordingTimeout
	recordingTimeout = 200 * time.Millisecond
	t.Cleanup(func() { recordingTimeout = old })
	h := newHarness(t)
	runID := h.ready()
	dir := h.mgr.RunDir(runID)
	logFrame(t, dir)

	started := time.Now()
	code, body := h.status(http.MethodGet, "/api/runs/"+runID+"/recording.mp4", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "deadline exceeded") {
		t.Fatalf("GET recording: %d %s, want 500 for the timeout", code, body)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("the hung build held the request for %v", elapsed)
	}
	if left := leftovers(t, dir); len(left) != 0 {
		t.Errorf("temp files left in the run directory: %v", left)
	}
}
