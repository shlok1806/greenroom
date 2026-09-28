package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// The toolkit's actions (daemon ADR 0006): each runs its actionability checks with auto-wait in
// the guest, acts once and returns its own effect. machine_type, machine_key and machine_scroll
// are shared with the old tools: their old calls (desktop.ToolkitCall) go the old way unchanged.

// effectNote ends every action's description: its result is its effect.
const effectNote = " The result is the action's effect: what changed, that nothing did, or that it was refused and why. " +
	"Do not take a snapshot or a screenshot to see whether it worked."

// clickDescriptionToolkit and inputDescriptionToolkit are machine_click's and machine_input's
// descriptions beside the toolkit (docs/21 section 7.5): unchanged tools, pointing at the new ones.
const (
	clickDescriptionToolkit = "Click the machine's screen at an element id from your latest machine_ui, or at x and y " +
		"as fractions of the screen (0 to 1). Prefer machine_press with a ref from machine_snapshot: it checks the " +
		"element can be clicked, waits for it, and returns what the click changed." + humanDriving
	inputDescriptionToolkit = "Post a raw ordered batch of actions (move, click, down, up, scroll, type, key, sleep) in " +
		"one round trip, for what the ref tools do not cover, such as a drag. It checks nothing and reports no " +
		"effect; prefer machine_press, machine_type, machine_key and machine_scroll by ref." + humanDriving
)

