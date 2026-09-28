package desktop

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"
)

// The wire types of the toolkit ops (daemon ADR 0006, "Ops and results"). Field names and JSON
// tags are the ADR's. Decoding tolerates fields it does not know, since the agent may add some
// before the daemon reads them, and absent ones, since the agent leaves empty fields out.

// Rect is a frame in guest points, top-left origin: [x, y, w, h].
type Rect [4]float64

// X is the left edge.
func (r Rect) X() float64 { return r[0] }

// Y is the top edge.
func (r Rect) Y() float64 { return r[1] }

// W is the width.
func (r Rect) W() float64 { return r[2] }

// H is the height.
func (r Rect) H() float64 { return r[3] }

// Empty reports whether nothing of the rect has area. An absent `vis` decodes to the zero rect,
// which is empty: the element shows nothing.
func (r Rect) Empty() bool { return r[2] <= 0 || r[3] <= 0 }

// IsZero lets `omitzero` leave an absent rect out when the types are encoded again (a step
// record holds the structured result).
func (r Rect) IsZero() bool { return r == Rect{} }

// Center is the rect's middle point.
func (r Rect) Center() Point { return Point{r[0] + r[2]/2, r[1] + r[3]/2} }

// Point is a guest point [x, y], top-left origin.
type Point [2]float64

// Fractions turns a guest point into fractions of the screen, the space every other tool uses.
// A screen with no size gives the zero point rather than a division by zero.
func (p Point) Fractions(s Screen) (x, y float64) {
	if s.Width <= 0 || s.Height <= 0 {
		return 0, 0
	}
	return p[0] / float64(s.Width), p[1] / float64(s.Height)
}

// Point turns fractions of the screen into a guest point, the space the agent works in. Fractions
// outside 0 to 1 are clamped, as machine_input's are: a point off the edge is a hand, not a bad
// request.
func (s Screen) Point(x, y float64) Point {
	return Point{math.Round(clamp01(x) * float64(s.Width)), math.Round(clamp01(y) * float64(s.Height))}
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return min(max(v, 0), 1)
}

// Screen is the guest's main display in points, as HELLO and `snapshot` report it.
type Screen struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale,omitempty"`
}

// Stamp is a process's start time. It only ever identifies a process together with its pid (a
// recycled pid has another start), so it is kept as text and compared for equality; it accepts a
// JSON number or string so the agent's choice of encoding cannot break a decode.
type Stamp string

// UnmarshalJSON accepts a string, a number or null.
func (s *Stamp) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case bytes.Equal(b, []byte("null")):
		*s = ""
		return nil
	case len(b) > 0 && b[0] == '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = Stamp(v)
		return nil
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		*s = Stamp(n.String())
		return nil
	}
}

