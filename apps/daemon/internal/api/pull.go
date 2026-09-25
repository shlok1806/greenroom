package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// Headers of GET /api/runs/{id}/pull. The step is known before the first byte, so a client
// can name its copy after it; the error trailer comes after the last, because tar can fail
// once the archive has begun and the status is long sent.
const (
	pullStepHeader   = "Greenroom-Step"
	pullErrorTrailer = "Greenroom-Error"
)

// pullArchive is machine_pull for a client on another host (ADR 0021), the mirror of
// uploadSync: it streams ?src= out of the guest as a gzipped tar made there, which the client
// unpacks with the same rules the upload route applies (internal/tarball). ?exclude= may
// repeat; only its literal names are pruned in the guest, and the client applies them all.
// A missing source is 404 before anything is sent. A failure after the archive has begun
// leaves it cut short and names itself in the Greenroom-Error trailer, which is empty on success.
func (a *api) pullArchive(w http.ResponseWriter, r *http.Request, runID string) {
	src := r.URL.Query().Get("src")
	if strings.TrimSpace(src) == "" {
		a.fail(w, http.StatusBadRequest, errors.New("src is required: a guest path relative to the home, ~/x, or absolute"))
		return
	}
	if !a.mgr.Live(runID) {
		a.fail(w, http.StatusConflict, fmt.Errorf("run %q has no machine to pull from", runID))
		return
	}
	started := false
	err := a.mgr.PullArchive(r.Context(), runID, src, r.URL.Query()["exclude"], func(step int) io.Writer {
		started = true
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set(pullStepHeader, strconv.Itoa(step))
		w.Header().Set("Trailer", pullErrorTrailer)
		w.WriteHeader(http.StatusOK)
		return w
	})
	switch {
	case started:
		w.Header().Set(pullErrorTrailer, trailerText(err))
		if err != nil {
			a.log.Warn("pull archive cut short", "runId", runID, "src", src, "err", err)
		}
	case errors.Is(err, machine.ErrNotInGuest):
		a.fail(w, http.StatusNotFound, err)
	case err != nil:
		a.failMachine(w, runID, err)
	}
}

// trailerText is err as one header line: tar's stderr can hold several.
func trailerText(err error) string {
	if err == nil {
		return ""
	}
	return strings.Join(strings.FieldsFunc(err.Error(), func(r rune) bool { return r == '\n' || r == '\r' }), "; ")
}
