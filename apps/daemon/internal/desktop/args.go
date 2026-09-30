package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// The tool arguments MCP and the verifier share. Small types validate themselves (Validate); the
// per-tool argument structs fill their defaults, clamp their bounds and validate in Normalize.
// Every error names the field and says what to send, since a model reads it and tries again.

// ArgError is a bad tool argument: which field, and what to send instead.
type ArgError struct {
	Field string
	Msg   string
}

func (e *ArgError) Error() string { return e.Field + ": " + e.Msg }

func argErr(field, format string, a ...any) error {
	return &ArgError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// within puts a nested type's error under its parent field: `to.ref: ...` inside `scroll`.
func within(parent string, err error) error {
	var ae *ArgError
	if parent == "" || !errors.As(err, &ae) {
		return err
	}
	f := parent
	if ae.Field != "" && ae.Field != parent {
		f = parent + "." + ae.Field
	}
	return &ArgError{Field: f, Msg: ae.Msg}
}

// DecodeArgs decodes a tool call's JSON arguments into v (one of the argument types here, or a
// surface's own struct that holds them), turning the decoder's errors into ones that name the
// field and say what to send. Fields v does not have are ignored: a surface has arguments of its
// own, such as runId. Call the type's Normalize after it.
func DecodeArgs(raw []byte, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	return decodeError("arguments", json.Unmarshal(raw, v))
}

// decodeError turns an error of encoding/json into an ArgError: the field the decoder names,
// under parent when the value is nested, and what it must be. An ArgError passes through.
func decodeError(parent string, err error) error {
	var te *json.UnmarshalTypeError
	var se *json.SyntaxError
	var ae *ArgError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ae):
		return ae
	case errors.As(err, &te):
		field := te.Field // the path of keys to the value, as the caller wrote them
		switch {
		case field == "" && parent == "arguments":
			return argErr(parent, "must be a JSON object, not %s", jsonKind(te.Value))
		case field == "":
			return argErr(parent, "must be %s, not %s", goKind(te.Type), jsonKind(te.Value))
		case parent != "arguments":
			field = parent + "." + field
		}
		return argErr(field, "must be %s, not %s", goKind(te.Type), jsonKind(te.Value))
	case errors.As(err, &se), errors.Is(err, io.ErrUnexpectedEOF):
		return argErr(parent, "not valid JSON (%v); send one JSON object", err)
	}
	if key, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return argErr(parent, "has no key %s", key)
	}
	return argErr(parent, "%v", err)
}

// withHelp adds what to send to an ArgError that does not say.
func withHelp(err error, help string) error {
	var ae *ArgError
	if !errors.As(err, &ae) {
		return err
	}
	return &ArgError{Field: ae.Field, Msg: ae.Msg + "; " + help}
}

// isNull reports whether raw is JSON null or nothing at all: an argument that was not given.
func isNull(raw []byte) bool {
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

// fromAny is DecodeArgs for a value a surface already decoded (a string, a map, raw JSON): it is
// encoded again and decoded into v, so every form takes the one path.
func fromAny(field string, value, v any) error {
	var raw []byte
	switch x := value.(type) {
	case nil:
	case json.RawMessage:
		raw = x
	case []byte:
		raw = x
	default:
		b, err := json.Marshal(value)
		if err != nil {
			return argErr(field, "cannot be read (%v)", err)
		}
		raw = b
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("null")
	}
	return DecodeArgs(raw, v)
}

// ScrollToFrom reads machine_scroll's `to` from whatever a surface decoded it as.
func ScrollToFrom(value any) (ScrollTo, error) {
	var t ScrollTo
	if err := fromAny("to", value, &t); err != nil {
		return ScrollTo{}, err
	}
	return t, t.Validate()
}

// WaitTargetFrom reads a wait's or an expectation's `target` from whatever a surface decoded it as.
func WaitTargetFrom(value any) (WaitTarget, error) {
	var t WaitTarget
	if err := fromAny("target", value, &t); err != nil {
		return WaitTarget{}, err
	}
	return t, t.Validate()
}

// goKind names the JSON type a Go type decodes from, with its article.
func goKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.String:
		return "a string"
	case reflect.Slice, reflect.Array:
		return "a list"
	}
	return "an object"
}

