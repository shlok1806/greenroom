package machine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// helperSources is the input helper and guest agent (daemon ADR 0005): the Swift files under
// guest/helper, compiled in the guest by installHelperScript, never on the host. The tests
// directory is not embedded; it runs on the host (guest/helper/swift_test.go).
//
//go:embed guest/helper/*.swift guest/helper/logic/*.swift
var helperSources embed.FS

// inputHelperVersion names the compiled helper. Bump it whenever the helper's behaviour
// changes in a way a daemon relies on (a new mode or op), or running machines and prepared
// images keep the old binary. Within a version, --version also carries the sources' hash
// (helperVersionLine), so any change to them is recompiled by the install and boot checks.
const inputHelperVersion = 9

// ControlTTL is how long an unused screen-control lease lives unless the taker
// asks otherwise. Every input renews it by its own ttl, so a crashed holder
// cannot lock the screen forever.
const ControlTTL = 60 * time.Second

// Lease seats for the two agents; the API's seat is "human". They are
// separate so the record says which agent drove.
const (
	HolderCoder    = "coder"    // the coding agent's MCP input tools
	HolderVerifier = "verifier" // greenroom's own verifier
)

// Control lease errors. The API maps each to its own status code.
var (
	ErrNoControl   = errors.New("nobody holds control of this screen")
	ErrControlHeld = errors.New("someone else holds control of this screen")
)

// Control is one screen-control lease (ADR 0009). It is a value, replaced
// rather than edited, so a machine snapshot can carry it safely.
type Control struct {
	Holder  string    `json:"holder"` // the seat, e.g. "human", not the person
	Since   time.Time `json:"since"`
	Expires time.Time `json:"expires"`
	Actions int       `json:"actions"` // events posted under this lease

	ttl time.Duration // what each renewal extends the lease by
}

// Screen is a guest display's size in points, the space input is posted in.
type Screen struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// InputAction is one thing to do to the screen. X and Y are fractions of the
// display (0 to 1), never pixels; only the manager knows the resolution.
type InputAction struct {
	Type   string   `json:"type"` // move, click, down, up, scroll, type, key, sleep
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Button string   `json:"button,omitempty"` // left (default), right, middle
	Clicks int      `json:"clicks,omitempty"` // 2 for a double click
	DeltaX float64  `json:"deltaX,omitempty"`
	DeltaY float64  `json:"deltaY,omitempty"`
	Text   string   `json:"text,omitempty"`
	Key    string   `json:"key,omitempty"`
	Mods   []string `json:"mods,omitempty"` // cmd, shift, alt, ctrl, fn
	MS     int      `json:"ms,omitempty"`
}

// InputResult is what one batch of actions did.
type InputResult struct {
	Actions int     `json:"actions"`
	Screen  Screen  `json:"screen"`
	Seconds float64 `json:"seconds"`
	Step    int     `json:"step"`
	// Degraded is a batch posted by the one-shot exec because the guest agent's channel was down
	// (daemon ADR 0005 point 13); nothing of it had been sent on the channel.
	Degraded bool `json:"degraded,omitempty"`
}

// inputState is a machine's installed helper. It has its own lock because the
// install is a Swift compile that must not hold Manager.mu.
type inputState struct {
	mu      sync.Mutex             // guards install; never held across a guest call
	install *installJob            // the install running now, if any (ensureInput)
	screen  atomic.Pointer[Screen] // set once installed; screenshots read it without mu
	uiMu    sync.Mutex
	ui      map[string]*UITree // each reader's last machine_ui read, which its clicks aim at (issue #35)
	looks   map[string]uint64  // each reader's handovers count as of its latest look (issue #124)
	// handovers counts the times the screen changed hands under anyone but the verifier: a fresh
	// take, a release or a lapse. A verifier input aimed before the latest one is refused (issue #124).
	handovers atomic.Uint64
	// handoverStep is the step the recorder had claimed when the latest handover happened: a look
	// with a higher step began after it. A verdict's evidence must be newer (ADR 0024).
	handoverStep atomic.Int64
	asMu         sync.Mutex // serializes InputAs so one call's release cannot end another's lease
	// ctlMu serializes each lease change with the events it emits, taken before Manager.mu: a
	// take that follows a lapse returns only once the lapse was announced (lapseLocked).
	ctlMu sync.Mutex

	screenMu sync.Mutex // serializes starting the live screen

	approval captureApproval // when replayd's approvals were last written or checked
	capture  captureGate     // one guest screencapture at a time (daemon ADR 0003)

	desk deskState // the desktop toolkit's per-reader ref origins (desktop.go)
}

