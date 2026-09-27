package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Heartbeat is how often an idle SSE stream writes a comment line to keep proxies from closing it. Tests shorten it.
var Heartbeat = 15 * time.Second

// clientBuffer is how far one SSE client may fall behind before it is dropped; the files on disk are the truth (ADR 0007).
const clientBuffer = 256

type sseEvent struct {
	name string
	data any
}

type sseClient struct {
	ch      chan sseEvent
	dropped chan struct{}
	once    sync.Once
}

// send never blocks: it runs inside store and manager callbacks (the store's under its lock), so a slow client is dropped.
func (c *sseClient) send(ev sseEvent) {
	select {
	case c.ch <- ev:
	case <-c.dropped:
	default:
		c.once.Do(func() { close(c.dropped) })
	}
}

// runEvent is a lifecycle event whose machine carries its boot phases, like /api/runs/{id}.
type runEvent struct {
	machine.LifecycleEvent
	Machine *LiveMachine `json:"machine,omitempty"`
}

func (a *api) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.fail(w, http.StatusInternalServerError, errors.New("this server cannot stream"))
		return
	}
	only := r.URL.Query().Get("runId")
	wants := func(runID string) bool { return only == "" || only == runID }
	c := &sseClient{ch: make(chan sseEvent, clientBuffer), dropped: make(chan struct{})}

	stopMachines := a.mgr.Listen(func(ev machine.LifecycleEvent) {
		if !wants(ev.RunID) {
			return
		}
		switch ev.Kind {
		case "created", "ready", "failed", "stopped", "destroyed", "rebooting", "control":
			c.send(sseEvent{name: "run", data: runEvent{LifecycleEvent: ev, Machine: liveMachine(ev.Machine)}})
		case "boot":
			if ev.Boot != nil {
				c.send(sseEvent{name: "boot", data: map[string]any{"runId": ev.RunID, "phase": ev.Boot}})
			}
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
		if wants(runID) {
			c.send(sseEvent{name: "message", data: map[string]any{"runId": runID, "message": m}})
		}
	})
	defer stopMessages()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// URLSession holds the response until the first body bytes; send some now.
	_, _ = fmt.Fprint(w, ": ok\n\n")
	flusher.Flush()

	ticker := time.NewTicker(Heartbeat)
	defer ticker.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case <-c.dropped:
			a.log.Warn("dropping an event stream that fell behind", "runId", only)
			return
		case ev := <-c.ch:
			data, merr := json.Marshal(ev.data)
			if merr != nil {
				continue
			}
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, data)
		case <-ticker.C:
			_, err = fmt.Fprint(w, ": ping\n\n")
		}
		if err != nil {
			return
		}
		flusher.Flush()
	}
}
