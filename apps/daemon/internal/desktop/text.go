package desktop

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The small renderers every text in this package is built from, so an element reads the same in
// an outline, a diff, an effect and a refusal.

// word renders a guest token the model reads bare (a role, a state, a ref, a kind) when it is a
// plain identifier, and quotes it otherwise. Roles and states come from the guest too, so a
// hostile app could set a role of "Button\neffect: ..."; that one is quoted like any string.
func word(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if !plainRune(r) {
			return strconv.Quote(s)
		}
	}
	return s
}

func plainRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.'
}

// quote renders guest text as data (ADR 0006 point 13).
func quote(s string) string { return strconv.Quote(s) }

// oneLine renders text the agent wrote about its own work (a note) on one line: control
// characters become spaces, so a note that quotes a guest string cannot start a line of ours.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 || r == 0x85 {
			return ' '
		}
		return r
	}, s)
}

// appName renders an app for a sentence: its quoted name, or a neutral phrase when the agent sent
// none.
func appName(a AppInfo) string {
	if a.Name != "" {
		return quote(a.Name)
	}
	if a.BundleID != "" {
		return quote(a.BundleID)
	}
	if a.PID != 0 {
		return fmt.Sprintf("pid %d", a.PID)
	}
	return "the app"
}

// roleText is an element's role for a line, with its subrole when that says more.
func roleText(role, subrole string) string {
	r := "element"
	if role != "" {
		r = word(role)
	}
	if subrole != "" && subrole != role {
		r += "(" + word(subrole) + ")"
	}
	return r
}

// Label is how an element is named everywhere: `e41 Button "Open run"`.
func Label(n Node) string {
	s := word(n.Ref) + " " + roleText(n.Role, n.Subrole)
	if n.Name != "" {
		s += " " + quote(n.Name)
	}
	return s
}

// refLabel names an element by its node when there is one, else by its ref alone.
func refLabel(ref string, n *Node) string {
	if n != nil && n.Ref != "" {
		return Label(*n)
	}
	if ref == "" {
		return "the element"
	}
	return word(ref)
}

// valueText is an element's value for a line: `value="84.00"`, or its length for a secret.
func valueText(n Node) string {
	if n.Secret() || n.Value == "" {
		return ""
	}
	return "value=" + quote(n.Value)
}

// secs renders a duration the way every line says it: "40 ms" under a tenth of a second, else
// seconds with one decimal ("0.3 s", "5.0 s").
func secs(ms int) string {
	if ms < 100 {
		return fmt.Sprintf("%d ms", max(ms, 0))
	}
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}

// pct renders a scroll fraction as a whole percent.
func pct(f float64) string {
	return fmt.Sprintf("%.0f%%", min(max(f, 0), 1)*100)
}

