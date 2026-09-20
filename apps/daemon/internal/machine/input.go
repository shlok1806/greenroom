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
	"time"
)

// inputHelper is the guest-side program that posts the events (guest/input.swift).
// It is compiled inside the machine on first use, never on the host: the
// events have to come from a process in the guest's own login session or
// nothing sees them.
//
//go:embed guest/input.swift
var inputHelper string

// inputHelperVersion names the compiled helper, so a daemon that ships a new
// helper recompiles rather than calling the old binary a running machine
// already has. Bump it whenever guest/input.swift changes.
const inputHelperVersion = 2

// ControlTTL is how long a screen-control lease lives without being used.
// The holder is an app that can crash or lose its network, and a lease that
// outlived it would lock everyone else out of the screen forever, so every
// input call renews it and silence ends it.
const ControlTTL = 60 * time.Second

// Errors the control lease answers with. The API turns each into its own
// status code, so a caller can tell "someone else is driving" from "you are
// not driving".
var (
	ErrNoControl   = errors.New("nobody holds control of this screen")
	ErrControlHeld = errors.New("someone else holds control of this screen")
)

// Control is one screen-control lease: who may move the mouse and press the
// keys of a machine, and until when (ADR 0009). It is a value, replaced
// rather than edited, so a snapshot of a machine can carry it safely.
type Control struct {
	Holder  string    `json:"holder"` // "human" today; the seat, not the person
	Since   time.Time `json:"since"`
	Expires time.Time `json:"expires"`
	Actions int       `json:"actions"` // how many events this lease has posted
}

// Screen is a guest display's size in points, which is the coordinate space
// every input action is in once the daemon has scaled it.
type Screen struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// InputAction is one thing to do to the machine's screen. Coordinates are
// **fractions of the display**, 0 to 1, never pixels: the companion shows a
// frame scaled to whatever the window happens to be, and a fraction is the
// only coordinate it can compute without knowing the guest's resolution. The
// manager multiplies them out (see pixels) because it is the side that knows
// the resolution.
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

// inputState is a machine's guest-side input helper: installed once, then
// reused. It has its own lock because installing takes seconds (a Swift
// compile in the guest) and must not be done twice or hold up the manager.
type inputState struct {
	mu        sync.Mutex
	installed bool
	screen    Screen
}

// helperName is the path of the compiled helper inside the guest, relative
// to the guest user's home.
func helperName() string {
	return fmt.Sprintf(".greenroom/bin/greenroom-input-%d", inputHelperVersion)
}

func helperSourceDir() string {
	return fmt.Sprintf(".greenroom/src/greenroom-input-%d", inputHelperVersion)
}

// --- the control lease ---

// TakeControl gives holder the machine's mouse and keyboard, or renews the
// lease it already has. A second holder is refused rather than queued: two
// hands on one mouse is not a state anything can recover from.
//
// The second return value says whether this is a fresh take rather than a
// renewal, so the caller announces the handover in the conversation once and
// not on every heartbeat.
func (m *Manager) TakeControl(runID, holder string, ttl time.Duration) (Control, bool, error) {
	if ttl <= 0 {
		ttl = ControlTTL
	}
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, false, err
	}
	now := time.Now().UTC()

	next, fresh, err := func() (Control, bool, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		current := mc.Control
		if current != nil && current.Holder != holder && now.Before(current.Expires) {
			return *current, false, fmt.Errorf("%w: %s has it until %s",
				ErrControlHeld, current.Holder, current.Expires.Format(time.RFC3339))
		}
		next := Control{Holder: holder, Since: now, Expires: now.Add(ttl)}
		fresh := true
		if current != nil && current.Holder == holder && now.Before(current.Expires) {
			next.Since, next.Actions, fresh = current.Since, current.Actions, false
		}
		mc.Control = &next
		return next, fresh, nil
	}()
	if err != nil {
		return next, false, err
	}
	if fresh {
		m.emit(LifecycleEvent{Kind: "control", RunID: runID, Machine: m.snapshot(mc)})
	}
	return next, fresh, nil
}

// ReleaseControl hands the screen back. Releasing a lease nobody holds is not
// an error: the app releases on quit, on a tab change and on a timeout, and
// all three may race.
func (m *Manager) ReleaseControl(runID, holder string) (Control, bool, error) {
	mc, err := m.get(runID)
	if err != nil {
		return Control{}, false, err
	}
	released, held, err := func() (Control, bool, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		current := mc.Control
		if current == nil {
			return Control{}, false, nil
		}
		if holder != "" && current.Holder != holder && time.Now().UTC().Before(current.Expires) {
			return *current, false, fmt.Errorf("%w: %s has it", ErrControlHeld, current.Holder)
		}
		mc.Control = nil
		return *current, true, nil
	}()
	if err != nil {
		return released, false, err
	}
	if held {
		m.emit(LifecycleEvent{Kind: "control", RunID: runID, Machine: m.snapshot(mc)})
	}
	return released, held, nil
}

// ControlState reports the live lease, if there is one. An expired lease is
// reported as no lease: expiry is read at the moment it is asked about,
// because nothing else would notice a holder that went away.
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

// claimActions checks the lease before an input batch and renews it. It
// returns the lease as it now stands so the caller can report it.
func (m *Manager) claimActions(mc *Machine, holder string, n int) (Control, error) {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	current := mc.Control
	if current == nil || !now.Before(current.Expires) {
		return Control{}, fmt.Errorf("%w: take control of run %s first", ErrNoControl, mc.RunID)
	}
	if current.Holder != holder {
		return *current, fmt.Errorf("%w: %s has it", ErrControlHeld, current.Holder)
	}
	next := *current
	next.Actions += n
	next.Expires = now.Add(ControlTTL)
	mc.Control = &next
	return next, nil
}