// installJob is one run of the helper install and screen read, shared by every caller that
// needs it while it runs.
type installJob struct {
	done   chan struct{} // closed when it ends; screen and err are set by then
	screen Screen
	err    error
}

// helperName is the compiled helper's path relative to the guest home.
func helperName() string {
	return fmt.Sprintf(".greenroom/bin/greenroom-input-%d", inputHelperVersion)
}

func helperSourceDir() string {
	return fmt.Sprintf(".greenroom/src/greenroom-input-%d", inputHelperVersion)
}

// TakeControl gives holder the machine's mouse and keyboard, or renews its
// lease. A second holder is refused, not queued. fresh is false for a
// renewal, so the caller announces a handover only once. A renewal with no
// ttl keeps the lease's own. A lease this replaces because it ran out is
// announced as lapsed first, whoever takes (issue #57).
func (m *Manager) TakeControl(runID, holder string, ttl time.Duration) (lease Control, fresh bool, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, false, err
	}
	mc.input.ctlMu.Lock()
	defer mc.input.ctlMu.Unlock()
	now := time.Now().UTC()

	m.mu.Lock()
	lapsed := m.lapseLocked(mc, now)
	current := mc.Control
	if current != nil && current.Holder != holder {
		m.mu.Unlock()
		return *current, false, fmt.Errorf("%w: %s has it until %s",
			ErrControlHeld, current.Holder, current.Expires.Format(time.RFC3339))
	}
	fresh = current == nil
	if fresh && holder != HolderVerifier {
		mc.handedOver()
	}
	if fresh {
		m.pauseAgentLocked(mc, holder) // an input already in flight stops at its next event (daemon ADR 0005 point 14)
	}
	if ttl <= 0 {
		ttl = ControlTTL
		if !fresh && current.ttl > 0 {
			ttl = current.ttl
		}
	}
	lease = Control{Holder: holder, Since: now, Expires: now.Add(ttl), ttl: ttl}
	if !fresh {
		lease.Since, lease.Actions = current.Since, current.Actions
	}
	mc.Control = &lease
	m.armLapseLocked(mc)
	m.mu.Unlock()

	m.announce(lapsed)
	if fresh {
		m.emit(LifecycleEvent{Kind: "control", RunID: runID, Machine: m.snapshot(mc)})
	}
	return lease, fresh, nil
}

// TTL is how long the lease lasts without input or renewal.
func (c Control) TTL() time.Duration {
	if c.ttl <= 0 {
		return ControlTTL
	}
	return c.ttl
}

// ReleaseControl hands the screen back. Releasing a lease nobody holds is not
// an error, since quit, tab change and timeout may all race to release. An
// empty holder releases whoever holds it. A lease that already ran out is not
// held: it is announced as lapsed instead.
func (m *Manager) ReleaseControl(runID, holder string) (Control, bool, error) {
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, false, err
	}
	mc.input.ctlMu.Lock()
	defer mc.input.ctlMu.Unlock()
	m.mu.Lock()
	lapsed := m.lapseLocked(mc, time.Now().UTC())
	current := mc.Control
	if current == nil {
		m.mu.Unlock()
		m.announce(lapsed)
		return Control{}, false, nil
	}
	if holder != "" && current.Holder != holder {
		m.mu.Unlock()
		return *current, false, fmt.Errorf("%w: %s has it", ErrControlHeld, current.Holder)
	}
	mc.Control = nil
	if current.Holder != HolderVerifier {
		mc.handedOver()
	}
	m.resumeAgentLocked(mc, current.Holder)
	m.mu.Unlock()

	m.emit(LifecycleEvent{Kind: "control", RunID: runID, Machine: m.snapshot(mc)})
	return *current, true, nil
}

