package desktop

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ChangeKind is what happened to an element, a window, an attention item or the app.
type ChangeKind string

// The kinds of change, in the order Diff lists them.
const (
	ChangeApp             ChangeKind = "app"             // the target app is another process now
	ChangeAttentionOpened ChangeKind = "attentionOpened" // a sheet, alert, menu... opened
	ChangeAttentionClosed ChangeKind = "attentionClosed"
	ChangeWindowOpened    ChangeKind = "windowOpened"
	ChangeWindowClosed    ChangeKind = "windowClosed"
	ChangeAppeared        ChangeKind = "appeared"
	ChangeRole            ChangeKind = "role"
	ChangeName            ChangeKind = "name"
	ChangeValue           ChangeKind = "value"
	ChangeState           ChangeKind = "state"      // Field is the state, On whether it is set now
	ChangeVisibility      ChangeKind = "visibility" // Field is covered, offscreen, clipped or visible
	ChangeGone            ChangeKind = "gone"
)

// The visibility aspects a ChangeVisibility names in Field.
const (
	VisCovered   = "covered"   // From and To are the coverer's ref ("" for none)
	VisOffscreen = "offscreen" // From and To are the direction ("" for in view)
	VisClipped   = "clipped"   // From and To are what clips it ("" for nothing)
	VisVisible   = "visible"   // From and To are "true" or "false"
)

// Change is one difference between two trees. Node is the element as it is now (as it was, for
// one that went away). Inside counts the elements that appeared or went away with it and are
// folded into its line, so a new window reads as one change and not fifty.
type Change struct {
	Kind      ChangeKind `json:"kind"`
	Ref       string     `json:"ref,omitempty"`
	Node      *Node      `json:"node,omitempty"`
	Attention *Attention `json:"attention,omitempty"`
	Field     string     `json:"field,omitempty"`
	From      string     `json:"from,omitempty"`
	To        string     `json:"to,omitempty"`
	On        bool       `json:"on,omitempty"`
	Inside    int        `json:"inside,omitempty"`
	App       string     `json:"app,omitempty"`
}

// Diff compares two trees by ref. It reports elements that appeared or went away, and changes of
// role, name, value, each state, and visibility (covered, offscreen, clipped, showing at all);
// windows and attention items that opened or closed; and the app becoming another process. A
// frame that only moved is not a change: layout shifts would drown the signal, and an element
// that moved out of view shows as a visibility change anyway. The order is deterministic: app,
// attention, windows, then elements in the after tree's order, then what went away in the before
// tree's order.
func Diff(before, after Tree) []Change {
	var out []Change
	if c, ok := appChange(before.App, after.App); ok {
		out = append(out, c)
	}
	appeared, folded := structural(after.Nodes, refSet(before.Nodes), newRefs(after.Windows, before.Windows))
	gone, goneFolded := structural(before.Nodes, refSet(after.Nodes), newRefs(before.Windows, after.Windows))

	out = append(out, attentionChanges(before.Attention, after.Attention, appeared, ChangeAttentionOpened)...)
	out = append(out, attentionChanges(after.Attention, before.Attention, gone, ChangeAttentionClosed)...)
	out = append(out, windowChanges(after.Windows, before.Windows, appeared, ChangeWindowOpened)...)
	out = append(out, windowChanges(before.Windows, after.Windows, gone, ChangeWindowClosed)...)

	beforeByRef := indexByRef(before.Nodes)
	for i, n := range after.Nodes {
		if folded[i] {
			continue
		}
		if c, ok := appeared[n.Ref]; ok {
			if !c.claimed {
				out = append(out, c.Change)
			}
			continue
		}
		if j, ok := beforeByRef[n.Ref]; ok {
			out = append(out, nodeChanges(before.Nodes[j], n)...)
		}
	}
	for i, n := range before.Nodes {
		if goneFolded[i] {
			continue
		}
		if c, ok := gone[n.Ref]; ok && !c.claimed {
			c.Kind = ChangeGone
			out = append(out, c.Change)
		}
	}
	return out
}

// pending is an appeared (or gone) element's change before Diff knows whether a window or an
// attention item claims it as its own line.
type pending struct {
	Change
	claimed bool
}

