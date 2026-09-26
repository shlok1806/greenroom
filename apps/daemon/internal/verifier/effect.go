package verifier

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// Every verifier input reports its effect (ADR 0024, issue #116): the tool reads the frontmost
// app's UI again, through the same Manager path as machine_ui (so the read is a step and a
// look), and diffs it against the verifier's previous read. The result's last lines say what
// changed, that nothing did, or that the effect is unknown. The read's step record carries the
// effect (machine.Step.Effect), which the verdict review reads; the text is for the model only.

// The effect lines the model reads.
const (
	effectLine     = "effect: "
	effectNone     = "effect: no change detected"
	effectUnknown  = "effect: unknown"
	effectReadLine = "machine_ui step %d read the UI after this input."
	effectNoTree   = effectUnknown + " (no UI tree). Take a machine_screenshot to see what this input did."
	effectQuit     = "effect: %s is no longer running (it quit or crashed)."
	// effectQuitCite follows effectQuit once the read's step is known: the model cited the input
	// itself as a fail's evidence, which the review refuses (ADR 0028, bench run 2).
	effectQuitCite = " To fail a check that depends on this input, cite step %d as its evidence."
)

// maxEffectChanges caps the changed elements listed; maxEffectValue caps each value shown.
const (
	maxEffectChanges = 8
	maxEffectValue   = 60
)

// isInputTool reports whether name is one of the verifier's input tools.
func isInputTool(name string) bool {
	switch name {
	case "machine_click", "machine_type", "machine_key", "machine_scroll", "machine_input":
		return true
	}
	return false
}

// effectCheck reads the UI after input step of, which started at inputAt, and describes what
// changed since prev, the verifier's read before the input (hadPrev false when it had none).
// The read's step record stores the effect. When the app quit, the effect says to cite the read
// and names the app's newest crash report since the input (ADR 0028). It returns the lines the
// model reads, the effect's kind and the read's step (0 when the read never started).
func (v *Verifier) effectCheck(ctx context.Context, runID string, of int, inputAt time.Time, prev machine.UITree,
	hadPrev bool) (text, kind string, read int) {
	kind = machine.EffectUnknown
	_, _, _ = v.mgr.UIEffect(ctx, runID, machine.HolderVerifier, verifierUILimit, of,
		func(tree machine.UITree, err error) machine.StepEffect {
			var summary string
			kind, summary = judgeEffect(prev, hadPrev, tree, err)
			if kind == machine.EffectQuit {
				summary += fmt.Sprintf(effectQuitCite, tree.Step) + v.crashLine(ctx, runID, prev.App, inputAt)
			}
			text, read = summary, tree.Step
			if kind != machine.EffectUnknown || err == nil && len(tree.Elements) > 0 {
				text = fmt.Sprintf(effectReadLine, tree.Step) + "\n" + summary
			}
			return machine.StepEffect{Kind: kind, Summary: summary}
		})
	if text == "" { // the read never started: nothing was recorded
		return effectNoTree, machine.EffectUnknown, 0
	}
	return text, kind, read
}

// crashLine is the line naming app's newest crash report since inputAt, or "" when there is none
// or it cannot be read: the quit alone is the evidence, the report only says why.
func (v *Verifier) crashLine(ctx context.Context, runID, app string, inputAt time.Time) string {
	r, found, err := v.mgr.FindCrashReport(ctx, runID, app, time.Since(inputAt))
	if err != nil {
		v.log.Warn("verifier cannot look for a crash report", "runId", runID, "app", app, "err", err)
	}
	if !found {
		return ""
	}
	line := "\nCrash report: " + r.Path
	if r.Exception != "" {
		line += "\n  " + clip(r.Exception, 200)
	}
	return line
}

// judgeEffect is the effect kind and the lines the model reads, for a read after an input. The
// app quit when it was a running app in a usable read before (one with elements, as a diff
// needs) and is not one now (ADR 0028); a read that lists no running apps cannot say.
func judgeEffect(prev machine.UITree, hadPrev bool, tree machine.UITree, err error) (kind, text string) {
	switch {
	case err == nil && hadPrev && len(prev.Elements) > 0 && prev.App != "" && slices.Contains(prev.Apps, prev.App) &&
		len(tree.Apps) > 0 && !slices.Contains(tree.Apps, prev.App):
		return machine.EffectQuit, fmt.Sprintf(effectQuit, prev.App)
	case err != nil || len(tree.Elements) == 0:
		return machine.EffectUnknown, effectNoTree
	case !hadPrev || len(prev.Elements) == 0:
		return machine.EffectUnknown, effectUnknown + " (no earlier machine_ui read to compare). Call machine_ui to see the screen now."
	}
	return describeEffect(prev, tree)
}