// ControlState reports the live lease, if any. Expiry is evaluated lazily here.
func (m *Manager) ControlState(runID string) (Control, bool) {
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if mc.Control == nil || !time.Now().UTC().Before(mc.Control.Expires) {
		return Control{}, false
	}
	return *mc.Control, true
}

// claimActions checks holder has the lease, renews it and counts n actions.
func (m *Manager) claimActions(mc *Machine, holder string, n int) error {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	current := mc.Control
	if current == nil || !now.Before(current.Expires) {
		return fmt.Errorf("%w: take control of run %s first", ErrNoControl, mc.RunID)
	}
	if current.Holder != holder {
		return fmt.Errorf("%w: %s has it", ErrControlHeld, current.Holder)
	}
	next := *current
	next.Actions += n
	if next.ttl <= 0 {
		next.ttl = ControlTTL
	}
	next.Expires = now.Add(next.ttl)
	mc.Control = &next
	m.armLapseLocked(mc)
	return nil
}

// armLapseLocked makes sure mc's lease is cleared, and its lapse announced, once it runs out
// with nobody renewing it: a holder that dies never touches it again (issue #57).
func (m *Manager) armLapseLocked(mc *Machine) {
	if mc.Control == nil {
		return
	}
	d := time.Until(mc.Control.Expires) + 20*time.Millisecond
	if mc.lapse == nil {
		mc.lapse = time.AfterFunc(d, func() { m.checkLapse(mc) })
		return
	}
	mc.lapse.Reset(d)
}

// checkLapse is the lapse timer: it clears a lease that ran out and announces it.
func (m *Manager) checkLapse(mc *Machine) {
	mc.input.ctlMu.Lock()
	defer mc.input.ctlMu.Unlock()
	now := time.Now().UTC()
	m.mu.Lock()
	if mc.Control != nil && now.Before(mc.Control.Expires) {
		m.armLapseLocked(mc) // renewed meanwhile
		m.mu.Unlock()
		return
	}
	lapsed := m.lapseLocked(mc, now)
	m.mu.Unlock()
	m.announce(lapsed)
}

// lapseLocked clears mc's lease if it ran out by now, and returns the "control" event that
// announces it, carrying the lapsed lease, or nil. The caller passes it to announce after
// releasing m.mu and before releasing ctlMu. Every path that finds an expired lease comes here,
// so each lapse is announced exactly once, before the take or release that found it returns. A
// machine being destroyed announces nothing: that would land after "destroyed" and reopen the
// conversation the daemon evicted.
func (m *Manager) lapseLocked(mc *Machine, now time.Time) *LifecycleEvent {
	c := mc.Control
	if c == nil || now.Before(c.Expires) {
		return nil
	}
	mc.Control = nil
	if c.Holder != HolderVerifier {
		mc.handedOver()
	}
	m.resumeAgentLocked(mc, c.Holder)
	if !m.liveLocked(mc) {
		return nil
	}
	old := *c
	return &LifecycleEvent{Kind: "control", RunID: mc.RunID, Machine: mc.publicLocked(), Lapsed: &old}
}

// announce emits a lapse from lapseLocked, if there is one.
func (m *Manager) announce(lapsed *LifecycleEvent) {
	if lapsed != nil {
		m.emit(*lapsed)
	}
}

// RenewControl extends holder's live lease and never takes a new one, so a seat that lost the
// screen (given back by another window of the same seat, or lapsed) is told so (issue #100).
func (m *Manager) RenewControl(runID, holder string) (Control, error) {
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, err
	}
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	current := mc.Control
	if current == nil || !now.Before(current.Expires) || current.Holder != holder {
		return Control{}, fmt.Errorf("%w: the screen was given back or its lease lapsed; take control again to drive", ErrNoControl)
	}
	next := *current
	if next.ttl <= 0 {
		next.ttl = ControlTTL
	}
	next.Expires = now.Add(next.ttl)
	mc.Control = &next
	m.armLapseLocked(mc)
	return next, nil
}

