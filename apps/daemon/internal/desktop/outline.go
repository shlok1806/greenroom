package desktop

import (
	"fmt"
	"strings"
)

// OutlineOptions tunes the outline.
type OutlineOptions struct {
	// Limit is the element limit the snapshot was taken with, named when the walk stopped at it.
	// Zero names none.
	Limit int
}

// Outline renders a snapshot as the model reads it (docs/21 section 5.2): a header, the
// attention items, one element a line indented by depth, and the reasons the tree may be
// incomplete. It is read on every snapshot, so it stays compact: one line per element, flags in
// one bracket, nothing a model does not act on.
func Outline(s Snapshot, o OutlineOptions) string {
	var b strings.Builder
	b.WriteString(headerLine(s))
	b.WriteByte('\n')
	if s.NotResponding() {
		fmt.Fprintf(&b, "%s is not responding to accessibility, so this tree may be empty or partial; look with machine_screenshot and try again shortly\n", appName(targetApp(s)))
	}
	writeAttention(&b, s.Attention)
	if len(s.Nodes) == 0 {
		b.WriteString("no elements\n")
	} else {
		fmt.Fprintf(&b, "act by ref (e.g. %s); a ref stays the same while its element lives, and each action returns what it changed\n", word(sampleRef(s)))
		base := minDepth(s.Nodes)
		for _, n := range s.Nodes {
			b.WriteString(strings.Repeat("  ", max(n.Depth-base, 0)))
			b.WriteString(nodeLine(n))
			b.WriteByte('\n')
		}
	}
	if s.TruncatedBy != "" {
		b.WriteString(truncatedLine(s.TruncatedBy, o.Limit))
		b.WriteByte('\n')
	}
	return b.String()
}

// targetApp is the app the snapshot is of: its own `app`, else the frontmost.
func targetApp(s Snapshot) AppInfo {
	if s.App != (AppInfo{}) {
		return s.App
	}
	return s.Frontmost
}

// headerLine is `screen 1024x768 · frontmost "TipSplit" (pid 812) · focused e4`, naming the
// snapshot's app too when it is not the frontmost one.
func headerLine(s Snapshot) string {
	parts := []string{fmt.Sprintf("screen %dx%d", s.Screen.Width, s.Screen.Height)}
	if s.Frontmost != (AppInfo{}) {
		parts = append(parts, "frontmost "+appWithPID(s.Frontmost))
	}
	if s.App != (AppInfo{}) && !sameApp(s.App, s.Frontmost) {
		parts = append(parts, "showing "+appWithPID(s.App)+", not frontmost")
	}
	if s.Focused != "" {
		parts = append(parts, "focused "+word(s.Focused))
	}
	return strings.Join(parts, " · ")
}

func appWithPID(a AppInfo) string {
	if a.PID != 0 && (a.Name != "" || a.BundleID != "") {
		return fmt.Sprintf("%s (pid %d)", appName(a), a.PID)
	}
	return appName(a)
}

// sameApp reports whether two app infos name one process.
func sameApp(a, b AppInfo) bool {
	if a.PID != 0 || b.PID != 0 {
		return a.PID == b.PID && a.Started == b.Started
	}
	return a.BundleID == b.BundleID && a.Name == b.Name
}

// writeAttention writes one line per attention item, or `attention: none`, so a sheet or a
// system alert is never lost in the middle of a tree.
func writeAttention(b *strings.Builder, items []Attention) {
	if len(items) == 0 {
		b.WriteString("attention: none\n")
		return
	}
	for _, a := range items {
		b.WriteString("attention: ")
		b.WriteString(attentionText(a))
		b.WriteByte('\n')
	}
}

// attentionText is `sheet e80 "Save changes?" of "TipSplit" (pid 812)`.
func attentionText(a Attention) string {
	kind := a.Kind
	if kind == "" {
		kind = "window"
	}
	s := word(kind)
	if a.Ref != "" {
		s += " " + word(a.Ref)
	}
	if a.Role != "" && !strings.EqualFold(a.Role, kind) {
		s += " " + word(a.Role)
	}
	if a.Name != "" {
		s += " " + quote(a.Name)
	}
	if a.App != "" {
		s += " of " + quote(a.App)
	}
	if a.PID != 0 {
		s += fmt.Sprintf(" (pid %d)", a.PID)
	}
	return s
}

