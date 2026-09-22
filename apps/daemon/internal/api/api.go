// Package api is the companion app's read and control surface (ADR 0007).
// It is a sibling of internal/mcpserver over the same manager and the same
// conversation store: everything it shows, the agent's tools can see, and
// every control it offers lands in the conversation. Nothing stateful lives
// here.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Heartbeat is how often an idle SSE stream writes a comment line, so that
// a proxy or URLSession does not decide the connection is dead. It is a
// variable so a test can shorten it.
var Heartbeat = 15 * time.Second

// clientBuffer is how many events one SSE client may fall behind before the
// daemon gives up on it. The files on disk are the truth and the stream is a
// hint (ADR 0007), so dropping a slow reader costs it a reconnect.
const clientBuffer = 256

// RunSummary is one row of the companion's run list: enough to show a run
// without opening it, whether or not its machine is still alive.
type RunSummary struct {
	RunID        string                `json:"runId"`
	CreatedAt    time.Time             `json:"createdAt"`
	DestroyedAt  *time.Time            `json:"destroyedAt"`
	Image        string                `json:"image"`
	Status       string                `json:"status"`
	IP           string                `json:"ip,omitempty"`
	VNCURL       string                `json:"vncUrl,omitempty"`
	Steps        int                   `json:"steps"`
	Frames       int                   `json:"frames"`
	Verdict      *session.VerdictState `json:"verdict"`
	LastActivity time.Time             `json:"lastActivity"`
	Messages     int                   `json:"messages"`
}

// Status values a run that has no live machine can have. A live machine
// reports its own machine.Status instead.
const (
	statusFinished = "finished" // the run was recorded and its machine is gone
	statusFailed   = "failed"   // the run directory has no readable manifest
)

// RunDetail is one run in full. The manifest is embedded, so the shape the
// app reads is the shape on disk, with the live machine and the verdict read
// from the conversation rather than from the manifest, which lags it.
type RunDetail struct {
	machine.Manifest
	Machine *machine.Machine      `json:"machine"`
	Verdict *session.VerdictState `json:"verdict"`
}

type api struct {
	mgr *machine.Manager
	reg *session.Registry
	log *slog.Logger
}

// New returns the handler for /api/. The patterns carry the /api prefix
// because the daemon mounts this under mux.Handle("/api/", ...), which does
// not strip it.
func New(mgr *machine.Manager, reg *session.Registry, log *slog.Logger) http.Handler {
	a := &api{mgr: mgr, reg: reg, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runs", a.listRuns)
	mux.HandleFunc("GET /api/runs/{id}", a.runDetail)
	mux.HandleFunc("GET /api/runs/{id}/steps", a.runSteps)
	mux.HandleFunc("GET /api/runs/{id}/frames", a.runFrames)
	mux.HandleFunc("GET /api/runs/{id}/frames/{file...}", a.frameFile)
	mux.HandleFunc("GET /api/runs/{id}/recording.mp4", a.recording)
	mux.HandleFunc("GET /api/runs/{id}/messages", a.readMessages)
	mux.HandleFunc("GET /api/runs/{id}/artifacts/{name...}", a.artifact)
	mux.HandleFunc("POST /api/runs/{id}/messages", a.postMessage)
	mux.HandleFunc("POST /api/runs/{id}/screenshot", a.screenshot)
	mux.HandleFunc("POST /api/runs/{id}/control", a.takeControl)
	mux.HandleFunc("DELETE /api/runs/{id}/control", a.releaseControl)
	mux.HandleFunc("POST /api/runs/{id}/input", a.input)
	mux.HandleFunc("POST /api/runs/{id}/destroy", a.destroy)
	mux.HandleFunc("GET /api/events", a.events)
	return mux
}

// --- runs ---

func (a *api) listRuns(w http.ResponseWriter, _ *http.Request) {
	ids, err := a.reg.RunIDs()
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	live := map[string]*machine.Machine{}
	for _, mc := range a.mgr.List() {
		live[mc.RunID] = mc
	}
	out := make([]RunSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, a.summary(id, live[id]))
	}
	// RunIDs sorts by name, which starts with the creation timestamp, so
	// reversing gives newest first.
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, out)
}

