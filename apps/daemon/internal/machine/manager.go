// Package machine owns the lifecycle of machines (VMs) and records runs.
package machine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

const (
	namePrefix = "greenroom-"
	guestUser  = "admin"

	// GuestWorkDir is where a project lands, relative to the guest home. It is
	// pinned because SwiftPM caches break when moved (docs/10-build-transport.md).
	// images/scripts/firstboot.sh clones to the same path; change both.
	GuestWorkDir = "work"

	readyTimeout = 3 * time.Minute
	readyGrace   = 20 * time.Second // how long guest tools wait for a booting machine
	stopTimeout  = 30 * time.Second

	// defaultVMPollInterval is how often a reattached machine is checked for a VM that stopped.
	defaultVMPollInterval = 15 * time.Second

	// defaultMaxMachines is Apple's licence limit of two macOS guests per host.
	defaultMaxMachines = 2

	defaultFrameInterval = 2 * time.Second // docs/adr/0008-run-recording.md
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
	VNCURL      string    `json:"vncUrl,omitempty"` // set when the machine is watched

	// Control is the screen-control lease (ADR 0009). It is replaced, never
	// edited in place, so snapshots can share it.
	Control *Control `json:"control,omitempty"`

	rec   *recorder
	ready chan struct{} // closed once Status leaves Booting
	proc  *tart.Process // nil for a reattached machine
	input *inputState

	// sessions is guarded by Manager.mu and deliberately not persisted: a
	// restarted daemon cannot prove a guest process is the one an old id named.
	sessions map[string]*PTYSession

	frameCancel context.CancelFunc // guarded by Manager.mu

	// bootCancel stops finishBoot and bootDone closes when it returns. Both are
	// set before the machine is shared, and are nil when no boot runs.
	bootCancel context.CancelFunc
	bootDone   chan struct{}
	destroying bool // guarded by Manager.mu
}

// Status is a machine's lifecycle state.
type Status string

// Machine lifecycle states.
const (
	Booting Status = "booting"
	Ready   Status = "ready"
	Failed  Status = "failed"
)

// Manager creates, drives and destroys machines. State lives under Root.
type Manager struct {
	Root string
	Log  *slog.Logger

	tart          *tart.Client
	createMu      sync.Mutex // serializes Create so the capacity check cannot be raced
	stateMu       sync.Mutex // serializes state.json writes; taken before mu, never under it
	mu            sync.Mutex
	machines      map[string]*Machine
	sshKey        string
	pubKey        string
	maxMachines   int
	readyTimeout  time.Duration
	frameInterval time.Duration
	vmPoll        time.Duration
	sshProbe      func(ctx context.Context, vmName, addr string) error
	onWatch       func(vncURL string)

	listenMu  sync.Mutex
	listeners map[int]func(LifecycleEvent)
	nextSub   int
}

// LifecycleEvent is one change a listener may care about.
type LifecycleEvent struct {
	Kind    string   `json:"kind"` // created, ready, failed, stopped, destroyed, step, frame, control
	RunID   string   `json:"runId"`
	Machine *Machine `json:"machine,omitempty"`
	Step    int      `json:"step,omitempty"`
	Frame   *Frame   `json:"frame,omitempty"`
}

// Option adjusts a Manager before it touches the disk or the host.
type Option func(*Manager)

// WithTartBin drives a different tart binary. An empty path keeps the default resolution.
func WithTartBin(path string) Option {
	return func(m *Manager) {
		if path != "" {
			m.tart = tart.NewAt(path)
		}
	}
}

// WithMaxMachines sets how many VMs the host may run at once. Zero or less removes the check.
func WithMaxMachines(n int) Option {
	return func(m *Manager) { m.maxMachines = n }
}

// WithReadyTimeout sets how long each boot phase may take.
func WithReadyTimeout(d time.Duration) Option {
	return func(m *Manager) { m.readyTimeout = d }
}

// WithFrameInterval sets how often a ready machine's screen is recorded. Zero disables it.
func WithFrameInterval(d time.Duration) Option {
	return func(m *Manager) { m.frameInterval = d }
}

// WithVMPollInterval sets how often a reattached machine's VM is checked for having stopped.
func WithVMPollInterval(d time.Duration) Option {
	return func(m *Manager) { m.vmPoll = d }
}

