package machine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// newBoot gives mc the handles Destroy uses to stop its boot, and returns the
// context finishBoot must run under. Call it before mc is shared.
func newBoot(mc *Machine) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	mc.bootCancel, mc.bootDone = cancel, make(chan struct{})
	return ctx
}

// finishBoot takes the machine to Ready or Failed: guest agent answers, tart
// reports an IP, the ssh key goes in, guest sshd accepts. The two waiting
// phases get separate readyTimeout budgets so a slow agent cannot starve ssh.
// If Destroy takes the machine first, it records nothing and cleans nothing.
func (m *Manager) finishBoot(boot context.Context, mc *Machine, started time.Time) {
	defer close(mc.bootDone)
	defer mc.bootCancel()
	ctx, cancel := context.WithTimeout(boot, m.readyTimeout)
	defer cancel()

	var ip string
	timings := map[string]any{}
	phase := func(key string, fn func() error) error {
		at := time.Now()
		err := fn()
		timings[key] = round1(time.Since(at))
		return err
	}
	err := phase("agentSeconds", func() error { return m.waitReady(ctx, mc) })
	if err == nil {
		err = phase("ipSeconds", func() (err error) { ip, err = m.tart.IP(ctx, mc.Name); return err })
	}
	if err == nil {
		err = phase("keySeconds", func() error { return m.installSSHKey(ctx, mc.Name) })
	}
	if err == nil {
		// Before ready, so before the frame recorder's first capture. Not fatal:
		// the machine works with the alert up, it only covers the screen.
		if aerr := phase("captureAlertSeconds", func() error { return approveScreenCapture(ctx, m.tart, mc.Name) }); aerr != nil {
			timings["captureAlertError"] = aerr.Error()
			m.Log.Warn("the screen-capture alert may cover this machine's screen", "runId", mc.RunID, "err", aerr)
		} else {
			mc.markCaptureApproved()
		}
		// Not fatal either: a machine that still hides windows on a wallpaper click works.
		if perr := phase("desktopPrefsSeconds", func() error { return applyDesktopPrefs(ctx, m.tart, mc.Name) }); perr != nil {
			timings["desktopPrefsError"] = perr.Error()
			m.Log.Warn("a click on this machine's wallpaper may hide its windows", "runId", mc.RunID, "err", perr)
		}
		// A stale image's helper is compiled here, not in the first UI call (issue #41).
		m.bootInputHelper(boot, mc, timings)
	}
	if err == nil {
		err = phase("sshSeconds", func() error {
			sshCtx, sshCancel := context.WithTimeout(boot, m.readyTimeout)
			defer sshCancel()
			return m.waitSSH(sshCtx, mc, ip)
		})
	}

	m.mu.Lock()
	if !m.liveLocked(mc) {
		m.mu.Unlock()
		close(mc.ready)
		return
	}
	if err != nil {
		mc.Status, mc.Error = Failed, err.Error()
	} else {
		mc.Status, mc.IP, mc.BootSeconds = Ready, ip, round1(time.Since(started))
	}
	m.mu.Unlock()
	m.persist()

	// The boot step is on disk before ready closes, so a caller returning from
	// Wait reads a record that agrees with it.
	if uerr := mc.rec.update(func(man *Manifest) { man.IP = ip }); uerr != nil {
		m.Log.Warn("cannot write the run manifest", "runId", mc.RunID, "err", uerr)
	}
	timings["status"], timings["ip"], timings["bootSeconds"] = mc.Status, ip, mc.BootSeconds
	seq := mc.rec.step("machine_boot", nil, timings, err, started)
	close(mc.ready)
	m.emitStep(mc.RunID, seq)
	if err != nil {
		m.Log.Warn("machine failed to boot", "runId", mc.RunID, "err", err)
		m.cleanupVM(mc.Name)
		mc.rec.markEnded()
		m.emit(LifecycleEvent{Kind: "failed", RunID: mc.RunID, Machine: m.snapshot(mc)})
		return
	}
	m.Log.Info("machine ready", "runId", mc.RunID, "ip", ip, "bootSeconds", mc.BootSeconds,
		"agentSeconds", timings["agentSeconds"], "sshSeconds", timings["sshSeconds"])
	m.emit(LifecycleEvent{Kind: "ready", RunID: mc.RunID, Machine: m.snapshot(mc)})
	m.watchProcess(mc)
	m.startFrames(mc)
}

// watchProcess fails a ready machine whose VM stops. This daemon's `tart run`
// stays in the foreground for the life of the VM; a reattached machine's
// belongs to an earlier daemon, so tart is polled for it instead.
func (m *Manager) watchProcess(mc *Machine) {
	if mc.proc != nil {
		go func() {
			mc.proc.Wait()
			m.machineGone(mc, mc.proc.Err())
		}()
		return
	}
	go m.watchVM(mc)
}