func (a *api) summary(runID string, mc *machine.Machine) RunSummary {
	s := RunSummary{RunID: runID, Status: statusFailed}
	man, err := machine.ReadManifest(a.mgr.RunDir(runID))
	if err == nil {
		s.CreatedAt, s.DestroyedAt, s.Image = man.CreatedAt, man.DestroyedAt, man.Image
		s.IP, s.Verdict, s.Status = man.IP, man.Verdict, statusFinished
	}
	// Counted from steps.jsonl, never taken from the manifest, whose Steps is
	// the highest number handed out rather than a count of what was recorded
	// (see machine.Manifest). The list has to agree with
	// /api/runs/{id}/steps, or the app cannot trust either one.
	steps := a.stepLog(runID)
	s.Steps = steps.Count
	if mc != nil {
		s.Status, s.Image, s.IP, s.VNCURL = string(mc.Status), mc.Image, mc.IP, mc.VNCURL
		s.CreatedAt = mc.CreatedAt
	}
	// LastActivity is the latest of everything the run recorded, not the
	// conversation alone: a run driven entirely by an agent says nothing in
	// the transcript for hours while its steps and frames pile up, and a
	// list that dates it from its creation sorts and reads as abandoned.
	s.LastActivity = s.CreatedAt
	later := func(t time.Time) {
		if t.After(s.LastActivity) {
			s.LastActivity = t
		}
	}
	later(steps.Last)
	if frames, err := machine.ReadFrames(a.mgr.RunDir(runID)); err == nil {
		s.Frames = len(frames)
		if s.Frames > 0 {
			later(frames[s.Frames-1].At)
		}
	}
	if store, err := a.reg.Get(runID); err == nil {
		msgs := store.After(0)
		s.Messages = len(msgs)
		if len(msgs) > 0 {
			later(msgs[len(msgs)-1].At)
		}
		v := store.Verdict()
		if v.Status != session.None {
			s.Verdict = &v
		}
	}
	return s
}

func (a *api) runDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	man, err := machine.ReadManifest(a.mgr.RunDir(id))
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	// Same count the list and /steps report: the manifest's own number is a
	// high-water mark, not a total (see machine.Manifest).
	man.Steps = a.stepLog(id).Count
	d := RunDetail{Manifest: man}
	for _, mc := range a.mgr.List() {
		if mc.RunID == id {
			d.Machine = mc
		}
	}
	// RunDetail.Verdict shadows the manifest's field of the same name, so it
	// has to carry the manifest's verdict when the conversation has none to
	// give: otherwise a run whose store will not open answers with no verdict
	// at all, and a run that never had one answers with an empty verdict
	// object where the list says null.
	d.Verdict = man.Verdict
	if store, err := a.reg.Get(id); err == nil {
		if v := store.Verdict(); v.Status != session.None {
			d.Verdict = &v
		}
	}
	writeJSON(w, http.StatusOK, d)
}

// stepLog is what a run's steps.jsonl says about itself. A log that cannot
// be read reads as empty and says so in the daemon log: a summary is worth
// answering without it, and the caller can still open /steps for the error.
func (a *api) stepLog(runID string) machine.StepLog {
	log, err := machine.ReadStepLog(a.mgr.RunDir(runID))
	if err != nil {
		a.log.Warn("cannot read a run's steps", "runId", runID, "err", err)
		return machine.StepLog{}
	}
	return log
}

func (a *api) runSteps(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	steps, err := machine.ReadSteps(a.mgr.RunDir(id))
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

// --- frames and the recording (ADR 0008) ---

func (a *api) runFrames(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	frames, err := machine.ReadFrames(a.mgr.RunDir(id))
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, frames)
}