// jsonKind names what the caller sent, from the decoder's word for it.
func jsonKind(v string) string {
	switch {
	case v == "string":
		return "a string"
	case v == "bool":
		return "true or false"
	case v == "array":
		return "a list"
	case v == "object":
		return "an object"
	case strings.HasPrefix(v, "number"):
		return "the " + v
	}
	return v
}

// Refs.

var (
	refPattern = regexp.MustCompile(`^e(0|[1-9][0-9]{0,8})$`)
	bareNumber = regexp.MustCompile(`^[0-9]+$`)
)

const refHelp = `refs look like "e17" and come from machine_snapshot, machine_find or an action's result`

// Ref is an element reference, `e<n>`. A bare number is refused, not read as a ref: machine_ui's
// element numbers are bare numbers too, and taking one as a ref would act on a different element.
type Ref string

// Validate reports a missing or malformed ref.
func (r Ref) Validate() error { return validateRef("ref", string(r)) }

func validateRef(field, s string) error {
	switch {
	case s == "":
		return argErr(field, "missing; pass a ref such as \"e17\" (%s)", refHelp)
	case refPattern.MatchString(s):
		return nil
	case bareNumber.MatchString(s):
		return argErr(field, "%q is not a ref; %s (machine_ui's element numbers are not refs)", s, refHelp)
	}
	return argErr(field, "%q is not a ref; %s", s, refHelp)
}

// Bounds.

// Bounds is a numeric argument's default and maximum. Zero means the default; a value over the
// maximum is clamped to it, since a model asking for a long wait wants the longest there is.
type Bounds struct {
	Field   string
	Default int
	Max     int
}

// The bounds of daemon ADR 0006 point 10. waitFor has no default in the ADR; 10 s is long enough
// for a typical UI change and leaves most of the 40 s cap for a wait asked for on purpose.
var (
	ActionTimeout = Bounds{Field: "timeoutMs", Default: 5000, Max: 30000}
	WaitTimeout   = Bounds{Field: "timeoutMs", Default: 10000, Max: 40000}
	ExpectTimeout = Bounds{Field: "timeoutMs", Default: 2000, Max: 40000}
	SnapshotLimit = Bounds{Field: "limit", Default: 250, Max: SnapshotLimitMax}
	FindLimit     = Bounds{Field: "limit", Default: 50, Max: 200}
)

// SnapshotLimitMax is the most elements one snapshot returns.
const SnapshotLimitMax = 1000

// Clamp applies the default and the maximum, and refuses a negative value.
func (b Bounds) Clamp(v int) (int, error) {
	switch {
	case v < 0:
		return 0, argErr(b.Field, "%d is negative; pass 0 or leave it out for the default of %d, at most %d", v, b.Default, b.Max)
	case v == 0:
		return b.Default, nil
	}
	return min(v, b.Max), nil
}

// Names.

// oneOf refuses a value outside names, listing them.
func oneOf(field, v string, names ...string) error {
	if slices.Contains(names, v) {
		return nil
	}
	return argErr(field, "%q is not one of %s", v, strings.Join(quoteAll(names), ", "))
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}

// The modifier and button names the helper posts, mirroring machine's validateActions (and
// helper/Input.swift's `flags` and `mouseButton`): an unknown name would be dropped or read as a left
// click, so a typo in cmd-Q would type a q (issue #31).
var (
	modifierNames = []string{"cmd", "command", "meta", "shift", "alt", "option", "opt", "ctrl", "control", "fn", "function"}
	buttonNames   = []string{"left", "right", "middle", "center"}
)

// normalizeMods validates modifier names and puts them in lower case, the form the agent reads.
func normalizeMods(field string, mods []string) ([]string, error) {
	if len(mods) == 0 {
		return nil, nil
	}
	out := make([]string, len(mods))
	for i, m := range mods {
		out[i] = strings.ToLower(m)
		if !slices.Contains(modifierNames, out[i]) {
			return nil, argErr(fmt.Sprintf("%s[%d]", field, i), "unknown modifier %q; use cmd, shift, alt, ctrl, fn (or command, meta, option, opt, control, function)", m)
		}
	}
	return out, nil
}

// Snapshot and find.

// SnapshotMode is what a snapshot includes.
type SnapshotMode string

