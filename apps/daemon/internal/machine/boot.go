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
	timings := map[string]any{}
	got, err := m.bootGuest(boot, mc, timings)
	ip := got.ip

	m.mu.Lock()
	if !m.liveLocked(mc) {
		m.mu.Unlock()
		close(mc.ready)
		return
	}
	if err != nil {
		mc.Status, mc.Error = Failed, err.Error()
		mc.vmDeleted = true // cleanupVM below; Destroy waits for this boot, so it cannot miss it
	} else {
		mc.Status, mc.IP, mc.BootSeconds = Ready, ip, round1(time.Since(started))
		mc.Toolchain, mc.Desktop = got.toolchain, got.desktop
	}
	gen := mc.gen
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
	m.watchProcess(mc, gen)
	m.startFrames(mc, gen)
}

// guestBoot is what the boot phases found out about the guest.
type guestBoot struct {
	ip        string
	toolchain map[string]any
	desktop   *DesktopReport
}

// bootGuest runs the phases that take a started VM to usable: agent, ip, key, settings,
// helper, checks, ssh. finishBoot runs them after Create, and machine_reboot after it
// starts the clone again (daemon ADR 0004). Each phase's time and every non-fatal error land
// in timings. boot bounds the whole of it.
func (m *Manager) bootGuest(boot context.Context, mc *Machine, timings map[string]any) (guestBoot, error) {
	ctx, cancel := context.WithTimeout(boot, m.readyTimeout)
	defer cancel()

	var got guestBoot
	phase := func(key string, fn func() error) error {
		at := time.Now()
		err := fn()
		timings[key] = round1(time.Since(at))
		return err
	}
	// shown publishes a phase to watchers as it starts and ends (bootphase.go).
	shown := func(name string, fn func() error) error {
		end := m.beginPhase(mc, name)
		err := fn()
		detail := ""
		if name == PhaseIP {
			detail = got.ip
		}
		end(detail, err)
		return err
	}
	err := shown(PhaseAgent, func() error { return phase("agentSeconds", func() error { return m.waitReady(ctx, mc) }) })
	if err == nil {
		err = shown(PhaseIP, func() error {
			return phase("ipSeconds", func() (err error) { got.ip, err = m.tart.IP(ctx, mc.Name); return err })
		})
	}
	if err == nil {
		err = shown(PhaseKey, func() error { return phase("keySeconds", func() error { return m.installSSHKey(ctx, mc.Name) }) })
	}
	if err == nil {
		endSettings := m.beginPhase(mc, PhaseSettings)
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
		// The guest's clock reads the host's local time, as every time greenroom prints does
		// (issue #77). Not fatal: a machine on UTC works.
		if zone := m.hostTimeZone(); zone != "" {
			timings["timeZone"] = zone
			if terr := phase("timeZoneSeconds", func() error { return setGuestTimeZone(ctx, m.tart, mc.Name, zone) }); terr != nil {
				timings["timeZoneError"] = terr.Error()
				m.Log.Warn("this machine's clock may not show the host's time zone", "runId", mc.RunID, "err", terr)
			}
		}
		endSettings("", nil) // each of them is never fatal
		// A stale image's helper is compiled here, not in the first UI call (issue #41).
		_ = shown(PhaseHelper, func() error { m.bootInputHelper(boot, mc, timings); return nil })
		endChecks := m.beginPhase(mc, PhaseChecks)
		// What the image says about its toolchain (ADR 0019) and what is on its screen
		// (ADR 0018), for machine_wait. Neither is fatal, and the desktop is only reported.
		_ = phase("toolchainSeconds", func() error {
			var terr error
			if got.toolchain, terr = readToolchain(ctx, m.tart, mc.Name); terr != nil {
				timings["toolchainError"] = terr.Error()
			}
			m.warnStaleRecipe(mc, got.toolchain, timings)
			return terr
		})
		_ = phase("desktopSeconds", func() error {
			got.desktop = m.checkDesktop(ctx, mc)
			if got.desktop.Error != "" {
				timings["desktopError"] = got.desktop.Error
			} else if !got.desktop.Clean {
				timings["desktopFindings"] = got.desktop.Findings()
			}
			return nil
		})
		endChecks("", nil)
	}
	if err == nil {
		err = shown(PhaseSSH, func() error {
			return phase("sshSeconds", func() error {
				sshCtx, sshCancel := context.WithTimeout(boot, m.readyTimeout)
				defer sshCancel()
				return m.waitSSH(sshCtx, mc, got.ip)
			})
		})
	}
	return got, err
}