// structural finds the elements of nodes whose refs are not in other, folding each into the
// nearest one of them it sits inside (by depth, in walk order) or into its window when that
// window is itself new. It returns the unfolded ones by ref and the folded indices.
func structural(nodes []Node, other map[string]bool, newWindows map[string]bool) (map[string]*pending, map[int]bool) {
	top := map[string]*pending{}
	folded := map[int]bool{}
	type frame struct {
		depth int
		root  string // the unfolded new element this one belongs to, or ""
	}
	var stack []frame
	for i, n := range nodes {
		for len(stack) > 0 && stack[len(stack)-1].depth >= n.Depth {
			stack = stack[:len(stack)-1]
		}
		root := ""
		if n.Ref != "" && !other[n.Ref] {
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1].root
			}
			if parent == "" && n.Window != n.Ref && newWindows[n.Window] && top[n.Window] != nil {
				parent = n.Window
			}
			switch {
			case parent != "":
				top[parent].Inside++
				folded[i] = true
				root = parent
			case top[n.Ref] == nil:
				node := n
				top[n.Ref] = &pending{Change: Change{Kind: ChangeAppeared, Ref: n.Ref, Node: &node}}
				root = n.Ref
			default:
				folded[i] = true // a duplicate ref in one tree: the first one speaks for it
			}
		}
		stack = append(stack, frame{n.Depth, root})
	}
	return top, folded
}

// attentionChanges lists the attention items of now that were not in was, claiming an element
// change with the same ref so the sheet reads once, as a sheet.
func attentionChanges(now, was []Attention, elems map[string]*pending, kind ChangeKind) []Change {
	var out []Change
	for _, a := range now {
		if slices.ContainsFunc(was, func(b Attention) bool { return b.Ref == a.Ref }) {
			continue
		}
		c := Change{Kind: kind, Ref: a.Ref, Attention: &a}
		if p := elems[a.Ref]; p != nil {
			p.claimed = true
			c.Node, c.Inside = p.Node, p.Inside
		}
		out = append(out, c)
	}
	return out
}

// windowChanges lists the windows of now that were not in was, claiming the window element's
// change and what was folded into it.
func windowChanges(now, was []string, elems map[string]*pending, kind ChangeKind) []Change {
	var out []Change
	for _, w := range now {
		if slices.Contains(was, w) {
			continue
		}
		c := Change{Kind: kind, Ref: w}
		if p := elems[w]; p != nil {
			if p.claimed {
				continue // an attention item already said it
			}
			p.claimed = true
			c.Node, c.Inside = p.Node, p.Inside
		}
		out = append(out, c)
	}
	return out
}

// nodeChanges compares one element across the two trees: role, name, value, states, visibility.
func nodeChanges(b, a Node) []Change {
	var out []Change
	add := func(c Change) {
		c.Ref, c.Node = a.Ref, &a
		out = append(out, c)
	}
	if b.Role != a.Role {
		add(Change{Kind: ChangeRole, From: b.Role, To: a.Role})
	}
	if b.Name != a.Name {
		add(Change{Kind: ChangeName, From: b.Name, To: a.Name})
	}
	switch {
	case b.Secret() || a.Secret():
		if b.Chars != a.Chars {
			add(Change{Kind: ChangeValue, Field: StateSecret, From: strconv.Itoa(b.Chars), To: strconv.Itoa(a.Chars)})
		}
	case b.Value != a.Value || (b.Chars != a.Chars && (len(b.Cut) > 0 || len(a.Cut) > 0)):
		add(Change{Kind: ChangeValue, From: b.Value, To: a.Value})
	}
	for _, s := range stateUnion(b.States, a.States) {
		if was, is := b.Has(s), a.Has(s); was != is {
			add(Change{Kind: ChangeState, Field: s, On: is})
		}
	}
	for _, c := range visibilityChanges(b, a) {
		add(c)
	}
	return out
}