// ScreenOf returns the guest's display size, installing the input helper on
// first use. Taking control calls it so the compile happens before the first click.
func (m *Manager) ScreenOf(ctx context.Context, runID string) (Screen, error) {
	mc, err := m.get(runID)
	if err != nil {
		return Screen{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return Screen{}, err
	}
	return m.ensureInput(ctx, mc)
}

// Input posts actions into the guest in one round trip, recorded as one step.
// holder must hold the control lease (ADR 0009).
func (m *Manager) Input(ctx context.Context, runID, holder string, actions []InputAction) (InputResult, error) {
	mc, err := m.get(runID)
	if err != nil {
		return InputResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return InputResult{}, err
	}
	if len(actions) == 0 {
		return InputResult{}, errors.New("no actions to post")
	}
	if err := validateActions(actions); err != nil {
		return InputResult{}, err
	}
	if err := m.claimActions(mc, holder, len(actions)); err != nil {
		return InputResult{}, err
	}
	screen, err := m.ensureInput(ctx, mc)
	if err != nil {
		return InputResult{}, err
	}

	started := time.Now()
	out := InputResult{Actions: len(actions), Screen: screen}
	out.Degraded, err = m.postInput(ctx, mc, holder, screen, actions)
	out.Seconds = time.Since(started).Seconds()
	output := map[string]any{"actions": out.Actions, "screen": screen}
	if out.Degraded {
		output["degraded"] = true
	}
	out.Step = mc.rec.stepAs(holder, "machine_input", map[string]any{"holder": holder, "actions": actions}, output, err, started)
	m.emitStep(mc.RunID, out.Step)
	return out, err
}

// InputAs takes the lease for holder, posts actions, and releases the lease
// if this call took it. mcpserver and verifier both use it (issue #12). A lease
// held by someone else becomes a readable error naming them, not ErrControlHeld.
func (m *Manager) InputAs(ctx context.Context, runID, holder string, actions []InputAction) (InputResult, error) {
	mc, err := m.get(runID)
	if err != nil {
		return InputResult{}, err
	}
	// The lease is per call, so it never spans a verifier turn: without this the coder's clicks
	// landed between the verifier's and each corrupted what the other checked (issue #82).
	if holder == HolderCoder && m.inVerifierTurn(runID) {
		return InputResult{}, errors.New("greenroom's verifier is in the middle of a turn on this machine and is using " +
			"the screen; wait for its reply or verdict with agent_wait, or send a note, then try again")
	}
	// Refused before the lease is taken, so a bad batch leaves no trace at all.
	if err := validateActions(actions); err != nil {
		return InputResult{}, err
	}
	mc.input.asMu.Lock()
	defer mc.input.asMu.Unlock()
	current, fresh, err := m.TakeControl(runID, holder, 0)
	if errors.Is(err, ErrControlHeld) {
		return InputResult{}, &ScreenTakenError{Holder: current.Holder, Until: current.Expires}
	}
	if err != nil {
		return InputResult{}, err
	}
	if fresh {
		defer func() { _, _, _ = m.ReleaseControl(runID, holder) }()
	}
	// Checked after the take, so a human's lease that it found lapsed counts, and a human who
	// holds the screen is a ScreenTakenError, not this (issue #124).
	if holder == HolderVerifier && mc.staleLook(holder) {
		return InputResult{}, ErrStaleLook
	}
	return m.Input(ctx, runID, holder, actions)
}

// handedOver counts a change of hands (issue #124) and marks the step it happened at. The count
// goes first: a look that claims its step in between is then called stale by both, never fresh
// by the step and stale by the count. Callers hold Manager.mu, which orders the stores.
func (mc *Machine) handedOver() {
	mc.input.handovers.Add(1)
	mc.input.handoverStep.Store(int64(mc.rec.currentStep()))
}

// HandoverStep is the step the run's recorder had claimed when the screen last changed hands
// under anyone but the verifier (issue #124), or 0 if it never has since the daemon started. A
// step numbered higher began after that handover (ADR 0024).
func (m *Manager) HandoverStep(runID string) int {
	mc, err := m.get(runID)
	if err != nil {
		return 0
	}
	return int(mc.input.handoverStep.Load())
}

// ErrStaleLook refuses a verifier input when someone else took, gave back or lost the screen
// after the verifier's latest machine_ui or machine_screenshot: a plan made on the old screen
// would be carried out on a new one (issue #124). Its text is what the model reads.
var ErrStaleLook = errors.New("the screen changed hands since your last look; call machine_ui (or machine_screenshot) before any input")

// noteLook records that reader looked at the screen as of handover count at, read before the
// look began: a handover during the look leaves it stale.
func (mc *Machine) noteLook(reader string, at uint64) {
	if reader == "" {
		return
	}
	mc.input.uiMu.Lock()
	defer mc.input.uiMu.Unlock()
	if mc.input.looks == nil {
		mc.input.looks = map[string]uint64{}
	}
	mc.input.looks[reader] = max(mc.input.looks[reader], at)
}

// staleLook reports whether the screen changed hands after reader's latest look. A reader that
// never looked is stale once anyone else has held the screen.
func (mc *Machine) staleLook(reader string) bool {
	mc.input.uiMu.Lock()
	defer mc.input.uiMu.Unlock()
	return mc.input.looks[reader] < mc.input.handovers.Load()
}

// ErrScreenTaken matches the error InputAs returns while another seat holds the screen.
var ErrScreenTaken = errors.New("another seat holds the screen")

// ScreenTakenError says who holds the screen and until when (the lease renews while they drive).
type ScreenTakenError struct {
	Holder string
	Until  time.Time
}

func (e *ScreenTakenError) Error() string {
	return fmt.Sprintf("a %s is driving this machine; try again in a moment", e.Holder)
}

// Is makes errors.Is(err, ErrScreenTaken) true.
func (e *ScreenTakenError) Is(target error) bool { return target == ErrScreenTaken }

// The names the helper posts (input.swift `flags`, `mouseButton`, `run`). Anything else is
// refused here, before a batch posts anything: the helper drops an unknown modifier and makes an
// unknown button a left click, so a typo in cmd-Q would type a q (issue #31).
var (
	actionTypes   = []string{"move", "click", "down", "up", "scroll", "type", "key", "sleep"}
	modifierNames = map[string]bool{"cmd": true, "command": true, "meta": true, "shift": true, "alt": true,
		"option": true, "opt": true, "ctrl": true, "control": true, "fn": true, "function": true}
	buttonNames = map[string]bool{"": true, "left": true, "right": true, "middle": true, "center": true}
)

const (
	modifierHelp = "use cmd, shift, alt, ctrl, fn (or command, meta, option, opt, control, function)"
	buttonHelp   = "use left, right or middle"
)

// validateActions refuses a batch with a name the helper would silently misread, or an action it
// would post as a no-op or at the wrong place.
func validateActions(actions []InputAction) error {
	for i, a := range actions {
		if !slices.Contains(actionTypes, strings.ToLower(a.Type)) {
			return fmt.Errorf("action %d: unknown action type %q; use one of %s", i+1, a.Type, strings.Join(actionTypes, ", "))
		}
		if !buttonNames[strings.ToLower(a.Button)] {
			return fmt.Errorf("action %d: unknown button %q; %s", i+1, a.Button, buttonHelp)
		}
		switch strings.ToLower(a.Type) {
		case "type":
			// The helper types nothing and the call would report success (issue #126).
			if a.Text == "" {
				return fmt.Errorf("action %d: type needs text: pass the characters to type", i+1)
			}
		case "click", "down", "up", "move":
			// The helper posts at the pointer when a coordinate is missing (issue #85).
			if a.X == nil || a.Y == nil {
				return fmt.Errorf("action %d: %s needs x and y, fractions of the screen 0 to 1 (to click an element, use machine_click with element)", i+1, a.Type)
			}
		}
		for _, mod := range a.Mods {
			if !modifierNames[strings.ToLower(mod)] {
				return fmt.Errorf("action %d: unknown modifier %q; %s", i+1, mod, modifierHelp)
			}
		}
	}
	return nil
}

// maxScrollDelta is the largest scroll the helper can post: CGEvent takes the wheel as an Int32.
const maxScrollDelta = math.MaxInt32

// pixels turns an action into what the helper posts. Fractions become guest points, and
// out-of-range fractions are clamped: a drag off the edge is a hand, not a bad request. A scroll's
// deltas are clamped to what CGEvent can carry (issue #36) and negated: InputAction's positive
// deltaY scrolls down and positive deltaX right, as every tool says, while a positive CGEvent
// wheel scrolls up and left (issue #51).
func pixels(a InputAction, s Screen) InputAction {
	if strings.EqualFold(a.Type, "scroll") {
		a.DeltaX, a.DeltaY = -clampDelta(a.DeltaX), -clampDelta(a.DeltaY)
	}
	if a.X != nil {
		x := math.Round(clamp01(*a.X) * float64(s.Width))
		a.X = &x
	}
	if a.Y != nil {
		y := math.Round(clamp01(*a.Y) * float64(s.Height))
		a.Y = &y
	}
	return a
}

func clampDelta(v float64) float64 {
	return math.Min(math.Max(math.Round(v), -maxScrollDelta), maxScrollDelta)
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Min(math.Max(v, 0), 1)
}

// postInput posts a batch for holder: over the guest agent's channel when the machine has one
// (op input, which a PAUSE by another seat stops), else on the live screen's pipe while it
// runs, else by the one-shot helper. It falls back from the channel only when nothing was sent
// on it (degraded), so a batch is never posted twice.
func (m *Manager) postInput(ctx context.Context, mc *Machine, holder string, screen Screen, actions []InputAction) (degraded bool, err error) {
	scaled := make([]InputAction, len(actions))
	for i, a := range actions {
		scaled[i] = pixels(a, screen)
	}
	_, route, err := m.viaAgent(ctx, mc, guestagent.Request{Op: "input", Reader: holder, Input: true,
		Args:     map[string]any{"actions": scaled},
		Deadline: min(inputCost(scaled)+m.screenInputSlack, guestagent.MaxDeadline)})
	switch route {
	case routeAgent:
		if err != nil {
			return false, fmt.Errorf("input failed: %w", err)
		}
		return false, nil
	case routeDegraded:
		degraded = true
	}
	if s := m.liveScreen(mc); s != nil {
		err := s.input(ctx, scaled)
		if !errors.Is(err, errScreenEnded) {
			if err != nil {
				return degraded, fmt.Errorf("input failed: %w", err)
			}
			return degraded, nil
		}
	}
	payload, err := json.Marshal(struct {
		Actions []InputAction `json:"actions"`
	}{scaled})
	if err != nil {
		return degraded, err
	}
	if _, err := runHelper(ctx, m.tart, mc.Name, "--json-base64", base64.StdEncoding.EncodeToString(payload)); err != nil {
		return degraded, fmt.Errorf("input failed: %w", err)
	}
	return degraded, nil
}

// runHelper runs the installed helper with args. Arguments go through a
// shell, so they must be shell-safe (base64 or fixed flags).
func runHelper(ctx context.Context, c *tart.Client, vm string, args ...string) (tart.ExecResult, error) {
	res, err := c.Exec(ctx, vm, "/bin/sh", "-c",
		fmt.Sprintf(`exec "$HOME/%s" %s`, helperName(), strings.Join(args, " ")))
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("exit %d: %s", res.ExitCode, helperError(res.Stderr))
	}
	return res, nil
}

