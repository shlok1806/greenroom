package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

const humanDriving = " If a human is driving the machine, this returns an error naming them and the machine is untouched."

// addInputTools exposes mouse and keyboard (ADR 0009). machine_click, _type, _key and _scroll are single-action
// conveniences over machine_input. Coordinates are screen fractions, 0 to 1 (see machine.InputAction).
func addInputTools(s *mcp.Server, mgr *machine.Manager) {
	post := func(ctx context.Context, runID string, actions ...machine.InputAction) (*mcp.CallToolResult, machine.InputResult, error) {
		// Per call, so a human can take the screen back between calls (issue #11).
		res, err := mgr.InputAs(ctx, runID, machine.HolderCoder, actions)
		return nil, res, err
	}

	type uiIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
		App   string `json:"app,omitempty" jsonschema:"Application name or bundle id to read, e.g. TipSplit. Default: the frontmost application."`
		Limit int    `json:"limit,omitempty" jsonschema:"Most elements to list. Default 250, max 1000."`
	}
	uiDescription := "Read the accessibility tree of the frontmost application (or a named one): every on-screen " +
		"control and text with its role, title, label, value, identifier, state, and its center and size as " +
		"fractions of the screen, the space machine_click takes. Call it before clicking and aim at element " +
		"centers (or pass machine_click an element id) instead of estimating from a screenshot. Read it again " +
		"after the UI changes. It only reads; it needs no control of the screen."
	if mgr.DesktopToolkit() {
		uiDescription = uiDescriptionToolkit
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_ui",
		Description: uiDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in uiIn) (*mcp.CallToolResult, machine.UITree, error) {
		tree, err := mgr.UI(ctx, in.RunID, machine.HolderCoder, in.App, in.Limit)
		if err != nil {
			return nil, machine.UITree{}, err
		}
		tree.Seconds = round(tree.Seconds)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("step %d\n%s", tree.Step, tree.Outline())}}}, tree, nil
	})

	type clickIn struct {
		RunID   string   `json:"runId" jsonschema:"runId from machine_create"`
		Element int      `json:"element,omitempty" jsonschema:"An element id from your most recent machine_ui: clicks that element's center, and x and y are ignored."`
		UIStep  int      `json:"uiStep,omitempty" jsonschema:"Optional: the step of the machine_ui read the element id comes from. The click is refused if that is not your latest read, so an id never lands on a newer tree."`
		X       *float64 `json:"x,omitempty" jsonschema:"Horizontal position as a fraction of the screen, 0 (left) to 1 (right): an element's center from machine_ui, or a fraction of a screenshot, never a guest pixel."`
		Y       *float64 `json:"y,omitempty" jsonschema:"Vertical position as a fraction of the screen, 0 (top) to 1 (bottom)."`
		Button  string   `json:"button,omitempty" jsonschema:"left (default), right, or middle. Any other name is an error."`
		Clicks  int      `json:"clicks,omitempty" jsonschema:"2 for a double click. Default 1."`
	}
	type clickOut struct {
		machine.InputResult
		Element *machine.UIElement `json:"element,omitempty"`
		App     string             `json:"app,omitempty" jsonschema:"The application the element's tree was read from"`
		UIStep  int                `json:"uiStep,omitempty" jsonschema:"The machine_ui step the element came from"`
	}
	clickDescription := "Click the machine's screen. Pass element, an id from your latest machine_ui, to click that " +
		"element (add uiStep, that read's step, to be refused rather than aimed at a newer read): its app is " +
		"brought to the front with its window raised first, and the click lands on the part of the element that " +
		"shows, or is refused naming what covers it (the Dock, another window). Or pass x and y as fractions of " +
		"the screen (0 to 1), never pixels, to click whatever is on top there. Ids are yours alone: greenroom's " +
		"verifier reading the UI never changes what they point at." + humanDriving
	if mgr.DesktopToolkit() {
		clickDescription = clickDescriptionToolkit
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_click",
		Description: clickDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in clickIn) (*mcp.CallToolResult, clickOut, error) {
		var out clickOut
		x, y := in.X, in.Y
		var actions []machine.InputAction
		if in.Element != 0 {
			e, err := mgr.ElementCenter(in.RunID, machine.HolderCoder, in.Element, in.UIStep)
			if err != nil {
				return nil, out, err
			}
			out.Element, out.App, out.UIStep = &e.UIElement, e.App, e.UIStep
			// Its app first, then the part of it that shows (daemon ADR 0009).
			actions = machine.FocusThenClick(e, in.Button, in.Clicks)
		} else {
			if x == nil || y == nil {
				return nil, out, errors.New("machine_click needs an element id from machine_ui, or both x and y")
			}
			actions = []machine.InputAction{{Type: "click", X: x, Y: y, Button: in.Button, Clicks: in.Clicks}}
		}
		res, err := mgr.InputAs(ctx, in.RunID, machine.HolderCoder, actions)
		out.InputResult = res
		return nil, out, err
	})

	// With the toolkit, machine_type, machine_key and machine_scroll take its arguments too, and
	// their old calls go this same way (desktopactions.go).
	if !mgr.DesktopToolkit() {
		addOldTypeKeyScroll(s, post)
	}

	// actionIn is machine.InputAction plus schema descriptions; the conversion below keeps the two in step.
	type actionIn struct {
		Type   string   `json:"type" jsonschema:"One of: move, click, down, up, scroll, type, key, sleep, focus."`
		X      *float64 `json:"x,omitempty" jsonschema:"Fraction of the screen, 0 to 1. For move, click, down and up."`
		Y      *float64 `json:"y,omitempty" jsonschema:"Fraction of the screen, 0 to 1. For move, click, down and up."`
		Button string   `json:"button,omitempty" jsonschema:"left (default), right, or middle. For click, down and up. Any other name is an error."`
		Clicks int      `json:"clicks,omitempty" jsonschema:"2 for a double click. For click, down and up."`
		DeltaX float64  `json:"deltaX,omitempty" jsonschema:"For scroll, in points. Positive scrolls right."`
		DeltaY float64  `json:"deltaY,omitempty" jsonschema:"For scroll, in points. Positive scrolls down."`
		Text   string   `json:"text,omitempty" jsonschema:"For type."`
		Key    string   `json:"key,omitempty" jsonschema:"For key."`
		Mods   []string `json:"mods,omitempty" jsonschema:"Modifiers held with key: cmd, shift, alt, ctrl, fn (also command, option, control, function). Any other name is an error."`
		MS     int      `json:"ms,omitempty" jsonschema:"Milliseconds to wait. For sleep, capped at 5000."`
		App    string   `json:"app,omitempty" jsonschema:"For focus: the application to bring to the front before the actions after it, a name or bundle id as machine_ui takes."`
	}
	type inputIn struct {
		RunID   string     `json:"runId" jsonschema:"runId from machine_create"`
		Actions []actionIn `json:"actions" jsonschema:"Ordered actions to post in one batch, for example down, move, up to drag. Coordinates are fractions of the screen (0 to 1). The whole batch records as one step."`
	}
	inputDescription := "Post an ordered batch of actions (move, click, down, up, scroll, type, key, sleep, focus) in " +
		"one round trip; compose a drag from down, move and up. Keys and text go to the frontmost app: start a " +
		"batch with focus and app to bring the app you mean to the front first. machine_click, machine_type, " +
		"machine_key and machine_scroll are shortcuts for a single action." + humanDriving
	if mgr.DesktopToolkit() {
		inputDescription = inputDescriptionToolkit
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_input",
		Description: inputDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in inputIn) (*mcp.CallToolResult, machine.InputResult, error) {
		actions := make([]machine.InputAction, len(in.Actions))
		for i, a := range in.Actions {
			actions[i] = machine.InputAction{Type: a.Type, X: a.X, Y: a.Y, Button: a.Button, Clicks: a.Clicks,
				DeltaX: a.DeltaX, DeltaY: a.DeltaY, Text: a.Text, Key: a.Key, Mods: a.Mods, MS: a.MS, App: a.App}
		}
		return post(ctx, in.RunID, actions...)
	})
}

