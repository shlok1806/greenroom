package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// RunSummary is one row of the companion's run list, live or finished.
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

// Statuses of a run with no live machine; a live one reports its machine.Status.
const (
	statusFinished = "finished"
	statusFailed   = "failed" // no readable manifest
)

// RunDetail is one run in full: the on-disk manifest, overlaid with the live machine and the conversation's verdict.
type RunDetail struct {
	machine.Manifest
	Machine *machine.Machine      `json:"machine"`
	Verdict *session.VerdictState `json:"verdict"`
}

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
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, out)
}

func (a *api) summary(runID string, mc *machine.Machine) RunSummary {
	s := RunSummary{RunID: runID, Status: statusFailed}
	if man, err := machine.ReadManifest(a.mgr.RunDir(runID)); err == nil {
		s.CreatedAt, s.DestroyedAt, s.Image = man.CreatedAt, man.DestroyedAt, man.Image
		s.IP, s.Verdict, s.Status = man.IP, man.Verdict, statusFinished
	}
	// Count steps.jsonl: manifest.Steps is a high-water mark, and the list must agree with /steps.
	steps := a.stepLog(runID)
	s.Steps = steps.Count
	if mc != nil {
		s.Status, s.Image, s.IP, s.VNCURL = string(mc.Status), mc.Image, mc.IP, mc.VNCURL
		s.CreatedAt = mc.CreatedAt
	}
	// Steps and messages, not frames: the recorder captures an idle machine too (machine.Manager.LastActivity).
	s.LastActivity = a.mgr.ActivityFrom(runID, s.CreatedAt, steps)
	if s.LastActivity.Before(s.CreatedAt) {
		s.LastActivity = s.CreatedAt
	}
	if frames, err := machine.ReadFrames(a.mgr.RunDir(runID)); err == nil {
		s.Frames = len(frames)
	}
	if store, err := a.reg.Get(runID); err == nil {
		s.Messages = store.Len()
		if last := store.LastAt(); last.After(s.LastActivity) {
			s.LastActivity = last // also counted by the manager when main wires it in
		}
		if v := store.Verdict(); v.Status != session.None {
			s.Verdict = &v
		}
	}
	return s
}

func (a *api) runDetail(w http.ResponseWriter, _ *http.Request, id string) {
	man, err := machine.ReadManifest(a.mgr.RunDir(id))
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	man.Steps = a.stepLog(id).Count
	d := RunDetail{Manifest: man, Verdict: man.Verdict}
	for _, mc := range a.mgr.List() {
		if mc.RunID == id {
			d.Machine = mc
		}
	}
	// Verdict shadows the manifest's field, so it falls back to the manifest's (possibly nil) verdict.
	if store, err := a.reg.Get(id); err == nil {
		if v := store.Verdict(); v.Status != session.None {
			d.Verdict = &v
		}
	}
	writeJSON(w, http.StatusOK, d)
}

// stepLog reads steps.jsonl; an unreadable log counts as empty so a summary still answers.
func (a *api) stepLog(runID string) machine.StepLog {
	log, err := machine.ReadStepLog(a.mgr.RunDir(runID))
	if err != nil {
		a.log.Warn("cannot read a run's steps", "runId", runID, "err", err)
		return machine.StepLog{}
	}
	return log
}

func (a *api) runSteps(w http.ResponseWriter, _ *http.Request, id string) {
	steps, err := machine.ReadSteps(a.mgr.RunDir(id))
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, steps)
}