func addActionTools(s *mcp.Server, mgr *machine.Manager) {
	legacy := func(ctx context.Context, runID string, actions ...machine.InputAction) (*mcp.CallToolResult, any, error) {
		res, err := mgr.InputAs(ctx, runID, machine.HolderCoder, actions)
		if err != nil {
			return nil, nil, err
		}
		return nil, res, nil
	}
	act := func(res machine.DesktopAction, err error) (*mcp.CallToolResult, any, error) {
		if err != nil {
			return nil, nil, err
		}
		return deskText(res.Step, res.Text), res, nil
	}

	type pressIn struct {
		RunID     string   `json:"runId" jsonschema:"runId from machine_create"`
		Ref       string   `json:"ref,omitempty" jsonschema:"The element to press, a ref from machine_snapshot, machine_find or an action's result."`
		X         *float64 `json:"x,omitempty" jsonschema:"Only for content with no ref (a canvas, a game): horizontal fraction of the screen, 0 to 1, with y and reason."`
		Y         *float64 `json:"y,omitempty" jsonschema:"Vertical fraction of the screen, 0 to 1, with x."`
		Reason    string   `json:"reason,omitempty" jsonschema:"Why a point and not a ref. Required with x and y."`
		Button    string   `json:"button,omitempty" jsonschema:"left (default), right or middle."`
		Count     int      `json:"count,omitempty" jsonschema:"1 (default), 2 for a double click, 3 for a triple."`
		Mods      []string `json:"mods,omitempty" jsonschema:"Modifiers held: cmd, shift, alt, ctrl, fn."`
		Via       string   `json:"via,omitempty" jsonschema:"pointer (default), or ax to use the element's accessibility press instead of a pointer event, only when a pointer press is impossible."`
		Force     bool     `json:"force,omitempty" jsonschema:"Press the point even when an element with a ref is there."`
		TimeoutMs int      `json:"timeoutMs,omitempty" jsonschema:"How long to wait for the element to become pressable. Default 5000, max 30000."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_press",
		Description: "Press an element by ref, as a person clicks it: it waits until the element is attached, " +
			"visible (scrolling it into view), enabled, still and not covered, brings its app to the front, then " +
			"clicks once. A point (x, y) needs a reason and is only for content with no ref." + effectNote + humanDriving,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pressIn) (*mcp.CallToolResult, any, error) {
		return act(mgr.Press(ctx, in.RunID, machine.HolderCoder, desktop.PressArgs{Ref: in.Ref, X: in.X, Y: in.Y,
			Reason: in.Reason, Button: in.Button, Count: in.Count, Mods: in.Mods, Via: in.Via, Force: in.Force, TimeoutMs: in.TimeoutMs}))
	})

	type setValueIn struct {
		RunID     string `json:"runId" jsonschema:"runId from machine_create"`
		Ref       string `json:"ref" jsonschema:"The element: a slider, a text field, a stepper."`
		Value     string `json:"value" jsonschema:"The value to set; empty clears a field."`
		TimeoutMs int    `json:"timeoutMs,omitempty" jsonschema:"How long to wait for the element. Default 5000, max 30000."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_set_value",
		Description: "Set an element's value through accessibility, not as a user types it: for setup (a slider, a " +
			"long field), never for the input a check is about; use machine_type for that." + effectNote + humanDriving,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in setValueIn) (*mcp.CallToolResult, any, error) {
		return act(mgr.SetValue(ctx, in.RunID, machine.HolderCoder, desktop.SetValueArgs{Ref: in.Ref, Value: in.Value, TimeoutMs: in.TimeoutMs}))
	})

	type typeIn struct {
		RunID     string `json:"runId" jsonschema:"runId from machine_create"`
		Text      string `json:"text" jsonschema:"The text to type."`
		Ref       string `json:"ref,omitempty" jsonschema:"The field to type into, pressed first to focus it. Without ref, and with no other option below, the text is typed into whatever has focus, as before."`
		Replace   bool   `json:"replace,omitempty" jsonschema:"Select all first, so the text replaces the value."`
		Submit    string `json:"submit,omitempty" jsonschema:"return or tab, pressed after the text."`
		Via       string `json:"via,omitempty" jsonschema:"unicode (default: exact text on any keyboard layout) or keys (real key presses, a US layout)."`
		PaceMs    *int   `json:"paceMs,omitempty" jsonschema:"Milliseconds between characters, 0 to 100. Default 20: a busy app drops faster keys."`
		TimeoutMs int    `json:"timeoutMs,omitempty" jsonschema:"How long to wait for the field. Default 5000, max 30000."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_type",
		Description: "Type text into a field by ref (pressed first to focus it), paced so no key is dropped, then read " +
			"the field back and say whether it shows what was typed. Without ref or any other option it types into " +
			"whatever has focus and returns only that it posted." + effectNote + humanDriving,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in typeIn) (*mcp.CallToolResult, any, error) {
		if !desktop.ToolkitCall("machine_type", req.Params.Arguments) {
			return legacy(ctx, in.RunID, machine.InputAction{Type: "type", Text: in.Text})
		}
		return act(mgr.Type(ctx, in.RunID, machine.HolderCoder, desktop.TypeArgs{Ref: in.Ref, Text: in.Text, Replace: in.Replace,
			Submit: in.Submit, Via: in.Via, PaceMs: in.PaceMs, TimeoutMs: in.TimeoutMs}))
	})

	type keyIn struct {
		RunID     string   `json:"runId" jsonschema:"runId from machine_create"`
		Key       string   `json:"key" jsonschema:"A key name: a letter, digit or punctuation character, or one of return, enter, tab, space, delete, forwarddelete, escape, left, right, up, down, home, end, pageup, pagedown, capslock, help, f1-f12."`
		Mods      []string `json:"mods,omitempty" jsonschema:"Modifiers held with the key: cmd, shift, alt, ctrl, fn (also command, option, control, function). Any other name is an error."`
		Ref       string   `json:"ref,omitempty" jsonschema:"An element to focus first. Without ref (and timeoutMs) the key goes to the frontmost app, as before."`
		TimeoutMs int      `json:"timeoutMs,omitempty" jsonschema:"How long to wait for the element. Default 5000, max 30000."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_key",
		Description: "Press one key with modifiers held, for example key f with mods [cmd] for command-F. With ref " +
			"it focuses that element first and returns the key's effect; without it the key goes to the frontmost " +
			"app and the result says only that it posted." + effectNote + humanDriving,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in keyIn) (*mcp.CallToolResult, any, error) {
		if !desktop.ToolkitCall("machine_key", req.Params.Arguments) {
			return legacy(ctx, in.RunID, machine.InputAction{Type: "key", Key: in.Key, Mods: in.Mods})
		}
		return act(mgr.Key(ctx, in.RunID, machine.HolderCoder, desktop.KeyArgs{Key: in.Key, Mods: in.Mods, Ref: in.Ref, TimeoutMs: in.TimeoutMs}))
	})

	type scrollIn struct {
		RunID     string   `json:"runId" jsonschema:"runId from machine_create"`
		Ref       string   `json:"ref,omitempty" jsonschema:"A scroll area, or any element inside one: the area around it scrolls."`
		To        any      `json:"to,omitempty" jsonschema:"Where to, with ref: top, bottom, a ref to scroll into view (e45), {\"pages\": n} or {\"by\": points}; positive scrolls down."`
		TimeoutMs int      `json:"timeoutMs,omitempty" jsonschema:"How long to wait for the area. Default 5000, max 30000."`
		X         *float64 `json:"x,omitempty" jsonschema:"Without ref, the old raw scroll: fraction of the screen to move the pointer to first."`
		Y         *float64 `json:"y,omitempty" jsonschema:"Without ref: fraction of the screen to move the pointer to first, paired with x."`
		DeltaX    float64  `json:"deltaX,omitempty" jsonschema:"Without ref: horizontal scroll in points. Positive scrolls right."`
		DeltaY    float64  `json:"deltaY,omitempty" jsonschema:"Without ref: vertical scroll in points. Positive scrolls down."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_scroll",
		Description: "Scroll a scroll area by ref to the top, the bottom, by pages or points, or until an element is in " +
			"view, and return where it went and what came into view. Without ref, the old raw scroll under x,y " +
			"or the pointer, with no effect reported." + effectNote + humanDriving,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in scrollIn) (*mcp.CallToolResult, any, error) {
		if !desktop.ToolkitCall("machine_scroll", req.Params.Arguments) {
			return legacy(ctx, in.RunID, machine.InputAction{Type: "scroll", X: in.X, Y: in.Y, DeltaX: in.DeltaX, DeltaY: in.DeltaY})
		}
		if in.X != nil || in.Y != nil || in.DeltaX != 0 || in.DeltaY != 0 {
			return nil, nil, errors.New("machine_scroll takes ref and to, or the old x, y, deltaX and deltaY, not both")
		}
		to, err := desktop.ScrollToFrom(in.To)
		if err != nil {
			return nil, nil, err
		}
		res, err := mgr.Scroll(ctx, in.RunID, machine.HolderCoder, desktop.ScrollArgs{Ref: in.Ref, To: to, TimeoutMs: in.TimeoutMs})
		if err != nil {
			return nil, nil, err
		}
		return deskText(res.Step, res.Text), res, nil
	})
}
