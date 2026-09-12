// Package machine owns the lifecycle of machines (VMs) and records runs.
package machine

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

const (
	namePrefix   = "greenroom-"
	guestUser    = "admin"
	readyTimeout = 3 * time.Minute
	readyGrace   = 20 * time.Second // how long guest tools wait for a booting machine
	stopTimeout  = 30 * time.Second
)

// Machine is a running VM bound to one run.
type Machine struct {
	RunID       string    `json:"runId"`
	Name        string    `json:"name"`
	Image       string    `json:"image"`
	IP          string    `json:"ip,omitempty"`
	Status      Status    `json:"status"`
	Error       string    `json:"error,omitempty"`
	BootSeconds float64   `json:"bootSeconds,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	Dir         string    `json:"dir"`

	rec   *recorder
	ready chan struct{} // closed once Status leaves Booting
}

// Status is a machine's lifecycle state.
type Status string

const (
	Booting Status = "booting"
	Ready   Status = "ready"
	Failed  Status = "failed"
)

// snapshot returns a copy safe to hand to callers and encoders.
func (m *Manager) snapshot(mc *Machine) *Machine {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *mc
	c.rec, c.ready = nil, nil
	return &c
}

// Manager creates, drives and destroys machines. State lives under Root.
type Manager struct {
	Root string
	Log  *slog.Logger

	tart     *tart.Client
	mu       sync.Mutex
	machines map[string]*Machine
	sshKey   string
	pubKey   string
}

// NewManager prepares Root (dirs, ssh key) and reloads machines that are
// still running from a previous daemon process.
func NewManager(root string, log *slog.Logger) (*Manager, error) {
	m := &Manager{Root: root, Log: log, tart: tart.New(), machines: map[string]*Machine{}}
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o755); err != nil {
		return nil, err
	}
	if err := m.ensureSSHKey(); err != nil {
		return nil, err
	}
	if err := m.loadState(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) ensureSSHKey() error {
	m.sshKey = filepath.Join(m.Root, "id_ed25519")
	if _, err := os.Stat(m.sshKey); errors.Is(err, os.ErrNotExist) {
		cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "greenroom", "-f", m.sshKey)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ssh-keygen: %w: %s", err, out)
		}
	}
	pub, err := os.ReadFile(m.sshKey + ".pub")
	if err != nil {
		return err
	}
	m.pubKey = strings.TrimSpace(string(pub))
	return nil
}

func (m *Manager) statePath() string { return filepath.Join(m.Root, "state.json") }

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
			continue
		}
		var err error
		mc.rec, err = newRecorder(mc.Dir, Manifest{RunID: mc.RunID, Image: mc.Image, MachineName: mc.Name, IP: mc.IP, CreatedAt: mc.CreatedAt})
		if err != nil {
			return err
		}
		mc.ready = make(chan struct{})
		m.machines[mc.RunID] = mc
		if mc.Status == Ready {
			close(mc.ready)
		} else {
			go m.finishBoot(mc, mc.CreatedAt)
		}
	}
	return m.saveStateLocked()
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

func newRunID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// List returns the live machines.
func (m *Manager) List() []*Machine {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Machine, 0, len(m.machines))
	for _, mc := range m.machines {
		c := *mc
		c.rec, c.ready = nil, nil
		out = append(out, &c)
	}
	return out
}

func (m *Manager) get(runID string) (*Machine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mc, ok := m.machines[runID]
	if !ok {
		return nil, fmt.Errorf("no machine for run %q", runID)
	}
	return mc, nil
}

// Create clones image and starts it, then returns at once with the machine
// in Booting state. Readiness (guest agent up, IP known, ssh key installed)
// is tracked in the background; use Wait to block for it.
func (m *Manager) Create(ctx context.Context, image string) (*Machine, error) {
	started := time.Now()
	runID := newRunID()
	name := namePrefix + runID
	dir := filepath.Join(m.Root, "runs", runID)

	rec, err := newRecorder(dir, Manifest{RunID: runID, Image: image, MachineName: name, CreatedAt: started.UTC()})
	if err != nil {
		return nil, err
	}
	mc := &Machine{RunID: runID, Name: name, Image: image, Status: Booting, CreatedAt: started.UTC(), Dir: dir, rec: rec, ready: make(chan struct{})}

	if err := m.tart.Clone(ctx, image, name); err != nil {
		rec.step("machine_create", map[string]any{"image": image}, nil, err, started)
		return nil, err
	}
	if _, err := m.tart.Start(name, filepath.Join(dir, "vm.log")); err != nil {
		m.cleanupVM(name)
		rec.step("machine_create", map[string]any{"image": image}, nil, err, started)
		return nil, err
	}

	m.mu.Lock()
	m.machines[runID] = mc
	err = m.saveStateLocked()
	m.mu.Unlock()
	if err != nil {
		m.cleanupVM(name)
		return nil, err
	}
	rec.step("machine_create", map[string]any{"image": image}, map[string]any{"runId": runID, "machineName": name, "status": Booting}, nil, started)
	go m.finishBoot(mc, started)
	return m.snapshot(mc), nil
}

// finishBoot waits for the guest agent, records the IP, installs the ssh key
// and flips the machine to Ready or Failed.
func (m *Manager) finishBoot(mc *Machine, started time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	err := m.waitReady(ctx, mc.Name)
	var ip string
	if err == nil {
		ip, err = m.tart.IP(ctx, mc.Name)
	}
	if err == nil {
		err = m.installSSHKey(ctx, mc.Name)
	}

	m.mu.Lock()
	if err != nil {
		mc.Status, mc.Error = Failed, err.Error()
	} else {
		mc.Status, mc.IP, mc.BootSeconds = Ready, ip, math.Round(time.Since(started).Seconds()*10)/10
	}
	_ = m.saveStateLocked()
	close(mc.ready)
	m.mu.Unlock()

	_ = mc.rec.update(func(man *Manifest) { man.IP = ip })
	mc.rec.step("machine_boot", nil, map[string]any{"status": mc.Status, "ip": ip, "bootSeconds": mc.BootSeconds}, err, started)
	if err != nil {
		m.Log.Warn("machine failed to boot", "runId", mc.RunID, "err", err)
		m.cleanupVM(mc.Name)
		return
	}
	m.Log.Info("machine ready", "runId", mc.RunID, "ip", ip, "bootSeconds", mc.BootSeconds)
}

func (m *Manager) cleanupVM(name string) {
	c, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	_ = m.tart.Stop(c, name)
	_ = m.tart.Delete(c, name)
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

func (m *Manager) waitReady(ctx context.Context, name string) error {
	deadline := time.Now().Add(readyTimeout)
	for {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := m.tart.Exec(c, name, "true")
		cancel()
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("machine %s did not become ready within %s", name, readyTimeout)
		}
		time.Sleep(time.Second)
	}
}

func (m *Manager) installSSHKey(ctx context.Context, name string) error {
	script := fmt.Sprintf(
		"mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && (grep -qF %s ~/.ssh/authorized_keys || echo %s >> ~/.ssh/authorized_keys)",
		shellQuote(m.pubKey), shellQuote(m.pubKey))
	res, err := m.tart.Exec(ctx, name, "sh", "-c", script)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("install ssh key: exit %d: %s", res.ExitCode, res.Stderr)
	}
	return nil
}

// ExecResult is a command's outcome plus timing.
type ExecResult struct {
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	ExitCode int     `json:"exitCode"`
	Seconds  float64 `json:"seconds"`
}

// Exec runs command in the guest's login shell, optionally in cwd.
func (m *Manager) Exec(ctx context.Context, runID, command, cwd string, timeout time.Duration) (ExecResult, error) {
	mc, err := m.get(runID)
	if err != nil {
		return ExecResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return ExecResult{}, err
	}
	started := time.Now()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	script := command
	if cwd != "" {
		script = "cd " + shellQuote(cwd) + " && " + command
	}
	res, err := m.tart.Exec(ctx, mc.Name, "/bin/zsh", "-lc", script)
	out := ExecResult{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode, Seconds: time.Since(started).Seconds()}
	mc.rec.step("machine_exec", map[string]any{"command": command, "cwd": cwd}, truncatedForLog(out), err, started)
	return out, err
}

func truncatedForLog(r ExecResult) ExecResult {
	const max = 64 * 1024
	if len(r.Stdout) > max {
		r.Stdout = r.Stdout[:max] + "\n...[truncated]"
	}
	if len(r.Stderr) > max {
		r.Stderr = r.Stderr[:max] + "\n...[truncated]"
	}
	return r
}

// Screenshot captures the guest display as PNG, stores it in the run
// directory, and returns the bytes and the stored path.
func (m *Manager) Screenshot(ctx context.Context, runID string) (png []byte, path string, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, "", err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return nil, "", err
	}
	started := time.Now()
	defer func() {
		mc.rec.step("machine_screenshot", nil, map[string]any{"path": path, "bytes": len(png)}, err, started)
	}()
	res, err := m.tart.Exec(ctx, mc.Name, "sh", "-c", "screencapture -x /tmp/greenroom-shot.png && base64 -i /tmp/greenroom-shot.png")
	if err != nil {
		return nil, "", err
	}
	if res.ExitCode != 0 {
		return nil, "", fmt.Errorf("screencapture failed: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	png, err = base64.StdEncoding.DecodeString(strings.TrimSpace(res.Stdout))
	if err != nil {
		return nil, "", fmt.Errorf("decode screenshot: %w", err)
	}
	seq := mc.rec.manifest.Steps + 1
	path = mc.rec.artifactPath(seq, "screenshot", "png")
	if err = os.WriteFile(path, png, 0o644); err != nil {
		return nil, "", err
	}
	return png, path, nil
}

// SyncResult reports what rsync did.
type SyncResult struct {
	Dest    string  `json:"dest"`
	Summary string  `json:"summary"`
	Seconds float64 `json:"seconds"`
}

// Sync copies a host directory into the guest with rsync over ssh. dest
// defaults to ~/work/<basename of source> in the guest.
func (m *Manager) Sync(ctx context.Context, runID, source, dest string, exclude []string) (SyncResult, error) {
	mc, err := m.get(runID)
	if err != nil {
		return SyncResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return SyncResult{}, err
	}
	started := time.Now()
	source = filepath.Clean(source)
	if st, err := os.Stat(source); err != nil || !st.IsDir() {
		return SyncResult{}, fmt.Errorf("source %q is not a directory", source)
	}
	if dest == "" {
		dest = "work/" + filepath.Base(source)
	}
	sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR", m.sshKey)
	args := []string{"-az", "--stats", "-e", sshCmd, "--rsync-path", "mkdir -p " + shellQuote(dest) + " && rsync"}
	for _, ex := range exclude {
		args = append(args, "--exclude", ex)
	}
	args = append(args, source+"/", fmt.Sprintf("%s@%s:%s/", guestUser, m.snapshot(mc).IP, dest))
	cmd := exec.CommandContext(ctx, "rsync", args...)
	out, err := cmd.CombinedOutput()
	res := SyncResult{Dest: dest, Summary: rsyncSummary(string(out)), Seconds: time.Since(started).Seconds()}
	if err != nil {
		err = fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(string(out)))
	}
	mc.rec.step("machine_sync", map[string]any{"source": source, "dest": dest, "exclude": exclude}, res, err, started)
	return res, err
}

func rsyncSummary(stats string) string {
	var keep []string
	for _, line := range strings.Split(stats, "\n") {
		if strings.HasPrefix(line, "Number of files") || strings.HasPrefix(line, "Number of regular files transferred") || strings.HasPrefix(line, "Total transferred file size") {
			keep = append(keep, strings.TrimSpace(line))
		}
	}
	return strings.Join(keep, "; ")
}

// Destroy stops and deletes the machine. The run directory is kept.
func (m *Manager) Destroy(ctx context.Context, runID string) error {
	mc, err := m.get(runID)
	if err != nil {
		return err
	}
	started := time.Now()
	m.mu.Lock()
	delete(m.machines, runID)
	err = m.saveStateLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	if err := m.tart.Stop(stopCtx, mc.Name); err != nil {
		m.Log.Warn("stop failed, deleting anyway", "runId", runID, "err", err)
	}
	err = m.tart.Delete(ctx, mc.Name)
	now := time.Now().UTC()
	_ = mc.rec.update(func(man *Manifest) { man.DestroyedAt = &now })
	mc.rec.step("machine_destroy", nil, nil, err, started)
	return err
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
