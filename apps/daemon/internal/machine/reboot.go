package machine

// machine_reboot (issue #187, daemon ADR 0004): a guest whose screen stopped answering
// (WindowServer hung, the guest agent wedged) is restarted on the same clone. The disk
// persists, so the run, its record and everything in the guest home stay; what lived only
// in the running guest or in this daemon's memory of it ends.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

const (
	// defaultRebootTimeout bounds a whole reboot: stop, start and every boot phase. A guest
	// that is not back by then leaves the machine failed with its disk kept.
	defaultRebootTimeout = 5 * time.Minute
	// rebootStopGrace is how long `tart stop` lets `tart run` end on its own before it
	// kills it.
	rebootStopGrace = 20 * time.Second
	// rebootSyncWait bounds the guest `sync` before the stop. The guest agent answers even
	// with WindowServer wedged; a wedged agent costs the reboot this long at most.
	rebootSyncWait = 15 * time.Second
	// rebootExitWait is how long the VM may take to be gone after the stop, and again
	// after the daemon kills its `tart run`.
	rebootExitWait = 30 * time.Second
)

// PhaseStop is the reboot's first boot phase: the VM is stopped, its disk kept.
// machine_reboot then publishes start and the phases of bootGuest.
const PhaseStop = "stop"

// ErrRebooting is what a call on a machine that is rebooting gets, at once: a reboot
// takes longer than any call may wait (daemon ADR 0004).
var ErrRebooting = errors.New("the machine is rebooting")

// errExecRebooted ends a machine_exec command still running when its machine reboots.
var errExecRebooted = errors.New("the command was ended because its machine rebooted (machine_reboot); " +
	"the guest process is gone: run it again once machine_wait says ready")

func rebootingError(runID string) error {
	return fmt.Errorf("%w (machine_reboot): call machine_wait on run %s until it is ready, then try again", ErrRebooting, runID)
}

// WithRebootTimeout bounds a whole machine_reboot (default 5 minutes).
func WithRebootTimeout(d time.Duration) Option {
	return func(m *Manager) { m.rebootTimeout = d }
}

// rebootRun is what Reboot hands its goroutine.
type rebootRun struct {
	seq     int // the machine_reboot step, claimed at the start
	started time.Time
	gen     int           // the boot this reboot starts
	proc    *tart.Process // the tart run to stop; nil for a reattached machine
	frames  chan struct{} // closed when the old frame recorder has returned; nil if none ran
	ready   chan struct{} // closed when the reboot ends, either way
	cancel  context.CancelFunc
	done    chan struct{} // closed when the goroutine returns (Destroy waits for it)
}

// Reboot restarts runID's VM on the same clone and runs every boot phase again (daemon ADR
// 0004). It returns at once with the machine rebooting and the step the reboot records when it
// ends; Wait (machine_wait) blocks until it is ready again, or failed with its disk kept.
//
// Kept: the disk (the guest home, ~/work and synced files, the input helper, authorized_keys,
// TCC and capture-approval rows), the run, its record and its conversation. Lost: pty sessions,
// running machine_exec commands (they end with an error saying so), background apps, /tmp, the
// live screen, the control lease and the UI trees read before it.
func (m *Manager) Reboot(ctx context.Context, runID string) (*Machine, int, error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, 0, err
	}
	started := time.Now()
	m.mu.Lock()
	if err := m.rebootRefusalLocked(mc); err != nil {
		m.mu.Unlock()
		return nil, 0, err
	}
	mc.Status, mc.Error, mc.IP = Rebooting, "", "" // the address may change
	mc.gen++                                       // every watcher and recorder of the old boot stands down
	mc.boot = nil                                  // this reboot's phases replace the first boot's
	mc.files, mc.filesWarned = nil, false          // they describe the old tart run; the new boot's watch counts again
	mc.ready = make(chan struct{})
	boot := newBoot(mc)
	r := rebootRun{started: started, gen: mc.gen, proc: mc.proc, frames: mc.frameDone,
		ready: mc.ready, cancel: mc.bootCancel, done: mc.bootDone}
	live := m.releaseLocked(mc, errors.New("the live screen ended: the machine is rebooting (machine_reboot); reconnect once it is ready"))
	for _, j := range mc.execs {
		j.abort(errExecRebooted) // a finished job ignores it; the results stay for machine_exec_wait
	}
	m.mu.Unlock()
	closeSessions(live)
	mc.input.forget()
	m.persist()

	r.seq = mc.rec.begin()
	snap := m.snapshot(mc)
	m.Log.Info("rebooting the machine", "runId", runID, "step", r.seq)
	m.emit(LifecycleEvent{Kind: "rebooting", RunID: runID, Machine: snap})
	go m.reboot(boot, mc, r)
	return snap, r.seq, nil
}

