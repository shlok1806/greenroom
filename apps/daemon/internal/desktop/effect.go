package desktop

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxChangeLines caps the changes an effect or a wait lists; the rest are counted in one
// "and N more" line, so a window that redrew everything does not flood the model.
const MaxChangeLines = 12

// EffectKind is what an action did, as a step records it (daemon ADR 0006 point 8). A refusal is
// not an effect: it is the step's error.
type EffectKind string

// The effect kinds.
const (
	EffectChanged EffectKind = "changed"
	EffectNone    EffectKind = "none"
	EffectUnknown EffectKind = "unknown"
)

// Effect is an action's own effect, computed from its before and after trees.
type Effect struct {
	Kind      EffectKind `json:"kind"`
	App       string     `json:"app,omitempty"`
	Changes   []Change   `json:"changes,omitempty"`
	AppGone   bool       `json:"appGone,omitempty"`
	Settled   bool       `json:"settled"`
	SettledMs int        `json:"settledMs,omitempty"`
	// Reason says why an unknown effect could not be verified.
	Reason string `json:"reason,omitempty"`
}

// EffectOf computes an action's effect. "changed" is never inferred from pixels: without an after
// tree the effect is unknown.
func EffectOf(r ActionResult) Effect {
	e := Effect{App: treeAppName(r.Before, r.After), Settled: r.Settled, SettledMs: r.SettledMs}
	switch {
	case r.AppGone:
		e.Kind, e.AppGone = EffectChanged, true
	case r.After == nil:
		e.Kind, e.Reason = EffectUnknown, "no tree after the action: the app stopped answering accessibility or has no accessibility content"
	case r.Before == nil:
		e.Kind, e.Reason = EffectUnknown, "no tree from before the action to compare with"
	case emptyTree(*r.Before) && emptyTree(*r.After):
		e.Kind, e.Reason = EffectUnknown, "the app shows no accessibility content to compare"
	default:
		e.Changes = Diff(*r.Before, *r.After)
		e.Kind = EffectNone
		if len(e.Changes) > 0 {
			e.Kind = EffectChanged
		}
	}
	return e
}

func emptyTree(t Tree) bool { return len(t.Nodes) == 0 && len(t.Windows) == 0 && len(t.Attention) == 0 }

// treeAppName is the target app's name from whichever tree names it.
func treeAppName(trees ...*Tree) string {
	for _, t := range slices.Backward(trees) {
		if t != nil && t.App.Name != "" {
			return t.App.Name
		}
	}
	return ""
}

func (e Effect) app() string {
	if e.App == "" {
		return "the app"
	}
	return quote(e.App)
}

// Text renders the effect: `effect: 3 changes in "TipSplit"` and a line per change,
// `effect: no change in "TipSplit" after 0.3 s`, or `effect: unverifiable (...)`.
func (e Effect) Text() string {
	switch {
	case e.AppGone:
		return fmt.Sprintf("effect: %s is no longer running (it quit or crashed)", e.app())
	case e.Kind == EffectUnknown:
		return fmt.Sprintf("effect: unverifiable (%s); look with machine_screenshot", e.Reason)
	case e.Kind == EffectNone:
		if !e.Settled {
			return fmt.Sprintf("effect: no change in %s after %s, though it had not settled; something may still be coming", e.app(), secs(e.SettledMs))
		}
		return fmt.Sprintf("effect: no change in %s after %s", e.app(), secs(e.SettledMs))
	}
	lines := []string{fmt.Sprintf("effect: %s in %s", plural(len(e.Changes), "change"), e.app())}
	lines = append(lines, changeLines(e.Changes, "  ")...)
	if !e.Settled {
		lines = append(lines, fmt.Sprintf("  (still changing after %s; machine_wait_for {\"idle\": true} before relying on this)", secs(e.SettledMs)))
	}
	return strings.Join(lines, "\n")
}