// The snapshot modes.
const (
	ModeInteractive SnapshotMode = "interactive" // controls and text, the default
	ModeAll         SnapshotMode = "all"
	ModeText        SnapshotMode = "text"
)

// Validate refuses an unknown mode; empty is the default.
func (m SnapshotMode) Validate() error {
	if m == "" {
		return nil
	}
	return oneOf("mode", string(m), string(ModeInteractive), string(ModeAll), string(ModeText))
}

// SnapshotArgs are machine_snapshot's arguments.
type SnapshotArgs struct {
	App      string       `json:"app,omitempty"`
	Window   string       `json:"window,omitempty"`
	Ref      string       `json:"ref,omitempty"`
	Mode     SnapshotMode `json:"mode,omitempty"`
	Limit    int          `json:"limit,omitempty"`
	FullText []string     `json:"fullText,omitempty"`
}

// Normalize fills the defaults, clamps the limit and validates.
func (a *SnapshotArgs) Normalize() error {
	if err := a.Mode.Validate(); err != nil {
		return err
	}
	if a.Mode == "" {
		a.Mode = ModeInteractive
	}
	if a.Ref != "" {
		if err := validateRef("ref", a.Ref); err != nil {
			return err
		}
	}
	for i, r := range a.FullText {
		if err := validateRef(fmt.Sprintf("fullText[%d]", i), r); err != nil {
			return err
		}
	}
	var err error
	a.Limit, err = SnapshotLimit.Clamp(a.Limit)
	return err
}

// FindArgs are machine_find's arguments.
type FindArgs struct {
	Text             string `json:"text"`
	Role             string `json:"role,omitempty"`
	App              string `json:"app,omitempty"`
	IncludeOffscreen *bool  `json:"includeOffscreen,omitempty"`
	Limit            int    `json:"limit,omitempty"`
}

// Normalize fills the defaults, clamps the limit and validates.
func (a *FindArgs) Normalize() error {
	if a.Text == "" {
		return argErr("text", "missing; pass the text to find (a substring, or /regex/)")
	}
	if a.Text == "//" {
		return argErr("text", "the /regex/ is empty; put a pattern between the slashes, or pass plain text")
	}
	if a.IncludeOffscreen == nil {
		t := true
		a.IncludeOffscreen = &t
	}
	var err error
	a.Limit, err = FindLimit.Clamp(a.Limit)
	return err
}

// Actions.

