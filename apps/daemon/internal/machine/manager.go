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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

const (
	namePrefix   = "greenroom-"
	guestUser    = "admin"
	readyTimeout = 3 * time.Minute
	readyGrace   = 20 * time.Second // how long guest tools wait for a booting machine
	stopTimeout  = 30 * time.Second

	// defaultMaxMachines is Apple's limit: a host may run at most two macOS
	// VMs at a time. See docs/04-landscape.md for the licence terms.
	defaultMaxMachines = 2

	// defaultFrameInterval is how often a ready machine's screen is captured
	// for the run's recording (docs/adr/0008-run-recording.md).
	defaultFrameInterval = 2 * time.Second
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

	rec   *recorder
	ready chan struct{} // closed once Status leaves Booting
	proc  *tart.Process // the `tart run` subprocess, nil for a reattached machine

	// frameCancel stops this machine's frame recorder (frames.go). It is set
	// once, by startFrames, and read by Destroy; both hold Manager.mu while
	// they touch it.
	frameCancel context.CancelFunc
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

	tart          *tart.Client
	createMu      sync.Mutex // serializes Create so the capacity check cannot be raced
	mu            sync.Mutex
	machines      map[string]*Machine
	sshKey        string
	pubKey        string
	maxMachines   int
	readyTimeout  time.Duration
	frameInterval time.Duration
	sshProbe      func(ctx context.Context, vmName, addr string) error
	onWatch       func(vncURL string)

	listenMu  sync.Mutex
	listeners map[int]func(LifecycleEvent)
	nextSub   int
}

// LifecycleEvent is one change a listener may care about: a machine was
// created, became ready or failed, was destroyed, or recorded a step.
type LifecycleEvent struct {
	Kind    string   `json:"kind"` // created, ready, failed, destroyed, step, frame
	RunID   string   `json:"runId"`
	Machine *Machine `json:"machine,omitempty"`
	Step    int      `json:"step,omitempty"`
	Frame   *Frame   `json:"frame,omitempty"`
}

// Listen calls fn for every lifecycle event. fn runs on the manager's
// goroutine and must return quickly. The returned function removes it.
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

// Option adjusts a Manager before it touches the disk or the host.
type Option func(*Manager)

// WithTartBin makes the Manager drive a different tart binary. Tests point
// this at a fake so that every path is reachable without a real VM.
func WithTartBin(path string) Option {
	return func(m *Manager) { m.tart = &tart.Client{Bin: path} }
}

// WithMaxMachines sets how many VMs the host may run at the same time. The
// default is two, which is Apple's limit for macOS guests. Zero or less
// removes the check.
func WithMaxMachines(n int) Option {
	return func(m *Manager) { m.maxMachines = n }
}

// WithReadyTimeout sets how long a machine may take to become usable.
func WithReadyTimeout(d time.Duration) Option {
	return func(m *Manager) { m.readyTimeout = d }
}

// WithFrameInterval sets how often a ready machine's screen is captured for
// the run's recording (docs/adr/0008-run-recording.md). Zero disables the
// recorder; the default is defaultFrameInterval.
func WithFrameInterval(d time.Duration) Option {
	return func(m *Manager) { m.frameInterval = d }
}

// WithWatchHandler is called with the screen address of each watched
// machine. The daemon uses it to open a viewer on the host.
func WithWatchHandler(fn func(vncURL string)) Option {
	return func(m *Manager) { m.onWatch = fn }
}

// WithSSHProbe replaces the check that guest ssh accepts connections. Tests
// use it, because a fake machine has no sshd. addr is the guest's host-side
// ssh address, carried for the error message and for any future host-side
// probe.
func WithSSHProbe(probe func(ctx context.Context, vmName, addr string) error) Option {
	return func(m *Manager) { m.sshProbe = probe }
}

