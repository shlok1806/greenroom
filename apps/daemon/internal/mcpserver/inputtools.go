package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// verifierHolder is the seat every computer-use tool (machine_click,
// machine_type, machine_key, machine_scroll, machine_input) takes the
// screen lease under. The verifier holds the lease per call, not for the
// whole turn (apps/daemon/CLAUDE.md, issue #11): each of these tools takes
// the lease, posts its batch, and releases it before returning, so a person
// watching the run from the companion can take the screen back between
// calls rather than only between turns. The take-post-release sequence
// itself lives once, in machine.Manager.InputAs (issue #12); this package
// and internal/verifier both call it rather than each keeping a copy,
// because internal/verifier cannot import this package.
const verifierHolder = "verifier"

// addInputTools exposes the verifier's mouse and keyboard (ADR 0009, issue
// #11): machine_click, machine_type, machine_key and machine_scroll are
// conveniences over machine_input, which posts an arbitrary ordered batch of
// actions in one step. Coordinates are fractions of the screen, 0 to 1, the
// same contract the companion uses (see machine.InputAction).
func addInputTools(s *mcp.Server, mgr *machine.Manager) {
	type clickIn struct {
		RunID  string  `json:"runId" jsonschema:"runId from machine_create"`
		X      float64 `json:"x" jsonschema:"Horizontal position as a fraction of the screen, 0 (left) to 1 (right). Look at a screenshot first: this is a fraction of the picture you were shown, not a guest pixel."`
		Y      float64 `json:"y" jsonschema:"Vertical position as a fraction of the screen, 0 (top) to 1 (bottom)."`
		Button string  `json:"button,omitempty" jsonschema:"left (default), right, or middle."`
		Clicks int     `json:"clicks,omitempty" jsonschema:"2 for a double click. Default 1."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_click",
		Description: "Click the machine's screen at a position. x and y are fractions of the screen (0 to 1), not " +
			"pixels: look at a machine_screenshot first and reason in that picture. A human may be driving the " +
			"machine; if so this comes back as an error naming them, and the machine is unharmed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in clickIn) (*mcp.CallToolResult, machine.InputResult, error) {
		res, err := mgr.InputAs(ctx, in.RunID, verifierHolder, []machine.InputAction{{
			Type: "click", X: &in.X, Y: &in.Y, Button: in.Button, Clicks: in.Clicks,
		}})
		return nil, res, err
	})

	type typeIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
		Text  string `json:"text" jsonschema:"The text to type, one character event at a time, into whatever has focus. Click into a field first if nothing does."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_type",
		Description: "Type text into the machine, into whatever currently has keyboard focus. A human may be " +
			"driving the machine; if so this comes back as an error naming them, and the machine is unharmed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in typeIn) (*mcp.CallToolResult, machine.InputResult, error) {
		res, err := mgr.InputAs(ctx, in.RunID, verifierHolder, []machine.InputAction{{Type: "type", Text: in.Text}})
		return nil, res, err
	})

	type keyIn struct {
		RunID string   `json:"runId" jsonschema:"runId from machine_create"`
		Key   string   `json:"key" jsonschema:"A key name: a letter, digit or punctuation character, or one of return, enter, tab, space, delete, forwarddelete, escape, left, right, up, down, home, end, pageup, pagedown, capslock, help, f1-f12."`
		Mods  []string `json:"mods,omitempty" jsonschema:"Modifiers held with the key: cmd, shift, alt, ctrl, fn."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_key",
		Description: "Press one key, optionally with modifiers held down, for example key f, mods [cmd] for " +
			"command-F. A human may be driving the machine; if so this comes back as an error naming them, and " +
			"the machine is unharmed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in keyIn) (*mcp.CallToolResult, machine.InputResult, error) {
		res, err := mgr.InputAs(ctx, in.RunID, verifierHolder, []machine.InputAction{{Type: "key", Key: in.Key, Mods: in.Mods}})
		return nil, res, err
	})

	type scrollIn struct {
		RunID  string   `json:"runId" jsonschema:"runId from machine_create"`
		X      *float64 `json:"x,omitempty" jsonschema:"Optional fraction of the screen to move the pointer to first. A scroll goes to whatever is under the pointer, so set this when you have not just clicked or moved there."`
		Y      *float64 `json:"y,omitempty" jsonschema:"Optional fraction of the screen to move the pointer to first, paired with x."`
		DeltaX float64  `json:"deltaX,omitempty" jsonschema:"Horizontal scroll amount, in points. Positive scrolls right."`
		DeltaY float64  `json:"deltaY,omitempty" jsonschema:"Vertical scroll amount, in points. Positive scrolls down, negative scrolls up."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_scroll",
		Description: "Scroll the machine's screen under the pointer's current position, or under x,y if given. A " +
			"human may be driving the machine; if so this comes back as an error naming them, and the machine is " +
			"unharmed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scrollIn) (*mcp.CallToolResult, machine.InputResult, error) {
		res, err := mgr.InputAs(ctx, in.RunID, verifierHolder, []machine.InputAction{{
			Type: "scroll", X: in.X, Y: in.Y, DeltaX: in.DeltaX, DeltaY: in.DeltaY,
		}})
		return nil, res, err
	})

	type actionIn struct {
		Type   string   `json:"type" jsonschema:"One of: move, click, down, up, scroll, type, key, sleep."`
		X      *float64 `json:"x,omitempty" jsonschema:"Fraction of the screen, 0 to 1. For move, click, down and up."`
		Y      *float64 `json:"y,omitempty" jsonschema:"Fraction of the screen, 0 to 1. For move, click, down and up."`
		Button string   `json:"button,omitempty" jsonschema:"left (default), right, or middle. For click, down and up."`
		Clicks int      `json:"clicks,omitempty" jsonschema:"2 for a double click. For click, down and up."`
		DeltaX float64  `json:"deltaX,omitempty" jsonschema:"For scroll."`
		DeltaY float64  `json:"deltaY,omitempty" jsonschema:"For scroll."`
		Text   string   `json:"text,omitempty" jsonschema:"For type."`
		Key    string   `json:"key,omitempty" jsonschema:"For key."`
		Mods   []string `json:"mods,omitempty" jsonschema:"Modifiers held with key: cmd, shift, alt, ctrl, fn."`
		MS     int      `json:"ms,omitempty" jsonschema:"Milliseconds to wait. For sleep, capped at 5000."`
	}
	type inputIn struct {
		RunID   string     `json:"runId" jsonschema:"runId from machine_create"`
		Actions []actionIn `json:"actions" jsonschema:"Ordered actions to post in one batch, for example down, move, up to drag. Coordinates are fractions of the screen (0 to 1). The whole batch records as one step."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_input",
		Description: "Post an ordered batch of actions (move, click, down, up, scroll, type, key, sleep) in one " +
			"round trip: compose a drag out of down, move and up. machine_click, machine_type, machine_key and " +
			"machine_scroll are conveniences over this for the common single-action case. A human may be driving " +
			"the machine; if so this comes back as an error naming them, and the machine is unharmed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in inputIn) (*mcp.CallToolResult, machine.InputResult, error) {
		actions := make([]machine.InputAction, len(in.Actions))
		for i, a := range in.Actions {
			actions[i] = machine.InputAction{
				Type: a.Type, X: a.X, Y: a.Y, Button: a.Button, Clicks: a.Clicks,
				DeltaX: a.DeltaX, DeltaY: a.DeltaY, Text: a.Text, Key: a.Key, Mods: a.Mods, MS: a.MS,
			}
		}
		res, err := mgr.InputAs(ctx, in.RunID, verifierHolder, actions)
		return nil, res, err
	})
}
