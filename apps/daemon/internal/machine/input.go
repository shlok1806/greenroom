package machine

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// inputHelper posts CGEvents from inside the guest's login session. It is
// compiled in the guest on first use, never on the host.
//
//go:embed guest/input.swift
var inputHelper string

// inputHelperVersion names the compiled helper. Bump it whenever
// guest/input.swift changes, or running machines and prepared images keep
// the old binary.
const inputHelperVersion = 5

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
}

// inputState is a machine's installed helper. It has its own lock because the
// install is a Swift compile that must not hold Manager.mu.
type inputState struct {
	mu     sync.Mutex             // held across the install
	screen atomic.Pointer[Screen] // set once installed; screenshots read it without mu
	uiMu   sync.Mutex
	ui     map[string]*UITree // each reader's last machine_ui read, which its clicks aim at (issue #35)
	asMu   sync.Mutex         // serializes InputAs so one call's release cannot end another's lease

	screenMu sync.Mutex // serializes starting the live screen

	approval captureApproval // when replayd's approvals were last written or checked
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
// ttl keeps the lease's own.
func (m *Manager) TakeControl(runID, holder string, ttl time.Duration) (lease Control, fresh bool, err error) {
	lease, fresh, _, err = m.TakeControlReporting(runID, holder, ttl)
	return lease, fresh, err
}

// TakeControlReporting is TakeControl that also returns the lease a fresh one replaced after it
// lapsed, if any. Expiry is lazy, so this is the first moment anyone can say it lapsed (issue #57).
func (m *Manager) TakeControlReporting(runID, holder string, ttl time.Duration) (lease Control, fresh bool, lapsed *Control, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, false, nil, err
	}
	now := time.Now().UTC()

	m.mu.Lock()
	current := mc.Control
	live := current != nil && now.Before(current.Expires)
	if live && current.Holder != holder {
		m.mu.Unlock()
		return *current, false, nil, fmt.Errorf("%w: %s has it until %s",
			ErrControlHeld, current.Holder, current.Expires.Format(time.RFC3339))
	}
	if current != nil && !live {
		old := *current
		lapsed = &old
	}
	fresh = !live
	if ttl <= 0 {
		ttl = ControlTTL
		if live && current.ttl > 0 {
			ttl = current.ttl
		}
	}
	lease = Control{Holder: holder, Since: now, Expires: now.Add(ttl), ttl: ttl}
	if live {
		lease.Since, lease.Actions = current.Since, current.Actions
	}
	mc.Control = &lease
	m.mu.Unlock()

	if fresh {
		m.emit(LifecycleEvent{Kind: "control", RunID: runID, Machine: m.snapshot(mc)})
	}
	return lease, fresh, lapsed, nil
}

// TTL is how long the lease lasts without input or renewal.
func (c Control) TTL() time.Duration {
	if c.ttl <= 0 {
		return ControlTTL
	}
	return c.ttl
}

// Lapsed reports whether the lease had expired by now.
func (c Control) Lapsed(now time.Time) bool { return !now.Before(c.Expires) }

// ReleaseControl hands the screen back. Releasing a lease nobody holds is not
// an error, since quit, tab change and timeout may all race to release. An
// empty holder releases whoever holds it.
func (m *Manager) ReleaseControl(runID, holder string) (Control, bool, error) {
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, false, err
	}
	m.mu.Lock()
	current := mc.Control
	if current == nil {
		m.mu.Unlock()
		return Control{}, false, nil
	}
	if holder != "" && current.Holder != holder && time.Now().UTC().Before(current.Expires) {
		m.mu.Unlock()
		return *current, false, fmt.Errorf("%w: %s has it", ErrControlHeld, current.Holder)
	}
	mc.Control = nil
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
	return nil
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
	if err := m.claimActions(mc, holder, len(actions)); err != nil {
		return InputResult{}, err
	}
	screen, err := m.ensureInput(ctx, mc)
	if err != nil {
		return InputResult{}, err
	}

	started := time.Now()
	out := InputResult{Actions: len(actions), Screen: screen}
	err = m.postInput(ctx, mc, screen, actions)
	out.Seconds = time.Since(started).Seconds()
	out.Step = mc.rec.step("machine_input", map[string]any{"holder": holder, "actions": actions},
		map[string]any{"actions": out.Actions, "screen": screen}, err, started)
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
	mc.input.asMu.Lock()
	defer mc.input.asMu.Unlock()
	current, fresh, err := m.TakeControl(runID, holder, 0)
	if errors.Is(err, ErrControlHeld) {
		return InputResult{}, fmt.Errorf("a %s is driving this machine; try again in a moment", current.Holder)
	}
	if err != nil {
		return InputResult{}, err
	}
	if fresh {
		defer func() { _, _, _ = m.ReleaseControl(runID, holder) }()
	}
	return m.Input(ctx, runID, holder, actions)
}