// AppInfo is a process with windows: the snapshot's target app, the frontmost app, a tree's app.
type AppInfo struct {
	Name     string `json:"name,omitempty"`
	BundleID string `json:"bundleId,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Started  Stamp  `json:"started,omitempty"`
}

// The states the agent reports, in the order the outline shows them (`main` is a window's). A
// state the daemon does not know yet is shown after these, sorted, so a new one in the agent
// needs no daemon change.
const (
	StateMain     = "main"
	StateDisabled = "disabled"
	StateSelected = "selected"
	StateFocused  = "focused"
	StateExpanded = "expanded"
	StateEditable = "editable"
	StateBusy     = "busy"
	// StateSecret is a secure text field: its value is never sent, only its length (Chars).
	StateSecret = "secret"
	// StateOverflow is a toolbar item behind the toolbar's overflow chevron (catalog M15): it is
	// not actionable until the chevron is pressed.
	StateOverflow = "overflow"
	// StateNotDrawn is set by the daemon, never the agent: the element says something, but its
	// visible rect on a capture of the screen holds no ink (the ink test, ADR 0027).
	StateNotDrawn = "notDrawn"
)

var stateOrder = []string{StateMain, StateDisabled, StateSelected, StateFocused, StateExpanded, StateEditable, StateBusy}

// sentenceStates are the states that render as their own flag, not as a bare word.
var sentenceStates = []string{StateSecret, StateOverflow, StateNotDrawn}

// Where a coverer sits relative to the covered element (Covered.Where).
const (
	WhereWindow = "window" // the same window
	WhereApp    = "app"    // another window of the same app
	WhereOther  = "other"  // another app's window
)

// Covered names what the hit test found at an element's visible center instead of the element.
type Covered struct {
	By    string `json:"by,omitempty"`
	Role  string `json:"role,omitempty"`
	Name  string `json:"name,omitempty"`
	Where string `json:"where,omitempty"`
	App   string `json:"app,omitempty"`
}

// ScrollPos is a scroll container's position, each axis a fraction 0 to 1 (nil when the axis does
// not scroll), and the ways it can still move.
type ScrollPos struct {
	X     *float64 `json:"x"`
	Y     *float64 `json:"y"`
	Up    bool     `json:"up,omitempty"`
	Down  bool     `json:"down,omitempty"`
	Left  bool     `json:"left,omitempty"`
	Right bool     `json:"right,omitempty"`
}

// Node is one element (daemon ADR 0006's `node`). Document and Edited are a window's (catalog
// W16): the file it shows, as a path, and whether it has unsaved changes.
type Node struct {
	Ref       string     `json:"ref"`
	Role      string     `json:"role,omitempty"`
	Subrole   string     `json:"subrole,omitempty"`
	Name      string     `json:"name,omitempty"`
	Value     string     `json:"value,omitempty"`
	Desc      string     `json:"desc,omitempty"`
	Help      string     `json:"help,omitempty"`
	ID        string     `json:"id,omitempty"`
	States    []string   `json:"states,omitempty"`
	Frame     Rect       `json:"frame,omitzero"`
	Vis       Rect       `json:"vis,omitzero"`
	Depth     int        `json:"depth,omitempty"`
	Window    string     `json:"window,omitempty"`
	Scroller  string     `json:"scroller,omitempty"`
	Covered   *Covered   `json:"covered,omitempty"`
	Offscreen string     `json:"offscreen,omitempty"`
	Clipped   string     `json:"clipped,omitempty"`
	Scroll    *ScrollPos `json:"scroll,omitempty"`
	Chars     int        `json:"chars,omitempty"`
	Cut       []string   `json:"cut,omitempty"`
	Document  string     `json:"document,omitempty"`
	Edited    bool       `json:"edited,omitempty"`
}

// UnmarshalJSON decodes a node and drops the value of a secure text field should an agent ever
// send one, so a secret cannot reach an outline, a diff or a step through this package.
func (n *Node) UnmarshalJSON(b []byte) error {
	type plain Node
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*n = Node(p)
	if n.Secret() {
		n.Value = ""
	}
	return nil
}

// Has reports whether the node is in state s.
func (n Node) Has(s string) bool { return slices.Contains(n.States, s) }

// Secret reports whether the node is a secure text field, whose value is never sent.
func (n Node) Secret() bool { return n.Has(StateSecret) }

// Visible reports whether any of the node shows on screen.
func (n Node) Visible() bool { return !n.Vis.Empty() }

// IsWindow reports whether the node is a window.
func (n Node) IsWindow() bool { return n.Role == "Window" }

// The kinds of attention item.
const (
	AttentionSheet   = "sheet"
	AttentionAlert   = "alert"
	AttentionDialog  = "dialog"
	AttentionMenu    = "menu"
	AttentionPopover = "popover"
	AttentionWindow  = "window"
)

// Attention is something that wants handling before the window under it: a sheet, alert, dialog,
// open menu or popover of the target app, or another process's window over it.
type Attention struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind,omitempty"`
	Role string `json:"role,omitempty"`
	Name string `json:"name,omitempty"`
	App  string `json:"app,omitempty"`
	PID  int    `json:"pid,omitempty"`
}