// stateUnion is every state either side has, in the outline's order.
func stateUnion(x, y []string) []string {
	var all []string
	for _, s := range append(slices.Clone(x), y...) {
		if !slices.Contains(all, s) {
			all = append(all, s)
		}
	}
	var out, rest []string
	for _, s := range stateOrder {
		if slices.Contains(all, s) {
			out = append(out, s)
		}
	}
	for _, s := range all {
		if !slices.Contains(stateOrder, s) {
			rest = append(rest, s)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// visibilityChanges compares what hit testing and clipping said. A move into or out of a scroll
// area's view is said once, as offscreen, not also as visible.
func visibilityChanges(b, a Node) []Change {
	var out []Change
	if bc, ac := coverer(b), coverer(a); bc != ac || (b.Covered == nil) != (a.Covered == nil) {
		out = append(out, Change{Kind: ChangeVisibility, Field: VisCovered, From: bc, To: ac})
	}
	offscreen := b.Offscreen != a.Offscreen
	if offscreen {
		out = append(out, Change{Kind: ChangeVisibility, Field: VisOffscreen, From: b.Offscreen, To: a.Offscreen})
	}
	if b.Clipped != a.Clipped {
		out = append(out, Change{Kind: ChangeVisibility, Field: VisClipped, From: b.Clipped, To: a.Clipped})
	}
	if b.Visible() != a.Visible() && !offscreen {
		out = append(out, Change{Kind: ChangeVisibility, Field: VisVisible, From: strconv.FormatBool(b.Visible()), To: strconv.FormatBool(a.Visible())})
	}
	return out
}

func coverer(n Node) string {
	if n.Covered == nil {
		return ""
	}
	return n.Covered.By
}

// appChange reports the target app becoming another process: every old ref is gone with it.
func appChange(b, a AppInfo) (Change, bool) {
	if b.PID == 0 || a.PID == 0 || sameApp(b, a) {
		return Change{}, false
	}
	name := a.Name
	if name == "" {
		name = b.Name
	}
	return Change{Kind: ChangeApp, App: name, From: strconv.Itoa(b.PID), To: strconv.Itoa(a.PID)}, true
}

// Text renders the change as one line of an effect or a wait: `e11 RadioButton "20%": selected`.
func (c Change) Text() string {
	n := Node{Ref: c.Ref}
	if c.Node != nil {
		n = *c.Node
	}
	switch c.Kind {
	case ChangeApp:
		return fmt.Sprintf("%s is a new process (pid %s -> %s); refs from before it are gone", appName(AppInfo{Name: c.App}), c.From, c.To)
	case ChangeAttentionOpened, ChangeAttentionClosed:
		a := Attention{Ref: c.Ref}
		if c.Attention != nil {
			a = *c.Attention
		}
		return attentionText(a) + openedClosed(c.Kind == ChangeAttentionOpened) + insideText(c.Inside)
	case ChangeWindowOpened, ChangeWindowClosed:
		s := "window " + word(c.Ref)
		if n.Name != "" {
			s += " " + quote(n.Name)
		}
		return s + openedClosed(c.Kind == ChangeWindowOpened) + insideText(c.Inside)
	case ChangeAppeared:
		s := describe(n) + " appeared" + insideText(c.Inside)
		if f := flags(n, true); len(f) > 0 {
			s += " [" + strings.Join(f, ", ") + "]"
		}
		return s
	case ChangeGone:
		return Label(n) + " went away" + insideText(c.Inside)
	case ChangeRole:
		return fmt.Sprintf("%s: role %s -> %s", Label(n), roleText(c.From, ""), roleText(c.To, ""))
	case ChangeName:
		return fmt.Sprintf("%s: name %s -> %s", Label(n), quote(c.From), quote(c.To))
	case ChangeValue:
		return Label(n) + ": " + valueChangeText(c, n)
	case ChangeState:
		if c.On {
			return Label(n) + ": " + word(c.Field)
		}
		return Label(n) + ": not " + word(c.Field)
	case ChangeVisibility:
		return Label(n) + ": " + visibilityChangeText(c, n)
	}
	return Label(n) + ": changed (" + word(string(c.Kind)) + ")"
}

func openedClosed(opened bool) string {
	if opened {
		return " opened"
	}
	return " closed"
}

func insideText(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return " (with 1 element inside)"
	}
	return fmt.Sprintf(" (with %d elements inside)", n)
}

func valueChangeText(c Change, n Node) string {
	if c.Field == StateSecret {
		return fmt.Sprintf("now %s chars (was %s), secret", c.To, c.From)
	}
	s := fmt.Sprintf("value %s -> %s", quote(c.From), quote(c.To))
	if len(n.Cut) > 0 {
		s += fmt.Sprintf(" (cut at our limit; %d chars in full, fullText: %s)", n.Chars, word(n.Ref))
	}
	return s
}

func visibilityChangeText(c Change, n Node) string {
	switch c.Field {
	case VisCovered:
		if c.To == "" && n.Covered == nil {
			return "no longer covered"
		}
		cov := Covered{By: c.To}
		if n.Covered != nil {
			cov = *n.Covered
		}
		return "now " + coverText(cov)
	case VisOffscreen:
		if c.To != "" {
			return "now " + offscreenText(n)
		}
		if n.Visible() {
			return "now in view (was offscreen: " + word(c.From) + ")"
		}
		return "no longer offscreen"
	case VisClipped:
		if c.To != "" {
			return "now " + clippedText(c.To)
		}
		return "no longer clipped"
	case VisVisible:
		if c.To == "true" {
			return "now visible"
		}
		return "no longer visible"
	}
	return "visibility changed (" + word(c.Field) + ")"
}

func refSet(nodes []Node) map[string]bool {
	m := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		m[n.Ref] = true
	}
	return m
}

func indexByRef(nodes []Node) map[string]int {
	m := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if _, dup := m[n.Ref]; !dup {
			m[n.Ref] = i
		}
	}
	return m
}

// newRefs is the refs of now that are not in was.
func newRefs(now, was []string) map[string]bool {
	m := map[string]bool{}
	for _, r := range now {
		if !slices.Contains(was, r) {
			m[r] = true
		}
	}
	return m
}