// --- input ---

// ScreenOf returns the guest's display size, installing the input helper if
// this is the first call for the machine. Taking control goes through it, so
// that the seconds the compile costs are spent before the person's first
// click rather than during it.
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

// Input posts actions into the guest, in order, in one round trip. holder
// must be the one that holds the control lease (ADR 0009): an input with no
// lease behind it is a hidden second operator, which is the thing the lease
// exists to prevent.
//
// The whole batch is one step in the run's evidence, so a drag or a typed
// word reads as the one thing the person did rather than as twenty.
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
	if _, err := m.claimActions(mc, holder, len(actions)); err != nil {
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
	if err != nil {
		return out, err
	}
	return out, nil
}

// InputAs takes the screen lease for holder, posts actions, and releases the
// lease again whether or not the batch succeeded. It is the one place the
// take-post-release sequence lives: internal/mcpserver's machine_click
// family and internal/verifier's own tool loop both call this rather than
// each keeping its own copy, because internal/verifier cannot import
// internal/mcpserver and the two used to duplicate the same three lines
// (issue #12). A human already holding the lease comes back as a readable
// error naming them, not the sentinel ErrControlHeld, so either caller can
// read it and simply try again in a moment instead of treating it as a
// broken run.
func (m *Manager) InputAs(ctx context.Context, runID, holder string, actions []InputAction) (InputResult, error) {
	current, _, err := m.TakeControl(runID, holder, 0)
	if err != nil {
		if errors.Is(err, ErrControlHeld) {
			return InputResult{}, fmt.Errorf("a %s is driving this machine; try again in a moment", current.Holder)
		}
		return InputResult{}, err
	}
	defer func() { _, _, _ = m.ReleaseControl(runID, holder) }()
	return m.Input(ctx, runID, holder, actions)
}

// pixels turns one action's fractional coordinates into guest pixels. A
// fraction outside 0..1 is clamped rather than refused: a drag that leaves
// the picture is a person's hand sliding off the edge, not a bad request.
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

// postInput runs one batch through the guest helper.
func (m *Manager) postInput(ctx context.Context, mc *Machine, screen Screen, actions []InputAction) error {
	scaled := make([]InputAction, len(actions))
	for i, a := range actions {
		scaled[i] = pixels(a, screen)
	}
	payload, err := json.Marshal(struct {
		Actions []InputAction `json:"actions"`
	}{scaled})
	if err != nil {
		return err
	}
	res, err := m.tart.Exec(ctx, mc.Name, "/bin/sh", "-c",
		fmt.Sprintf(`exec "$HOME/%s" --json-base64 %s`, helperName(), base64.StdEncoding.EncodeToString(payload)))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("input failed: exit %d: %s", res.ExitCode, helperError(res.Stderr))
	}
	return nil
}

// helperError pulls the message out of the helper's `{"error": "..."}` so a
// window shows the reason rather than a JSON object.
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

// ensureInput compiles the helper in the guest once per machine and reads
// the display size back from it. Everything after the first call is one exec.
func (m *Manager) ensureInput(ctx context.Context, mc *Machine) (Screen, error) {
	st := mc.input
	if st == nil {
		return Screen{}, fmt.Errorf("machine %s has no input state", mc.RunID)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.installed {
		return st.screen, nil
	}
	if err := m.installInputHelper(ctx, mc); err != nil {
		return Screen{}, err
	}
	screen, err := m.readScreen(ctx, mc)
	if err != nil {
		return Screen{}, err
	}
	st.installed, st.screen = true, screen
	return screen, nil
}

// installHelperScript is the /bin/sh -c script that writes the embedded
// Swift source into the guest and compiles it, at the exact path and
// version `helperName` and `inputHelperVersion` name. It is the one place
// that combination is spelled out, so `installInputHelper` (a machine's own
// first control request, issue #9) and `PrepareGuest` (baking the helper
// into the greenroom base image ahead of time, issue #12) can never drift
// apart on where the binary lives or what version it claims to be.
//
// The source travels as base64 in one argument, so no part of it is read by
// a shell, and the compile is skipped when the machine already has this
// version of the binary: `if [ -x "$bin" ] && "$bin" --version` is the whole
// contract between this script and the image build that wants to make it a
// no-op.
//
// It is main.swift and it is built at language version 5 on purpose: a
// single file of top-level code is only a program under those two
// conditions, and the guest's toolchain is whichever one the image happens
// to carry.
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

// installInputHelper runs installHelperScript against a machine this
// Manager owns.
func (m *Manager) installInputHelper(ctx context.Context, mc *Machine) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	res, err := m.tart.Exec(ctx, mc.Name, "/bin/sh", "-c", installHelperScript())
	if err != nil {
		return fmt.Errorf("install the input helper: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("install the input helper: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// readScreen asks the helper for the display size by giving it nothing to do.
func (m *Manager) readScreen(ctx context.Context, mc *Machine) (Screen, error) {
	res, err := m.tart.Exec(ctx, mc.Name, "/bin/sh", "-c",
		fmt.Sprintf(`exec "$HOME/%s" --json-base64 %s`, helperName(),
			base64.StdEncoding.EncodeToString([]byte(`{"actions":[]}`))))
	if err != nil {
		return Screen{}, err
	}
	if res.ExitCode != 0 {
		return Screen{}, fmt.Errorf("read the screen size: exit %d: %s", res.ExitCode, helperError(res.Stderr))
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