// Tree is the target app's windows in interactive mode, the before and after of an action or wait.
type Tree struct {
	Nodes     []Node      `json:"nodes,omitempty"`
	Attention []Attention `json:"attention,omitempty"`
	Windows   []string    `json:"windows,omitempty"`
	App       AppInfo     `json:"app,omitzero"`
}

// The reasons a walk stopped early (Snapshot.TruncatedBy).
const (
	TruncatedByLimit   = "limit"
	TruncatedByVisited = "visited"
	TruncatedByDepth   = "depth"
	TruncatedByTime    = "time"
)

// Snapshot is the result of the `snapshot` op.
type Snapshot struct {
	Screen      Screen      `json:"screen"`
	Frontmost   AppInfo     `json:"frontmost,omitzero"`
	App         AppInfo     `json:"app,omitzero"`
	Focused     string      `json:"focused,omitempty"`
	Attention   []Attention `json:"attention,omitempty"`
	Nodes       []Node      `json:"nodes,omitempty"`
	TruncatedBy string      `json:"truncatedBy,omitempty"`
	// Responding is nil when the agent did not say; only an explicit false means the app did not
	// answer AX, so an older or terser agent never makes every app look hung.
	Responding *bool `json:"responding,omitempty"`
}

// NotResponding reports whether the agent said the target app does not answer AX.
func (s Snapshot) NotResponding() bool { return s.Responding != nil && !*s.Responding }

// FindResult is the result of the `find` op.
type FindResult struct {
	Matches  []Node `json:"matches,omitempty"`
	Searched int    `json:"searched,omitempty"`
}

// Check is one actionability check and how long it waited. Detail is whatever the agent says
// about it, kept raw because its shape varies by check.
type Check struct {
	Check  string          `json:"check"`
	Ms     int             `json:"ms,omitempty"`
	Detail json.RawMessage `json:"detail,omitempty"`
}

// ActionResult is the result of `press`, `type`, `setValue` and `key`, which share one shape.
// The pointer fields are pointers where absent and empty differ: a read-back of "" is an
// empty field, no read-back is an element whose value could not be read.
type ActionResult struct {
	Target     *Node    `json:"target,omitempty"`
	Point      *Point   `json:"point,omitempty"`
	Tried      []Point  `json:"tried,omitempty"`
	Via        string   `json:"via,omitempty"`
	Checks     []Check  `json:"checks,omitempty"`
	WaitedMs   int      `json:"waitedMs,omitempty"`
	Notes      []string `json:"notes,omitempty"`
	Overlay    *Node    `json:"overlay,omitempty"`
	Before     *Tree    `json:"before,omitempty"`
	After      *Tree    `json:"after,omitempty"`
	Settled    bool     `json:"settled,omitempty"`
	SettledMs  int      `json:"settledMs,omitempty"`
	AppGone    bool     `json:"appGone,omitempty"`
	Typed      string   `json:"typed,omitempty"`
	ReadBack   *string  `json:"readBack,omitempty"`
	ReadBackOK *bool    `json:"readBackOK,omitempty"`
	Secret     bool     `json:"secret,omitempty"`
	ReResolved bool     `json:"reResolved,omitempty"`
}

// UnmarshalJSON decodes a result and, for a secure target, keeps only the length of what was
// typed and read back, should an agent ever send the text itself (catalog I15).
func (r *ActionResult) UnmarshalJSON(b []byte) error {
	type plain ActionResult
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*r = ActionResult(p)
	if !r.IsSecret() {
		return nil
	}
	r.Typed = lengthOnly(r.Typed)
	if r.ReadBack != nil {
		s := lengthOnly(*r.ReadBack)
		r.ReadBack = &s
	}
	return nil
}

// lengthOnly is a secret's text as SecretText, unless it already is one or is empty.
func lengthOnly(s string) string {
	if _, ok := secretChars(s); ok || s == "" {
		return s
	}
	return SecretText(utf8.RuneCountInString(s))
}

