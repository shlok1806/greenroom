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
	"slices"
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

	// Toolchain is the image's toolchain manifest (ADR 0019), passed through as the image
	// wrote it at ToolchainPath, or {"known":false}. Set at ready.
	Toolchain map[string]any `json:"toolchain,omitempty"`
	// Desktop is what the screen showed at ready: any window or app a fresh machine should
	// not have (ADR 0018). Reported, never closed. Set at ready.
	Desktop *DesktopReport `json:"desktop,omitempty"`
	// Models is who verifies this run (issue #154), as its manifest records it. Never edited
	// after Create, so copies share it.
	Models *Models `json:"models,omitempty"`
	// Files is how many files the machine's `tart run` has open against its limit (issue #186,
	// daemon ADR 0002), from the latest count (filewatch.go). Only copies carry it, from files,
	// so state.json never holds a stale count.
	Files *FileUse `json:"files,omitempty"`
	// AgentReconnects counts the guest agent's connections after the first (daemon ADR 0005
	// point 11), so a reconnect loop shows next to Files. Only copies carry it, like Files.
	AgentReconnects int `json:"agentReconnects,omitempty"`

	// boot is each boot phase as it started and ended (bootphase.go), guarded by
	// Manager.mu. Unexported, so neither MCP results nor state.json carry it; the
	// companion API reads it through BootPhases.
	boot []BootPhase

	// Control is the screen-control lease (ADR 0009). It is replaced, never
	// edited in place, so snapshots can share it.
	Control *Control    `json:"control,omitempty"`
	lapse   *time.Timer // clears Control when it runs out (armLapseLocked)

	rec   *recorder
	ready chan struct{} // closed once Status leaves Booting
	proc  *tart.Process // nil for a reattached machine
	input *inputState

	files       *FileUse // guarded by Manager.mu; replaced, never edited in place
	filesWarned bool     // guarded by Manager.mu; the near-limit warning was logged

	// sessions is guarded by Manager.mu and deliberately not persisted: a
	// restarted daemon cannot prove a guest process is the one an old id named.
	sessions map[string]*PTYSession
	// cleanups counts guest cleanups of evicted sessions still running, so
	// Destroy returns only after they have. Guarded by Manager.mu; nil until the first.
	cleanups *sync.WaitGroup

	// execs are machine_exec commands by execId (execjob.go), guarded by
	// Manager.mu. Not persisted either; detachLocked ends the running ones.
	execs map[string]*execJob
	// promptsSeen are the prompts a watched exec already stopped a command for (execprompt.go),
	// by promptKey, guarded by Manager.mu. A look that no longer finds one forgets it.
	promptsSeen map[string]bool

	frameCancel context.CancelFunc // guarded by Manager.mu
	frameDone   chan struct{}      // guarded by Manager.mu; closed when the frame recorder returns
	screen      *screenStream      // guarded by Manager.mu; the live screen, if one has started
	// agent is this boot's guest agent (agent.go, daemon ADR 0005), nil with the toolkit off.
	// Guarded by Manager.mu; releaseLocked stops it, and agentDone closes once it has.
	agent     *agentLink
	agentDone chan struct{}

	// bootCancel stops finishBoot and bootDone closes when it returns. Both are
	// set before the machine is shared, and are nil when no boot runs.
	bootCancel context.CancelFunc
	bootDone   chan struct{}
	destroying bool // guarded by Manager.mu

	// gen counts the machine's boots of its clone: machine_reboot starts the next (reboot.go,
	// daemon ADR 0004). A process watcher or frame recorder of an earlier one stands down.
	// Guarded by Manager.mu.
	gen int
	// vmDeleted is set once the VM is gone: a failed boot or a VM that stopped on its own
	// deletes its clone, a failed reboot never does. Guarded by Manager.mu.
	vmDeleted bool
}

// Status is a machine's lifecycle state.
type Status string

// Machine lifecycle states.
const (
	Booting Status = "booting"
	Ready   Status = "ready"
	Failed  Status = "failed"
	// Rebooting is a ready (or failed-reboot) machine whose clone machine_reboot is
	// restarting; it returns to Ready, or to Failed with its disk kept (daemon ADR 0004).
	Rebooting Status = "rebooting"
)