// probeSSHInGuest reports whether sshd is listening inside the guest. rsync is
// the first thing a caller does after a machine is ready, and rsync needs port
// 22, so a machine is not ready until this succeeds.
//
// The guest is asked about itself, over vsock, because the daemon must never
// open a TCP connection to a guest of its own: see the local-network invariant
// in CLAUDE.md. addr names the host-side address for the error only.
func (m *Manager) probeSSHInGuest(ctx context.Context, vmName, addr string) error {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := m.tart.Exec(c, vmName, "/usr/bin/nc", "-z", "127.0.0.1", "22")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("nc -z 127.0.0.1 22 in guest (for %s): exit %d: %s",
			addr, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// withLastErr appends the last probe failure to a timeout message, so a boot
// that runs out of budget names its cause instead of discarding it.
func withLastErr(msg string, last error) string {
	if last == nil {
		return msg
	}
	return msg + fmt.Sprintf(" (last error: %v)", last)
}

// NewManager prepares Root (dirs, ssh key) and reloads machines that are
// still running from a previous daemon process.
func NewManager(root string, log *slog.Logger, opts ...Option) (*Manager, error) {
	m := &Manager{
		Root: root, Log: log, tart: tart.New(), machines: map[string]*Machine{},
		maxMachines: defaultMaxMachines, readyTimeout: readyTimeout, frameInterval: defaultFrameInterval,
	}
	m.sshProbe = m.probeSSHInGuest
	for _, opt := range opts {
		opt(m)
	}
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
			if m.frameInterval > 0 {
				m.startFrames(mc)
			}
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

// round1 reports a duration in seconds with one decimal.
func round1(d time.Duration) float64 { return math.Round(d.Seconds()*10) / 10 }

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
func (m *Manager) Create(ctx context.Context, image string, watch bool) (*Machine, error) {
	// One create at a time. The capacity check below is a check and then an
	// act, so two creates that overlap would both pass a limit that only has
	// room for one. A clone of a local image takes about 0.1 s, so the cost
	// of serializing is small next to a 45 s boot.
	m.createMu.Lock()
	defer m.createMu.Unlock()

	if err := m.checkHostCapacity(ctx); err != nil {
		return nil, err
	}
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
	proc, err := m.tart.Start(name, filepath.Join(dir, "vm.log"), watch)
	if err != nil {
		m.cleanupVM(name)
		rec.step("machine_create", map[string]any{"image": image}, nil, err, started)
		return nil, err
	}
	mc.proc = proc
	if watch {
		// tart prints the address a moment after start. Waiting here keeps
		// the address in the create result, so the caller can open the screen
		// straight away instead of polling for it.
		mc.VNCURL = proc.VNCURL(10 * time.Second)
		if mc.VNCURL == "" {
			m.Log.Warn("watched machine has no screen address", "runId", runID)
		} else if m.onWatch != nil {
			m.onWatch(mc.VNCURL)
		}
	}

	m.mu.Lock()
	m.machines[runID] = mc
	err = m.saveStateLocked()
	m.mu.Unlock()
	if err != nil {
		m.cleanupVM(name)
		return nil, err
	}
	seq := rec.step("machine_create", map[string]any{"image": image}, map[string]any{"runId": runID, "machineName": name, "status": Booting}, nil, started)
	m.emitStep(runID, seq)
	m.emit(LifecycleEvent{Kind: "created", RunID: runID, Machine: m.snapshot(mc)})
	go m.finishBoot(mc, started)
	return m.snapshot(mc), nil
}

// checkHostCapacity refuses a create when the host already holds as many VMs
// as it is allowed. Apple permits two macOS guests for each host. Call it
// with createMu held: it reads the host state and the caller then acts on it.
//
// A slot is held either by a VM that tart reports as running, or by one of our
// own machines that is still booting. The second case matters because tart
// does not report a VM as running the instant it starts, so two creates in
// quick succession would otherwise both see a free slot.
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

	held := map[string]string{} // VM name -> what the caller can do about it
	for _, vm := range vms {
		// A name is always present in real tart output. The guard keeps a
		// blank row from counting as a slot.
		if vm.State == "running" && vm.Name != "" {
			held[vm.Name] = vm.Name
		}
	}
	m.mu.Lock()
	for _, mc := range m.machines {
		if mc.Status != Failed {
			held[mc.Name] = "runId " + mc.RunID
		}
	}
	m.mu.Unlock()

	if len(held) < m.maxMachines {
		return nil
	}
	// Name what holds each slot. Our own machines are named by runId, because
	// that is what machine_destroy takes. A VM we did not create cannot be
	// destroyed through greenroom at all, so say so.
	ours, foreign := []string{}, []string{}
	for name, how := range held {
		if strings.HasPrefix(how, "runId ") {
			ours = append(ours, how)
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

// finishBoot takes the machine to Ready or Failed through four phases in
// order: the guest agent answers, tart reports an IP, the ssh key goes in,
// and guest ssh accepts a connection. The two waiting phases each get their
// own budget of readyTimeout, because a slow guest agent must not consume the
// time that the ssh phase needs. Every phase is timed into the run record.
func (m *Manager) finishBoot(mc *Machine, started time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), m.readyTimeout)
	defer cancel()

	// Time each phase. Boot time varies with host load, and the recording is
	// the only way to answer "which part was slow" after the fact.
	var ip string
	timings := map[string]float64{}
	at := time.Now()
	err := m.waitReady(ctx, mc)
	timings["agentSeconds"] = round1(time.Since(at))
	if err == nil {
		at = time.Now()
		ip, err = m.tart.IP(ctx, mc.Name)
		timings["ipSeconds"] = round1(time.Since(at))
	}
	if err == nil {
		at = time.Now()
		err = m.installSSHKey(ctx, mc.Name)
		timings["keySeconds"] = round1(time.Since(at))
	}
	if err == nil {
		// The ssh phase gets its own budget. Measured on a loaded host, the
		// guest agent alone took 112.6 s of a 180 s budget, so a shared clock
		// would let a slow agent starve this phase and produce an error that
		// blames ssh for something ssh did not do.
		sshCtx, sshCancel := context.WithTimeout(context.Background(), m.readyTimeout)
		at = time.Now()
		err = m.waitSSH(sshCtx, mc, ip)
		timings["sshSeconds"] = round1(time.Since(at))
		sshCancel()
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
	out := map[string]any{"status": mc.Status, "ip": ip, "bootSeconds": mc.BootSeconds}
	for k, v := range timings {
		out[k] = v
	}
	seq := mc.rec.step("machine_boot", nil, out, err, started)
	m.emitStep(mc.RunID, seq)
	if err != nil {
		m.Log.Warn("machine failed to boot", "runId", mc.RunID, "err", err)
		m.cleanupVM(mc.Name)
		m.emit(LifecycleEvent{Kind: "failed", RunID: mc.RunID, Machine: m.snapshot(mc)})
		return
	}
	m.Log.Info("machine ready", "runId", mc.RunID, "ip", ip, "bootSeconds", mc.BootSeconds,
		"agentSeconds", timings["agentSeconds"], "sshSeconds", timings["sshSeconds"])
	m.emit(LifecycleEvent{Kind: "ready", RunID: mc.RunID, Machine: m.snapshot(mc)})
	if m.frameInterval > 0 {
		m.startFrames(mc)
	}
}

// startFrames begins the frame recorder for a ready machine. It holds
// Manager.mu just long enough to record the cancel function and to check
// that the machine was not destroyed in the window between becoming ready
// and this call; either way the caller does not block on the recorder.
func (m *Manager) startFrames(mc *Machine) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if _, alive := m.machines[mc.RunID]; !alive {
		m.mu.Unlock()
		cancel()
		return
	}
	mc.frameCancel = cancel
	m.mu.Unlock()
	go m.recordFrames(ctx, mc)
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

// waitReady polls the guest agent until it answers. It also watches the tart
// process: a VM that cannot start makes tart exit at once, and without this
// check the machine would wait out the whole readiness timeout for a machine
// that will never answer.
// waitReady polls the guest agent until it answers. ctx carries this phase's
// budget, so the loop needs no deadline of its own.
func (m *Manager) waitReady(ctx context.Context, mc *Machine) error {
	var last error
	for {
		if mc.proc != nil && mc.proc.Exited() {
			return mc.proc.Err()
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := m.tart.Exec(c, mc.Name, "true")
		cancel()
		if err == nil && res.ExitCode == 0 {
			// The agent answered. Make sure the VM is still up, because a
			// process that has already exited means the answer is stale.
			if mc.proc != nil && mc.proc.Exited() {
				return mc.proc.Err()
			}
			return nil
		}
		// Keep the real failure. Once the budget runs out the loop reports a
		// deadline, and without this the cause would be thrown away.
		if ctx.Err() == nil {
			if err != nil {
				last = err
			} else {
				last = fmt.Errorf("exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
			}
		}
		if ctx.Err() != nil {
			return fmt.Errorf("%s: %w", withLastErr(fmt.Sprintf(
				"machine %s: guest agent did not answer within %s", mc.Name, m.readyTimeout), last), ctx.Err())
		}
		time.Sleep(time.Second)
	}
}

// waitSSH blocks until sshd inside the guest accepts a connection. The guest
// agent comes up over vsock before sshd is listening, so a machine that
// reports ready on the agent alone can still refuse the first rsync.
func (m *Manager) waitSSH(ctx context.Context, mc *Machine, ip string) error {
	addr := net.JoinHostPort(ip, "22")
	var last error
	for {
		if mc.proc != nil && mc.proc.Exited() {
			return mc.proc.Err()
		}
		err := m.sshProbe(ctx, mc.Name, addr)
		if err == nil {
			return nil
		}
		// Keep the real failure, so the timeout below names its cause.
		if ctx.Err() == nil {
			last = err
		}
		if ctx.Err() != nil {
			return fmt.Errorf("%s: %w", withLastErr(fmt.Sprintf(
				"machine %s: ssh on %s did not answer within %s", mc.Name, addr, m.readyTimeout), last), ctx.Err())
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
	Step     int     `json:"step"` // its number in steps.jsonl, so a conversation can cite it
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
	out.Step = mc.rec.step("machine_exec", map[string]any{"command": command, "cwd": cwd}, truncatedForLog(out), err, started)
	m.emitStep(mc.RunID, out.Step)
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

// captureScreen runs the guest screencapture-and-base64 command and returns
// the decoded PNG bytes. Both ScreenshotStep, which stores the lossless
// on-demand shot, and the frame recorder (frames.go), which stores a resized
// JPEG every frameInterval, share this: it is the one place that knows how
// to ask the guest for its screen.
func (m *Manager) captureScreen(ctx context.Context, mc *Machine) ([]byte, error) {
	res, err := m.tart.Exec(ctx, mc.Name, "sh", "-c", "screencapture -x /tmp/greenroom-shot.png && base64 -i /tmp/greenroom-shot.png")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("screencapture failed: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimSpace(res.Stdout))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return png, nil
}

// Screenshot captures the guest display as PNG, stores it in the run
// directory, and returns the bytes and the stored path.
func (m *Manager) Screenshot(ctx context.Context, runID string) (png []byte, path string, err error) {
	png, path, _, err = m.ScreenshotStep(ctx, runID)
	return png, path, err
}

// ScreenshotStep is Screenshot that also returns the step number, so the
// caller can cite the artifact by step.
func (m *Manager) ScreenshotStep(ctx context.Context, runID string) (png []byte, path string, seq int, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, "", 0, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return nil, "", 0, err
	}
	started := time.Now()
	// Claim the step number before the artifact is named, so that two
	// screenshots at the same time cannot choose the same file.
	seq = mc.rec.begin()
	defer func() {
		mc.rec.complete(seq, "machine_screenshot", nil, map[string]any{"path": path, "bytes": len(png)}, err, started)
		m.emitStep(mc.RunID, seq)
	}()
	png, err = m.captureScreen(ctx, mc)
	if err != nil {
		return nil, "", seq, err
	}
	path = mc.rec.artifactPath(seq, "screenshot", "png")
	if err = os.WriteFile(path, png, 0o644); err != nil {
		return nil, "", seq, err
	}
	return png, path, seq, nil
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
	if !filepath.IsAbs(source) {
		return SyncResult{}, fmt.Errorf("source %q must be an absolute path on the host", source)
	}
	source = filepath.Clean(source)
	if st, err := os.Stat(source); err != nil || !st.IsDir() {
		return SyncResult{}, fmt.Errorf("source %q is not a directory", source)
	}
	if dest == "" {
		dest = "work/" + filepath.Base(source)
	}
	dest, err = guestDest(dest)
	if err != nil {
		return SyncResult{}, err
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
	m.emitStep(runID, mc.rec.step("machine_sync", map[string]any{"source": source, "dest": dest, "exclude": exclude}, res, err, started))
	return res, err
}

// guestDest keeps a sync inside the guest home. machine_sync describes dest
// as relative to that home, and a path that climbs above it would write over
// the guest system instead.
func guestDest(dest string) (string, error) {
	if filepath.IsAbs(dest) {
		return "", fmt.Errorf("dest %q must be relative to the guest home", dest)
	}
	clean := filepath.Clean(dest)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("dest %q must stay inside the guest home", dest)
	}
	return clean, nil
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
	// The frame recorder must not outlive the machine, but it also must not
	// hold up Destroy: cancel and move on, never wait for the goroutine.
	if mc.frameCancel != nil {
		mc.frameCancel()
	}
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
	seq := mc.rec.step("machine_destroy", nil, nil, err, started)
	m.emitStep(runID, seq)
	m.emit(LifecycleEvent{Kind: "destroyed", RunID: runID, Machine: m.snapshot(mc)})
	return err
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

// RecordVerdict writes the conversation's verdict state into the manifest.
// A live machine goes through its recorder, the only writer while it lives;
// a finished run is edited on disk, where nothing else writes any more.
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
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