// IsSecret reports whether the action's target is a secure text field, by the result's own flag
// or by the target's state. No text about such an action shows what was typed or the value.
func (r ActionResult) IsSecret() bool {
	return r.Secret || (r.Target != nil && r.Target.Secret())
}

// ScrollAxes is a scroll position, each axis a fraction 0 to 1 or nil.
type ScrollAxes struct {
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
}

// ScrollResult is the result of the `scroll` op.
type ScrollResult struct {
	Container Node       `json:"container"`
	From      ScrollAxes `json:"from"`
	To        ScrollAxes `json:"to"`
	AtEnd     bool       `json:"atEnd,omitempty"`
	Steps     int        `json:"steps,omitempty"`
	Via       string     `json:"via,omitempty"`
	Target    *Node      `json:"target,omitempty"`
	Visible   bool       `json:"visible,omitempty"`
	Before    *Tree      `json:"before,omitempty"`
	After     *Tree      `json:"after,omitempty"`
}

// WaitResult is the result of the `waitFor` op.
type WaitResult struct {
	Satisfied bool    `json:"satisfied"`
	ElapsedMs int     `json:"elapsedMs"`
	Node      *Node   `json:"node,omitempty"`
	Value     *string `json:"value,omitempty"`
	Before    *Tree   `json:"before,omitempty"`
	After     *Tree   `json:"after,omitempty"`
}

// ExpectResult is the result of the `expect` op. Observed is whatever the property read as (a
// string, a number, a bool, or null when the target was not found), kept raw so it is recorded
// exactly.
type ExpectResult struct {
	Passed    bool            `json:"passed"`
	Observed  json.RawMessage `json:"observed,omitempty"`
	ElapsedMs int             `json:"elapsedMs"`
	Node      *Node           `json:"node,omitempty"`
}

// CaptureResult is the result of the `capture` op; the image itself comes as a BLOB.
type CaptureResult struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale,omitempty"`
	Rect   Rect    `json:"rect,omitzero"`
}

// The reasons an actionability check refuses (the `refused` error's detail.reason).
const (
	ReasonCovered      = "covered"
	ReasonHidden       = "hidden"
	ReasonOffscreen    = "offscreen"
	ReasonDisabled     = "disabled"
	ReasonUnstable     = "unstable"
	ReasonModal        = "modal"
	ReasonNotEditable  = "not_editable"
	ReasonNotFrontmost = "not_frontmost"
)

// Cause names what a refusal blames: the coverer, the modal sheet, the app that would not come
// to the front. It decodes from a bare ref string too, so an agent that names only the ref still
// decodes.
type Cause struct {
	Ref   string `json:"ref,omitempty"`
	Role  string `json:"role,omitempty"`
	Name  string `json:"name,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Where string `json:"where,omitempty"`
	App   string `json:"app,omitempty"`
	PID   int    `json:"pid,omitempty"`
}

// UnmarshalJSON accepts an object, a ref string or null.
func (c *Cause) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*c = Cause{}
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var ref string
		if err := json.Unmarshal(b, &ref); err != nil {
			return err
		}
		*c = Cause{Ref: ref}
		return nil
	}
	type plain Cause
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = Cause(p)
	return nil
}

// Refusal is the `detail` of a `refused` error: which check failed, what it blames, and how long
// the checks ran. Message is the error's own message, which the caller copies in for reasons this
// package does not know.
type Refusal struct {
	Reason   string  `json:"reason"`
	By       *Cause  `json:"by,omitempty"`
	WaitedMs int     `json:"waitedMs,omitempty"`
	Checks   []Check `json:"checks,omitempty"`
	Message  string  `json:"message,omitempty"`
}

// jsonText renders raw JSON from the guest (a check's detail, an observed value) for a line: a
// JSON string is quoted like any guest text, anything else is its compact JSON, which cannot hold
// a raw newline. Absent and null render as "".
func jsonText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strconv.Quote(s)
	}
	var out bytes.Buffer
	if json.Compact(&out, raw) != nil {
		return strconv.Quote(string(raw))
	}
	return out.String()
}
