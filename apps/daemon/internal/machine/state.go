package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

func (m *Manager) statePath() string { return filepath.Join(m.Root, "state.json") }

// loadState reattaches the machines in state.json that tart still runs, and
// records the end of each run it drops.
func (m *Manager) loadState() error {
	saved, err := m.readState()
	if err != nil || len(saved) == 0 {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	running, err := m.runningVMs(ctx)
	if err != nil {
		return err
	}
	for _, mc := range saved {
		if mc == nil || mc.RunID == "" || mc.Dir == "" {
			m.Log.Warn("skipping an incomplete machine in state.json", "machine", mc)
			continue
		}
		if !running[mc.Name] {
			m.Log.Warn("dropping machine that is no longer running", "runId", mc.RunID, "name", mc.Name)
			m.endRun(mc.Dir)
			continue
		}
		if mc.rec, err = newRecorder(mc.Dir, m.reattachManifest(mc), m.Log); err != nil {
			m.Log.Error("cannot reopen a running machine's run; leaving its VM unmanaged",
				"runId", mc.RunID, "name", mc.Name, "err", err)
			continue
		}
		mc.ready = make(chan struct{})
		mc.input = &inputState{}
		mc.Control = nil // its holder did not survive the daemon (ADR 0009)
		var boot context.Context
		if mc.Status == Ready {
			close(mc.ready)
		} else {
			boot = newBoot(mc)
		}
		m.mu.Lock() // goroutines started for earlier machines already read the map
		m.machines[mc.RunID] = mc
		m.mu.Unlock()
		if boot != nil {
			go m.finishBoot(boot, mc, mc.CreatedAt)
		} else {
			m.watchProcess(mc)
			m.startFrames(mc)
		}
	}
	return m.saveState()
}

// readState parses state.json. A corrupt file is moved aside so the daemon
// still starts; any VMs it named then count as foreign.
func (m *Manager) readState() ([]*Machine, error) {
	path := m.statePath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var saved []*Machine
	if perr := json.Unmarshal(data, &saved); perr != nil {
		aside := path + ".corrupt-" + time.Now().UTC().Format("20060102-150405.000000000")
		if err := os.Rename(path, aside); err != nil {
			return nil, fmt.Errorf("parse %s: %w (and cannot move it aside: %v)", path, perr, err)
		}
		m.Log.Error("state.json is corrupt; moved it aside and starting with no machines", "movedTo", aside, "err", perr)
		return nil, nil
	}
	return saved, nil
}

// reattachManifest carries the run's manifest forward, taking the step
// high-water mark from steps.jsonl when the manifest is behind it: reusing a
// spent number would overwrite that step's artifact.
func (m *Manager) reattachManifest(mc *Machine) Manifest {
	man := Manifest{RunID: mc.RunID, Image: mc.Image, MachineName: mc.Name, IP: mc.IP, CreatedAt: mc.CreatedAt}
	if saved, err := ReadManifest(mc.Dir); err == nil {
		man.Steps = saved.Steps
		man.Verdict = saved.Verdict
		if !saved.CreatedAt.IsZero() {
			man.CreatedAt = saved.CreatedAt
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		m.Log.Warn("cannot read the run's manifest; its step count restarts", "runId", mc.RunID, "err", err)
	}
	if log, err := ReadStepLog(mc.Dir); err != nil {
		m.Log.Warn("cannot read the run's steps; trusting the manifest's step count", "runId", mc.RunID, "err", err)
	} else if log.Highest > man.Steps {
		m.Log.Warn("manifest is behind the recorded steps; carrying the steps forward",
			"runId", mc.RunID, "manifest", man.Steps, "recorded", log.Highest)
		man.Steps = log.Highest
	}
	return man
}

// saveState atomically writes the current machine list to state.json. m.mu is
// held only to marshal; stateMu keeps an older list from landing after a newer one.
func (m *Manager) saveState() error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.mu.Lock()
	list := make([]*Machine, 0, len(m.machines))
	for _, mc := range m.machines {
		list = append(list, mc)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(m.statePath(), data)
}

// persist saves state for callers that cannot act on a failure: the next save retries.
func (m *Manager) persist() {
	if err := m.saveState(); err != nil {
		m.Log.Error("cannot save state.json", "err", err)
	}
}

// endRun stamps destroyedAt on a run whose machine vanished while the daemon
// was down. It is dated from the last step or frame, not now, so downtime is
// not credited to the run. A manifest that already has an end is left alone.
func (m *Manager) endRun(dir string) {
	man, err := ReadManifest(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.Log.Warn("cannot read a finished run's manifest", "dir", dir, "err", err)
		}
		return
	}
	if man.DestroyedAt != nil {
		return
	}
	end := man.CreatedAt
	if log, err := ReadStepLog(dir); err == nil && log.Last.After(end) {
		end = log.Last
	}
	if frames, err := ReadFrames(dir); err == nil && len(frames) > 0 {
		if last := frames[len(frames)-1].At; last.After(end) {
			end = last
		}
	}
	end = end.UTC()
	man.DestroyedAt = &end
	if err := saveManifest(dir, man); err != nil {
		m.Log.Warn("cannot record the end of a finished run", "dir", dir, "err", err)
	}
}

// RecordVerdict writes the conversation's verdict into the run's manifest,
// through the live recorder if the machine is alive, else directly on disk.
func (m *Manager) RecordVerdict(runID string, v session.VerdictState) error {
	if mc, err := m.get(runID); err == nil {
		return mc.rec.update(func(man *Manifest) { man.Verdict = &v })
	}
	dir := m.RunDir(runID)
	man, err := ReadManifest(dir)
	if err != nil {
		return err
	}
	man.Verdict = &v
	return saveManifest(dir, man)
}

// RecordFinish writes how the run finished into its manifest (ADR 0034), through the live
// recorder if the machine is alive, else directly on disk, like RecordVerdict.
func (m *Manager) RecordFinish(runID string, f session.Finish) error {
	if mc, err := m.get(runID); err == nil {
		return mc.rec.update(func(man *Manifest) { man.Finish = &f })
	}
	dir := m.RunDir(runID)
	man, err := ReadManifest(dir)
	if err != nil {
		return err
	}
	man.Finish = &f
	return saveManifest(dir, man)
}
