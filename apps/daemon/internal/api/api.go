// Package api is the companion app's HTTP surface over the manager and the conversation store (ADR 0007).
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

type api struct {
	mgr *machine.Manager
	reg *session.Registry
	log *slog.Logger

	recMu    sync.Mutex
	recLocks map[string]*sync.Mutex // per run, held while its recording is checked or built

	upMu    sync.Mutex
	upLocks map[string]*sync.Mutex // per run, held while its uploads are written or removed
}

// runHandler is a route under /api/runs/{id} whose run is known to exist.
type runHandler func(w http.ResponseWriter, r *http.Request, runID string)

// New returns the handler for /api/. Patterns keep the /api prefix because the daemon mounts it without stripping.
func New(mgr *machine.Manager, reg *session.Registry, log *slog.Logger) http.Handler {
	a := &api{mgr: mgr, reg: reg, log: log, recLocks: map[string]*sync.Mutex{}, upLocks: map[string]*sync.Mutex{}}
	// A human lease that ran out with nobody renewing it (the Companion quit or crashed) is
	// recorded when it lapses, not when someone next touches the screen (issue #57). The manager
	// announces every lapse, whichever call found it, before the take or release that follows
	// returns; posting synchronously keeps "lost control" ahead of the next "took control".
	mgr.Listen(func(ev machine.LifecycleEvent) {
		if ev.Kind == "control" && ev.Lapsed != nil && ev.Lapsed.Holder == humanSeat {
			a.controlEvent(ev.RunID, lapsedText(*ev.Lapsed), session.ControlReturned)
		}
		if ev.Kind == "destroyed" {
			go a.removeUploads(ev.RunID) // may wait for an upload in progress, so not on the manager's goroutine
		}
	})
	a.sweepUploads()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runs", a.listRuns)
	mux.HandleFunc("GET /api/events", a.events)
	for pattern, h := range map[string]runHandler{
		"GET /api/runs/{id}":                     a.runDetail,
		"GET /api/runs/{id}/steps":               a.runSteps,
		"GET /api/runs/{id}/frames":              a.runFrames,
		"GET /api/runs/{id}/frames/{file...}":    a.frameFile,
		"GET /api/runs/{id}/recording.mp4":       a.recording,
		"GET /api/runs/{id}/messages":            a.readMessages,
		"GET /api/runs/{id}/artifacts/{name...}": a.artifact,
		"POST /api/runs/{id}/messages":           a.postMessage,
		"POST /api/runs/{id}/screenshot":         a.screenshot,
		"GET /api/runs/{id}/screen/live":         a.screenLive,
		"POST /api/runs/{id}/control":            a.takeControl,
		"DELETE /api/runs/{id}/control":          a.releaseControl,
		"POST /api/runs/{id}/input":              a.input,
		"POST /api/runs/{id}/destroy":            a.destroy,
	} {
		mux.HandleFunc(pattern, a.withRun(h))
	}
	// The one write whose body is not JSON: a remote client's project as a gzipped tar. It is
	// exempt from jsonBodies and demands application/gzip itself, which no HTML form can send
	// (forms send urlencoded, multipart or text/plain), so a web page still cannot post it.
	root := http.NewServeMux()
	root.HandleFunc("PUT /api/runs/{id}/sync", a.withRun(a.uploadSync))
	root.Handle("/", a.jsonBodies(mux))
	return root
}

// withRun answers 404 for a run that was never recorded, so every route says the same thing about it.
func (a *api) withRun(h runHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !bareName(id) || !isDir(a.mgr.RunDir(id)) {
			a.fail(w, http.StatusNotFound, fmt.Errorf("no run %q", id))
			return
		}
		h(w, r, id)
	}
}

// bareName reports whether name is a single path element that cannot leave its directory.
func bareName(name string) bool {
	return name != "" && name != "." && !strings.ContainsAny(name, `/\`) && !strings.Contains(name, "..")
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (a *api) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		a.fail(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

// failMachine reports a machine operation's error: 409 when the machine is gone, 500 otherwise.
func (a *api) failMachine(w http.ResponseWriter, runID string, err error) {
	code := http.StatusInternalServerError
	if !a.mgr.Live(runID) {
		code = http.StatusConflict
	}
	a.fail(w, code, err)
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

// serveFile answers the regular file at path, or 404 with notFound.
func (a *api) serveFile(w http.ResponseWriter, r *http.Request, path, contentType string, notFound error) {
	f, err := os.Open(path)
	if err != nil {
		a.fail(w, http.StatusNotFound, notFound)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		a.fail(w, http.StatusNotFound, notFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// event posts a system event into the run's conversation, so the coder hears of it on its next agent_wait (ADR 0006).
func (a *api) event(runID, text string) {
	a.post(runID, session.Message{From: session.System, Kind: session.Event, Text: text})
}

// controlEvent posts a system event saying the human took the screen or that it came back
// (session.ControlTaken, ControlReturned): the verifier is told to look before acting again, and
// resumes an interrupted task when it comes back (issue #124).
func (a *api) controlEvent(runID, text, control string) {
	a.post(runID, session.Message{From: session.System, Kind: session.Event, Text: text, Control: control})
}

func (a *api) post(runID string, m session.Message) {
	store, err := a.reg.Get(runID)
	if err != nil {
		a.log.Warn("cannot reach the conversation", "runId", runID, "err", err)
		return
	}
	if _, err := store.Append(m); err != nil {
		a.log.Warn("cannot record a human action", "runId", runID, "err", err)
	}
}