// pixels scales an action's fractions to guest points. Out-of-range fractions
// are clamped: a drag off the edge is a hand, not a bad request.
func pixels(a InputAction, s Screen) InputAction {
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

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Min(math.Max(v, 0), 1)
}

func (m *Manager) postInput(ctx context.Context, mc *Machine, screen Screen, actions []InputAction) error {
	scaled := make([]InputAction, len(actions))
	for i, a := range actions {
		scaled[i] = pixels(a, screen)
	}
	if s := m.liveScreen(mc); s != nil {
		err := s.input(ctx, scaled)
		if !errors.Is(err, errScreenEnded) {
			if err != nil {
				return fmt.Errorf("input failed: %w", err)
			}
			return nil
		}
	}
	payload, err := json.Marshal(struct {
		Actions []InputAction `json:"actions"`
	}{scaled})
	if err != nil {
		return err
	}
	if _, err := runHelper(ctx, m.tart, mc.Name, "--json-base64", base64.StdEncoding.EncodeToString(payload)); err != nil {
		return fmt.Errorf("input failed: %w", err)
	}
	return nil
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

// ensureInput installs the helper once per machine and caches the screen size.
func (m *Manager) ensureInput(ctx context.Context, mc *Machine) (Screen, error) {
	st := mc.input
	st.mu.Lock()
	defer st.mu.Unlock()
	if s := st.screen.Load(); s != nil {
		return *s, nil
	}
	installCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	_, err := execChecked(installCtx, m.tart, mc.Name, "/bin/sh", "-c", installHelperScript())
	cancel()
	if err != nil {
		return Screen{}, fmt.Errorf("install the input helper: %w", err)
	}
	screen, err := readScreen(ctx, m.tart, mc.Name)
	if err != nil {
		return Screen{}, err
	}
	st.screen.Store(&screen)
	return screen, nil
}

// installHelperScript writes the embedded source into the guest and compiles
// it, unless this version already answers --version. It is the single
// definition of the helper's path, shared with PrepareGuest. main.swift and
// -swift-version 5 are both required for top-level code to build.
func installHelperScript() string {
	return fmt.Sprintf(`set -e
bin="$HOME/%s"
src="$HOME/%s"
if [ -x "$bin" ] && "$bin" --version >/dev/null 2>&1; then exit 0; fi
mkdir -p "$(dirname "$bin")" "$src"
printf %%s %s > "$src/main.swift.b64"
base64 -D -i "$src/main.swift.b64" > "$src/main.swift" 2>/dev/null || base64 -d -i "$src/main.swift.b64" > "$src/main.swift"
rm -f "$src/main.swift.b64"
command -v swiftc >/dev/null 2>&1 || { echo "swiftc is not installed in this machine" >&2; exit 127; }
swiftc -O -swift-version 5 "$src/main.swift" -o "$bin"
`, helperName(), helperSourceDir(), base64.StdEncoding.EncodeToString([]byte(inputHelper)))
}

// readScreen asks the helper for the display size by posting no actions.
func readScreen(ctx context.Context, c *tart.Client, vm string) (Screen, error) {
	res, err := runHelper(ctx, c, vm, "--json-base64", base64.StdEncoding.EncodeToString([]byte(`{"actions":[]}`)))
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