// helperError extracts the message from the helper's `{"error": "..."}`.
func helperError(stderr string) string {
	var out struct {
		Error string `json:"error"`
	}
	trimmed := strings.TrimSpace(stderr)
	if err := json.Unmarshal([]byte(trimmed), &out); err == nil && out.Error != "" {
		return out.Error
	}
	return trimmed
}

// helperCheck bounds asking the guest whether its helper is current, the part of an install
// that needs no compile, in the guest and on the host. The compile has helperCompileTimeout.
var helperCheck = lookLimit{guest: 20 * time.Second, grace: 10 * time.Second}

// ensureInput installs the helper once per machine and caches the screen size. The install
// runs detached from the caller: a caller that gives up (its ctx, a look's cap) returns at
// once, the install goes on, and the next caller waits for the same one rather than
// starting another. Nothing is held across the install that blocks another caller beyond
// its own ctx (issue #187, where a 3 minute install lock queued every UI read).
func (m *Manager) ensureInput(ctx context.Context, mc *Machine) (Screen, error) {
	st := mc.input
	if s := st.screen.Load(); s != nil {
		return *s, nil
	}
	st.mu.Lock()
	if s := st.screen.Load(); s != nil {
		st.mu.Unlock()
		return *s, nil
	}
	job := st.install
	if job == nil {
		job = &installJob{done: make(chan struct{})}
		st.install = job
		go m.runInstall(context.WithoutCancel(ctx), mc, job)
	}
	st.mu.Unlock()
	select {
	case <-job.done:
		return job.screen, job.err
	case <-ctx.Done():
		// Not wrapping ctx.Err(): a look's cap must not read this as a screen that did not answer.
		return Screen{}, fmt.Errorf("the input helper is still being installed in the guest (%v); try again in a minute", ctx.Err())
	}
}

