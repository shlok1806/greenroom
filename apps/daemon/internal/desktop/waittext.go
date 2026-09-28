package desktop

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ScrollText renders a scroll's result: `scrolled e20 ScrollArea "Items" y 0% -> 100% (at the
// end, 6 steps, wheel); e45 is now visible`, then what else changed. Elements moving into and out
// of view are what a scroll does, so they are left out of its change lines.
func ScrollText(r ScrollResult) string {
	how := []string{}
	if r.AtEnd {
		how = append(how, "at the end")
	}
	if r.Steps > 0 {
		how = append(how, plural(r.Steps, "step"))
	}
	if r.Via != "" {
		how = append(how, viaText(r.Via))
	}
	paren := ""
	if len(how) > 0 {
		paren = " (" + strings.Join(how, ", ") + ")"
	}
	var lead string
	if moved := axesMoved(r.From, r.To); moved != "" {
		lead = "scrolled " + Label(r.Container) + " " + moved + paren
	} else {
		lead = Label(r.Container) + " did not move" + positionText(r.To) + paren
	}
	if r.Target != nil {
		// Covered is not visible, whatever the agent's `visible` says: the Dock over the bottom of
		// a window's scroll area takes the press, not the target.
		if r.Visible && r.Target.Covered == nil {
			lead += "; " + Label(*r.Target) + " is now visible"
		} else {
			lead += "; " + Label(*r.Target) + " is still not visible"
			if f := visibilityFlags(*r.Target); len(f) > 0 {
				lead += " [" + strings.Join(f, ", ") + "]"
			}
		}
	}
	lines := []string{lead}
	for _, n := range r.Notes {
		lines = append(lines, "note: "+oneLine(n))
	}
	if r.Before != nil && r.After != nil {
		var other []Change
		for _, c := range Diff(*r.Before, *r.After) {
			if c.Kind == ChangeVisibility && c.Field != VisCovered {
				continue
			}
			other = append(other, c)
		}
		if len(other) > 0 {
			lines = append(lines, fmt.Sprintf("effect: %s in %s besides what scrolled into and out of view", plural(len(other), "change"), appOf(r.After, r.Before)))
			lines = append(lines, changeLines(other, "  ")...)
		}
	}
	return strings.Join(lines, "\n")
}

// axesMoved is `y 62% -> 100%` for each axis whose position changed.
func axesMoved(from, to ScrollAxes) string {
	var parts []string
	for _, ax := range []struct {
		name     string
		from, to *float64
	}{{"x", from.X, to.X}, {"y", from.Y, to.Y}} {
		if ax.from != nil && ax.to != nil && pct(*ax.from) != pct(*ax.to) {
			parts = append(parts, fmt.Sprintf("%s %s -> %s", ax.name, pct(*ax.from), pct(*ax.to)))
		}
	}
	return strings.Join(parts, ", ")
}

// positionText is ` (y 100%)`, where a container that did not move sits.
func positionText(p ScrollAxes) string {
	var parts []string
	if p.X != nil {
		parts = append(parts, "x "+pct(*p.X))
	}
	if p.Y != nil {
		parts = append(parts, "y "+pct(*p.Y))
	}
	if len(parts) == 0 {
		return ""
	}
	return " from " + strings.Join(parts, " ")
}

func viaText(via string) string {
	switch via {
	case "axScrollToVisible":
		return "AX scroll to visible"
	case "wheel":
		return "wheel"
	case "scrollBar":
		return "set the scroll bar"
	}
	return word(via)
}

// appOf is the quoted name of the app a tree belongs to.
func appOf(trees ...*Tree) string {
	if n := treeAppName(trees...); n != "" {
		return quote(n)
	}
	return "the app"
}