func (a *api) frameFile(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	name := r.PathValue("file")
	// Only a bare file name in the run's frames directory. A separator or a
	// dot-dot is the caller asking for someone else's file.
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		a.fail(w, http.StatusBadRequest, fmt.Errorf("%q is not a frame in run %q", name, id))
		return
	}
	path := filepath.Join(a.mgr.RunDir(id), "frames", name)
	f, err := os.Open(path)
	if err != nil {
		a.fail(w, http.StatusNotFound, fmt.Errorf("no frame %q in run %q", name, id))
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		a.fail(w, http.StatusNotFound, fmt.Errorf("no frame %q in run %q", name, id))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// recording answers the run's timelapse as an mp4: the cached file if one
// was already built, a freshly built one when ffmpeg is on the host's PATH,
// or a 404 that says why not (ADR 0008: "a video is an export, not the
// record").
func (a *api) recording(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	dir := a.mgr.RunDir(id)
	path := filepath.Join(dir, "recording.mp4")
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		a.serveFile(w, r, path, "video/mp4")
		return
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		writeJSON(w, http.StatusNotFound, struct {
			Error string `json:"error"`
		}{fmt.Sprintf("ffmpeg is not installed on this host; frames are available at /api/runs/%s/frames", id)})
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
	a.serveFile(w, r, path, "video/mp4")
}

// buildRecording renders frames into an H.264 file with the concat demuxer,
// giving each frame the duration it was actually shown on screen (computed
// from consecutive frame timestamps) so playback runs in real time rather
// than at a fixed rate.
func buildRecording(ffmpeg, dir string, frames []machine.Frame, out string) error {
	listPath := filepath.Join(dir, "recording-concat.txt")
	var b strings.Builder
	for i, fr := range frames {
		framePath := filepath.Join(dir, "frames", fr.File)
		dur := 2.0
		if i < len(frames)-1 {
			if d := frames[i+1].At.Sub(fr.At).Seconds(); d > 0 {
				dur = d
			}
		}
		fmt.Fprintf(&b, "file '%s'\nduration %.3f\n", framePath, dur)
	}
	// The concat demuxer ignores the duration on the last listed entry
	// unless the same file is repeated once more without one.
	fmt.Fprintf(&b, "file '%s'\n", filepath.Join(dir, "frames", frames[len(frames)-1].File))
	if err := os.WriteFile(listPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write concat list: %w", err)
	}
	defer func() { _ = os.Remove(listPath) }()

	cmd := exec.Command(ffmpeg, "-y", "-f", "concat", "-safe", "0", "-i", listPath,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", out)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, out)
	}
	return nil
}

// serveFile answers a file already known to exist on disk, the way artifact
// and frameFile do, for handlers that name their content type themselves.
func (a *api) serveFile(w http.ResponseWriter, r *http.Request, path, contentType string) {
	f, err := os.Open(path)
	if err != nil {
		a.fail(w, http.StatusNotFound, err)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

// --- the conversation ---

type transcriptOut struct {
	Messages []session.Message    `json:"messages"`
	Last     int                  `json:"last"`
	Verdict  session.VerdictState `json:"verdict"`
}

func (a *api) readMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	after := 0
	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			a.fail(w, http.StatusBadRequest, fmt.Errorf("after must be a number, not %q", s))
			return
		}
		after = n
	}
	store, err := a.reg.Get(id)
	if err != nil {
		a.fail(w, http.StatusNotFound, err)
		return
	}
	msgs := store.After(after)
	last := after
	if n := len(msgs); n > 0 {
		last = msgs[n-1].Seq
	} else if after == 0 {
		last = store.Len()
	}
	writeJSON(w, http.StatusOK, transcriptOut{Messages: msgs, Last: last, Verdict: store.Verdict()})
}