// Manager creates, drives and destroys machines. State lives under Root.
type Manager struct {
	Root string
	Log  *slog.Logger

	tart             *tart.Client
	createMu         sync.Mutex // serializes Create so the capacity check cannot be raced
	stateMu          sync.Mutex // serializes state.json writes; taken before mu, never under it
	mu               sync.Mutex
	machines         map[string]*Machine
	sshKey           string
	pubKey           string
	maxMachines      int
	readyTimeout     time.Duration
	frameInterval    time.Duration
	vmPoll           time.Duration
	sshProbe         func(ctx context.Context, vmName, addr string) error
	screenIdle       time.Duration
	turnMu           sync.Mutex
	verifierTurns    map[string]bool // runs whose verifier is in a turn (SetVerifierTurn)
	hostTimeZone     func() string   // the zone boot puts the guest in; "" skips it
	screenBuffer     int
	screenInputSlack time.Duration
	rebootTimeout    time.Duration
	messageActivity  func(runID string) time.Time // guarded by mu; see SetMessageActivity
	models           *Models                      // guarded by mu; see SetModels
	fileCheck        FileCheck
	lookTimes        lookTimes     // a look's limits (look.go); zero means defaultLookTimes
	desktopToolkit   bool          // machines run the guest agent (agent.go, daemon ADR 0005)
	agentT           agentTimes    // the guest agent's limits; zero means defaultAgentTimes
	promptLook       time.Duration // how often ExecWatched looks for a prompt (execprompt.go)

	listenMu  sync.Mutex
	listeners map[int]func(LifecycleEvent)
	nextSub   int
}