// sampleRef is the ref the usage line shows: the focused element when it is in the tree, else the
// first element that is not a window.
func sampleRef(s Snapshot) string {
	for _, n := range s.Nodes {
		if n.Ref == s.Focused && n.Ref != "" {
			return n.Ref
		}
	}
	for _, n := range s.Nodes {
		if !n.IsWindow() && n.Ref != "" {
			return n.Ref
		}
	}
	return s.Nodes[0].Ref
}

func minDepth(nodes []Node) int {
	m := nodes[0].Depth
	for _, n := range nodes[1:] {
		m = min(m, n.Depth)
	}
	return m
}

// nodeLine is one element: `e4 TextField "Bill" value="84.00" [focused]`, or for a window
// `window e1 "TipSplit" (main, focused) 480x360 at (272,204)`.
func nodeLine(n Node) string {
	if n.IsWindow() {
		return windowLine(n)
	}
	parts := []string{Label(n)}
	if v := valueText(n); v != "" {
		parts = append(parts, v)
	}
	parts = append(parts, extraText(n)...)
	if s := scrollText(n.Scroll); s != "" {
		parts = append(parts, s)
	}
	if f := flags(n, true); len(f) > 0 {
		parts = append(parts, "["+strings.Join(f, ", ")+"]")
	}
	return strings.Join(parts, " ")
}

// extraText is the description, help and identifier when they add to the name.
func extraText(n Node) []string {
	var out []string
	if n.Desc != "" && n.Desc != n.Name {
		out = append(out, "desc="+quote(n.Desc))
	}
	if n.Help != "" && n.Help != n.Name {
		out = append(out, "help="+quote(n.Help))
	}
	if n.ID != "" {
		out = append(out, "id="+quote(n.ID))
	}
	return out
}

func windowLine(n Node) string {
	s := "window " + word(n.Ref)
	if n.Subrole != "" && n.Subrole != "StandardWindow" {
		s += " " + word(n.Subrole)
	}
	if n.Name != "" {
		s += " " + quote(n.Name)
	}
	st := orderedStates(n.States)
	if n.Edited {
		st = append(st, "edited")
	}
	if len(st) > 0 {
		s += " (" + strings.Join(st, ", ") + ")"
	}
	if !n.Frame.Empty() {
		s += fmt.Sprintf(" %.0fx%.0f at (%.0f,%.0f)", n.Frame.W(), n.Frame.H(), n.Frame.X(), n.Frame.Y())
	}
	if n.Document != "" {
		s += " document " + quote(n.Document)
	}
	if f := flags(n, false); len(f) > 0 {
		s += " [" + strings.Join(f, ", ") + "]"
	}
	return s
}

// truncatedLine says plainly why the tree stopped and how to see the rest.
func truncatedLine(by string, limit int) string {
	switch by {
	case TruncatedByLimit:
		if limit > 0 {
			return fmt.Sprintf("truncated: stopped at the limit of %d elements; pass window or ref to narrow, or a higher limit (max %d)", limit, SnapshotLimitMax)
		}
		return fmt.Sprintf("truncated: stopped at the element limit; pass window or ref to narrow, or a higher limit (max %d)", SnapshotLimitMax)
	case TruncatedByVisited:
		return "truncated: the walk visited as many elements as it may; pass window or ref to narrow"
	case TruncatedByDepth:
		return "truncated: the tree goes deeper than the walk; pass the ref of the deepest element shown to see below it"
	case TruncatedByTime:
		return "truncated: the walk ran out of time because the app answered slowly; pass window or ref to narrow"
	}
	return "truncated (" + word(by) + "): the tree is incomplete; pass window or ref to narrow"
}