// PressArgs are machine_press's arguments: a ref, or a point (fractions of the screen) with a
// reason, for content that has no ref.
type PressArgs struct {
	Ref       string   `json:"ref,omitempty"`
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Button    string   `json:"button,omitempty"`
	Count     int      `json:"count,omitempty"`
	Mods      []string `json:"mods,omitempty"`
	Via       string   `json:"via,omitempty"`
	Force     bool     `json:"force,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// The ways a press is delivered.
const (
	ViaPointer = "pointer"
	ViaAX      = "ax"
)

// Normalize fills the defaults, clamps the timeout, puts the button and modifiers in lower case
// and validates.
func (a *PressArgs) Normalize() error {
	point := a.X != nil || a.Y != nil
	switch {
	case a.Ref != "" && point:
		return argErr("ref", "pass a ref or x and y, not both; a ref is better when the element has one")
	case point && (a.X == nil || a.Y == nil):
		return argErr("x", "a point needs both x and y, fractions of the screen 0 to 1")
	case point && (!finite(*a.X) || !finite(*a.Y)):
		return argErr("x", "x and y must be numbers, fractions of the screen 0 to 1")
	case point && strings.TrimSpace(a.Reason) == "":
		return argErr("reason", "a press at a point needs a reason: say why the target has no ref (a canvas, a game); otherwise pass its ref")
	case !point:
		if err := validateRef("ref", a.Ref); err != nil {
			return err
		}
	}
	if a.Button != "" && !slices.Contains(buttonNames, strings.ToLower(a.Button)) {
		return argErr("button", "unknown button %q; use left, right or middle", a.Button)
	}
	a.Button = strings.ToLower(a.Button)
	switch {
	case a.Count == 0:
		a.Count = 1
	case a.Count < 0 || a.Count > 3:
		return argErr("count", "%d is out of range; pass 1 (a click), 2 (a double click) or 3", a.Count)
	}
	var err error
	if a.Mods, err = normalizeMods("mods", a.Mods); err != nil {
		return err
	}
	if a.Via == "" {
		a.Via = ViaPointer
	}
	if err := oneOf("via", a.Via, ViaPointer, ViaAX); err != nil {
		return err
	}
	if a.Via == ViaAX && point {
		return argErr("via", "\"ax\" presses an element and needs its ref; a point is pressed with the pointer")
	}
	a.TimeoutMs, err = ActionTimeout.Clamp(a.TimeoutMs)
	return err
}

// PressOp is the `press` op's arguments as the agent reads them (daemon ADR 0006): the point in
// guest points, where the tool takes fractions of the screen.
type PressOp struct {
	Ref       string   `json:"ref,omitempty"`
	Point     *Point   `json:"point,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Force     bool     `json:"force,omitempty"`
	Button    string   `json:"button,omitempty"`
	Count     int      `json:"count,omitempty"`
	Mods      []string `json:"mods,omitempty"`
	Via       string   `json:"via,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// Op turns normalized arguments into the op's, placing a point on screen s.
func (a PressArgs) Op(s Screen) PressOp {
	op := PressOp{Ref: a.Ref, Reason: a.Reason, Force: a.Force, Button: a.Button,
		Count: a.Count, Mods: a.Mods, Via: a.Via, TimeoutMs: a.TimeoutMs}
	if a.X != nil && a.Y != nil {
		p := s.Point(*a.X, *a.Y)
		op.Point = &p
	}
	return op
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// The ways text is typed (catalog I9).
const (
	// TypeViaUnicode posts each character as the event's Unicode string: exact whatever the
	// keyboard layout, but no key equivalents and no input method.
	TypeViaUnicode = "unicode"
	// TypeViaKeys posts virtual key codes with shift, as a US keyboard sends them.
	TypeViaKeys = "keys"
)

// The keys machine_type may press after the text.
const (
	SubmitReturn = "return"
	SubmitTab    = "tab"
)

// TypeArgs are machine_type's arguments. Without a ref it types into the focused element.
// PaceMs is the least time between two characters (daemon ADR 0006 point 7); nil is the default
// (Pace), and 0 is allowed, so it is a pointer.
type TypeArgs struct {
	Ref       string `json:"ref,omitempty"`
	Text      string `json:"text"`
	Replace   bool   `json:"replace,omitempty"`
	Submit    string `json:"submit,omitempty"`
	Via       string `json:"via,omitempty"`
	PaceMs    *int   `json:"paceMs,omitempty"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
}

// The typing pace (daemon ADR 0006 point 7): an app that is busy drops keys that come faster than
// a hand types, and 20 ms a key is still 50 characters a second.
const (
	DefaultPaceMs = 20
	MaxPaceMs     = 100
)

// Pace is the typing pace to send the agent: PaceMs after Normalize, else the default.
func (a TypeArgs) Pace() int {
	if a.PaceMs == nil {
		return DefaultPaceMs
	}
	return *a.PaceMs
}

// Normalize clamps the timeout and validates. Whitespace-only text is allowed and read back;
// empty text is refused, since typing nothing would report success (issue #126).
func (a *TypeArgs) Normalize() error {
	if a.Ref != "" {
		if err := validateRef("ref", a.Ref); err != nil {
			return err
		}
	}
	if a.Text == "" {
		return argErr("text", "missing; pass the characters to type (to clear a field, use machine_set_value with an empty value, or replace with the new text)")
	}
	if a.Submit != "" && a.Submit != SubmitReturn && a.Submit != SubmitTab {
		return argErr("submit", "%q is not one of \"return\", \"tab\"; leave it out to press nothing after typing", a.Submit)
	}
	if a.Via == "" {
		a.Via = TypeViaUnicode
	}
	if a.Via != TypeViaUnicode && a.Via != TypeViaKeys {
		return argErr("via", "%q is not one of \"unicode\", \"keys\"; leave it out for \"unicode\" (exact text on any keyboard layout), or pass \"keys\" when the app must see real key presses", a.Via)
	}
	switch {
	case a.PaceMs == nil:
	case *a.PaceMs < 0:
		return argErr("paceMs", "%d is negative; pass 0 to %d milliseconds between characters, or leave it out for %d", *a.PaceMs, MaxPaceMs, DefaultPaceMs)
	case *a.PaceMs > MaxPaceMs:
		p := MaxPaceMs
		a.PaceMs = &p
	}
	var err error
	a.TimeoutMs, err = ActionTimeout.Clamp(a.TimeoutMs)
	return err
}