func (a *api) postMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	var in struct {
		Kind    string `json:"kind"`
		Text    string `json:"text"`
		ReplyTo int    `json:"replyTo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		a.fail(w, http.StatusBadRequest, err)
		return
	}
	store, err := a.reg.Get(id)
	if err != nil {
		a.fail(w, http.StatusNotFound, err)
		return
	}
	m, err := store.Append(session.Message{
		From:    session.Human,
		Kind:    session.Kind(in.Kind),
		Text:    in.Text,
		ReplyTo: in.ReplyTo,
	})
	if err != nil {
		// A contested verdict is not the caller's mistake, it is the state of
		// the conversation, so it gets its own status.
		if errors.Is(err, session.ErrContested) {
			a.fail(w, http.StatusConflict, err)
			return
		}
		a.fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Seq int       `json:"seq"`
		At  time.Time `json:"at"`
	}{m.Seq, m.At})
}

// --- controls ---

func (a *api) screenshot(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	_, shot, err := a.mgr.Screenshot(r.Context(), id)
	if err != nil {
		code := http.StatusInternalServerError
		if !a.mgr.Live(id) {
			code = http.StatusConflict
		}
		a.fail(w, code, err)
		return
	}
	// The coder learns what the human did on its next agent_wait (ADR 0006).
	a.event(id, fmt.Sprintf("human took a screenshot (step %d)", shot.Step))
	// The companion draws the frame at whatever size its window is, so it
	// needs the image's own size and scale to put a click back where the
	// person aimed it.
	writeJSON(w, http.StatusOK, shot)
}

// --- driving the screen (ADR 0009) ---

// humanSeat is who the companion is in the conversation, and therefore who
// holds a control lease taken through this API. The daemon has one seat per
// kind of participant, not one per window, so a second companion asking for
// the same run's screen is the same hand, not a rival.
const humanSeat = "human"

// controlOut is what every control route answers with: the lease as it now
// stands, plus the size of the screen the coordinates are fractions of.
type controlOut struct {
	Control *machine.Control `json:"control"`
	Screen  *machine.Screen  `json:"screen,omitempty"`
}

func (a *api) takeControl(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	var in struct {
		TTLSeconds int `json:"ttlSeconds"`
	}
	// An empty body is the ordinary case: take it for the default lease.
	_ = json.NewDecoder(r.Body).Decode(&in)

	c, fresh, err := a.mgr.TakeControl(id, humanSeat, time.Duration(in.TTLSeconds)*time.Second)
	if err != nil {
		a.failControl(w, id, err)
		return
	}
	// The helper is compiled in the guest on first use, which takes seconds.
	// Doing it here rather than on the first click means the pointer works
	// from the moment the app says it has control.
	screen, err := a.mgr.ScreenOf(r.Context(), id)
	if err != nil {
		_, _, _ = a.mgr.ReleaseControl(id, humanSeat)
		a.fail(w, http.StatusConflict, err)
		return
	}
	if fresh {
		// The coder learns on its next agent_wait that someone else is
		// driving its machine (ADR 0006).
		a.event(id, "human took control of the screen")
	}
	writeJSON(w, http.StatusOK, controlOut{Control: &c, Screen: &screen})
}

func (a *api) releaseControl(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	c, held, err := a.mgr.ReleaseControl(id, humanSeat)
	if err != nil {
		a.failControl(w, id, err)
		return
	}
	if held {
		a.event(id, fmt.Sprintf("human gave the screen back after %d actions", c.Actions))
	}
	writeJSON(w, http.StatusOK, controlOut{})
}

// input posts one batch of mouse and keyboard actions. Coordinates are
// fractions of the screen (ADR 0009), because the app is looking at a frame
// scaled to its window and cannot know the guest's resolution.
func (a *api) input(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	var in struct {
		Actions []machine.InputAction `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		a.fail(w, http.StatusBadRequest, err)
		return
	}
	if len(in.Actions) == 0 {
		a.fail(w, http.StatusBadRequest, errors.New("actions is empty"))
		return
	}
	res, err := a.mgr.Input(r.Context(), id, humanSeat, in.Actions)
	if err != nil {
		a.failControl(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// failControl gives the lease errors their own status codes, so the app can
// tell "take control first" (409) and "someone else is driving" (423) from a
// daemon fault, and a run whose machine is gone from either.
func (a *api) failControl(w http.ResponseWriter, runID string, err error) {
	switch {
	case errors.Is(err, machine.ErrControlHeld):
		a.fail(w, http.StatusLocked, err)
	case errors.Is(err, machine.ErrNoControl):
		a.fail(w, http.StatusConflict, err)
	case !a.mgr.Live(runID):
		a.fail(w, http.StatusConflict, err)
	default:
		a.fail(w, http.StatusInternalServerError, err)
	}
}

func (a *api) destroy(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	if err := a.mgr.Destroy(r.Context(), id); err != nil {
		// A run whose machine is already gone is a state the app can show,
		// not a daemon fault.
		code := http.StatusInternalServerError
		if !a.mgr.Live(id) {
			code = http.StatusConflict
		}
		a.fail(w, code, err)
		return
	}
	// Only a machine that really went away is worth announcing, so this
	// follows the destroy rather than preceding it. The daemon's lifecycle
	// bridge posts its own "machine destroyed" from the manager's event.
	a.event(id, "human destroyed the machine")
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{true})
}

func (a *api) event(runID, text string) {
	store, err := a.reg.Get(runID)
	if err != nil {
		a.log.Warn("cannot reach the conversation", "runId", runID, "err", err)
		return
	}
	if _, err := store.Append(session.Message{From: session.System, Kind: session.Event, Text: text}); err != nil {
		a.log.Warn("cannot record a human action", "runId", runID, "err", err)
	}
}

// --- artifacts ---

// contentTypes maps a run artifact's extension to what the app should do
// with it. Anything else is served as a download.
var contentTypes = map[string]string{
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".json":  "application/json",
	".jsonl": "text/plain; charset=utf-8",
	".log":   "text/plain; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
}

func (a *api) artifact(w http.ResponseWriter, r *http.Request) {
	id, ok := a.run(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	// Only a bare file name in the run directory. A separator or a dot-dot
	// is the caller asking for someone else's file.
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		a.fail(w, http.StatusBadRequest, fmt.Errorf("%q is not a file in the run directory", name))
		return
	}
	path := filepath.Join(a.mgr.RunDir(id), name)
	f, err := os.Open(path)
	if err != nil {
		a.fail(w, http.StatusNotFound, fmt.Errorf("no artifact %q in run %q", name, id))
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		a.fail(w, http.StatusNotFound, fmt.Errorf("no artifact %q in run %q", name, id))
		return
	}
	ct := contentTypes[strings.ToLower(filepath.Ext(name))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// --- events ---

type sseEvent struct {
	name string
	data any
}

type sseClient struct {
	ch      chan sseEvent
	dropped chan struct{}
	once    sync.Once
}

// send never blocks: a client that cannot keep up is dropped and reconnects,
// which is cheaper than holding up the manager or the store. This runs under
// the store's lock, so it must do nothing else.
func (c *sseClient) send(ev sseEvent) {
	select {
	case c.ch <- ev:
	case <-c.dropped:
	default:
		c.once.Do(func() { close(c.dropped) })
	}
}

func (a *api) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.fail(w, http.StatusInternalServerError, errors.New("this server cannot stream"))
		return
	}
	only := r.URL.Query().Get("runId")
	c := &sseClient{ch: make(chan sseEvent, clientBuffer), dropped: make(chan struct{})}
	wants := func(runID string) bool { return only == "" || only == runID }

	stopMachines := a.mgr.Listen(func(ev machine.LifecycleEvent) {
		if !wants(ev.RunID) {
			return
		}
		switch ev.Kind {
		case "created", "ready", "failed", "stopped", "destroyed", "control":
			c.send(sseEvent{name: "run", data: ev})
		case "step":
			c.send(sseEvent{name: "step", data: map[string]any{"runId": ev.RunID, "step": ev.Step}})
		case "frame":
			if ev.Frame != nil {
				c.send(sseEvent{name: "frame", data: map[string]any{
					"runId": ev.RunID, "at": ev.Frame.At, "file": ev.Frame.File, "step": ev.Frame.Step,
				}})
			}
		}
	})
	defer stopMachines()
	stopMessages := a.reg.Listen(func(runID string, m session.Message) {
		if !wants(runID) {
			return
		}
		c.send(sseEvent{name: "message", data: map[string]any{"runId": runID, "message": m}})
	})
	defer stopMessages()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.dropped:
			a.log.Warn("dropping an event stream that fell behind", "runId", only)
			return
		case ev := <-c.ch:
			data, err := json.Marshal(ev.data)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// --- plumbing ---

// run reads the {id} wildcard and answers 404 itself when no such run was
// ever recorded, so every route says the same thing about an unknown run.
func (a *api) run(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		a.fail(w, http.StatusNotFound, fmt.Errorf("no run %q", id))
		return "", false
	}
	if st, err := os.Stat(a.mgr.RunDir(id)); err != nil || !st.IsDir() {
		a.fail(w, http.StatusNotFound, fmt.Errorf("no run %q", id))
		return "", false
	}
	return id, true
}

func (a *api) fail(w http.ResponseWriter, code int, err error) {
	if code >= 500 {
		a.log.Error("api", "status", code, "err", err)
	}
	writeJSON(w, code, struct {
		Error string `json:"error"`
	}{err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
