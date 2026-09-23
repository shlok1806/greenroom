package api

import (
	"errors"
	"net/http"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// screenLive streams the machine's live screen (ADR 0011) until the client goes away or the stream ends.
func (a *api) screenLive(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.fail(w, http.StatusInternalServerError, errors.New("this server cannot stream"))
		return
	}
	watch, err := a.mgr.WatchScreen(r.Context(), id)
	if errors.Is(err, machine.ErrNotReady) {
		a.fail(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		a.failMachine(w, id, err)
		return
	}
	defer watch.Close()

	w.Header().Set("Content-Type", "application/x-greenroom-screen")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-watch.C:
			if !ok {
				if err := watch.Err(); err != nil {
					a.log.Info("live screen ended", "runId", id, "err", err)
				}
				return
			}
			if _, err := msg.WriteTo(w); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
