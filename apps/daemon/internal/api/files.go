package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// contentTypes maps a run artifact's extension to its type; anything else is served as a download.
var contentTypes = map[string]string{
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".json":  "application/json",
	".jsonl": "text/plain; charset=utf-8",
	".log":   "text/plain; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
}

func (a *api) artifact(w http.ResponseWriter, r *http.Request, id string) {
	name := r.PathValue("name")
	if !bareName(name) {
		a.fail(w, http.StatusBadRequest, fmt.Errorf("%q is not a file in the run directory", name))
		return
	}
	ct := contentTypes[strings.ToLower(filepath.Ext(name))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	a.serveFile(w, r, filepath.Join(a.mgr.RunDir(id), name), ct, fmt.Errorf("no artifact %q in run %q", name, id))
}

func (a *api) runFrames(w http.ResponseWriter, _ *http.Request, id string) {
	frames, err := machine.ReadFrames(a.mgr.RunDir(id))
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, frames)
}

func (a *api) frameFile(w http.ResponseWriter, r *http.Request, id string) {
	name := r.PathValue("file")
	if !bareName(name) {
		a.fail(w, http.StatusBadRequest, fmt.Errorf("%q is not a frame in run %q", name, id))
		return
	}
	a.serveFile(w, r, filepath.Join(a.mgr.RunDir(id), "frames", name), "image/jpeg", fmt.Errorf("no frame %q in run %q", name, id))
}

// recordingTimeout bounds one ffmpeg build; a var so tests can shorten it.
var recordingTimeout = 5 * time.Minute

// recording serves the run's timelapse mp4, rebuilt with ffmpeg whenever frames.jsonl is newer, or a 404 saying why not (ADR 0008).
func (a *api) recording(w http.ResponseWriter, r *http.Request, id string) {
	dir := a.mgr.RunDir(id)
	path := filepath.Join(dir, "recording.mp4")
	unlock := a.lockRun(id)
	code, err := freshRecording(r.Context(), dir, path, id)
	unlock()
	if err != nil {
		a.fail(w, code, err)
		return
	}
	a.serveFile(w, r, path, "video/mp4", fmt.Errorf("no recording in run %q", id))
}

// lockRun serialises recording builds per run, so concurrent requests wait for one ffmpeg instead of racing it.
func (a *api) lockRun(id string) func() {
	a.recMu.Lock()
	mu, ok := a.recLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		a.recLocks[id] = mu
	}
	a.recMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// freshRecording makes path current with the frame log, or says why it cannot, with the status to answer.
func freshRecording(ctx context.Context, dir, path, id string) (int, error) {
	frameLog, logErr := os.Stat(filepath.Join(dir, "frames.jsonl"))
	if mp4, err := os.Stat(path); err == nil && !mp4.IsDir() && (logErr != nil || !frameLog.ModTime().After(mp4.ModTime())) {
		return 0, nil
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return http.StatusNotFound, fmt.Errorf("ffmpeg is not installed on this host; frames are available at /api/runs/%s/frames", id)
	}
	frames, err := machine.ReadFrames(dir)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if len(frames) == 0 {
		return http.StatusNotFound, fmt.Errorf("run %q has no frames to build a recording from", id)
	}
	ctx, cancel := context.WithTimeout(ctx, recordingTimeout)
	defer cancel()
	if err := buildRecording(ctx, ffmpeg, dir, frames, path); err != nil {
		return http.StatusInternalServerError, err
	}
	// Date the mp4 by the frame log it was built from, so a frame logged during the build still makes it stale.
	if logErr == nil {
		_ = os.Chtimes(path, frameLog.ModTime(), frameLog.ModTime())
	}
	return 0, nil
}

// buildRecording renders frames to H.264 with the concat demuxer, showing each frame for as long as it was on screen.
// ffmpeg writes a temp file renamed over out, so a failed or cancelled build never leaves a partial mp4.
func buildRecording(ctx context.Context, ffmpeg, dir string, frames []machine.Frame, out string) error {
	var b strings.Builder
	for i, fr := range frames {
		dur := 2.0
		if i < len(frames)-1 {
			if d := frames[i+1].At.Sub(fr.At).Seconds(); d > 0 {
				dur = d
			}
		}
		fmt.Fprintf(&b, "file '%s'\nduration %.3f\n", filepath.Join(dir, "frames", fr.File), dur)
	}
	// The concat demuxer ignores the last entry's duration unless that file is listed once more.
	fmt.Fprintf(&b, "file '%s'\n", filepath.Join(dir, "frames", frames[len(frames)-1].File))
	list, err := writeTemp(dir, "recording-*.txt", b.String())
	if err != nil {
		return fmt.Errorf("write concat list: %w", err)
	}
	defer func() { _ = os.Remove(list) }()
	tmp, err := writeTemp(dir, "recording-*.mp4", "")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }() // a no-op after the rename

	cmd := exec.CommandContext(ctx, ffmpeg, "-y", "-f", "concat", "-safe", "0", "-i", list,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", tmp)
	cmd.WaitDelay = 5 * time.Second // a killed ffmpeg's children may still hold the output pipe
	if msg, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return os.Rename(tmp, out)
}

func writeTemp(dir, pattern, content string) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