// watchProcess fails a ready machine whose VM stops. This daemon's `tart run`
// stays in the foreground for the life of the VM; a reattached machine's
// belongs to an earlier daemon, so tart is polled for it instead. gen is the
// boot being watched: the stop machine_reboot makes ends an earlier boot's
// watch without a word.
func (m *Manager) watchProcess(mc *Machine, gen int) {
	m.mu.Lock()
	proc := mc.proc // a reboot replaces it
	m.mu.Unlock()
	go m.watchFiles(mc, gen)
	if proc != nil {
		go func() {
			proc.Wait()
			m.machineGone(mc, gen, proc.Err())
		}()
		return
	}
	go m.watchVM(mc, gen)
}

// watchVM polls tart until the reattached machine leaves the map, is rebooted or its VM stops.
func (m *Manager) watchVM(mc *Machine, gen int) {
	for {
		time.Sleep(m.vmPoll)
		m.mu.Lock()
		alive := m.liveLocked(mc) && mc.gen == gen
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
			m.machineGone(mc, gen, errors.New("tart no longer lists the VM as running"))
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
// already destroyed is left alone, since that stop is expected, and so is one
// rebooted since boot gen was watched: machine_reboot stops the VM on purpose and
// keeps its clone (daemon ADR 0004).
func (m *Manager) machineGone(mc *Machine, gen int, cause error) {
	m.mu.Lock()
	if !m.liveLocked(mc) || mc.gen != gen {
		m.mu.Unlock()
		return
	}
	mc.Status, mc.Error = Failed, cause.Error()
	mc.vmDeleted = true // cleanupVM below
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
// machine was destroyed or rebooted after boot gen made it ready.
func (m *Manager) startFrames(mc *Machine, gen int) {
	if m.frameInterval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if !m.liveLocked(mc) || mc.gen != gen || mc.Status != Ready {
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

// Wait blocks until the machine leaves Booting or Rebooting or timeout passes, and
// returns its current state either way.
func (m *Manager) Wait(ctx context.Context, runID string, timeout time.Duration) (*Machine, error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, err
	}
	select {
	case <-m.readyCh(mc):
	case <-time.After(timeout):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return m.snapshot(mc), nil
}

// awaitReady is what every tool that touches the guest calls first. A machine
// that is rebooting is refused at once: its reboot takes longer than any call may wait.
func (m *Manager) awaitReady(ctx context.Context, mc *Machine) error {
	m.mu.Lock()
	ready, status := mc.ready, mc.Status
	m.mu.Unlock()
	if status == Rebooting {
		return rebootingError(mc.RunID)
	}
	select {
	case <-ready:
	case <-time.After(readyGrace):
		return fmt.Errorf("machine %s is still booting; call machine_wait and try again", mc.RunID)
	case <-ctx.Done():
		return ctx.Err()
	}
	switch st := m.snapshot(mc); st.Status {
	case Ready:
		return nil
	case Rebooting:
		return rebootingError(mc.RunID)
	default:
		return fmt.Errorf("machine %s is %s: %s", mc.RunID, st.Status, st.Error)
	}
}

// readyCh is the channel the machine's current boot closes when it leaves Booting or
// Rebooting. A reboot replaces it, so it is read under m.mu.
func (m *Manager) readyCh(mc *Machine) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mc.ready
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