// rebootRefusalLocked says why mc cannot be rebooted now, or nil. The caller holds m.mu.
func (m *Manager) rebootRefusalLocked(mc *Machine) error {
	switch {
	case !m.liveLocked(mc):
		return fmt.Errorf("machine %s is being destroyed", mc.RunID)
	case mc.Status == Rebooting:
		return fmt.Errorf("%w already (machine_reboot): call machine_wait on run %s until it is ready; "+
			"a second reboot would not help", ErrRebooting, mc.RunID)
	case mc.Status == Booting:
		return fmt.Errorf("machine %s is still booting; call machine_wait until it is ready before rebooting it", mc.RunID)
	case mc.Status == Failed && mc.vmDeleted:
		return fmt.Errorf("machine %s failed and its VM is deleted, so there is nothing to reboot: "+
			"call machine_destroy, then machine_create", mc.RunID)
	}
	return nil // ready, or failed with its disk kept (a failed reboot): reboot it
}

// reboot is the reboot's goroutine: stop the VM, start the clone again, run every boot
// phase, then ready or failed. Destroy cancels boot and waits for it; it then records
// nothing and leaves the VM to Destroy.
func (m *Manager) reboot(boot context.Context, mc *Machine, r rebootRun) {
	defer close(r.done)
	defer r.cancel()
	ctx, cancel := context.WithTimeout(boot, m.rebootTimeout)
	defer cancel()

	timings := map[string]any{}
	if r.frames != nil {
		<-r.frames // no frame of the old boot lands after the stop
	}
	err := m.stopForReboot(ctx, mc, r.proc, timings)
	if err == nil {
		err = m.startForReboot(mc, timings)
	}
	var got guestBoot
	if err == nil {
		got, err = m.bootGuest(ctx, mc, timings)
	}
	if err != nil && boot.Err() == nil && ctx.Err() != nil {
		err = fmt.Errorf("the machine did not come back within %s: %w", m.rebootTimeout, err)
	}

	if err != nil && boot.Err() == nil {
		// The disk stays. The VM stops, so a guest that did not come back holds no host
		// slot; machine_reboot starts it again, machine_destroy deletes it.
		m.haltAfterFailedReboot(mc)
	}
	mc.input.forget() // a call that raced the reboot may have cached the old guest
	m.mu.Lock()
	if !m.liveLocked(mc) {
		m.mu.Unlock()
		close(r.ready)
		return
	}
	if err != nil {
		mc.Status, mc.Error = Failed, "reboot failed: "+err.Error()+
			"; the disk is kept: call machine_reboot to try again, or machine_destroy"
	} else {
		mc.Status, mc.Error, mc.IP = Ready, "", got.ip
		mc.Toolchain, mc.Desktop = got.toolchain, got.desktop
	}
	m.mu.Unlock()
	m.persist()

	if uerr := mc.rec.update(func(man *Manifest) { man.IP = got.ip }); uerr != nil {
		m.Log.Warn("cannot write the run manifest", "runId", mc.RunID, "err", uerr)
	}
	timings["status"], timings["ip"] = Ready, got.ip
	if err != nil {
		timings["status"] = Failed
	}
	timings["rebootSeconds"] = round1(time.Since(r.started))
	mc.rec.complete(r.seq, "machine_reboot", nil, timings, err, r.started)
	close(r.ready)
	m.emitStep(mc.RunID, r.seq)
	if err != nil {
		m.Log.Warn("machine failed to reboot; its disk is kept", "runId", mc.RunID, "err", err)
		m.emit(LifecycleEvent{Kind: "failed", RunID: mc.RunID, Machine: m.snapshot(mc), Reboot: true})
		return
	}
	m.Log.Info("machine rebooted", "runId", mc.RunID, "ip", got.ip, "rebootSeconds", timings["rebootSeconds"])
	m.emit(LifecycleEvent{Kind: "ready", RunID: mc.RunID, Machine: m.snapshot(mc), Reboot: true})
	m.watchProcess(mc, r.gen)
	m.startFrames(mc, r.gen)
}