// addOldTypeKeyScroll adds machine_type, machine_key and machine_scroll as they are without the
// desktop toolkit: single-action conveniences over machine_input.
func addOldTypeKeyScroll(s *mcp.Server, post func(ctx context.Context, runID string, actions ...machine.InputAction) (*mcp.CallToolResult, machine.InputResult, error)) {
	type typeIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
		Text  string `json:"text" jsonschema:"The text to type, one character event at a time, into whatever has focus. Click into a field first if nothing does."`
		App   string `json:"app,omitempty" jsonschema:"Optional application to bring to the front first, a name or bundle id as machine_ui takes. Without it the text goes to the frontmost app."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_type",
		Description: "Type text into the machine, into whatever currently has keyboard focus in the frontmost app. An " +
			"app started with machine_exec is not frontmost: pass app, or click one of its elements first." + humanDriving,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in typeIn) (*mcp.CallToolResult, machine.InputResult, error) {
		return post(ctx, in.RunID, machine.Focused(in.App, machine.InputAction{Type: "type", Text: in.Text})...)
	})

	type keyIn struct {
		RunID string   `json:"runId" jsonschema:"runId from machine_create"`
		Key   string   `json:"key" jsonschema:"A key name: a letter, digit or punctuation character, or one of return, enter, tab, space, delete, forwarddelete, escape, left, right, up, down, home, end, pageup, pagedown, capslock, help, f1-f12."`
		Mods  []string `json:"mods,omitempty" jsonschema:"Modifiers held with the key: cmd, shift, alt, ctrl, fn (also command, option, control, function). Any other name is an error."`
		App   string   `json:"app,omitempty" jsonschema:"Optional application to bring to the front first, a name or bundle id as machine_ui takes. Without it the key goes to the frontmost app."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_key",
		Description: "Press one key, optionally with modifiers held, for example key f with mods [cmd] for " +
			"command-F. The key goes to the frontmost app; an app started with machine_exec is not frontmost, so " +
			"pass app to bring it to the front first." + humanDriving,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in keyIn) (*mcp.CallToolResult, machine.InputResult, error) {
		return post(ctx, in.RunID, machine.Focused(in.App, machine.InputAction{Type: "key", Key: in.Key, Mods: in.Mods})...)
	})

	type scrollIn struct {
		RunID  string   `json:"runId" jsonschema:"runId from machine_create"`
		X      *float64 `json:"x,omitempty" jsonschema:"Optional fraction of the screen to move the pointer to first. A scroll goes to whatever is under the pointer, so set this when you have not just clicked or moved there."`
		Y      *float64 `json:"y,omitempty" jsonschema:"Optional fraction of the screen to move the pointer to first, paired with x."`
		DeltaX float64  `json:"deltaX,omitempty" jsonschema:"Horizontal scroll amount, in points. Positive scrolls right, negative scrolls left."`
		DeltaY float64  `json:"deltaY,omitempty" jsonschema:"Vertical scroll amount, in points. Positive scrolls down, negative scrolls up."`
		App    string   `json:"app,omitempty" jsonschema:"Optional application to bring to the front first, a name or bundle id as machine_ui takes."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_scroll",
		Description: "Scroll the machine's screen under the pointer's current position, or under x,y if given." + humanDriving,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scrollIn) (*mcp.CallToolResult, machine.InputResult, error) {
		return post(ctx, in.RunID, machine.Focused(in.App,
			machine.InputAction{Type: "scroll", X: in.X, Y: in.Y, DeltaX: in.DeltaX, DeltaY: in.DeltaY})...)
	})
}