// SetValueArgs are machine_set_value's arguments. An empty value is allowed: it clears the field.
type SetValueArgs struct {
	Ref       string `json:"ref"`
	Value     string `json:"value"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
}

// Normalize clamps the timeout and validates.
func (a *SetValueArgs) Normalize() error {
	if err := validateRef("ref", a.Ref); err != nil {
		return err
	}
	var err error
	a.TimeoutMs, err = ActionTimeout.Clamp(a.TimeoutMs)
	return err
}

// KeyArgs are machine_key's arguments. Without a ref the key goes to the frontmost app.
type KeyArgs struct {
	Key       string   `json:"key"`
	Mods      []string `json:"mods,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// Normalize clamps the timeout and validates. Key names are the helper's to judge; this only
// refuses an empty one.
func (a *KeyArgs) Normalize() error {
	if strings.TrimSpace(a.Key) == "" {
		return argErr("key", "missing; pass a key such as \"return\", \"escape\", \"tab\" or \"s\" (with mods [\"cmd\"] for cmd-S)")
	}
	var err error
	if a.Mods, err = normalizeMods("mods", a.Mods); err != nil {
		return err
	}
	if a.Ref != "" {
		if err := validateRef("ref", a.Ref); err != nil {
			return err
		}
	}
	a.TimeoutMs, err = ActionTimeout.Clamp(a.TimeoutMs)
	return err
}

// Scroll.

// ScrollTo is where machine_scroll goes: "top", "bottom", a ref (as a string or {"ref"}),
// {"pages": n} or {"by": dy}. Positive pages and by scroll down, as every tool says.
type ScrollTo struct {
	Edge  string // "top" or "bottom"
	Ref   string
	Pages float64
	By    float64
}

const scrollToHelp = `pass "top", "bottom", a ref such as "e45", {"pages": n} or {"by": points}`

// Scroll bounds: enough to cross any real list in one call, small enough to catch a unit mistake.
const (
	maxScrollPages = 100
	maxScrollBy    = 100000
)

// UnmarshalJSON accepts every form the tools document. An object with a key it does not know is
// refused, so {"page": 2} is not read as no scroll at all.
func (t *ScrollTo) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	*t = ScrollTo{}
	if isNull(b) {
		return nil // Validate says it is missing
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return decodeError("to", err)
		}
		if s == "top" || s == "bottom" {
			t.Edge = s
		} else {
			t.Ref = s
		}
		return nil
	}
	if b[0] != '{' {
		return argErr("to", "%s", scrollToHelp)
	}
	var o struct {
		Ref   string   `json:"ref"`
		Pages *float64 `json:"pages"`
		By    *float64 `json:"by"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return withHelp(decodeError("to", err), `pass {"ref": "e45"}, {"pages": n} or {"by": points}`)
	}
	set := 0
	if o.Ref != "" {
		t.Ref, set = o.Ref, set+1
	}
	if o.Pages != nil {
		t.Pages, set = *o.Pages, set+1
		if t.Pages == 0 {
			return argErr("to.pages", "0 scrolls nowhere; pass a positive number to scroll down or a negative one to scroll up")
		}
	}
	if o.By != nil {
		t.By, set = *o.By, set+1
		if t.By == 0 {
			return argErr("to.by", "0 scrolls nowhere; pass positive points to scroll down or negative to scroll up")
		}
	}
	if set > 1 {
		return argErr("to", "pass one of ref, pages or by, not several")
	}
	return nil
}

// MarshalJSON writes the agent's wire form (daemon ADR 0006): a ref always as {"ref"}.
func (t ScrollTo) MarshalJSON() ([]byte, error) {
	switch {
	case t.Edge != "":
		return json.Marshal(t.Edge)
	case t.Ref != "":
		return json.Marshal(map[string]string{"ref": t.Ref})
	case t.Pages != 0:
		return json.Marshal(map[string]float64{"pages": t.Pages})
	case t.By != 0:
		return json.Marshal(map[string]float64{"by": t.By})
	}
	return []byte("null"), nil
}

// Validate refuses a missing or out-of-range destination.
func (t ScrollTo) Validate() error {
	switch {
	case t.Edge != "":
		return oneOf("to", t.Edge, "top", "bottom")
	case t.Ref != "":
		return within("to", validateRef("ref", t.Ref))
	case t.Pages != 0:
		if !finite(t.Pages) || math.Abs(t.Pages) > maxScrollPages {
			return argErr("to.pages", "pass a number of pages between -%d and %d", maxScrollPages, maxScrollPages)
		}
		return nil
	case t.By != 0:
		if !finite(t.By) || math.Abs(t.By) > maxScrollBy {
			return argErr("to.by", "pass points between -%d and %d", maxScrollBy, maxScrollBy)
		}
		return nil
	}
	return argErr("to", "missing; %s", scrollToHelp)
}

// Describe says where the scroll goes, for a sentence: `to the bottom`, `e45 into view`.
func (t ScrollTo) Describe() string {
	switch {
	case t.Edge != "":
		return "to the " + t.Edge
	case t.Ref != "":
		return word(t.Ref) + " into view"
	case t.Pages != 0:
		return fmt.Sprintf("%g %s %s", math.Abs(t.Pages), pluralWord(math.Abs(t.Pages), "page"), upDown(t.Pages))
	case t.By != 0:
		return fmt.Sprintf("%g points %s", math.Abs(t.By), upDown(t.By))
	}
	return "nowhere"
}

func pluralWord(n float64, w string) string {
	if n == 1 {
		return w
	}
	return w + "s"
}

func upDown(v float64) string {
	if v < 0 {
		return "up"
	}
	return "down"
}

// ScrollArgs are machine_scroll's toolkit arguments; the old raw scroll (x, y, deltaX, deltaY
// and no ref) is the MCP layer's to keep.
type ScrollArgs struct {
	Ref       string   `json:"ref"`
	To        ScrollTo `json:"to"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// Normalize clamps the timeout and validates.
func (a *ScrollArgs) Normalize() error {
	if a.Ref == "" {
		return argErr("ref", "missing; pass the ref of a scroll container, or of any element inside one")
	}
	if err := validateRef("ref", a.Ref); err != nil {
		return err
	}
	if err := a.To.Validate(); err != nil {
		return err
	}
	var err error
	a.TimeoutMs, err = ActionTimeout.Clamp(a.TimeoutMs)
	return err
}

// Waits and expectations.

// WaitTarget is what a wait or an expectation watches: a ref, an element by text (and role), an
// app, a window by title, or the app going idle. It decodes from a ref string, "idle", or an
// object.
type WaitTarget struct {
	Ref    string `json:"ref,omitempty"`
	Text   string `json:"text,omitempty"`
	Role   string `json:"role,omitempty"`
	App    string `json:"app,omitempty"`
	Window string `json:"window,omitempty"`
	Idle   bool   `json:"idle,omitempty"`
}

// The kinds of wait target.
const (
	TargetRef    = "ref"
	TargetText   = "text"
	TargetApp    = "app"
	TargetWindow = "window"
	TargetIdle   = "idle"
)

const targetHelp = `pass a ref such as "e62", {"text": "Done", "role": "Button"}, {"app": "TipSplit"}, {"window": "Settings"} or {"idle": true}`

// UnmarshalJSON accepts a ref string, "idle", or an object; an object with a key it does not know
// is refused rather than read as no target.
func (t *WaitTarget) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	*t = WaitTarget{}
	if isNull(b) {
		return nil // Validate says it is missing
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return decodeError("target", err)
		}
		if s == TargetIdle {
			t.Idle = true
		} else {
			t.Ref = s
		}
		return nil
	}
	if b[0] != '{' {
		return argErr("target", "%s", targetHelp)
	}
	type plain WaitTarget
	var p plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return withHelp(decodeError("target", err), targetHelp)
	}
	*t = WaitTarget(p)
	return nil
}