// watchVM polls tart until the reattached machine leaves the map or its VM stops.
func (m *Manager) watchVM(mc *Machine) {
	for {
		time.Sleep(m.vmPoll)
		m.mu.Lock()
		alive := m.liveLocked(mc)
		m.mu.Unlock()
		if !alive {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		running, err := m.runningVMs(ctx)
		cancel()
		if err != nil {
			m.Log.Debug("cannot list VMs to watch a reattached machine", "runId", mc.RunID, "err", err)
			continue
		}
		if !running[mc.Name] {
			m.machineGone(mc, errors.New("tart no longer lists the VM as running"))
			return
		}
	}
}

// runningVMs returns the names of the VMs tart reports as running.
func (m *Manager) runningVMs(ctx context.Context) (map[string]bool, error) {
	vms, err := m.tart.List(ctx)
	if err != nil {
		return nil, err
	}
	running := map[string]bool{}
	for _, vm := range vms {
		if vm.State == "running" {
			running[vm.Name] = true
		}
	}
	return running, nil
}

// machineGone ends the run of a machine whose VM stopped on its own. A machine
// already destroyed is left alone, since that stop is expected.
func (m *Manager) machineGone(mc *Machine, cause error) {
	m.mu.Lock()
	if !m.liveLocked(mc) {
		m.mu.Unlock()
		return
	}
	mc.Status, mc.Error = Failed, cause.Error()
	live := m.forgetLocked(mc)
	m.mu.Unlock()
	closeSessions(live)
	m.persist()

	m.Log.Warn("machine stopped on its own", "runId", mc.RunID, "err", cause)
	m.cleanupVM(mc.Name)
	mc.rec.markEnded()
	m.emit(LifecycleEvent{Kind: "stopped", RunID: mc.RunID, Machine: m.snapshot(mc)})
}

// startFrames starts the frame recorder unless recording is off or the
// machine was destroyed after it became ready.
func (m *Manager) startFrames(mc *Machine) {
	if m.frameInterval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if !m.liveLocked(mc) {
		m.mu.Unlock()
		cancel()
		return
	}
	done := make(chan struct{})
	mc.frameCancel, mc.frameDone = cancel, done
	m.mu.Unlock()
	go func() {
		defer close(done)
		m.recordFrames(ctx, mc)
	}()
}

// Wait blocks until the machine leaves Booting or timeout passes, and
// returns its current state either way.
func (m *Manager) Wait(ctx context.Context, runID string, timeout time.Duration) (*Machine, error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, err
	}
	select {
	case <-mc.ready:
	case <-time.After(timeout):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return m.snapshot(mc), nil
}

// awaitReady is what every tool that touches the guest calls first.
func (m *Manager) awaitReady(ctx context.Context, mc *Machine) error {
	select {
	case <-mc.ready:
	case <-time.After(readyGrace):
		return fmt.Errorf("machine %s is still booting; call machine_wait and try again", mc.RunID)
	case <-ctx.Done():
		return ctx.Err()
	}
	if st := m.snapshot(mc); st.Status != Ready {
		return fmt.Errorf("machine %s is %s: %s", mc.RunID, st.Status, st.Error)
	}
	return nil
}

// waitReady polls the guest agent over vsock until it answers.
func (m *Manager) waitReady(ctx context.Context, mc *Machine) error {
	return m.poll(ctx, mc, "guest agent", func() error {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := execChecked(c, m.tart, mc.Name, "true")
		return err
	})
}

// waitSSH polls until guest sshd accepts. The agent comes up before sshd, so
// without this the first rsync after ready can fail.
func (m *Manager) waitSSH(ctx context.Context, mc *Machine, ip string) error {
	addr := net.JoinHostPort(ip, "22")
	return m.poll(ctx, mc, "ssh on "+addr, func() error { return m.sshProbe(ctx, mc.Name, addr) })
}

// poll retries probe every second until it succeeds, the tart process exits,
// or ctx ends. A timeout carries the last probe error so it names its cause.
func (m *Manager) poll(ctx context.Context, mc *Machine, what string, probe func() error) error {
	exited := func() bool { return mc.proc != nil && mc.proc.Exited() }
	var last error
	for {
		if exited() {
			return mc.proc.Err()
		}
		err := probe()
		if err == nil {
			if exited() { // the answer is stale
				return mc.proc.Err()
			}
			return nil
		}
		if ctx.Err() != nil {
			msg := fmt.Sprintf("machine %s: %s did not answer within %s", mc.Name, what, m.readyTimeout)
			if last != nil {
				msg += fmt.Sprintf(" (last error: %v)", last)
			}
			return fmt.Errorf("%s: %w", msg, ctx.Err())
		}
		last = err
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

// probeSSHInGuest asks the guest, over vsock, whether sshd is listening. The
// daemon must never dial a guest itself (local-network invariant, CLAUDE.md);
// addr is for the error message only.
func (m *Manager) probeSSHInGuest(ctx context.Context, vmName, addr string) error {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := execChecked(c, m.tart, vmName, "/usr/bin/nc", "-z", "127.0.0.1", "22"); err != nil {
		return fmt.Errorf("nc -z 127.0.0.1 22 in guest (for %s): %w", addr, err)
	}
	return nil
}

func (m *Manager) installSSHKey(ctx context.Context, name string) error {
	if _, err := execChecked(ctx, m.tart, name, "sh", "-c", appendAuthorizedKeyScript(m.pubKey)); err != nil {
		return fmt.Errorf("install ssh key: %w", err)
	}
	return nil
}

// appendAuthorizedKeyScript idempotently appends pubKey to authorized_keys.
// Boot and PrepareGuest both run it.
func appendAuthorizedKeyScript(pubKey string) string {
	return fmt.Sprintf(
		"mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && (grep -qF %s ~/.ssh/authorized_keys || echo %s >> ~/.ssh/authorized_keys)",
		shellQuote(pubKey), shellQuote(pubKey))
}

// execChecked runs args in the guest and turns a non-zero exit into an error.
func execChecked(ctx context.Context, c *tart.Client, vm string, args ...string) (tart.ExecResult, error) {
	res, err := c.Exec(ctx, vm, args...)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res, nil
}
