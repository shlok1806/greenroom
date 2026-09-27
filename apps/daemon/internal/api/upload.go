package api

import (
	"errors"
	"fmt"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tarball"
)

// Upload limits for PUT /api/runs/{id}/sync; vars so tests can shrink them.
var (
	maxUploadBytes = int64(2 << 30) // the compressed body
	uploadLimits   = tarball.Limits{Bytes: 4 << 30, Entries: 200_000}
)

// uploadSync is machine_sync for a client whose project is on another host (ADR 0021): the body
// is a gzipped tar of the project, unpacked into <root>/uploads/<runId>/<name> and synced into
// the guest from there with the same Manager.Sync, so dest rules, rsync's incremental copy and
// the recorded step are the local tool's, and so are mirror and the stray count (mirror=true and
// every exclude pattern come as query parameters). The staging directory is emptied before
// each upload and removed with the machine.
func (a *api) uploadSync(w http.ResponseWriter, r *http.Request, runID string) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/gzip" {
		a.fail(w, http.StatusUnsupportedMediaType, errors.New("the body must be a gzipped tar (Content-Type: application/gzip)"))
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "project"
	}
	if !bareName(name) {
		a.fail(w, http.StatusBadRequest, fmt.Errorf("name %q must be a single path element", name))
		return
	}
	dest := r.URL.Query().Get("dest")
	if err := machine.CheckDest(dest); err != nil {
		a.fail(w, http.StatusBadRequest, err)
		return
	}
	// connect left excluded paths out of the archive; the same patterns keep a mirror from
	// deleting the guest's copies of them and a stray count from counting them (daemon ADR 0001).
	opts := machine.SyncOptions{Dest: dest, Exclude: r.URL.Query()["exclude"]}
	switch m := r.URL.Query().Get("mirror"); m {
	case "", "false":
	case "true":
		opts.Mirror = true
	default:
		a.fail(w, http.StatusBadRequest, fmt.Errorf("mirror %q must be true or false", m))
		return
	}
	if opts.Mirror {
		if err := machine.CheckMirrorDest(dest); err != nil {
			a.fail(w, http.StatusBadRequest, err)
			return
		}
	}
	if !a.mgr.Live(runID) {
		a.fail(w, http.StatusConflict, fmt.Errorf("run %q has no machine to sync into", runID))
		return
	}

	unlock := a.lockUploads(runID)
	defer unlock()
	// A destroy that ran while this request waited or unpacked removed nothing of this upload.
	defer func() {
		if !a.mgr.Live(runID) {
			a.removeUploadsLocked(runID)
		}
	}()
	staging := filepath.Join(a.uploadsDir(runID), name)
	if err := os.RemoveAll(staging); err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := tarball.Untar(http.MaxBytesReader(w, r.Body, maxUploadBytes), staging, uploadLimits, nil); err != nil {
		var tooBig *http.MaxBytesError
		code := http.StatusBadRequest
		if errors.As(err, &tooBig) || errors.Is(err, tarball.ErrTooLarge) {
			code = http.StatusRequestEntityTooLarge
		}
		_ = os.RemoveAll(staging)
		a.fail(w, code, err)
		return
	}
	res, err := a.mgr.Sync(r.Context(), runID, staging, opts)
	if err != nil {
		a.failMachine(w, runID, err)
		return
	}
	res.Seconds = math.Round(res.Seconds*100) / 100 // as machine_sync reports it
	writeJSON(w, http.StatusOK, res)
}

// uploadsDir holds every staging directory of a run.
func (a *api) uploadsDir(runID string) string {
	return filepath.Join(a.mgr.Root, "uploads", runID)
}

// lockUploads serialises a run's uploads with each other and with their removal.
func (a *api) lockUploads(runID string) func() {
	a.upMu.Lock()
	mu, ok := a.upLocks[runID]
	if !ok {
		mu = &sync.Mutex{}
		a.upLocks[runID] = mu
	}
	a.upMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// removeUploads deletes a run's staging directories once no upload is using them.
func (a *api) removeUploads(runID string) {
	unlock := a.lockUploads(runID)
	defer unlock()
	a.removeUploadsLocked(runID)
}

func (a *api) removeUploadsLocked(runID string) {
	if err := os.RemoveAll(a.uploadsDir(runID)); err != nil {
		a.log.Warn("cannot remove a destroyed run's uploads", "runId", runID, "err", err)
	}
}

// sweepUploads removes the uploads of runs with no machine, left by a daemon that stopped
// before it saw their destroy.
func (a *api) sweepUploads() {
	entries, err := os.ReadDir(filepath.Join(a.mgr.Root, "uploads"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !a.mgr.Live(e.Name()) {
			a.removeUploads(e.Name())
		}
	}
}