// runInstall is one installJob: on success the screen is cached before the job is cleared,
// so no later caller starts another; on failure the job is cleared and the next caller retries.
func (m *Manager) runInstall(ctx context.Context, mc *Machine, job *installJob) {
	defer close(job.done)
	job.screen, job.err = m.installInput(ctx, mc)
	st := mc.input
	st.mu.Lock()
	defer st.mu.Unlock()
	if job.err == nil {
		st.screen.Store(&job.screen)
	}
	st.install = nil
}

// installInput makes sure this version of the helper is in the guest and reads the screen
// size with it. Only a helper that is missing or stale pays the compile's deadline; asking is
// bounded by helperCheck, and the screen read by the look watchdog.
//
// With the guest agent connected the helper is current (boot's helper phase checked it before
// starting the agent, which is that helper), so only the screen is read, over the channel.
func (m *Manager) installInput(ctx context.Context, mc *Machine) (Screen, error) {
	if s, route, err := m.agentScreen(ctx, mc); route == routeAgent {
		return s, err
	}
	res, timedOut, err := guestLook(ctx, m.tart, mc.Name, helperCheck, "/bin/sh", "-c", helperCheckScript())
	if timedOut {
		// The helper's --version touches nothing but its own binary (input.swift), so a check
		// that hangs is a guest that hangs, and the install script's own check would too.
		return Screen{}, &ScreenNotAnsweringError{What: "the input helper's version check", After: helperCheck.guest}
	}
	if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "current" {
		installCtx, cancel := context.WithTimeout(ctx, helperCompileTimeout)
		_, err := execChecked(installCtx, m.tart, mc.Name, "/bin/sh", "-c", installHelperScript())
		cancel()
		if err != nil {
			return Screen{}, fmt.Errorf("install the input helper: %w", err)
		}
	}
	return readScreenWithin(ctx, m.tart, mc.Name, m.looks().captureLimit())
}

