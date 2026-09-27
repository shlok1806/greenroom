package machine

// A `tart run` leaks one file on every `tart exec` (a vsock proxy in tart's control socket
// that never closes, issue #186; measured in run 20260927-210125-687fa19deff41e76), and dies with Virtualization.framework's "FIXME:
// Handle this: Error(24)" when it runs out. The daemon raises the limit its children inherit
// (internal/openfiles, daemon ADR 0002); this watch counts what each machine's `tart run`
// has open, so machine_list shows how close it is and the log says when it gets near.

import (
	"context"
	"fmt"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/openfiles"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// FileUse is one count of the files a machine's `tart run` has open.
type FileUse struct {
	PID  int `json:"pid"`
	Open int `json:"open"`
	// Limit is the soft limit the daemon gave `tart run` when it started it. 0 means unknown: a
	// machine reattached from an earlier daemon, which may have given it 256.
	Limit     uint64    `json:"limit,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	// Warning is set from nearLimit percent of the limit (of assumedLimit when it is unknown).
	Warning string `json:"warning,omitempty"`
}

const (
	// nearLimit is the percentage of the limit from which a count warns.
	nearLimit = 80
	// assumedLimit stands in for an unknown limit: what launchd gives, and what a daemon from
	// before ADR 0002 passed on to every `tart run`.
	assumedLimit = 256
	// defaultFileInterval is how often each machine's `tart run` is counted. Each count runs
	// lsof once; a leak of a few files a minute needs no finer view.
	defaultFileInterval = 30 * time.Second
)

// FileCheck is how the manager counts a machine's open files. Nil fields keep the default.
type FileCheck struct {
	// Interval is the time between counts; the first comes one interval after ready. 0 or
	// less turns the watch off.
	Interval time.Duration
	// Count returns how many files pid has open.
	Count func(ctx context.Context, pid int) (int, error)
	// Limit is the soft limit a `tart run` started now inherits, if known.
	Limit func() (uint64, bool)
	// RunPID finds the `tart run` of a reattached machine by VM name.
	RunPID func(vmName string) (int, error)
}

func defaultFileCheck() FileCheck {
	return FileCheck{Interval: defaultFileInterval, Count: openfiles.Count, Limit: openfiles.Inherited, RunPID: tart.RunPID}
}

// WithFileCheck replaces how machines' open files are counted (issue #186).
func WithFileCheck(fc FileCheck) Option {
	return func(m *Manager) {
		def := defaultFileCheck()
		if fc.Count == nil {
			fc.Count = def.Count
		}
		if fc.Limit == nil {
			fc.Limit = def.Limit
		}
		if fc.RunPID == nil {
			fc.RunPID = def.RunPID
		}
		m.fileCheck = fc
	}
}

// watchFiles counts the machine's `tart run` files every interval until the machine leaves
// the map, is rebooted (boot gen is no longer current: the reboot starts a new watch for the
// new `tart run`) or its process exits. A count that fails is logged at debug and skipped.
func (m *Manager) watchFiles(mc *Machine, gen int) {
	fc := m.fileCheck
	if fc.Interval <= 0 {
		return
	}
	tick := time.NewTicker(fc.Interval)
	defer tick.Stop()
	for range tick.C {
		m.mu.Lock()
		current := m.liveLocked(mc) && mc.gen == gen
		proc := mc.proc
		m.mu.Unlock()
		if !current || (proc != nil && proc.Exited()) {
			return
		}
		m.countFiles(mc, gen, proc)
	}
}

// countFiles takes one count of proc (boot gen's `tart run`; nil for a reattached machine,
// found by its VM lock instead) and stores it on the machine, logging a warning the first
// time the machine is near its limit. A count that finishes after a reboot is dropped: it
// describes the old process.
func (m *Manager) countFiles(mc *Machine, gen int, proc *tart.Process) {
	fc := m.fileCheck
	var pid int
	var limit uint64
	if proc != nil {
		pid = proc.Pid()
		if l, ok := fc.Limit(); ok {
			limit = l
		}
	} else {
		var err error
		if pid, err = fc.RunPID(mc.Name); err != nil {
			m.Log.Debug("cannot find a reattached machine's tart run to count its files", "runId", mc.RunID, "err", err)
			return
		}
	}
	open, err := fc.Count(context.Background(), pid)
	if err != nil {
		m.Log.Debug("cannot count tart run's open files", "runId", mc.RunID, "pid", pid, "err", err)
		return
	}
	use := newFileUse(pid, open, limit, time.Now().UTC())
	m.mu.Lock()
	if !m.liveLocked(mc) || mc.gen != gen {
		m.mu.Unlock()
		return
	}
	mc.files = use
	warn := use.Warning != "" && !mc.filesWarned
	if warn {
		mc.filesWarned = true
	}
	m.mu.Unlock()
	if warn {
		m.Log.Warn("tart run is near its open file limit; the machine may die (issue #186)",
			"runId", mc.RunID, "pid", pid, "open", open, "limit", limit)
	}
}

// newFileUse is a count with its warning, if it is near the limit (or near assumedLimit when
// the limit is unknown).
func newFileUse(pid, open int, limit uint64, at time.Time) *FileUse {
	u := &FileUse{PID: pid, Open: open, Limit: limit, CheckedAt: at}
	against := limit
	if against == 0 {
		against = assumedLimit
	}
	if uint64(open)*100 < against*nearLimit {
		return u
	}
	if limit == 0 {
		u.Warning = fmt.Sprintf("tart run has %d files open and its limit is unknown (an earlier daemon started it, "+
			"likely with %d); pull what you need, the machine may die (#186)", open, assumedLimit)
	} else {
		u.Warning = fmt.Sprintf("tart run has %d of %d files open; pull what you need, the machine may die (#186)", open, limit)
	}
	return u
}