// WaitText renders a wait's result: `e62 StaticText "Result" appeared after 3.1 s, value "42"`,
// then the changes since the wait began.
func WaitText(a WaitArgs, r WaitResult) string {
	subject := a.Target.Describe()
	if r.Node != nil && a.Target.Kind() != TargetIdle {
		subject = Label(*r.Node)
	}
	took, limit := secs(r.ElapsedMs), secs(a.TimeoutMs)
	if a.TimeoutMs <= 0 {
		limit = took
	}
	secret := r.Node != nil && r.Node.Secret()
	var lead string
	if r.Satisfied {
		lead = subject + " " + waitMet(a, took, secret)
	} else {
		lead = subject + " " + waitMissed(a, limit, secret)
	}
	switch {
	case secret:
		lead += ", value " + SecretText(r.Node.Chars)
	case r.Value != nil:
		lead += ", value " + quote(*r.Value)
	}
	switch {
	case r.Satisfied:
	case a.TimeoutMs >= WaitTimeout.Max:
		lead += "; wait again to keep waiting, or look with machine_snapshot"
	default:
		lead += fmt.Sprintf("; wait longer (timeoutMs, at most %d) or look with machine_snapshot", WaitTimeout.Max)
	}
	lines := []string{lead}
	if r.Before != nil && r.After != nil {
		if ch := Diff(*r.Before, *r.After); len(ch) > 0 {
			lines = append(lines, fmt.Sprintf("since the wait began: %s in %s", plural(len(ch), "change"), appOf(r.After, r.Before)))
			lines = append(lines, changeLines(ch, "  ")...)
		}
	}
	return strings.Join(lines, "\n")
}

func waitMet(a WaitArgs, took string, secret bool) string {
	if a.Target.Idle {
		return "went idle after " + took
	}
	switch a.State {
	case WaitDisappears:
		return "went away after " + took
	case WaitEnabled:
		return "became enabled after " + took
	case WaitDisabled:
		return "became disabled after " + took
	case WaitFocused:
		return "got focus after " + took
	case WaitChanges:
		return "changed after " + took
	case WaitValue:
		return valueMatchText(a.Value, secret) + ": matched after " + took
	}
	return "appeared after " + took
}

func waitMissed(a WaitArgs, limit string, secret bool) string {
	if a.Target.Idle {
		return "was still changing after " + limit
	}
	switch a.State {
	case WaitDisappears:
		return "was still there after " + limit
	case WaitEnabled:
		return "did not become enabled within " + limit
	case WaitDisabled:
		return "did not become disabled within " + limit
	case WaitFocused:
		return "did not get focus within " + limit
	case WaitChanges:
		return "did not change within " + limit
	case WaitValue:
		return valueMatchText(a.Value, secret) + ": not matched within " + limit
	}
	return "did not appear within " + limit
}

// valueMatchText is the condition a value wait waits for. Against a secure field the expected
// text is a secret too, and only its length is said.
func valueMatchText(m *ValueMatch, secret bool) string {
	switch {
	case m == nil:
		return "value"
	case secret:
		return "value " + word(m.Op) + " " + SecretText(utf8.RuneCountInString(m.Expected))
	}
	return "value " + word(m.Op) + " " + quote(m.Expected)
}

// ExpectText renders an expectation's result: `expect e62 value equals "42": passed after 0.3 s
// (observed "42")`, or `...: FAILED after 2.0 s (observed "41")`.
func ExpectText(a ExpectArgs, r ExpectResult) string {
	verdict := "FAILED"
	if r.Passed {
		verdict = "passed"
	}
	observed, expected := jsonText(r.Observed), expectedText(a.Expected)
	switch {
	case r.Node != nil && r.Node.Secret() && a.Property == PropValue:
		observed = SecretText(r.Node.Chars)
		if s, ok := a.Expected.(string); ok {
			expected = SecretText(utf8.RuneCountInString(s))
		}
	case observed == "":
		observed = "nothing: the target was not found"
	}
	subject := a.Target.Describe()
	if r.Node != nil && a.Property != PropCount {
		subject = Label(*r.Node)
	}
	return fmt.Sprintf("expect %s %s: %s after %s (observed %s)", subject, claimText(a, expected), verdict, secs(r.ElapsedMs), observed)
}

// claimText is what an expectation claims: `value equals "42"`, `count atLeast 3`, and for the
// properties that are true or false, `enabled` or `not enabled`.
func claimText(a ExpectArgs, expected string) string {
	if on, ok := a.Expected.(bool); ok && a.Property.kind() == "flag" && (a.Op == "" || a.Op == OpEquals) {
		if on {
			return word(string(a.Property))
		}
		return "not " + word(string(a.Property))
	}
	return word(string(a.Property)) + " " + word(string(a.Op)) + " " + expected
}

// expectedText renders what the caller expected: strings quoted, numbers and bools bare.
func expectedText(v any) string {
	if s, ok := v.(string); ok {
		return quote(s)
	}
	if n, ok := number(v); ok {
		return fmt.Sprintf("%g", n)
	}
	return fmt.Sprintf("%v", v)
}