// sourceHashFile is the one helper file the install script writes itself: it names the
// sources' hash, which --version prints (daemon ADR 0005).
const sourceHashFile = "SourceHash.swift"

// helperBundle is the embedded sources as a gzipped tar, with SourceHash.swift naming their
// hash, and the line this build's helper answers --version with. Computed once.
var helperBundle = sync.OnceValues(func() (bundle []byte, versionLine string) {
	type file struct {
		name string
		data []byte
	}
	var files []file
	for _, name := range helperSourceFiles() {
		if name == sourceHashFile {
			continue
		}
		data, err := helperSources.ReadFile("guest/helper/" + name)
		if err != nil {
			panic(fmt.Sprintf("the embedded helper sources: %v", err))
		}
		files = append(files, file{name, data})
	}
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.name))
		h.Write([]byte{0})
		h.Write(f.data)
		h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))[:12]
	files = append(files, file{sourceHashFile, fmt.Appendf(nil,
		"// Written by the install script (daemon ADR 0005).\nlet helperSource = %q\n", sum)})

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		panic(err)
	}
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			panic(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		panic(err)
	}
	return buf.Bytes(), fmt.Sprintf("greenroom-input %d %s", inputHelperVersion, sum)
})

// helperVersionLine is what this daemon's helper prints for --version: its version and the
// hash of its sources. The install and boot checks compare the whole line.
func helperVersionLine() string {
	_, line := helperBundle()
	return line
}