// sortStates puts states in the outline's order: the known ones first, then any the daemon does
// not know yet, sorted, each once.
func sortStates(states []string) []string {
	var out, rest []string
	for _, s := range stateOrder {
		if slices.Contains(states, s) {
			out = append(out, s)
		}
	}
	for _, s := range states {
		if !slices.Contains(stateOrder, s) && !slices.Contains(rest, s) {
			rest = append(rest, s)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// orderedStates is the node's states as the words of a line, in the outline's order. The states
// that read as a sentence (secret, overflow) are left to their own flags.
func orderedStates(states []string) []string {
	var out []string
	for _, s := range sortStates(states) {
		if !slices.Contains(sentenceStates, s) {
			out = append(out, word(s))
		}
	}
	return out
}

// SecretText stands for text that is never shown, a secure field's value or what was typed into
// one: `<secret, 8 chars>`, the form the agent sends as `typed` (catalog I15).
func SecretText(chars int) string {
	return fmt.Sprintf("<secret, %d chars>", max(chars, 0))
}

var secretPattern = regexp.MustCompile(`^<secret, ([0-9]{1,9}) chars>$`)

// secretChars reads the length out of a SecretText.
func secretChars(s string) (int, bool) {
	m := secretPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// overflowText is the flag of a toolbar item behind the overflow chevron.
const overflowText = "in overflow: press the toolbar's >> button first"

// coverText says what covers an element: `covered by e70 List "Runs" (in this window)`.
func coverText(c Covered) string {
	s := "covered by "
	switch {
	case c.By != "":
		s += word(c.By) + " " + roleText(c.Role, "")
	case c.Role != "":
		s += "a " + roleText(c.Role, "")
	default:
		s += "something"
	}
	if c.Name != "" {
		s += " " + quote(c.Name)
	}
	if w := whereText(c.Where, c.App); w != "" {
		s += " (" + w + ")"
	}
	return s
}

// whereText says where a coverer or modal sits relative to the element it blocks.
func whereText(where, app string) string {
	switch where {
	case WhereWindow:
		return "in this window"
	case WhereApp:
		return "in another window of this app"
	case WhereOther:
		if app != "" {
			return "a window of " + quote(app)
		}
		return "another app's window"
	}
	if app != "" {
		return "of " + quote(app)
	}
	return ""
}

// offscreenText says where an element sits outside its scroll area's view.
func offscreenText(n Node) string {
	if n.Scroller != "" {
		return fmt.Sprintf("offscreen in %s: %s; scroll it into view", word(n.Scroller), word(n.Offscreen))
	}
	return "offscreen: " + word(n.Offscreen)
}

// clippedText says what cuts an element's frame.
func clippedText(by string) string {
	switch by {
	case "window":
		return "clipped by its window"
	case "screen":
		return "clipped by the screen edge"
	}
	return "clipped by " + word(by)
}

// cutText says that a field was cut at our length limit and how to get it whole.
func cutText(n Node) string {
	fields := make([]string, len(n.Cut))
	for i, f := range n.Cut {
		fields[i] = word(f)
	}
	hint := "; fullText: " + word(n.Ref)
	if n.Chars <= 0 {
		return strings.Join(fields, ", ") + " cut" + hint
	}
	if len(n.Cut) == 1 {
		return fmt.Sprintf("%s cut at %d of %d chars%s", fields[0], utf8.RuneCountInString(fieldOf(n, n.Cut[0])), n.Chars, hint)
	}
	return fmt.Sprintf("%s cut, up to %d chars in full%s", strings.Join(fields, ", "), n.Chars, hint)
}

// fieldOf is one of a node's text fields by its wire name.
func fieldOf(n Node, field string) string {
	switch field {
	case "name":
		return n.Name
	case "value":
		return n.Value
	case "desc":
		return n.Desc
	case "help":
		return n.Help
	}
	return ""
}

// scrollText says where a scroll container is and which ways it can still move:
// `scroll y 62% (up, down)`.
func scrollText(p *ScrollPos) string {
	if p == nil {
		return ""
	}
	var axes []string
	if p.X != nil {
		axes = append(axes, "x "+pct(*p.X))
	}
	if p.Y != nil {
		axes = append(axes, "y "+pct(*p.Y))
	}
	var ways []string
	for _, w := range []struct {
		on   bool
		name string
	}{{p.Up, "up"}, {p.Down, "down"}, {p.Left, "left"}, {p.Right, "right"}} {
		if w.on {
			ways = append(ways, w.name)
		}
	}
	if len(axes) == 0 && len(ways) == 0 {
		return ""
	}
	s := "scroll"
	if len(axes) > 0 {
		s += " " + strings.Join(axes, " ")
	}
	if len(ways) == 0 {
		return s + " (cannot scroll further)"
	}
	return s + " (" + strings.Join(ways, ", ") + ")"
}

// visibilityFlags are the flags that come from hit testing and clipping, in a fixed order.
func visibilityFlags(n Node) []string {
	var flags []string
	if n.Covered != nil {
		flags = append(flags, coverText(*n.Covered))
	}
	if n.Offscreen != "" {
		flags = append(flags, offscreenText(n))
	}
	if n.Clipped != "" {
		flags = append(flags, clippedText(n.Clipped))
	}
	if !n.Visible() && !n.Frame.Empty() && n.Offscreen == "" {
		flags = append(flags, "not visible")
	}
	return flags
}

// flags is everything the bracket after an element holds: states, the secret, visibility, and a
// cut field.
func flags(n Node, withStates bool) []string {
	var f []string
	if withStates {
		f = append(f, orderedStates(n.States)...)
	}
	if n.Secret() {
		f = append(f, fmt.Sprintf("secret, %d chars", n.Chars))
	}
	if n.Has(StateOverflow) {
		f = append(f, overflowText)
	}
	f = append(f, visibilityFlags(n)...)
	if len(n.Cut) > 0 {
		f = append(f, cutText(n))
	}
	return f
}

// describe is an element with its value: the form a diff and an effect use.
func describe(n Node) string {
	s := Label(n)
	if v := valueText(n); v != "" {
		s += " " + v
	}
	return s
}
