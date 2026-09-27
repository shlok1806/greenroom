package machine

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// Orphaned run clones (issue #103). A run clone is named greenroom-<runId> (newRunID). One whose
// run record is gone (a test killed mid-run takes its temporary root with it; a daemon that
// died between the clone and the run record never wrote one) stays in tart's storage holding
// about 30 GB, and nothing ever deletes it.

// runCloneRE is exactly a run clone's name: "greenroom-" and a runId. Images
// (greenroom-base-v7-r2, greenroom-lean-a) and anything else never match.
var runCloneRE = regexp.MustCompile(`^greenroom-([0-9]{8}-[0-9]{6})-[0-9a-f]{16}$`)

// DefaultOrphanAge is how old a run clone must be before the sweep may delete it: well past any
// boot, trial or test, so a clone another manager made a moment ago is never taken.
const DefaultOrphanAge = 6 * time.Hour

// SweptVM is one run clone the sweep deleted, or would delete in a dry run.
type SweptVM struct {
	Name   string        `json:"name"`
	Age    time.Duration `json:"age"`
	DryRun bool          `json:"dryRun,omitempty"`
	Error  string        `json:"error,omitempty"` // tart delete failed; the clone is still there
}

// SweepOrphans deletes the run clones nothing owns: named greenroom-<runId>, local, stopped
// (tart lists a VM with a live `tart run` as running), not a machine of this manager, with no
// run directory under this root, alsoRoots, or a root one level under any of them (the
// bench's is <root>/bench), and older than olderThan by both its runId's time and its directory
// in tart's storage. serve passes the default root as alsoRoots, so a daemon on a scratch root
// never takes the real daemon's clones. olderThan 0 turns it off. dryRun names them and deletes
// nothing. It logs each one.
func (m *Manager) SweepOrphans(ctx context.Context, olderThan time.Duration, dryRun bool, alsoRoots ...string) ([]SweptVM, error) {
	return SweepOrphans(ctx, m.tart, append([]string{m.Root}, alsoRoots...), m.Live, olderThan, dryRun, m.Log)
}

// SweepOrphans is Manager.SweepOrphans for a caller with no manager (greenroom sweep-orphans):
// live says which runIds a manager holds; a stopped clone with no run directory is an orphan
// either way, since a live machine's run always has one.
func SweepOrphans(ctx context.Context, c *tart.Client, roots []string, live func(string) bool, olderThan time.Duration,
	dryRun bool, log *slog.Logger) ([]SweptVM, error) {
	if olderThan <= 0 {
		return nil, nil
	}
	vms, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []SweptVM
	for _, vm := range vms {
		match := runCloneRE.FindStringSubmatch(vm.Name)
		if vm.Source != "local" || vm.State != "stopped" || match == nil {
			continue
		}
		runID := vm.Name[len("greenroom-"):]
		if live(runID) || slices.ContainsFunc(roots, func(root string) bool { return hasRunDir(root, runID) }) {
			continue
		}
		created, err := time.Parse("20060102-150405", match[1])
		if err != nil {
			continue
		}
		age := now.Sub(created)
		if info, err := os.Stat(filepath.Join(tart.Home(), "vms", vm.Name)); err == nil {
			age = min(age, now.Sub(info.ModTime()))
		}
		if age < olderThan {
			continue
		}
		s := SweptVM{Name: vm.Name, Age: age.Round(time.Minute), DryRun: dryRun}
		if dryRun {
			log.Info("would delete an orphaned run clone: no run directory, stopped", "name", vm.Name, "age", s.Age)
		} else if err := c.Delete(ctx, vm.Name); err != nil {
			s.Error = err.Error()
			log.Warn("cannot delete an orphaned run clone", "name", vm.Name, "err", err)
		} else {
			log.Warn("deleted an orphaned run clone: no run directory, stopped", "name", vm.Name, "age", s.Age)
		}
		out = append(out, s)
	}
	return out, nil
}

// hasRunDir reports whether runID has a run directory under root or one level down, where the
// bench keeps its own root.
func hasRunDir(root, runID string) bool {
	if _, err := os.Stat(filepath.Join(root, "runs", runID)); err == nil {
		return true
	}
	found, _ := filepath.Glob(filepath.Join(root, "*", "runs", runID))
	return len(found) > 0
}
