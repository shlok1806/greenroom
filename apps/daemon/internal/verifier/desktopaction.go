package verifier

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

// The toolkit's actions for the verifier (daemon ADR 0006): each is one call whose result is its
// own effect, recorded on its own step, so runTool reads no UI after it (the old tools keep their
// effect read until wave 4). machine_type, machine_key and machine_scroll are shared with the old
// tools; desktop.ToolkitCall says which way a call goes.

// toolkitActions are the tools whose toolkit calls are actions: their steps are inputs for the
// verdict review, and each records its own effect.
var toolkitActions = []string{"machine_press", "machine_set_value", "machine_type", "machine_key", "machine_scroll"}

// isToolkitAction reports whether call is a toolkit action.
func isToolkitAction(call nim.ToolCall) bool {
	return slices.Contains(toolkitActions, call.Name) && desktop.ToolkitCall(call.Name, []byte(call.Arguments))
}

// isActionStep reports whether a recorded step is an input a check may cite in its actions: the
// old tools' batches, or a toolkit action.
func isActionStep(tool string) bool {
	return tool == "machine_input" || slices.Contains(toolkitActions, tool)
}

const effectNote = " Its result is its effect: what changed, that nothing did, or that it was refused and why; do " +
	"not look again to see whether it worked."

// toolkitActionDefs are the actions only the toolkit has.
var toolkitActionDefs = []nim.Tool{
	{
		Name: "machine_press",
		Description: "Press an element by ref as a person clicks it: it waits until the element is visible (scrolling " +
			"it into view), enabled, still and not covered, then clicks once. A point (x, y) needs a reason and is " +
			"only for content with no ref, such as a canvas." + effectNote + " " + humanDriving,
		Schema: object(map[string]any{
			"ref":       str("The element, a ref from machine_snapshot, machine_find or an action's result."),
			"x":         num("Only for content with no ref: fraction of the screen, 0 to 1, with y and reason."),
			"y":         num("Fraction of the screen, 0 to 1, with x."),
			"reason":    str("Why a point and not a ref. Required with x and y."),
			"button":    str("left (default), right or middle."),
			"count":     integer("1 (default), 2 for a double click, 3 for a triple."),
			"mods":      strList("Modifiers held: cmd, shift, alt, ctrl, fn."),
			"via":       str("pointer (default), or ax for the element's accessibility press, only when a pointer press is impossible and never for the action a check is about."),
			"timeoutMs": integer("How long to wait for the element to become pressable. Default 5000, max 30000."),
		}),
	},
	{
		Name: "machine_set_value",
		Description: "Set an element's value through accessibility (a slider, a long field): for setup only, never for " +
			"the input a check is about." + effectNote + " " + humanDriving,
		Schema: object(map[string]any{
			"ref":       str("The element."),
			"value":     str("The value to set; empty clears a field."),
			"timeoutMs": integer("How long to wait for the element. Default 5000, max 30000."),
		}, "ref", "value"),
	},
}

// sharedToolkitDefs are machine_type, machine_key and machine_scroll with the toolkit's arguments
// beside their old ones.
var sharedToolkitDefs = []nim.Tool{
	{
		Name: "machine_type",
		Description: "Type text. With ref it presses that field to focus it, types paced so no key is dropped, and " +
			"reads the field back." + effectNote + " With only text it types into whatever has focus, as before. " + humanDriving,
		Schema: object(map[string]any{
			"text":      str("The text to type."),
			"ref":       str("The field to type into."),
			"replace":   map[string]any{"type": "boolean", "description": "Select all first, so the text replaces the value."},
			"submit":    str("return or tab, pressed after the text."),
			"via":       str("unicode (default) or keys (real key presses, US layout)."),
			"paceMs":    integer("Milliseconds between characters, 0 to 100. Default 20."),
			"timeoutMs": integer("How long to wait for the field. Default 5000, max 30000."),
		}, "text"),
	},
	{
		Name: "machine_key",
		Description: "Press one key with modifiers held, for example key f with mods [cmd] for command-F. With ref it " +
			"focuses that element first." + effectNote + " Without ref it goes to the frontmost app, as before. " + humanDriving,
		Schema: object(map[string]any{
			"key": str("A key name: a letter, digit or punctuation character, or one of return, enter, tab, space, " +
				"delete, forwarddelete, escape, left, right, up, down, home, end, pageup, pagedown, capslock, help, f1-f12."),
			"mods":      strList("Modifiers held with the key: cmd, shift, alt, ctrl, fn. Any other name is an error."),
			"ref":       str("An element to focus first."),
			"timeoutMs": integer("How long to wait for the element. Default 5000, max 30000."),
		}, "key"),
	},
	{
		Name: "machine_scroll",
		Description: "Scroll a scroll area by ref: to the top, the bottom, by pages or points, or until an element is " +
			"in view; the area scrolls, not whatever is under the pointer." + effectNote + " Without ref, the old raw " +
			"scroll under x,y. " + humanDriving,
		Schema: object(map[string]any{
			"ref":       str("A scroll area, or any element inside one."),
			"to":        map[string]any{"description": "With ref: \"top\", \"bottom\", a ref to scroll into view, {\"pages\": n} or {\"by\": points}; positive scrolls down."},
			"timeoutMs": integer("How long to wait for the area. Default 5000, max 30000."),
			"x":         num("Without ref: fraction of the screen to move the pointer to first."),
			"y":         num("Without ref: fraction of the screen to move the pointer to first, paired with x."),
			"deltaX":    num("Without ref: horizontal scroll in points. Positive scrolls right."),
			"deltaY":    num("Without ref: vertical scroll in points. Positive scrolls down."),
		}),
	},
}