// Kind is which of the target forms this is. App alone is an app target; with text or a window
// it only scopes them.
func (t WaitTarget) Kind() string {
	switch {
	case t.Idle:
		return TargetIdle
	case t.Ref != "":
		return TargetRef
	case t.Text != "":
		return TargetText
	case t.Window != "":
		return TargetWindow
	case t.App != "":
		return TargetApp
	}
	return ""
}

// Validate refuses an empty target, one naming two things, and a role without text.
func (t WaitTarget) Validate() error {
	n := 0
	for _, set := range []bool{t.Idle, t.Ref != "", t.Text != "", t.Window != ""} {
		if set {
			n++
		}
	}
	switch {
	case n == 0 && t.App == "":
		return argErr("target", "missing; %s", targetHelp)
	case n > 1:
		return argErr("target", "names more than one thing; pass one of ref, text, window or idle (app may scope text, window or idle)")
	case t.Ref != "" && t.App != "":
		return argErr("target.app", "a ref already names one element; leave app out")
	case t.Role != "" && t.Text == "":
		return argErr("target.role", "role narrows a text search; pass text with it")
	case t.Ref != "":
		return within("target", validateRef("ref", t.Ref))
	}
	return nil
}

// Describe names the target for a sentence.
func (t WaitTarget) Describe() string {
	switch t.Kind() {
	case TargetIdle:
		if t.App != "" {
			return quote(t.App)
		}
		return "the app"
	case TargetRef:
		return word(t.Ref)
	case TargetText:
		s := "an element"
		if t.Role != "" {
			s = "a " + word(t.Role)
		}
		return s + " with text " + quote(t.Text)
	case TargetWindow:
		return "window " + quote(t.Window)
	case TargetApp:
		return "app " + quote(t.App)
	}
	return "the target"
}

