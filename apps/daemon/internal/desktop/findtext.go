package desktop

import (
	"fmt"
	"strings"
)

// FindText renders a find's result: how many elements matched, then each as the outline shows it
// with the window it is in, so a match is acted on by its ref without a snapshot.
func FindText(a FindArgs, r FindResult) string {
	what := quote(a.Text)
	if a.Role != "" {
		what += " with role " + word(a.Role)
	}
	among := ""
	if r.Searched > 0 {
		among = " among " + plural(r.Searched, "element")
	}
	if len(r.Matches) == 0 {
		return fmt.Sprintf("no match for %s%s; try a shorter text or no role, or take a machine_snapshot to read what is there", what, among)
	}
	lines := []string{fmt.Sprintf("%s for %s%s", plural(len(r.Matches), "match"), what, among)}
	if a.Limit > 0 && len(r.Matches) >= a.Limit {
		lines[0] += fmt.Sprintf("; stopped at the limit of %d, so there may be more: pass a longer text or a role to narrow", a.Limit)
	}
	for _, n := range r.Matches {
		lines = append(lines, "  "+nodeLine(n)+windowOf(n))
	}
	return strings.Join(lines, "\n")
}

// windowOf is ` in window e1` for an element that names its window.
func windowOf(n Node) string {
	if n.Window == "" || n.Window == n.Ref || n.IsWindow() {
		return ""
	}
	return " in window " + word(n.Window)
}