// withToolkitDefs is the old tools with the shared ones replaced by their toolkit definitions, and
// the toolkit's own tools added.
func withToolkitDefs(old []nim.Tool, own ...[]nim.Tool) []nim.Tool {
	shared := append(slices.Clone(sharedToolkitDefs), screenshotToolkitDef)
	out := make([]nim.Tool, 0, len(old))
	for _, t := range old {
		if i := slices.IndexFunc(shared, func(s nim.Tool) bool { return s.Name == t.Name }); i >= 0 {
			t = shared[i]
		}
		out = append(out, t)
	}
	for _, o := range own {
		out = append(out, o...)
	}
	return out
}

// deskAction runs one toolkit action as the verifier and returns the text the model reads, the
// step (0 when none was recorded), the effect kind the step recorded, and the ref it acted on.
// The screen-taken and stale-look refusals read as the old tools' do, so a turn yields the same way.
func deskAction(ctx context.Context, mgr *machine.Manager, runID string, call nim.ToolCall) (result string, step int, kind, ref string) {
	args := []byte(call.Arguments)
	var res machine.DesktopAction
	var err error
	switch call.Name {
	case "machine_press":
		var a desktop.PressArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_press: " + err.Error(), 0, "", ""
		}
		ref = a.Ref
		res, err = mgr.Press(ctx, runID, machine.HolderVerifier, a)
	case "machine_set_value":
		var a desktop.SetValueArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_set_value: " + err.Error(), 0, "", ""
		}
		ref = a.Ref
		res, err = mgr.SetValue(ctx, runID, machine.HolderVerifier, a)
	case "machine_type":
		var a desktop.TypeArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_type: " + err.Error(), 0, "", ""
		}
		ref = a.Ref
		res, err = mgr.Type(ctx, runID, machine.HolderVerifier, a)
	case "machine_key":
		var a desktop.KeyArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_key: " + err.Error(), 0, "", ""
		}
		ref = a.Ref
		res, err = mgr.Key(ctx, runID, machine.HolderVerifier, a)
	case "machine_scroll":
		var in struct {
			desktop.ScrollArgs
			X      *float64 `json:"x"`
			Y      *float64 `json:"y"`
			DeltaX float64  `json:"deltaX"`
			DeltaY float64  `json:"deltaY"`
		}
		if err := desktop.DecodeArgs(args, &in); err != nil {
			return "error: machine_scroll: " + err.Error(), 0, "", ""
		}
		if in.X != nil || in.Y != nil || in.DeltaX != 0 || in.DeltaY != 0 {
			return "error: machine_scroll takes ref and to, or the old x, y, deltaX and deltaY, not both", 0, "", ""
		}
		sc, err := mgr.Scroll(ctx, runID, machine.HolderVerifier, in.ScrollArgs)
		if err != nil {
			return actionError(err), sc.Step, "", in.Ref
		}
		return fmt.Sprintf("step %d\n%s", sc.Step, sc.Text), sc.Step, sc.EffectKind(), in.Ref
	default:
		return "error: no tool named " + call.Name, 0, "", ""
	}
	if err != nil {
		return actionError(err), res.Step, "", ref
	}
	return fmt.Sprintf("step %d\n%s", res.Step, res.Text), res.Step, res.EffectKind(), ref
}

// actionError is a failed action's result, worded as the old tools' are for the refusals a turn
// handles: the screen taken, and a look that is stale.
func actionError(err error) string {
	switch {
	case errors.Is(err, machine.ErrScreenTaken):
		return screenTakenResult(err)
	case errors.Is(err, machine.ErrStaleLook):
		return staleLookResult(err)
	}
	return "error: " + err.Error()
}