// WaitState is what a wait waits for.
type WaitState string

// The wait states.
const (
	WaitAppears    WaitState = "appears"
	WaitDisappears WaitState = "disappears"
	WaitEnabled    WaitState = "enabled"
	WaitDisabled   WaitState = "disabled"
	WaitFocused    WaitState = "focused"
	WaitChanges    WaitState = "changes"
	WaitValue      WaitState = "value"
)

var waitStates = []string{
	string(WaitAppears), string(WaitDisappears), string(WaitEnabled), string(WaitDisabled),
	string(WaitFocused), string(WaitChanges), string(WaitValue),
}

// Validate refuses an unknown state; empty is the default (appears).
func (s WaitState) Validate() error {
	if s == "" {
		return nil
	}
	return oneOf("state", string(s), waitStates...)
}

// The text comparisons.
const (
	OpEquals   = "equals"
	OpContains = "contains"
	OpMatches  = "matches"
	OpAtLeast  = "atLeast"
	OpAtMost   = "atMost"
)

// ValueMatch is a wait's value condition.
type ValueMatch struct {
	Op       string `json:"op"`
	Expected string `json:"expected"`
}

// Validate refuses an unknown op and an empty pattern. A pattern is the agent's to compile (it
// runs NSRegularExpression, not Go's regexp), so it is not compiled here.
func (m ValueMatch) Validate() error {
	if err := oneOf("value.op", m.Op, OpEquals, OpContains, OpMatches); err != nil {
		return err
	}
	if m.Op == OpMatches && m.Expected == "" {
		return argErr("value.expected", "an empty pattern matches anything; pass the pattern to match")
	}
	return nil
}

// WaitArgs are machine_wait_for's arguments.
type WaitArgs struct {
	Target    WaitTarget  `json:"target"`
	State     WaitState   `json:"state,omitempty"`
	Value     *ValueMatch `json:"value,omitempty"`
	TimeoutMs int         `json:"timeoutMs,omitempty"`
}

// Normalize fills the defaults, clamps the timeout and validates.
func (a *WaitArgs) Normalize() error {
	if err := a.Target.Validate(); err != nil {
		return err
	}
	if err := a.State.Validate(); err != nil {
		return err
	}
	switch {
	case a.Target.Idle && a.State != "" && a.State != WaitChanges:
		return argErr("state", "an idle wait has no state; leave state out")
	case a.Target.Idle:
		a.State = ""
	case a.State == "":
		a.State = WaitAppears
	}
	switch {
	case a.State == WaitValue && a.Value == nil:
		return argErr("value", "state \"value\" needs value: {\"op\": \"equals\", \"expected\": \"42\"} (op equals, contains or matches)")
	case a.State != WaitValue && a.Value != nil:
		return argErr("value", "value is only used with state \"value\"; set state to \"value\" or leave value out")
	case a.Value != nil:
		if err := a.Value.Validate(); err != nil {
			return err
		}
	}
	var err error
	a.TimeoutMs, err = WaitTimeout.Clamp(a.TimeoutMs)
	return err
}