// WithWatchHandler is called with the screen address of each watched machine.
func WithWatchHandler(fn func(vncURL string)) Option {
	return func(m *Manager) { m.onWatch = fn }
}

// WithSSHProbe replaces the check that guest sshd accepts connections.
func WithSSHProbe(probe func(ctx context.Context, vmName, addr string) error) Option {
	return func(m *Manager) { m.sshProbe = probe }
}

// NewManager prepares Root (dirs, ssh key) and reattaches machines still
// running from a previous daemon process.
func NewManager(root string, log *slog.Logger, opts ...Option) (*Manager, error) {
	m := &Manager{
		Root: root, Log: log, tart: tart.New(), machines: map[string]*Machine{},
		maxMachines: defaultMaxMachines, readyTimeout: readyTimeout, frameInterval: defaultFrameInterval,
		vmPoll: defaultVMPollInterval,
	}
	m.sshProbe = m.probeSSHInGuest
	for _, opt := range opts {
		opt(m)
	}
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o755); err != nil {
		return nil, err
	}
	var err error
	if m.sshKey, m.pubKey, err = EnsureSSHKey(root); err != nil {
		return nil, err
	}
	if err := m.loadState(); err != nil {
		return nil, err
	}
	return m, nil
}

// CheckTart logs the tart binary in use and warns if it is not the pinned version.
func (m *Manager) CheckTart(ctx context.Context) {
	m.tart.CheckVersion(ctx, m.Log)
}

