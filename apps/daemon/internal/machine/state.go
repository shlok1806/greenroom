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
	data, err := os.ReadFile(m.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved []*Machine
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("parse %s: %w", m.statePath(), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	vms, err := m.tart.List(ctx)
	if err != nil {
		return err
	}
	running := map[string]bool{}
	for _, vm := range vms {
		running[vm.Name] = vm.State == "running"
	}
	for _, mc := range saved {
		if !running[mc.Name] {
			m.Log.Warn("dropping machine that is no longer running", "runId", mc.RunID, "name", mc.Name)
			m.endRun(mc.Dir)
			continue
		}
		if mc.rec, err = newRecorder(mc.Dir, m.reattachManifest(mc)); err != nil {
			return err
		}
		mc.ready = make(chan struct{})
		mc.input = &inputState{}
		mc.Control = nil // its holder did not survive the daemon (ADR 0009)
		m.machines[mc.RunID] = mc
		if mc.Status == Ready {
			close(mc.ready)
			m.startFrames(mc)
		} else {
			go m.finishBoot(mc, mc.CreatedAt)
		}
	}
	return m.saveStateLocked()
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

func (m *Manager) saveStateLocked() error {
	list := make([]*Machine, 0, len(m.machines))
	for _, mc := range m.machines {
		list = append(list, mc)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.statePath(), data, 0o644)
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
