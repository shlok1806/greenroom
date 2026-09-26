package verifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

// An input that changes nothing is evidence (ADR 0029). A click on a control that does nothing
// "succeeds", so the #125 repeat guard, which counts failing calls, never saw the verifier click
// a dead Clear done button 3 to 12 times in a turn (bench run 2: up to 40 steps and 424k tokens).
// From the second click on the same control whose effect read found no change, the result says
// so and names the reads a fail can cite. Nothing ends the turn: a correct fail must not become
// a question.

// deadHint follows the effect of a repeated click that changed nothing.
const deadHint = "This is the %s time this control (%s) changed nothing. If the task says it should change " +
	"something, that is evidence for fail: cite the effect reads (%s). Otherwise, try something different or ask."

// pointNear is how close, as a fraction of the screen on each axis, two clicks aimed at no
// known control must be to count as the same point.
const pointNear = 0.02

// maxControlArea is the largest element, as a fraction of the screen's area, that a click by
// position is taken to aim at: a bigger one (a window, a list, a group) holds many controls.
const maxControlArea = 0.02

// target is the control a click aimed at: an element of the verifier's read before it, by
// identity (ids change between reads), or else a point.
type target struct {
	key  string // identity of the element, or "" for a point
	name string // what the hint calls it
	x, y float64
}

// same reports whether two clicks aimed at the same control.
func (t target) same(o target) bool {
	if t.key != "" || o.key != "" {
		return t.key == o.key
	}
	return math.Abs(t.x-o.x) <= pointNear && math.Abs(t.y-o.y) <= pointNear
}

// clickTarget is the control call clicks, found in prev, the verifier's read before it; nil for
// an input that is not one click (typing, keys, scrolls, batches of more than one click).
func clickTarget(call nim.ToolCall, prev machine.UITree, hadPrev bool) *target {
	var element int
	var x, y *float64
	switch call.Name {
	case "machine_click":
		var in struct {
			Element int      `json:"element"`
			X       *float64 `json:"x"`
			Y       *float64 `json:"y"`
		}
		if json.Unmarshal([]byte(call.Arguments), &in) != nil {
			return nil
		}
		element, x, y = in.Element, in.X, in.Y
	case "machine_input":
		var in struct {
			Actions []machine.InputAction `json:"actions"`
		}
		dec := json.NewDecoder(bytes.NewReader([]byte(call.Arguments)))
		if dec.Decode(&in) != nil {
			return nil
		}
		clicks := 0
		for _, a := range in.Actions {
			switch a.Type {
			case "sleep", "move":
			case "click":
				clicks++
				x, y = a.X, a.Y
			default:
				return nil
			}
		}
		if clicks != 1 {
			return nil
		}
	default:
		return nil
	}
	if !hadPrev {
		prev = machine.UITree{}
	}
	if element != 0 {
		for _, e := range prev.Elements {
			if e.ID == element {
				return &target{key: identity(e), name: elementName(e), x: e.X, y: e.Y}
			}
		}
		return nil
	}
	if x == nil || y == nil {
		return nil
	}
	// The smallest element under the point, when it is control-sized.
	var hit *machine.UIElement
	for i, e := range prev.Elements {
		if math.Abs(*x-e.X) <= e.W/2 && math.Abs(*y-e.Y) <= e.H/2 && e.W*e.H <= maxControlArea &&
			(hit == nil || e.W*e.H < hit.W*hit.H) {
			hit = &prev.Elements[i]
		}
	}
	if hit != nil {
		return &target{key: identity(*hit), name: elementName(*hit), x: *x, y: *y}
	}
	return &target{name: fmt.Sprintf("the point (%.3f, %.3f)", *x, *y), x: *x, y: *y}
}

// deadControls is one turn's clicks whose effect read found no change, since the last input
// that changed something: an input that did resets them, since the screen then differs.
type deadControls struct {
	misses []miss
}

type miss struct {
	target
	read int // the effect read that found no change
}

// record notes an input's effect, with the control it clicked (nil for any other input), and
// returns the hint for the model, or "".
func (d *deadControls) record(t *target, kind string, read int) string {
	switch {
	case kind == machine.EffectChanged || kind == machine.EffectQuit:
		d.misses = nil
		return ""
	case kind != machine.EffectNone || t == nil || read == 0:
		return ""
	}
	d.misses = append(d.misses, miss{*t, read})
	var reads []string
	for _, m := range d.misses {
		if m.same(*t) {
			reads = append(reads, fmt.Sprintf("%d", m.read))
		}
	}
	if len(reads) < 2 {
		return ""
	}
	return fmt.Sprintf(deadHint, ordinal(len(reads)), t.name, stepList(reads))
}

// ordinal is "second", "third", and then "4th", "5th".
func ordinal(n int) string {
	switch n {
	case 2:
		return "second"
	case 3:
		return "third"
	}
	return fmt.Sprintf("%dth", n)
}

// stepList is "steps 32 and 35", or "steps 32, 35 and 38".
func stepList(steps []string) string {
	if len(steps) == 1 {
		return "step " + steps[0]
	}
	return "steps " + strings.Join(steps[:len(steps)-1], ", ") + " and " + steps[len(steps)-1]
}