// changeLines renders changes one a line, capped at MaxChangeLines.
func changeLines(changes []Change, indent string) []string {
	var out []string
	for i, c := range changes {
		if i == MaxChangeLines && len(changes) > MaxChangeLines+1 {
			out = append(out, fmt.Sprintf("%sand %d more", indent, len(changes)-i))
			break
		}
		out = append(out, indent+c.Text())
	}
	return out
}

// plural is `1 change`, `3 changes`, `2 matches`.
func plural(n int, noun string) string {
	switch {
	case n == 1:
		return "1 " + noun
	case strings.HasSuffix(noun, "ch"):
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// The action ops that share ActionResult.
const (
	OpPress    = "press"
	OpType     = "type"
	OpSetValue = "setValue"
	OpKey      = "key"
)

// Action is what the caller asked for, which the result alone does not say (the key, the button,
// whether a ref was named). The texts combine it with the result.
type Action struct {
	Op      string
	Ref     string // the ref the call named; "" for a point, the focus or the frontmost app
	Button  string
	Count   int
	Mods    []string
	Text    string // type: the text asked for
	Via     string // type: TypeViaUnicode or TypeViaKeys
	Replace bool
	Submit  string
	Value   string // setValue
	Key     string
}

// Redacted is the action as a step may record it: when the result says the target is a secure
// text field, the typed text and the value set are replaced by SecretText, so a password never
// reaches steps.jsonl (catalog I15). Any other action comes back unchanged.
func (a Action) Redacted(r ActionResult) Action {
	if !r.IsSecret() {
		return a
	}
	if a.Text != "" {
		a.Text = SecretText(utf8.RuneCountInString(a.Text))
	}
	if a.Op == OpSetValue || a.Value != "" {
		a.Value = SecretText(utf8.RuneCountInString(a.Value))
	}
	return a
}

// ActionText is everything a model reads after an action: the lead line, how long the checks
// waited, the agent's notes, an overlay, and the effect.
func ActionText(a Action, r ActionResult, s Screen) string {
	now := currentRef(a, r)
	if now != "" && r.Target != nil {
		// The lead names the element by the ref the snapshots and the effect below use.
		t := *r.Target
		t.Ref = now
		r.Target = &t
	}
	lines := []string{LeadLine(a, r, s)}
	if w := waitedLine(r); w != "" {
		lines = append(lines, w)
	}
	switch {
	case r.ReResolved && now != "":
		lines = append(lines, fmt.Sprintf("note: %s was re-resolved to %s (its element was rebuilt)", word(a.Ref), word(now)))
	case r.ReResolved:
		lines = append(lines, fmt.Sprintf("note: %s was re-resolved (its element was rebuilt; it was matched again by fingerprint, uniquely)", word(a.Ref)))
	case now != "":
		lines = append(lines, fmt.Sprintf("note: %s is listed as %s now (its element was rebuilt); use %s from here on", word(a.Ref), word(now), word(now)))
	}
	for _, n := range r.Notes {
		lines = append(lines, "note: "+oneLine(n))
	}
	if r.Overlay != nil {
		lines = append(lines, fmt.Sprintf("overlay: after the input the point hits %s, not the target; something opened over it or took the input", describe(*r.Overlay)))
	}
	lines = append(lines, EffectOf(r).Text())
	return strings.Join(lines, "\n")
}

// currentRef is the ref the walks list an action's target under when that is not the ref the
// call named, else "". Either the agent re-resolved the named ref to a rebuilt element that
// already had a ref of its own (it sends the target under that ref), or the named ref's old
// element still answers (a SwiftUI view rebuilt in place can keep its old element alive) while
// the before tree lists the rebuilt one under another ref: the one element of the target's
// window with its role, name and frame, where the named ref is not listed at all.
func currentRef(a Action, r ActionResult) string {
	if a.Ref == "" || r.Target == nil {
		return ""
	}
	if t := r.Target.Ref; t != "" && t != a.Ref {
		return t
	}
	if r.Before == nil || r.Target.Frame.Empty() {
		return ""
	}
	found := ""
	for _, n := range r.Before.Nodes {
		if n.Ref == a.Ref {
			return ""
		}
		if n.Role != r.Target.Role || n.Name != r.Target.Name || n.Frame != r.Target.Frame || n.Window != r.Target.Window {
			continue
		}
		if found != "" {
			return ""
		}
		found = n.Ref
	}
	return found
}

// LeadLine is the first line of an action's result, saying what was done to what:
// `pressed e11 RadioButton "20%" at (0.596, 0.467) (pointer click, point 1 of 9)`.
func LeadLine(a Action, r ActionResult, s Screen) string {
	switch a.Op {
	case OpType:
		return typeLead(a, r)
	case OpSetValue:
		return setValueLead(a, r)
	case OpKey:
		return keyLead(a, r)
	}
	return pressLead(a, r, s)
}

func pressLead(a Action, r ActionResult, s Screen) string {
	if r.Via == "ax" {
		return fmt.Sprintf("pressed %s (AX action, not a pointer event)", refLabel(a.Ref, r.Target))
	}
	how := pointerHow(a.Button, a.Count, a.Mods)
	at := ""
	if r.Point != nil {
		x, y := r.Point.Fractions(s)
		at = fmt.Sprintf("(%.3f, %.3f)", x, y)
	}
	if a.Ref == "" {
		switch {
		case r.Target != nil:
			return fmt.Sprintf("pressed at %s on %s (%s)", at, Label(*r.Target), how)
		case at != "":
			return fmt.Sprintf("pressed at %s (%s; nothing with a ref there)", at, how)
		}
		return fmt.Sprintf("pressed (%s)", how)
	}
	s2 := "pressed " + refLabel(a.Ref, r.Target)
	if at != "" {
		s2 += " at " + at
	}
	if n := pointIndex(r); n > 0 {
		return fmt.Sprintf("%s (%s, point %d of 9)", s2, how, n)
	}
	return fmt.Sprintf("%s (%s)", s2, how)
}

// pointIndex is which of the up to nine hit-tested points received the event (1-based), or 0.
func pointIndex(r ActionResult) int {
	if r.Point == nil || len(r.Tried) == 0 {
		return 0
	}
	if i := slices.Index(r.Tried, *r.Point); i >= 0 {
		return i + 1
	}
	return len(r.Tried)
}

// pointerHow is `pointer click`, `pointer double right click with cmd+shift`.
func pointerHow(button string, count int, mods []string) string {
	s := "pointer "
	switch count {
	case 0, 1:
	case 2:
		s += "double "
	case 3:
		s += "triple "
	default:
		s += fmt.Sprintf("%d-times ", count)
	}
	if b := strings.ToLower(button); b != "" && b != "left" {
		s += word(b) + " "
	}
	s += "click"
	if len(mods) > 0 {
		s += " with " + combo(mods, "")
	}
	return s
}

// combo is `cmd+shift+s`.
func combo(mods []string, key string) string {
	parts := make([]string, 0, len(mods)+1)
	for _, m := range mods {
		parts = append(parts, word(strings.ToLower(m)))
	}
	if key != "" {
		parts = append(parts, word(key))
	}
	return strings.Join(parts, "+")
}

func typeLead(a Action, r ActionResult) string {
	secret := r.IsSecret()
	what := quote(typedText(a, r))
	if secret {
		what = secretTyped(a.Text, r.Typed)
	}
	into := "into " + refLabel(a.Ref, r.Target)
	if a.Ref == "" {
		into = "into the focused element"
		if r.Target != nil {
			into += " " + Label(*r.Target)
		}
	}
	s := "typed " + what + " " + into
	if a.Via == TypeViaKeys {
		s += " as key presses"
	}
	if a.Replace {
		s += ", replacing its value"
	}
	if a.Submit != "" {
		s += ", then pressed " + word(a.Submit)
	}
	return s + readBackText(r, secret, a.Replace, "what was typed")
}

// typedText is what the agent says it posted, else what the caller asked for.
func typedText(a Action, r ActionResult) string {
	if r.Typed != "" {
		return r.Typed
	}
	return a.Text
}

// secretTyped stands for text typed into a secure field. The agent's own count is preferred,
// since its read-back compares against that; the text itself is never rendered.
func secretTyped(asked, typed string) string {
	if n, ok := secretChars(typed); ok {
		return SecretText(n)
	}
	if asked != "" {
		return SecretText(utf8.RuneCountInString(asked))
	}
	return "<secret>"
}

func setValueLead(a Action, r ActionResult) string {
	secret := r.IsSecret()
	value := quote(a.Value)
	if secret {
		value = SecretText(utf8.RuneCountInString(a.Value))
	}
	s := fmt.Sprintf("set %s to %s through accessibility (not a user input)", refLabel(a.Ref, r.Target), value)
	return s + readBackText(r, secret, true, "the value set")
}

// readBackText is the read-back clause: what the element shows now, and whether that is what was
// asked. A secure field is compared by length only and never shown.
func readBackText(r ActionResult, secret, whole bool, asked string) string {
	ok := r.ReadBackOK == nil || *r.ReadBackOK
	if secret {
		return secretReadBack(r, ok, asked)
	}
	if r.ReadBack == nil {
		return "; its value could not be read back"
	}
	shows := "; it shows " + quote(*r.ReadBack)
	switch {
	case ok:
		return shows
	case whole:
		return shows + ", not " + asked
	}
	return shows + ", which does not contain " + asked
}

// secretReadBack says how many characters a secure field holds now, when the agent said.
func secretReadBack(r ActionResult, ok bool, asked string) string {
	n, known := secretHeld(r)
	switch {
	case known && ok:
		return "; it holds " + plural(n, "character")
	case known:
		return fmt.Sprintf("; it holds %s, which is not the length of %s", plural(n, "character"), asked)
	case !ok:
		return "; its length does not match " + asked
	}
	return ""
}

// secretHeld is the length of a secure field after the action: from the after tree, where the
// target is as it is now, else from the read-back.
func secretHeld(r ActionResult) (int, bool) {
	if r.Target != nil && r.Target.Ref != "" && r.After != nil {
		for _, n := range r.After.Nodes {
			if n.Ref == r.Target.Ref {
				return n.Chars, true
			}
		}
	}
	if r.ReadBack != nil {
		return secretChars(*r.ReadBack)
	}
	return 0, false
}

func keyLead(a Action, r ActionResult) string {
	k := combo(a.Mods, a.Key)
	if a.Ref != "" {
		return fmt.Sprintf("pressed key %s on %s", k, refLabel(a.Ref, r.Target))
	}
	if app := treeAppName(r.Before, r.After); app != "" {
		return fmt.Sprintf("pressed key %s in %s", k, quote(app))
	}
	return fmt.Sprintf("pressed key %s in the frontmost app", k)
}

// waitedLine says how long the actionability checks waited and on which, when that was long
// enough to matter.
func waitedLine(r ActionResult) string {
	if r.WaitedMs < 100 {
		return ""
	}
	var parts []string
	for _, c := range r.Checks {
		if c.Ms >= 100 {
			parts = append(parts, word(c.Check)+" "+secs(c.Ms))
		}
	}
	s := "waited " + secs(r.WaitedMs) + " for the target to become actionable"
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	return s
}

// RefusalText renders a `refused` error: what blocked the action, for how long the checks tried,
// and what to do next, one sentence per reason.
func RefusalText(ref string, target *Node, r Refusal) string {
	subject := refLabel(ref, target)
	after := ""
	if r.WaitedMs > 0 {
		after = " after " + secs(r.WaitedMs) + " of checks"
	}
	var cause Cause
	if r.By != nil {
		cause = *r.By
	}
	switch r.Reason {
	case ReasonCovered:
		return fmt.Sprintf("refused: %s is %s%s; close or move it first", subject, coverText(coverOf(cause, target)), after)
	case ReasonHidden:
		if target != nil && target.Has(StateOverflow) {
			return fmt.Sprintf("refused: %s is in its toolbar's overflow%s; press the toolbar's >> button first, then the item in the menu it opens", subject, after)
		}
		return fmt.Sprintf("refused: %s shows nothing on screen (its window may be minimized, hidden or off the screen)%s; bring its window into view first", subject, after)
	case ReasonOffscreen:
		in := "its scroll area"
		if target != nil && target.Scroller != "" {
			in = word(target.Scroller)
		}
		return fmt.Sprintf("refused: %s is out of view in %s and scrolling did not bring it in%s; scroll with machine_scroll to find it, or check it is the element you mean", subject, in, after)
	case ReasonDisabled:
		return fmt.Sprintf("refused: %s is disabled%s; do what enables it first, or machine_wait_for it with state \"enabled\"", subject, after)
	case ReasonUnstable:
		return fmt.Sprintf("refused: %s kept moving (its frame changed between reads)%s; wait for the animation to end with machine_wait_for {\"idle\": true}, then try again", subject, after)
	case ReasonModal:
		return fmt.Sprintf("refused: %s is behind %s%s; handle the %s first (%s)", subject, modalText(cause), after, modalNoun(cause), modalAdvice(cause))
	case ReasonNotEditable:
		return fmt.Sprintf("refused: %s is not editable (it takes no typed text or value)%s; pick a text field, text area or another editable element", subject, after)
	case ReasonNotFrontmost:
		held := ""
		if cause.App != "" {
			held = " (" + quote(cause.App) + " stayed in front)"
		}
		return fmt.Sprintf("refused: the app of %s could not be brought to the front%s%s; close or quit what holds the front, then try again", subject, held, after)
	}
	s := fmt.Sprintf("refused: %s is not actionable%s", subject, after)
	if r.Reason != "" {
		s = fmt.Sprintf("refused: %s is not actionable (%s)%s", subject, word(r.Reason), after)
	}
	if r.Message != "" {
		s += ": " + oneLine(r.Message)
	}
	return s + "; take a machine_snapshot to see what state it is in"
}

// coverOf is the coverer a refusal names, completed from the target's own covered flag.
func coverOf(c Cause, target *Node) Covered {
	cov := Covered{By: c.Ref, Role: c.Role, Name: c.Name, Where: c.Where, App: c.App}
	if target != nil && target.Covered != nil && (cov.By == "" || cov.By == target.Covered.By) {
		t := *target.Covered
		if cov.Role == "" {
			cov.Role = t.Role
		}
		if cov.Name == "" {
			cov.Name = t.Name
		}
		if cov.Where == "" {
			cov.Where = t.Where
		}
		if cov.App == "" {
			cov.App = t.App
		}
		cov.By = t.By
	}
	return cov
}

// modalText names the modal thing: `sheet e80 "Save changes?" of "TipSplit"`.
func modalText(c Cause) string {
	if c.Ref == "" && c.Name == "" {
		return "a modal " + modalNoun(c)
	}
	a := Attention{Ref: c.Ref, Kind: c.Kind, Role: c.Role, Name: c.Name, App: c.App}
	if a.Kind == "" {
		a.Kind = "modal"
	}
	return attentionText(a)
}

// modalAdvice is how to get a modal thing out of the way with wave 1's tools.
func modalAdvice(c Cause) string {
	switch c.Kind {
	case AttentionMenu:
		return "pick one of its items, or press escape with machine_key to close it"
	case AttentionPopover:
		return "finish with it, or press escape with machine_key to close it"
	case "":
		return "press one of its buttons, or escape with machine_key"
	}
	return "press one of its buttons"
}

// modalNoun is what kind of modal thing it is, for a sentence.
func modalNoun(c Cause) string {
	if c.Kind == "" {
		return "sheet, alert or menu"
	}
	return word(c.Kind)
}
