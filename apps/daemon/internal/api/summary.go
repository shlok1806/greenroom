package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/summary"
)

// summaries holds the summaries of runs with no live machine (root ADR 0036). Such a summary
// changes only when its conversation does (a person accepts a verdict after the run ended), so
// it is kept until the conversation grows or the manifest is rewritten, and the board does
// not re-read every old run's steps on each request.
type summaries struct {
	mu   sync.Mutex
	done map[string]doneSummary
}

// doneKey is what a finished run's summary is made from that can still change.
type doneKey struct {
	messages int
	manifest time.Time // the manifest's modification time
}

type doneSummary struct {
	key doneKey
	sum summary.Summary
}

func (c *summaries) get(runID string, key doneKey) (summary.Summary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.done[runID]
	return d.sum, ok && d.key == key
}

func (c *summaries) put(runID string, key doneKey, s summary.Summary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done == nil {
		c.done = map[string]doneSummary{}
	}
	c.done[runID] = doneSummary{key: key, sum: s}
}

func (c *summaries) forget(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.done, runID)
}

// board answers GET /api/summary: every run's summary in its group, and the free Macs.
func (a *api) board(w http.ResponseWriter, _ *http.Request) {
	ids, err := a.reg.RunIDs()
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	live := a.liveMachines()
	now := time.Now().UTC()
	runs := make([]summary.Summary, 0, len(ids))
	for _, id := range ids {
		runs = append(runs, a.runSummary(id, live[id], now))
	}
	writeJSON(w, http.StatusOK, summary.NewBoard(runs, a.macs(live)))
}

// oneSummary answers GET /api/runs/{id}/summary.
func (a *api) oneSummary(w http.ResponseWriter, _ *http.Request, id string) {
	writeJSON(w, http.StatusOK, a.runSummary(id, a.liveMachines()[id], time.Now().UTC()))
}

func (a *api) liveMachines() map[string]*machine.Machine {
	live := map[string]*machine.Machine{}
	for _, mc := range a.mgr.List() {
		live[mc.RunID] = mc
	}
	return live
}

// macs counts the host's free machines: every machine the manager holds takes a slot but a
// failed boot's (machine.Manager.checkHostCapacity).
func (a *api) macs(live map[string]*machine.Machine) summary.Macs {
	inUse := 0
	for _, mc := range live {
		if mc.Status != machine.Failed {
			inUse++
		}
	}
	return summary.MacsFree(a.mgr.MaxMachines(), inUse)
}

// runSummary is one run's summary as of now; mc is its live machine, or nil.
func (a *api) runSummary(runID string, mc *machine.Machine, now time.Time) summary.Summary {
	store, _ := a.reg.Get(runID) // nil when unreadable: the summary words what the manifest has
	var msgs []session.Message
	if store != nil {
		msgs = store.After(0)
	}
	key := doneKey{messages: len(msgs)}
	if st, err := os.Stat(filepath.Join(a.mgr.RunDir(runID), "manifest.json")); err == nil {
		key.manifest = st.ModTime()
	}
	if mc == nil {
		if s, ok := a.sums.get(runID, key); ok {
			return s
		}
	} else {
		a.sums.forget(runID)
	}
	s := summary.Derive(a.summaryInput(runID, mc, store, msgs, now))
	if mc == nil {
		a.sums.put(runID, key, s)
	}
	return s
}

// summaryInput gathers what the summary is made from. Steps are read only when the summary
// uses them: for an open run (what it is doing now, a screen that stopped answering) and for
// the pictures of a verdict's checks.
func (a *api) summaryInput(runID string, mc *machine.Machine, store *session.Store, msgs []session.Message, now time.Time) summary.Input {
	dir := a.mgr.RunDir(runID)
	in := summary.Input{RunID: runID, Messages: msgs, Now: now}
	if man, err := machine.ReadManifest(dir); err == nil {
		in.CreatedAt, in.EndedAt, in.Name, in.Source = man.CreatedAt, man.DestroyedAt, man.Name, man.Source
		in.Finish = man.Finish
		if man.Verdict != nil {
			in.Verdict = *man.Verdict
		}
	}
	if store != nil {
		in.Verdict = store.Verdict()
		if f := store.Finished(); f != nil {
			in.Finish = f
		}
	}
	if mc != nil {
		in.CreatedAt = mc.CreatedAt
		in.Machine = &summary.LiveMachine{Status: mc.Status, Error: mc.Error, Boot: mc.BootPhases(),
			LowOnFiles: mc.Files != nil && mc.Files.Warning != ""}
		if mc.Control != nil {
			in.Machine.Controller = mc.Control.Holder
		}
	}
	if frames, err := machine.ReadFrames(dir); err == nil {
		in.Frames = frames
		if n := len(frames); n > 0 {
			in.LastFrame = &frames[n-1]
		}
	}
	if mc != nil || citesEvidence(in.Verdict) {
		steps, err := machine.ReadSteps(dir)
		if err != nil {
			a.log.Warn("cannot read a run's steps for its summary", "runId", runID, "err", err)
		}
		in.Steps = steps
		in.StartFailure = machine.StartFailureOf(steps)
	} else {
		// The light read the run list makes; a closed run's summary is cached after it.
		in.StartFailure = a.stepLog(runID).StartFailure
	}
	return in
}

// SummaryEvery is how often an event stream sends the summaries of runs that changed: a
// burst of steps and messages becomes one summary event per run. Tests shorten it.
var SummaryEvery = 250 * time.Millisecond

// dirtyRuns is the runs whose summary may have changed since the stream last looked. It is
// marked from listener callbacks, which must not block.
type dirtyRuns struct {
	mu  sync.Mutex
	set map[string]bool
}

func (d *dirtyRuns) mark(runID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.set == nil {
		d.set = map[string]bool{}
	}
	d.set[runID] = true
}

func (d *dirtyRuns) take() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.set))
	for id := range d.set {
		out = append(out, id)
	}
	d.set = nil
	return out
}

// summaryEvents are the `summary` events for the runs given whose summary differs from the one
// this stream last sent (sent, updated in place). The running clock (elapsedSeconds) and a
// newer record that changed no word (updatedAt) are not a change: a client counts the clock
// itself, and a step that leaves "now" as it was would otherwise send one event per step.
func (a *api) summaryEvents(runIDs []string, sent map[string][]byte) []sseEvent {
	if len(runIDs) == 0 {
		return nil
	}
	live := a.liveMachines()
	now := time.Now().UTC()
	var out []sseEvent
	for _, id := range runIDs {
		if !isDir(a.mgr.RunDir(id)) {
			continue
		}
		s := a.runSummary(id, live[id], now)
		still := s
		// lastFrame moves every frame interval, and `frame` events already say so.
		still.ElapsedSeconds, still.UpdatedAt, still.LastFrame = 0, time.Time{}, nil
		key, err := json.Marshal(still)
		if err != nil || bytes.Equal(key, sent[id]) {
			continue
		}
		sent[id] = key
		out = append(out, sseEvent{name: "summary", data: summaryEvent{RunID: id, Summary: s, Macs: a.macs(live)}})
	}
	return out
}

// summaryEvent is the data of a `summary` event.
type summaryEvent struct {
	RunID   string          `json:"runId"`
	Summary summary.Summary `json:"summary"`
	Macs    summary.Macs    `json:"macs"`
}

// citesEvidence reports whether a verdict's checks cite any step: each check row shows its
// proof, so the summary reads the steps to find the pictures and marks.
func citesEvidence(v session.VerdictState) bool {
	for _, c := range v.Checks {
		if len(c.Evidence) > 0 {
			return true
		}
	}
	return false
}
