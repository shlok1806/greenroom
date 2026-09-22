package api

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// recording serves the run's timelapse mp4: cached, freshly built with ffmpeg, or a 404 saying why not (ADR 0008).
func (a *api) recording(w http.ResponseWriter, r *http.Request, id string) {
	dir := a.mgr.RunDir(id)
	path := filepath.Join(dir, "recording.mp4")
	notFound := fmt.Errorf("no recording in run %q", id)
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		a.serveFile(w, r, path, "video/mp4", notFound)
		return
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		a.fail(w, http.StatusNotFound, fmt.Errorf("ffmpeg is not installed on this host; frames are available at /api/runs/%s/frames", id))
		return
	}
	frames, err := machine.ReadFrames(dir)
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	if len(frames) == 0 {
		a.fail(w, http.StatusNotFound, fmt.Errorf("run %q has no frames to build a recording from", id))
		return
	}
	if err := buildRecording(ffmpeg, dir, frames, path); err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	a.serveFile(w, r, path, "video/mp4", notFound)
}

// buildRecording renders frames to H.264 with the concat demuxer, showing each frame for as long as it was on screen.
func buildRecording(ffmpeg, dir string, frames []machine.Frame, out string) error {
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
	list := filepath.Join(dir, "recording-concat.txt")
	if err := os.WriteFile(list, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write concat list: %w", err)
	}
	defer func() { _ = os.Remove(list) }()

	cmd := exec.Command(ffmpeg, "-y", "-f", "concat", "-safe", "0", "-i", list,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return nil
}