// EnsureSSHKey loads or creates the daemon's ssh key under root and returns
// the private key path and the trimmed public key. prepare-image uses it so a
// base image and the daemon agree on one key.
func EnsureSSHKey(root string) (sshKey, pubKey string, err error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", "", err
	}
	sshKey = filepath.Join(root, "id_ed25519")
	if _, err := os.Stat(sshKey); errors.Is(err, os.ErrNotExist) {
		cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "greenroom", "-f", sshKey)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("ssh-keygen: %w: %s", err, out)
		}
	}
	pub, err := os.ReadFile(sshKey + ".pub")
	if errors.Is(err, os.ErrNotExist) {
		// Images already trust the private key, so rebuild the lost half rather than a new pair.
		var stderr strings.Builder
		cmd := exec.Command("ssh-keygen", "-y", "-f", sshKey)
		cmd.Stderr = &stderr
		if pub, err = cmd.Output(); err != nil {
			return "", "", fmt.Errorf("ssh-keygen -y: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		err = os.WriteFile(sshKey+".pub", pub, 0o644)
	}
	if err != nil {
		return "", "", err
	}
	return sshKey, strings.TrimSpace(string(pub)), nil
}

// Listen calls fn for every lifecycle event, synchronously on the emitting
// goroutine, so fn must return quickly. The returned function unsubscribes.
func (m *Manager) Listen(fn func(LifecycleEvent)) func() {
	m.listenMu.Lock()
	defer m.listenMu.Unlock()
	if m.listeners == nil {
		m.listeners = map[int]func(LifecycleEvent){}
	}
	id := m.nextSub
	m.nextSub++
	m.listeners[id] = fn
	return func() {
		m.listenMu.Lock()
		defer m.listenMu.Unlock()
		delete(m.listeners, id)
	}
}

func (m *Manager) emit(ev LifecycleEvent) {
	m.listenMu.Lock()
	fns := make([]func(LifecycleEvent), 0, len(m.listeners))
	for _, fn := range m.listeners {
		fns = append(fns, fn)
	}
	m.listenMu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
}

func (m *Manager) emitStep(runID string, seq int) {
	m.emit(LifecycleEvent{Kind: "step", RunID: runID, Step: seq})
}

// publicLocked copies mc without its internal handles. The caller holds m.mu.
func (mc *Machine) publicLocked() *Machine {
	c := *mc
	c.rec, c.ready, c.proc, c.input, c.sessions, c.frameCancel = nil, nil, nil, nil, nil, nil
	c.bootCancel, c.bootDone = nil, nil
	return &c
}

// snapshot returns a copy safe to hand to callers and encoders.
func (m *Manager) snapshot(mc *Machine) *Machine {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mc.publicLocked()
}

// List returns the live machines.
func (m *Manager) List() []*Machine {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Machine, 0, len(m.machines))
	for _, mc := range m.machines {
		out = append(out, mc.publicLocked())
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

// Live reports whether runID has a machine in the map.
func (m *Manager) Live(runID string) bool {
	_, err := m.get(runID)
	return err == nil
}

// RunDir is where a run's evidence lives, whether or not its machine is alive.
func (m *Manager) RunDir(runID string) string {
	return filepath.Join(m.Root, "runs", runID)
}

// newRunID names a run. Eight random bytes, because a runId is the map key,
// VM name and run directory, and three bytes collided in CI. No randomness is
// fatal: a zero buffer would give every run the same name.
func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("greenroom: the system has no randomness to name a run with: " + err.Error())
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Create clones image and starts it, returning at once in Booting state.
// Readiness is tracked in the background; use Wait to block for it.
func (m *Manager) Create(ctx context.Context, image string, watch bool) (*Machine, error) {
	m.createMu.Lock()
	defer m.createMu.Unlock()

	if err := m.checkHostCapacity(ctx); err != nil {
		return nil, err
	}
	started := time.Now()
	runID := newRunID()
	name := namePrefix + runID
	dir := m.RunDir(runID)
	input := map[string]any{"image": image}

	rec, err := newRecorder(dir, Manifest{RunID: runID, Image: image, MachineName: name, CreatedAt: started.UTC()}, m.Log)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Machine, error) {
		rec.step("machine_create", input, nil, err, started)
		rec.markEnded()
		return nil, err
	}
	mc := &Machine{RunID: runID, Name: name, Image: image, Status: Booting, CreatedAt: started.UTC(), Dir: dir,
		rec: rec, ready: make(chan struct{}), input: &inputState{}}

	if err := m.tart.Clone(ctx, image, name); err != nil {
		return fail(err)
	}
	proc, err := m.tart.Start(name, filepath.Join(dir, "vm.log"), watch)
	if err != nil {
		m.cleanupVM(name)
		return fail(err)
	}
	mc.proc = proc
	if watch {
		// Wait for the address so the create result carries it.
		mc.VNCURL = proc.VNCURL(10 * time.Second)
		if mc.VNCURL == "" {
			m.Log.Warn("watched machine has no screen address", "runId", runID)
		} else if m.onWatch != nil {
			m.onWatch(mc.VNCURL)
		}
	}

	bootCtx := newBoot(mc)
	m.mu.Lock()
	m.machines[runID] = mc
	m.mu.Unlock()
	if err := m.saveState(); err != nil {
		m.mu.Lock()
		owned := !mc.destroying // else a racing Destroy owns the VM and the record
		live := m.forgetLocked(mc)
		m.mu.Unlock()
		closeSessions(live)
		mc.bootCancel()
		close(mc.bootDone)
		if !owned {
			return nil, err
		}
		m.cleanupVM(name)
		return fail(err)
	}
	seq := rec.step("machine_create", input, map[string]any{"runId": runID, "machineName": name, "status": Booting}, nil, started)
	m.emitStep(runID, seq)
	m.emit(LifecycleEvent{Kind: "created", RunID: runID, Machine: m.snapshot(mc)})
	go m.finishBoot(bootCtx, mc, started)
	return m.snapshot(mc), nil
}

// checkHostCapacity refuses a create when the host is at its VM limit. Call
// it with createMu held. Our own booting machines count too, because tart
// does not report a VM as running the instant it starts.
func (m *Manager) checkHostCapacity(ctx context.Context) error {
	if m.maxMachines <= 0 {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	vms, err := m.tart.List(c)
	if err != nil {
		return fmt.Errorf("count running machines: %w", err)
	}

	held := map[string]string{} // VM name -> runId if ours, "" if foreign
	for _, vm := range vms {
		if vm.State == "running" && vm.Name != "" {
			held[vm.Name] = ""
		}
	}
	m.mu.Lock()
	for _, mc := range m.machines {
		if mc.Status != Failed {
			held[mc.Name] = mc.RunID
		}
	}
	m.mu.Unlock()

	if len(held) < m.maxMachines {
		return nil
	}
	// Name ours by runId, which is what machine_destroy takes.
	var ours, foreign []string
	for name, runID := range held {
		if runID != "" {
			ours = append(ours, "runId "+runID)
		} else {
			foreign = append(foreign, name)
		}
	}
	sort.Strings(ours)
	sort.Strings(foreign)
	switch {
	case len(ours) > 0 && len(foreign) > 0:
		return fmt.Errorf("host is at its limit of %d machines; destroy one of %s, or stop %s outside greenroom",
			m.maxMachines, strings.Join(ours, ", "), strings.Join(foreign, ", "))
	case len(ours) > 0:
		return fmt.Errorf("host is at its limit of %d machines; call machine_destroy on one of %s first",
			m.maxMachines, strings.Join(ours, ", "))
	default:
		return fmt.Errorf("host is at its limit of %d machines, all started outside greenroom (%s); stop one of them first",
			m.maxMachines, strings.Join(foreign, ", "))
	}
}

// Destroy stops and deletes the machine. The run directory is kept. The
// machine stays in state.json until its VM is gone, so a daemon that dies
// midway leaves it for the next one to find.
func (m *Manager) Destroy(ctx context.Context, runID string) error {
	mc, err := m.get(runID)
	if err != nil {
		return err
	}
	started := time.Now()
	m.mu.Lock()
	if mc.destroying {
		m.mu.Unlock()
		return fmt.Errorf("machine %s is already being destroyed", runID)
	}
	mc.destroying = true
	live := m.detachLocked(mc)
	m.mu.Unlock()
	closeSessions(live)
	if mc.bootCancel != nil {
		mc.bootCancel()
		<-mc.bootDone
	}

	m.mu.Lock()
	failed := mc.Status == Failed // a failed boot has already deleted its VM
	m.mu.Unlock()
	if !failed {
		// Detached so a caller that gives up cannot leave the VM running.
		if err = m.stopAndDelete(context.WithoutCancel(ctx), mc.Name); err != nil {
			m.Log.Error("cannot delete the VM; it is left behind", "runId", runID, "name", mc.Name, "err", err)
		}
	}

	m.mu.Lock()
	live = m.forgetLocked(mc)
	m.mu.Unlock()
	closeSessions(live)
	m.persist()
	mc.rec.markEnded()
	m.emitStep(runID, mc.rec.step("machine_destroy", nil, nil, err, started))
	m.emit(LifecycleEvent{Kind: "destroyed", RunID: runID, Machine: m.snapshot(mc)})
	return err
}

// liveLocked reports whether mc is in the map and not being destroyed.
func (m *Manager) liveLocked(mc *Machine) bool {
	return m.machines[mc.RunID] == mc && !mc.destroying
}

// forgetLocked is the only way a machine leaves the map. The caller must pass
// the returned sessions to closeSessions after releasing m.mu.
func (m *Manager) forgetLocked(mc *Machine) []*tart.Session {
	if m.machines[mc.RunID] == mc {
		delete(m.machines, mc.RunID)
	}
	return m.detachLocked(mc)
}

// detachLocked stops the frame recorder and detaches the sessions. It is safe to repeat.
func (m *Manager) detachLocked(mc *Machine) []*tart.Session {
	if mc.frameCancel != nil {
		mc.frameCancel()
	}
	live := make([]*tart.Session, 0, len(mc.sessions))
	for _, s := range mc.sessions {
		if s.proc != nil {
			live = append(live, s.proc)
		}
	}
	mc.sessions = nil
	return live
}

func closeSessions(live []*tart.Session) {
	for _, p := range live {
		_ = p.Close()
	}
}

// stopAndDelete gives stop and delete separate budgets so a hung stop cannot starve the delete.
func (m *Manager) stopAndDelete(ctx context.Context, name string) error {
	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	if err := m.tart.Stop(stopCtx, name); err != nil {
		m.Log.Warn("stop failed, deleting anyway", "name", name, "err", err)
	}
	delCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	return m.tart.Delete(delCtx, name)
}

func (m *Manager) cleanupVM(name string) {
	if err := m.stopAndDelete(context.Background(), name); err != nil {
		m.Log.Error("cannot delete the VM; it is left behind", "name", name, "err", err)
	}
}

// round1 reports a duration in seconds with one decimal.
func round1(d time.Duration) float64 { return math.Round(d.Seconds()*10) / 10 }

// round2 rounds a scale factor to two decimals.
func round2(f float64) float64 { return math.Round(f*100) / 100 }

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
