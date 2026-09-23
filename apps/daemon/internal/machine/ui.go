package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// The UI tree (ADR 0012): what the frontmost application's accessibility
// tree says is on the screen, with every frame in the same fractions
// machine_click takes. A text-only brain cannot aim from a description of a
// picture; it can aim at an element's center.

// DefaultUILimit and MaxUILimit bound how many elements one read lists.
const (
	DefaultUILimit = 250
	MaxUILimit     = 1000
)

// UIElement is one on-screen accessibility element. X and Y are its center
// and W and H its size, all fractions of the screen (0 to 1), clipped to
// what is visible. Depth is its nesting among listed elements, for indenting.
type UIElement struct {
	ID         int     `json:"id"`
	Role       string  `json:"role"`
	Subrole    string  `json:"subrole,omitempty"`
	Title      string  `json:"title,omitempty"`
	Label      string  `json:"label,omitempty"`
	Value      string  `json:"value,omitempty"`
	Help       string  `json:"help,omitempty"`
	Identifier string  `json:"identifier,omitempty"`
	Disabled   bool    `json:"disabled,omitempty"`
	Selected   bool    `json:"selected,omitempty"`
	Focused    bool    `json:"focused,omitempty"`
	Depth      int     `json:"depth"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	W          float64 `json:"w"`
	H          float64 `json:"h"`
}

// UITree is one read of an application's accessibility tree.
type UITree struct {
	App       string      `json:"app"`
	BundleID  string      `json:"bundleId,omitempty"`
	PID       int         `json:"pid"`
	Apps      []string    `json:"apps"` // running regular applications, for naming one
	Screen    Screen      `json:"screen"`
	Elements  []UIElement `json:"elements"`
	Truncated bool        `json:"truncated"`
	// TruncatedBy is why the walk stopped: "limit" (the element limit), or
	// "visited" or "depth", the helper's caps on elements visited (5000) and depth (40).
	TruncatedBy string  `json:"truncatedBy,omitempty"`
	Seconds     float64 `json:"seconds"`
	Step        int     `json:"step"`
}

// rawUITree is what the helper prints: frames in points, top-left origin.
type rawUITree struct {
	App struct {
		Name     string `json:"name"`
		BundleID string `json:"bundleId"`
		PID      int    `json:"pid"`
	} `json:"app"`
	Apps     []string `json:"apps"`
	Screen   Screen   `json:"screen"`
	Elements []struct {
		Role       string `json:"role"`
		Subrole    string `json:"subrole"`
		Title      string `json:"title"`
		Label      string `json:"label"`
		Value      string `json:"value"`
		Help       string `json:"help"`
		Identifier string `json:"identifier"`
		Enabled    *bool  `json:"enabled"`
		Selected   bool   `json:"selected"`
		Focused    bool   `json:"focused"`
		Depth      int    `json:"depth"`
		Frame      struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
			W float64 `json:"w"`
			H float64 `json:"h"`
		} `json:"frame"`
	} `json:"elements"`
	Truncated   bool   `json:"truncated"`
	TruncatedBy string `json:"truncatedBy"`
}

// UI reads the accessibility tree of the frontmost application, or of app
// (a name or bundle id) if given. It needs no control lease: it only reads.
// The result is remembered for reader (HolderCoder, HolderVerifier) alone, so
// ElementCenter aims that reader's clicks at the tree it read and never at one
// another reader fetched in between (issue #35).
func (m *Manager) UI(ctx context.Context, runID, reader, app string, limit int) (UITree, error) {
	mc, err := m.get(runID)
	if err != nil {
		return UITree{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return UITree{}, err
	}
	if limit <= 0 {
		limit = DefaultUILimit
	}
	limit = min(limit, MaxUILimit)
	started := time.Now()
	tree, err := m.readUI(ctx, mc, app, limit)
	tree.Seconds = time.Since(started).Seconds()
	input := map[string]any{"limit": limit, "reader": reader}
	if app != "" {
		input["app"] = app
	}
	tree.Step = mc.rec.step("machine_ui", input, tree, err, started)
	if err == nil {
		kept := tree // a copy, with its step: the caller may change what it was handed
		mc.input.uiMu.Lock()
		if mc.input.ui == nil {
			mc.input.ui = map[string]*UITree{}
		}
		mc.input.ui[reader] = &kept
		mc.input.uiMu.Unlock()
	}
	m.emitStep(mc.RunID, tree.Step)
	return tree, err
}

func (m *Manager) readUI(ctx context.Context, mc *Machine, app string, limit int) (UITree, error) {
	if _, err := m.ensureInput(ctx, mc); err != nil {
		return UITree{}, err
	}
	req, err := json.Marshal(map[string]any{"app": app, "limit": limit})
	if err != nil {
		return UITree{}, err
	}
	res, err := runHelper(ctx, m.tart, mc.Name, "--ui-base64", base64.StdEncoding.EncodeToString(req))
	if err != nil {
		return UITree{}, fmt.Errorf("read the UI tree: %w", err)
	}
	var raw rawUITree
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &raw); err != nil {
		return UITree{}, fmt.Errorf("read the UI tree: %w: %.200s", err, strings.TrimSpace(res.Stdout))
	}
	return uiFractions(raw)
}

// uiFractions turns the helper's point frames into center fractions of the
// screen it reported, the space every input action is posted in.
func uiFractions(raw rawUITree) (UITree, error) {
	s := raw.Screen
	if s.Width <= 0 || s.Height <= 0 {
		return UITree{}, fmt.Errorf("the machine reports a %dx%d screen", s.Width, s.Height)
	}
	w, h := float64(s.Width), float64(s.Height)
	out := UITree{App: raw.App.Name, BundleID: raw.App.BundleID, PID: raw.App.PID, Apps: raw.Apps,
		Screen: s, Truncated: raw.Truncated, TruncatedBy: raw.TruncatedBy, Elements: make([]UIElement, 0, len(raw.Elements))}
	for i, e := range raw.Elements {
		f := e.Frame
		out.Elements = append(out.Elements, UIElement{
			ID: i + 1, Role: strings.TrimPrefix(e.Role, "AX"), Subrole: strings.TrimPrefix(e.Subrole, "AX"),
			Title: e.Title, Label: e.Label, Value: e.Value, Help: e.Help, Identifier: e.Identifier,
			Disabled: e.Enabled != nil && !*e.Enabled, Selected: e.Selected, Focused: e.Focused, Depth: e.Depth,
			X: round3(clamp01((f.X + f.W/2) / w)), Y: round3(clamp01((f.Y + f.H/2) / h)),
			W: round3(clamp01(f.W / w)), H: round3(clamp01(f.H / h)),
		})
	}
	return out, nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// ErrNoUITree means a click named an element before any machine_ui read.
var ErrNoUITree = errors.New("you have not read a UI tree on this machine; call machine_ui first")

// ElementTarget is an element a click aims at, and the read it came from.
type ElementTarget struct {
	UIElement
	App    string // the application the tree was read from
	UIStep int    // the machine_ui step of that read
}

// ElementCenter finds element id in reader's most recent UI read, never another
// reader's (issue #35). uiStep, if not 0, is the machine_ui step the caller took
// the id from; a click is refused when that is not the reader's latest read, so
// ids cannot silently resolve against a newer tree. The tree is not re-read: a
// window that moved since needs a fresh machine_ui.
func (m *Manager) ElementCenter(runID, reader string, id, uiStep int) (ElementTarget, error) {
	mc, err := m.get(runID)
	if err != nil {
		return ElementTarget{}, err
	}
	mc.input.uiMu.Lock()
	tree := mc.input.ui[reader]
	mc.input.uiMu.Unlock()
	if tree == nil {
		return ElementTarget{}, ErrNoUITree
	}
	if uiStep != 0 && uiStep != tree.Step {
		return ElementTarget{}, fmt.Errorf("element %d is from the machine_ui read at step %d, but your latest read is step %d (%s); use an id from step %d or call machine_ui again",
			id, uiStep, tree.Step, tree.App, tree.Step)
	}
	for _, e := range tree.Elements {
		if e.ID == id {
			return ElementTarget{UIElement: e, App: tree.App, UIStep: tree.Step}, nil
		}
	}
	return ElementTarget{}, fmt.Errorf("your last machine_ui read (step %d, %s) has no element %d; call machine_ui again", tree.Step, tree.App, id)
}

// Name is how an outline and a click report the element, e.g. RadioButton "25%".
func (e UIElement) Name() string {
	role := e.Role
	if e.Subrole != "" && e.Subrole != e.Role {
		role += "/" + e.Subrole
	}
	for _, s := range []string{e.Title, e.Label, e.Value, e.Help, e.Identifier} {
		if s != "" {
			return fmt.Sprintf("%s %q", role, s)
		}
	}
	return role
}

// Outline is the tree as indented lines for a language model: one element a
// line with its id, what it says, its state, and its center to click.
func (t UITree) Outline() string {
	var b strings.Builder
	fmt.Fprintf(&b, "App: %s", orDash(t.App))
	if len(t.Apps) > 0 {
		fmt.Fprintf(&b, " (running: %s)", strings.Join(t.Apps, ", "))
	}
	fmt.Fprintf(&b, ". Screen %dx%d points.\n", t.Screen.Width, t.Screen.Height)
	b.WriteString("Each line: [id] role \"title\" label=... value=... state, then center (x, y) and size as fractions of the screen, " +
		"the same space machine_click takes. To click an element, pass its id as element, or its center as x and y.\n")
	if len(t.Elements) == 0 {
		b.WriteString("(no on-screen elements: the app has no visible window, or does not expose accessibility)\n")
	}
	for _, e := range t.Elements {
		b.WriteString(strings.Repeat("  ", min(e.Depth, 12)))
		fmt.Fprintf(&b, "[%d] %s", e.ID, e.Role)
		if e.Subrole != "" && e.Subrole != e.Role {
			b.WriteString("/" + e.Subrole)
		}
		if e.Title != "" {
			fmt.Fprintf(&b, " %q", e.Title)
		}
		for _, kv := range [][2]string{{"label", e.Label}, {"value", e.Value}, {"help", e.Help}, {"id", e.Identifier}} {
			if kv[1] != "" {
				fmt.Fprintf(&b, " %s=%q", kv[0], kv[1])
			}
		}
		for _, st := range []struct {
			on   bool
			word string
		}{{e.Selected, "selected"}, {e.Focused, "focused"}, {e.Disabled, "disabled"}} {
			if st.on {
				b.WriteString(" " + st.word)
			}
		}
		fmt.Fprintf(&b, " center (%.3f, %.3f) size %.3fx%.3f\n", e.X, e.Y, e.W, e.H)
	}
	switch {
	case !t.Truncated:
	case t.TruncatedBy == "visited" || t.TruncatedBy == "depth":
		b.WriteString("(truncated: the app's tree is larger than the walk reads, 5000 elements or 40 levels; name one app or window's app, a higher limit will not help)\n")
	default:
		b.WriteString("(truncated: more elements exist; name the app or raise limit)\n")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