// ExpectProperty is what an expectation reads.
type ExpectProperty string

// The expectation properties.
const (
	PropValue    ExpectProperty = "value"
	PropName     ExpectProperty = "name"
	PropExists   ExpectProperty = "exists"
	PropVisible  ExpectProperty = "visible"
	PropEnabled  ExpectProperty = "enabled"
	PropSelected ExpectProperty = "selected"
	PropCount    ExpectProperty = "count"
)

var expectProps = []string{
	string(PropValue), string(PropName), string(PropExists), string(PropVisible),
	string(PropEnabled), string(PropSelected), string(PropCount),
}

// Validate refuses an unknown property.
func (p ExpectProperty) Validate() error {
	if p == "" {
		return argErr("property", "missing; pass one of %s", strings.Join(quoteAll(expectProps), ", "))
	}
	return oneOf("property", string(p), expectProps...)
}

// kind is the type of value the property has: text, flag or count.
func (p ExpectProperty) kind() string {
	switch p {
	case PropValue, PropName:
		return "text"
	case PropCount:
		return "count"
	}
	return "flag"
}

// ExpectOp is how an expectation compares.
type ExpectOp string

// Validate refuses an op the property does not take.
func (o ExpectOp) Validate(p ExpectProperty) error {
	var ops []string
	switch p.kind() {
	case "text":
		ops = []string{OpEquals, OpContains, OpMatches}
	case "count":
		ops = []string{OpEquals, OpAtLeast, OpAtMost}
	default:
		ops = []string{OpEquals}
	}
	if slices.Contains(ops, string(o)) {
		return nil
	}
	return argErr("op", "%q does not apply to %s; use %s", string(o), string(p), strings.Join(quoteAll(ops), ", "))
}

// ExpectArgs are machine_expect's arguments. Expected is a string for value and name, a bool for
// the flags (default true), a whole number for count.
type ExpectArgs struct {
	Target    WaitTarget     `json:"target"`
	Property  ExpectProperty `json:"property"`
	Op        ExpectOp       `json:"op,omitempty"`
	Expected  any            `json:"expected,omitempty"`
	TimeoutMs int            `json:"timeoutMs,omitempty"`
}

// Normalize fills the defaults, clamps the timeout and validates.
func (a *ExpectArgs) Normalize() error {
	if err := a.Target.Validate(); err != nil {
		return err
	}
	if a.Target.Idle {
		return argErr("target", "idle is for machine_wait_for; expect needs an element, a window or an app")
	}
	if err := a.Property.Validate(); err != nil {
		return err
	}
	if a.Op == "" {
		a.Op = OpEquals
	}
	if err := a.Op.Validate(a.Property); err != nil {
		return err
	}
	if err := a.normalizeExpected(); err != nil {
		return err
	}
	var err error
	a.TimeoutMs, err = ExpectTimeout.Clamp(a.TimeoutMs)
	return err
}

func (a *ExpectArgs) normalizeExpected() error {
	switch a.Property.kind() {
	case "text":
		s, ok := a.Expected.(string)
		switch {
		case !ok:
			return argErr("expected", "%s compares text; pass a string", string(a.Property))
		case a.Op == OpMatches && s == "":
			return argErr("expected", "an empty pattern matches anything; pass the pattern to match")
		}
	case "count":
		n, ok := number(a.Expected)
		if !ok || n < 0 || n != math.Trunc(n) {
			return argErr("expected", "count compares a whole number of elements; pass one, such as 3")
		}
		a.Expected = int(n)
	default:
		if a.Expected == nil {
			a.Expected = true
		}
		if _, ok := a.Expected.(bool); !ok {
			return argErr("expected", "%s is true or false; pass a bool, or leave it out for true", string(a.Property))
		}
	}
	return nil
}

// number reads a JSON number however it was decoded.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, finite(n)
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil && finite(f)
	}
	return 0, false
}