// stopForReboot stops the VM and waits until it is gone, keeping its disk: `tart stop` with
// a grace for a clean shutdown, then, for this daemon's own `tart run`, a kill of it. A
// reattached machine's `tart run` belongs to an earlier daemon, so tart is asked instead.
func (m *Manager) stopForReboot(ctx context.Context, mc *Machine, proc *tart.Process, timings map[string]any) error {
	end := m.beginPhase(mc, PhaseStop)
	at := time.Now()
	m.syncGuest(ctx, mc)
	timings["syncSeconds"] = round1(time.Since(at))
	at = time.Now()
	err := m.stopVM(ctx, mc.Name, proc)
	timings["stopSeconds"] = round1(time.Since(at))
	end(mc.Name, err)
	return err
}

// syncGuest flushes the guest's disk before the stop. `tart stop` is no guest shutdown: it
// ends `tart run`, which powers the VM off at once, so a file written seconds before the
// reboot was lost (stress test of #187). The agent's `sync` makes the disk hold what the guest
// wrote. A failure is logged and the reboot goes on: a guest whose agent is wedged needs it most.
func (m *Manager) syncGuest(ctx context.Context, mc *Machine) {
	syncCtx, cancel := context.WithTimeout(ctx, rebootSyncWait)
	defer cancel()
	res, err := m.tart.Exec(syncCtx, mc.Name, "/bin/sync")
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("sync exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	if err != nil {
		m.Log.Warn("cannot flush the guest's disk before the reboot; what it wrote in its last seconds may be lost",
			"runId", mc.RunID, "err", err)
	}
}

func (m *Manager) stopVM(ctx context.Context, name string, proc *tart.Process) error {
	stopCtx, cancel := context.WithTimeout(ctx, rebootStopGrace+stopTimeout)
	stopErr := m.tart.StopWithin(stopCtx, name, rebootStopGrace)
	cancel()
	if proc != nil {
		if exited(ctx, proc, rebootExitWait) {
			return nil
		}
		// tart stop failed or hung: ending tart run ends the VM it hosts.
		m.Log.Warn("tart stop did not end the VM; killing its tart run", "name", name, "err", stopErr)
		_ = proc.Kill()
		if exited(ctx, proc, rebootExitWait) {
			return nil
		}
		return fmt.Errorf("the VM did not stop (tart stop: %v)", stopErr)
	}
	deadline := time.Now().Add(rebootExitWait)
	for {
		listCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		running, err := m.runningVMs(listCtx)
		cancel()
		if err == nil && !running[name] {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			if err == nil {
				err = errors.New("tart still lists it as running")
			}
			return fmt.Errorf("the VM did not stop (tart stop: %v; %v)", stopErr, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

// exited waits up to d for proc to exit.
func exited(ctx context.Context, proc *tart.Process, d time.Duration) bool {
	done := make(chan struct{})
	go func() { proc.Wait(); close(done) }()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
	case <-ctx.Done():
	}
	return proc.Exited()
}

// startForReboot boots the same clone again with a new `tart run`, which this daemon now
// owns: a reattached machine is watched through its process from here on.
func (m *Manager) startForReboot(mc *Machine, timings map[string]any) error {
	end := m.beginPhase(mc, PhaseStart)
	at := time.Now()
	proc, err := m.tart.Start(mc.Name, filepath.Join(mc.Dir, "vm.log"))
	timings["startSeconds"] = round1(time.Since(at))
	end(mc.Name, err)
	if err != nil {
		return err
	}
	m.mu.Lock()
	mc.proc = proc
	m.mu.Unlock()
	return nil
}

// haltAfterFailedReboot stops a VM whose reboot failed, keeping its disk. A failure is only
// logged: the machine is failed either way, and machine_destroy stops it again.
func (m *Manager) haltAfterFailedReboot(mc *Machine) {
	m.mu.Lock()
	proc := mc.proc
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), rebootStopGrace+2*rebootExitWait+stopTimeout)
	defer cancel()
	if err := m.stopVM(ctx, mc.Name, proc); err != nil {
		m.Log.Warn("cannot stop the VM of a failed reboot; machine_destroy stops and deletes it", "runId", mc.RunID, "err", err)
	}
}

// forget drops what the daemon remembers of the running guest, which a reboot makes wrong:
// the helper's screen size (the next UI call checks the helper again), every reader's UI tree
// and its looks. A look taken before the reboot is older than the screen; element ids from it
// aim at nothing. The capture approval's state stays: the boot phases write it again.
func (st *inputState) forget() {
	st.screen.Store(nil)
	st.uiMu.Lock()
	st.ui, st.looks = nil, nil
	st.uiMu.Unlock()
}