// helperSourceFiles lists the embedded sources, relative to guest/helper, sorted.
func helperSourceFiles() []string {
	var out []string
	err := fs.WalkDir(helperSources, "guest/helper", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, strings.TrimPrefix(p, "guest/helper/"))
		}
		return err
	})
	if err != nil {
		panic(fmt.Sprintf("the embedded helper sources: %v", err))
	}
	slices.Sort(out)
	return out
}

// installHelperScript writes the embedded sources into the guest and compiles them, unless
// this exact build (version and source hash) already answers --version. It is the single
// definition of the helper's path, shared with PrepareGuest. The sources travel as one base64
// gzipped tar, never through a shell as text. main.swift holds the only top-level code, and
// -swift-version 5 is required for it to build. The binary is compiled beside its final name
// and moved over it, so an interrupted compile never leaves a half-written helper.
func installHelperScript() string {
	bundle, line := helperBundle()
	return fmt.Sprintf(`set -e
bin="$HOME/%s"
src="$HOME/%s"
if [ -x "$bin" ] && [ "$("$bin" --version 2>/dev/null)" = %q ]; then exit 0; fi
rm -rf "$src"
mkdir -p "$(dirname "$bin")" "$src"
printf %%s %s > "$src/sources.tgz.b64"
base64 -D -i "$src/sources.tgz.b64" > "$src/sources.tgz" 2>/dev/null || base64 -d -i "$src/sources.tgz.b64" > "$src/sources.tgz"
tar -xzf "$src/sources.tgz" -C "$src"
rm -f "$src/sources.tgz.b64" "$src/sources.tgz"
command -v swiftc >/dev/null 2>&1 || { echo "swiftc is not installed in this machine" >&2; exit 127; }
swiftc -O -swift-version 5 "$src"/*.swift "$src"/logic/*.swift -o "$bin.new"
mv -f "$bin.new" "$bin"
`, helperName(), helperSourceDir(), line, base64.StdEncoding.EncodeToString(bundle))
}

// readScreen asks the helper for the display size by posting no actions.
func readScreen(ctx context.Context, c *tart.Client, vm string) (Screen, error) {
	return readScreenWithin(ctx, c, vm, defaultLookTimes.captureLimit())
}

// readScreenWithin is readScreen under the look watchdog lim.
func readScreenWithin(ctx context.Context, c *tart.Client, vm string, lim lookLimit) (Screen, error) {
	res, err := readHelper(ctx, c, vm, lim, "the screen size read",
		"--json-base64", base64.StdEncoding.EncodeToString([]byte(`{"actions":[]}`)))
	if err != nil {
		return Screen{}, fmt.Errorf("read the screen size: %w", err)
	}
	var out struct {
		Screen Screen `json:"screen"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &out); err != nil {
		return Screen{}, fmt.Errorf("read the screen size: %w: %s", err, strings.TrimSpace(res.Stdout))
	}
	if out.Screen.Width <= 0 || out.Screen.Height <= 0 {
		return Screen{}, fmt.Errorf("the machine reports a %dx%d screen", out.Screen.Width, out.Screen.Height)
	}
	return out.Screen, nil
}

// SetVerifierTurn records whether greenroom's verifier is in a turn on runID. The actor sets it
// around each turn; while it is open the coding agent's input is refused (issue #82).
func (m *Manager) SetVerifierTurn(runID string, open bool) {
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	if m.verifierTurns == nil {
		m.verifierTurns = map[string]bool{}
	}
	if open {
		m.verifierTurns[runID] = true
	} else {
		delete(m.verifierTurns, runID)
	}
}

// VerifierTurnOpen reports whether greenroom's verifier is in a turn on runID right now
// (SetVerifierTurn). run_finish refuses while it is (ADR 0034).
func (m *Manager) VerifierTurnOpen(runID string) bool { return m.inVerifierTurn(runID) }

func (m *Manager) inVerifierTurn(runID string) bool {
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	return m.verifierTurns[runID]
}