// LifecycleEvent is one change a listener may care about.
type LifecycleEvent struct {
	Kind    string   `json:"kind"` // created, ready, failed, stopped, destroyed, rebooting, step, frame, control, boot, desktop
	RunID   string   `json:"runId"`
	Machine *Machine `json:"machine,omitempty"`
	Step    int      `json:"step,omitempty"`
	Frame   *Frame   `json:"frame,omitempty"`
	// Lapsed is the lease that ran out with nobody renewing it, on a "control" event (issue #57).
	Lapsed *Control `json:"lapsed,omitempty"`
	// Boot is the phase that started or ended, on a "boot" event.
	Boot *BootPhase `json:"boot,omitempty"`
	// Reboot marks the "ready" or "failed" that ends a machine_reboot (daemon ADR 0004).
	Reboot bool `json:"reboot,omitempty"`
	// By and Via say who destroyed the machine and through what, on a "destroyed" event
	// (daemon ADR 0008); empty when the caller did not say.
	By  string `json:"by,omitempty"`
	Via string `json:"via,omitempty"`
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

// WithHostTimeZone replaces how boot learns the host's time zone (HostTimeZone).
func WithHostTimeZone(fn func() string) Option {
	return func(m *Manager) { m.hostTimeZone = fn }
}

// WithScreenIdle sets how long a live screen runs with nobody watching.
func WithScreenIdle(d time.Duration) Option {
	return func(m *Manager) { m.screenIdle = d }
}

// WithScreenInputSlack sets how long a live screen may take to ACK an input
// beyond the time its actions and those queued before it take to post.
func WithScreenInputSlack(d time.Duration) Option {
	return func(m *Manager) { m.screenInputSlack = d }
}

// WithPromptLook sets how often ExecWatched looks at the screen for a prompt while a command runs.
func WithPromptLook(d time.Duration) Option {
	return func(m *Manager) { m.promptLook = d }
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
		vmPoll: defaultVMPollInterval, screenIdle: defaultScreenIdle, hostTimeZone: HostTimeZone, screenBuffer: defaultScreenBuffer,
		screenInputSlack: screenInputSlack, fileCheck: defaultFileCheck(), rebootTimeout: defaultRebootTimeout,
		promptLook: defaultPromptLook,
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
	if c.Control != nil && !time.Now().UTC().Before(c.Control.Expires) {
		c.Control = nil // lapsed, whether or not the lapse timer has run yet
	}
	c.lapse = nil
	c.rec, c.ready, c.proc, c.input, c.sessions, c.frameCancel, c.screen = nil, nil, nil, nil, nil, nil, nil
	c.execs, c.cleanups = nil, nil
	c.bootCancel, c.bootDone, c.frameDone = nil, nil, nil
	c.agent, c.agentDone = nil, nil
	if mc.agent != nil {
		c.AgentReconnects = mc.agent.sup.Reconnects()
	}
	c.boot = slices.Clone(mc.boot) // putPhase rewrites elements in place
	c.Files, c.files = mc.files, nil
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

// Steps is runID's step records as written to steps.jsonl, oldest first. It reads the run
// directory, so it answers for a run after a restart or once its machine is gone.
func (m *Manager) Steps(runID string) ([]Step, error) {
	if runID == "" || runID == "." || strings.HasPrefix(runID, "..") || strings.ContainsAny(runID, `/\`) {
		return nil, fmt.Errorf("no run %q", runID) // a runId that would leave runs/, as session.Registry refuses
	}
	return ReadSteps(m.RunDir(runID))
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
func (m *Manager) Create(ctx context.Context, image string) (*Machine, error) {
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

	m.mu.Lock()
	models := m.models
	m.mu.Unlock()
	rec, err := newRecorder(dir, Manifest{RunID: runID, Image: image, MachineName: name, CreatedAt: started.UTC(), Models: models}, m.Log)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Machine, error) {
		rec.step("machine_create", input, nil, err, started)
		rec.markEnded()
		return nil, err
	}
	mc := &Machine{RunID: runID, Name: name, Image: image, Status: Booting, CreatedAt: started.UTC(), Dir: dir,
		Models: models, rec: rec, ready: make(chan struct{}), input: &inputState{}}

	endClone := m.beginPhase(mc, PhaseClone)
	err = m.tart.Clone(ctx, image, name)
	endClone(image, err)
	if err != nil {
		// tart says only that the VM does not exist; say what exists instead and how to build it
		// (issue #285). Asked after the failure, so a create that clones pays no tart list for it.
		if missing := m.MissingImage(ctx, image); missing != nil {
			err = fmt.Errorf("%w (tart: %w)", missing, err)
		}
		return fail(err)
	}
	endStart := m.beginPhase(mc, PhaseStart)
	proc, err := m.tart.Start(name, filepath.Join(dir, "vm.log"))
	endStart(name, err)
	if err != nil {
		m.cleanupVM(name)
		return fail(err)
	}
	mc.proc = proc

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
	created := m.snapshot(mc)
	m.emit(LifecycleEvent{Kind: "created", RunID: runID, Machine: created})
	// Clone and start ran before anyone knew the run; publish them now, in order.
	for _, p := range created.boot {
		m.emitPhase(runID, p)
	}
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

	held := map[string]*Machine{} // VM name -> this daemon's machine, nil if another's
	for _, vm := range vms {
		if vm.State == "running" && vm.Name != "" {
			held[vm.Name] = nil
		}
	}
	m.mu.Lock()
	for _, mc := range m.machines {
		if _, running := held[mc.Name]; mc.Status != Failed || running { // a failed reboot's VM may still run
			held[mc.Name] = mc
		}
	}
	m.mu.Unlock()

	if len(held) < m.maxMachines {
		return nil
	}
	// The daemon cannot tell one MCP caller from another (daemon ADR 0008), so no run is called
	// the caller's and none is offered to destroy: each is described by its name, creator, age,
	// idle time and who is at it, and named by a short runId an agent can match against a runId
	// its own machine_create returned. greenroom never destroys a machine it was not asked to.
	var runs []string
	for name, mc := range held {
		switch {
		case mc != nil:
			runs = append(runs, m.describeRun(mc))
		case strings.HasPrefix(name, namePrefix):
			// Another root or port: probably another agent's run in progress (issue #42).
			runs = append(runs, name+" (another greenroom daemon's)")
		default:
			runs = append(runs, name+" (not greenroom's)")
		}
	}
	sort.Strings(runs)
	return fmt.Errorf("host is at its limit of %d machines. Running now: %s. greenroom cannot tell which of these, "+
		"if any, you created: a run is yours only if your own machine_create returned its runId, and then you may "+
		"end it with run_finish when you are done with it. Every other run is another agent's or a person's work, "+
		"however idle it looks: do not stop, destroy or finish it; wait a few minutes for one to end and call "+
		"machine_create again", m.maxMachines, strings.Join(runs, "; "))
}

// Destroy stops and deletes the machine. The run directory is kept. The
// machine stays in state.json until its VM is gone, so a daemon that dies
// midway leaves it for the next one to find.
func (m *Manager) Destroy(ctx context.Context, runID string) error {
	return m.DestroyBy(ctx, runID, "", "")
}

// DestroyBy is Destroy that says who asked (daemon ADR 0008): by is the caller, such as
// "agent (claude-code)" or "human", and via the tool or route it used. The step records by,
// the destroyed event carries both, and the daemon log names them.
func (m *Manager) DestroyBy(ctx context.Context, runID, by, via string) error {
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
	bootCancel, bootDone := mc.bootCancel, mc.bootDone // a reboot replaces them under m.mu
	m.mu.Unlock()
	closeSessions(live)
	if bootCancel != nil {
		bootCancel()
		<-bootDone
	}

	m.mu.Lock()
	gone := mc.vmDeleted // a failed boot has already deleted its VM; a failed reboot kept it
	m.mu.Unlock()
	if !gone {
		// Detached so a caller that gives up cannot leave the VM running.
		if err = m.stopAndDelete(context.WithoutCancel(ctx), mc.Name); err != nil {
			m.Log.Error("cannot delete the VM; it is left behind", "runId", runID, "name", mc.Name, "err", err)
		}
	}

	m.mu.Lock()
	live = m.forgetLocked(mc)
	frames, cleanups, agent := mc.frameDone, mc.cleanups, mc.agentDone
	m.mu.Unlock()
	closeSessions(live)
	if cleanups != nil {
		cleanups.Wait() // out of the map, so no eviction can add one now
	}
	if frames != nil {
		<-frames // no frame lands in the run directory after Destroy returns
	}
	if agent != nil {
		<-agent // its tart exec has ended too
	}
	m.persist()
	mc.rec.markEnded()
	m.emitStep(runID, mc.rec.stepAs(by, "machine_destroy", nil, nil, err, started))
	m.Log.Info("machine destroyed", "runId", runID, "by", by, "via", via, "err", err)
	m.emit(LifecycleEvent{Kind: "destroyed", RunID: runID, Machine: m.snapshot(mc), By: by, Via: via})
	return err
}

// liveLocked reports whether mc is in the map and not being destroyed.
func (m *Manager) liveLocked(mc *Machine) bool {
	return m.machines[mc.RunID] == mc && !mc.destroying
}

// forgetLocked is the only way a machine leaves the map. The caller must pass
// the returned sessions to closeSessions after releasing m.mu.
func (m *Manager) forgetLocked(mc *Machine) []*PTYSession {
	if m.machines[mc.RunID] == mc {
		delete(m.machines, mc.RunID)
	}
	return m.detachLocked(mc)
}

// detachLocked stops the frame recorder and the live screen, and detaches the
// sessions. It is safe to repeat. The lease goes too: a machine that is going away is driven by
// nobody, and a lease left on it would be announced as a lapse after it was destroyed.
func (m *Manager) detachLocked(mc *Machine) []*PTYSession {
	live := m.releaseLocked(mc, errors.New("the live screen ended: the machine is gone"))
	// Kills each running command's host tart exec, so none outlives the machine.
	for _, j := range mc.execs {
		j.cancel()
	}
	mc.execs = nil
	return live
}

// releaseLocked drops the lease and stops the frame recorder, the live screen (ending its
// viewers with screenEnd) and the guest agent, and detaches the sessions, which the caller passes
// to closeSessions after releasing m.mu. Destroy and machine_reboot both start with it.
func (m *Manager) releaseLocked(mc *Machine, screenEnd error) []*PTYSession {
	if mc.lapse != nil {
		mc.lapse.Stop()
	}
	mc.Control = nil
	m.stopAgentLocked(mc)
	if mc.frameCancel != nil {
		mc.frameCancel()
	}
	if mc.screen != nil {
		mc.screen.end(screenEnd)
		mc.screen = nil
	}
	live := make([]*PTYSession, 0, len(mc.sessions))
	for _, s := range mc.sessions {
		if s.proc != nil {
			live = append(live, s)
		}
	}
	mc.sessions = nil
	return live
}

// closeSessions ends each session's follower and host `tart exec`. The guest
// side goes with the VM.
func closeSessions(live []*PTYSession) {
	for _, s := range live {
		_ = s.stop()
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