// describeEffect is the effect kind, and the effect line and any change lines under it, for a
// read before and after.
func describeEffect(before, after machine.UITree) (kind, text string) {
	if before.App != after.App {
		return machine.EffectChanged, fmt.Sprintf("%sthe frontmost app is now %s (was %s). Call machine_ui before clicking by id.",
			effectLine, orDash(after.App), orDash(before.App))
	}
	lines, reordered := diffElements(before.Elements, after.Elements)
	if len(lines) == 0 {
		return machine.EffectNone, effectNone + ". If you expected a change, the input may have been lost: read machine_ui and " +
			"work out why before you rely on it."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d change", effectLine, len(lines))
	if len(lines) != 1 {
		b.WriteString("s")
	}
	for i, l := range lines {
		if i == maxEffectChanges {
			fmt.Fprintf(&b, "\n  and %d more", len(lines)-maxEffectChanges)
			break
		}
		b.WriteString("\n  " + l)
	}
	if reordered {
		b.WriteString("\nElement ids changed: call machine_ui before clicking by id.")
	}
	return machine.EffectChanged, b.String()
}

// diffElements pairs the elements of two reads by identity and lists what differs: changed
// values and states, elements that appeared (+) or went away (-), with the new read's ids.
// Identity is the role with the accessibility identifier, or else with the title and label; the
// value and states are what an input changes. reordered is true when ids no longer line up.
func diffElements(before, after []machine.UIElement) (lines []string, reordered bool) {
	// Exact matches first, so a changed element pairs only with a counterpart that changed too.
	exact := map[string][]int{}
	for i, e := range before {
		exact[signature(e)] = append(exact[signature(e)], i)
	}
	usedBefore := make([]bool, len(before))
	var restAfter []int
	moved := 0
	for j, e := range after {
		s := signature(e)
		if q := exact[s]; len(q) > 0 {
			i := q[0]
			exact[s] = q[1:]
			usedBefore[i] = true
			if math.Abs(before[i].X-e.X) > 0.01 || math.Abs(before[i].Y-e.Y) > 0.01 {
				moved++
			}
			continue
		}
		restAfter = append(restAfter, j)
	}
	byKey := map[string][]int{}
	for i, e := range before {
		if !usedBefore[i] {
			byKey[identity(e)] = append(byKey[identity(e)], i)
		}
	}
	var appeared []string
	for _, j := range restAfter {
		e := after[j]
		k := identity(e)
		if q := byKey[k]; len(q) > 0 {
			i := q[0]
			byKey[k] = q[1:]
			usedBefore[i] = true
			lines = append(lines, fmt.Sprintf("[%d] %s: %s", e.ID, elementName(e), strings.Join(stateChanges(before[i], e), ", ")))
			continue
		}
		appeared = append(appeared, fmt.Sprintf("+ [%d] %s", e.ID, describeElement(e)))
	}
	lines = append(lines, appeared...)
	gone := 0
	for i, e := range before {
		if !usedBefore[i] {
			gone++
			lines = append(lines, "- "+describeElement(e))
		}
	}
	if len(lines) == 0 && moved > 0 {
		lines = append(lines, fmt.Sprintf("%d elements moved (the view scrolled or resized)", moved))
	}
	return lines, len(appeared) > 0 || gone > 0
}

// identity is who an element is across two reads; signature adds what an input changes.
func identity(e machine.UIElement) string {
	if e.Identifier != "" {
		return e.Role + "\x00" + e.Subrole + "\x00id\x00" + e.Identifier
	}
	return e.Role + "\x00" + e.Subrole + "\x00" + e.Title + "\x00" + e.Label
}

func signature(e machine.UIElement) string {
	return identity(e) + "\x00" + e.Value + "\x00" + strconv.FormatBool(e.Selected) + strconv.FormatBool(e.Focused) +
		strconv.FormatBool(e.Disabled) + "\x00" + e.Rendered
}

// stateChanges names each difference between two reads of one element.
func stateChanges(a, b machine.UIElement) []string {
	var out []string
	if a.Value != b.Value {
		out = append(out, fmt.Sprintf("value %q -> %q", clip(a.Value, maxEffectValue), clip(b.Value, maxEffectValue)))
	}
	for _, st := range []struct {
		was, now bool
		word     string
	}{{a.Selected, b.Selected, "selected"}, {a.Focused, b.Focused, "focused"}, {a.Disabled, b.Disabled, "disabled"}} {
		switch {
		case st.now && !st.was:
			out = append(out, "now "+st.word)
		case st.was && !st.now:
			out = append(out, "no longer "+st.word)
		}
	}
	switch {
	case a.Rendered == b.Rendered:
	case b.Rendered == "":
		out = append(out, "now drawn")
	default:
		out = append(out, "now "+renderedMarkWords[b.Rendered])
	}
	return out
}

// renderedMarkWords names a machine.UIElement.Rendered mark in a change line.
var renderedMarkWords = map[string]string{
	machine.RenderedBlank:     "not drawn",
	machine.RenderedOffscreen: "offscreen",
	machine.RenderedCovered:   "covered",
}

// elementName is the role and the first name an element has, never its value, which a change
// line shows on its own.
func elementName(e machine.UIElement) string {
	role := e.Role
	if e.Subrole != "" && e.Subrole != e.Role {
		role += "/" + e.Subrole
	}
	for _, s := range []string{e.Title, e.Label, e.Identifier, e.Help} {
		if s != "" {
			return fmt.Sprintf("%s %q", role, clip(s, maxEffectValue))
		}
	}
	return role
}

func describeElement(e machine.UIElement) string {
	s := elementName(e)
	if e.Value != "" {
		s += fmt.Sprintf(" value=%q", clip(e.Value, maxEffectValue))
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
