package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

const maxToolOutput = 6000 // characters of guest output fed back to the model

const humanDriving = "A human may be driving the machine; if so this comes back as an error naming them, and the machine is unharmed."

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func object(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

// tools is what the reasoning model may call.
var tools = []nim.Tool{
	{
		Name:        "machine_exec",
		Description: "Run a shell command in the machine with zsh -lc and return stdout, stderr and the exit code.",
		Schema: object(map[string]any{
			"command": str("The shell command to run."),
			"cwd":     str("Optional working directory in the guest, relative to the home directory."),
		}, "command"),
	},
	{
		Name:        "machine_screenshot",
		Description: "Capture the machine's screen. Returns a written description of what is on it and the path of the saved image.",
		Schema:      object(map[string]any{}),
	},
	{
		Name: "machine_ui",
		Description: "List the frontmost application's on-screen controls and text from its accessibility tree: " +
			"for each an id, role, title, label, value, state (selected, focused, disabled), and its center and " +
			"size as fractions of the screen. Call it before every click and after every action: it is exact, " +
			"where a screenshot description is not.",
		Schema: object(map[string]any{
			"app": str("Optional application name or bundle id to read instead of the frontmost one."),
		}),
	},
	{
		Name: "machine_click",
		Description: "Click the machine's screen. Pass element, an id from your latest machine_ui, to click that " +
			"element's center. Otherwise pass x and y, fractions of the screen (0 to 1), not pixels. " + humanDriving,
		Schema: object(map[string]any{
			"element": integer("An element id from the latest machine_ui. Clicks its center; x and y are then ignored."),
			"x":       num("Horizontal position as a fraction of the screen, 0 (left) to 1 (right)."),
			"y":       num("Vertical position as a fraction of the screen, 0 (top) to 1 (bottom)."),
			"button":  str("left (default), right, or middle."),
			"clicks":  integer("2 for a double click. Default 1."),
		}),
	},
	{
		Name: "machine_type",
		Description: "Type text into the machine, into whatever currently has keyboard focus. Click into a " +
			"field first if nothing does. " + humanDriving,
		Schema: object(map[string]any{"text": str("The text to type, one character event at a time.")}, "text"),
	},
	{
		Name: "machine_key",
		Description: "Press one key, optionally with modifiers held down, for example key f with mods [cmd] " +
			"for command-F. " + humanDriving,
		Schema: object(map[string]any{
			"key": str("A key name: a letter, digit or punctuation character, or one of return, enter, tab, space, " +
				"delete, forwarddelete, escape, left, right, up, down, home, end, pageup, pagedown, capslock, " +
				"help, f1-f12."),
			"mods": strList("Modifiers held with the key: cmd, shift, alt, ctrl, fn. Any other name is an error."),
		}, "key"),
	},
	{
		Name: "machine_scroll",
		Description: "Scroll the machine's screen under the pointer's current position, or under x,y if given. " +
			humanDriving,
		Schema: object(map[string]any{
			"x":      num("Optional fraction of the screen to move the pointer to first."),
			"y":      num("Optional fraction of the screen to move the pointer to first, paired with x."),
			"deltaX": num("Horizontal scroll amount, in points. Positive scrolls right."),
			"deltaY": num("Vertical scroll amount, in points. Positive scrolls down, negative scrolls up."),
		}),
	},
	{
		Name: "machine_input",
		Description: "Post an ordered batch of actions (move, click, down, up, scroll, type, key, sleep) in " +
			"one round trip: compose a drag out of down, move and up. machine_click, machine_type, " +
			"machine_key and machine_scroll are conveniences over this for the common single-action case. " +
			"Coordinates are fractions of the screen (0 to 1). " + humanDriving,
		Schema: object(map[string]any{
			"actions": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"type":   str("One of: move, click, down, up, scroll, type, key, sleep."),
					"x":      num("Fraction of the screen, 0 to 1. For move, click, down and up."),
					"y":      num("Fraction of the screen, 0 to 1. For move, click, down and up."),
					"button": str("left (default), right, or middle. For click, down and up."),
					"clicks": integer("2 for a double click. For click, down and up."),
					"deltaX": num("For scroll."),
					"deltaY": num("For scroll. Positive scrolls down."),
					"text":   str("For type."),
					"key":    str("For key."),
					"mods":   strList("Modifiers held with key: cmd, shift, alt, ctrl, fn. Any other name is an error."),
					"ms":     integer("Milliseconds to wait. For sleep, capped at 5000."),
				}, "type"),
				"description": "Ordered actions to post in one batch, for example down, move, up to drag. The whole batch records as one step.",
			},
		}, "actions"),
	},
	{
		Name:        "reply",
		Description: "Answer whoever spoke when no verdict is called for: a status update, an explanation, or a plain answer to a question. Ends your turn.",
		Schema:      object(map[string]any{"text": str("What you want to say, in plain words.")}, "text"),
	},
	{
		Name:        "ask",
		Description: "Ask the coder or the human for something you need and cannot find out yourself. Ends your turn; you continue when someone answers.",
		Schema:      object(map[string]any{"question": str("What you need to know, and why.")}, "question"),
	},
	{
		Name:        "report_verdict",
		Description: "End the turn with a verdict. It is a proposal: the coder or a human may accept or dispute it.",
		Schema: object(map[string]any{
			"verdict": map[string]any{
				"type":        "string",
				"enum":        []string{"pass", "fail", "inconclusive"},
				"description": "pass if the task succeeded, fail if the thing under test is broken, inconclusive if you could not tell.",
			},
			"summary":  str("What happened and what the evidence shows, in a few sentences."),
			"evidence": strList("Step numbers (as 'step 4') and screenshot paths the verdict rests on. When the task is about the screen, include the full path of your latest machine_screenshot."),
		}, "verdict", "summary"),
	},
}

// runTool executes one machine tool call and returns what the model should
// see, plus the step it recorded (0 if none).
func (v *Verifier) runTool(ctx context.Context, runID string, call nim.ToolCall) (result string, step int) {
	switch call.Name {
	case "machine_exec", "machine_screenshot", "machine_ui",
		"machine_click", "machine_type", "machine_key", "machine_scroll", "machine_input":
		// Still costs a step, so a model retrying a booting machine cannot spin.
		if why := unusable(ctx, v.mgr, runID); why != "" {
			return "error: the machine is not usable: " + why, 0
		}
	}
	args := []byte(call.Arguments)
	switch call.Name {
	case "machine_exec":
		var in struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
		}
		if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Command) == "" {
			return "error: machine_exec needs a command", 0
		}
		res, err := v.mgr.Exec(ctx, runID, in.Command, in.Cwd, execTimeout)
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		return execResultText(res), res.Step

	case "machine_screenshot":
		png, shot, err := v.mgr.Screenshot(ctx, runID)
		if err != nil {
			return "error: " + err.Error(), shot.Step
		}
		desc, err := v.describe(ctx, png)
		if err != nil {
			// A blind verifier is still useful.
			return fmt.Sprintf("step %d\nThe screenshot was saved to %s but it could not be described: %v", shot.Step, shot.Path, err), shot.Step
		}
		return fmt.Sprintf("step %d\n%s\nThe screen shows:\n%s\n\nThe image is saved at %s (cite this path in a verdict's evidence)",
			shot.Step, shotGeometry(shot), desc, shot.Path), shot.Step

	case "machine_ui":
		var in struct {
			App string `json:"app"`
		}
		_ = json.Unmarshal(args, &in)
		return uiResult(ctx, v.mgr, runID, in.App)

	case "machine_click":
		var in struct {
			Element int      `json:"element"`
			X       *float64 `json:"x"`
			Y       *float64 `json:"y"`
			Button  string   `json:"button"`
			Clicks  int      `json:"clicks"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "error: machine_click needs an element id or x and y", 0
		}
		return click(ctx, v.mgr, runID, in.Element, in.X, in.Y, in.Button, in.Clicks)

	case "machine_type":
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil || in.Text == "" {
			return "error: machine_type needs text", 0
		}
		return postInput(ctx, v.mgr, runID, fmt.Sprintf("typed %q", in.Text),
			machine.InputAction{Type: "type", Text: in.Text})

	case "machine_key":
		var in struct {
			Key  string   `json:"key"`
			Mods []string `json:"mods"`
		}
		if err := json.Unmarshal(args, &in); err != nil || in.Key == "" {
			return "error: machine_key needs a key", 0
		}
		return postInput(ctx, v.mgr, runID, "pressed "+keyLabel(in.Key, in.Mods),
			machine.InputAction{Type: "key", Key: in.Key, Mods: in.Mods})

	case "machine_scroll":
		var in struct {
			X      *float64 `json:"x"`
			Y      *float64 `json:"y"`
			DeltaX float64  `json:"deltaX"`
			DeltaY float64  `json:"deltaY"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "error: machine_scroll needs deltaX or deltaY", 0
		}
		return postInput(ctx, v.mgr, runID, fmt.Sprintf("scrolled (deltaX %.0f, deltaY %.0f)", in.DeltaX, in.DeltaY),
			machine.InputAction{Type: "scroll", X: in.X, Y: in.Y, DeltaX: in.DeltaX, DeltaY: in.DeltaY})

	case "machine_input":
		// The tool schema mirrors machine.InputAction's JSON tags.
		var in struct {
			Actions []machine.InputAction `json:"actions"`
		}
		if err := json.Unmarshal(args, &in); err != nil || len(in.Actions) == 0 {
			return "error: machine_input needs a non-empty actions array", 0
		}
		return postInput(ctx, v.mgr, runID, fmt.Sprintf("posted %d actions", len(in.Actions)), in.Actions...)

	default:
		return "error: no tool named " + call.Name, 0
	}
}

func (v *Verifier) describe(ctx context.Context, png []byte) (string, error) {
	if v.cfg.VisionModel == "" {
		return "", errors.New("no vision model configured")
	}
	jpeg, err := toJPEG(png)
	if err != nil {
		return "", err
	}
	// The vision model sometimes answers with nothing but <unk> tokens; one
	// more try has been enough. A second garbage answer is an error, so the
	// reasoning model is told it could not see rather than handed noise.
	for attempt := 0; ; attempt++ {
		text, err := v.llm.Describe(ctx, v.cfg.VisionModel, jpeg, visionPrompt)
		if err != nil || !strings.Contains(text, "<unk>") {
			return text, err
		}
		if attempt == 1 {
			return "", errors.New("the vision model answered with unreadable tokens twice")
		}
	}
}

// postInput posts one batch as the verifier and returns the tool result text,
// "step N\n<done>" on success, and the step it recorded.
func postInput(ctx context.Context, mgr *machine.Manager, runID, done string, actions ...machine.InputAction) (string, int) {
	// Per call, never per turn (ADR 0009, issue #11).
	res, err := mgr.InputAs(ctx, runID, machine.HolderVerifier, actions)
	if err != nil {
		return "error: " + err.Error(), res.Step
	}
	return fmt.Sprintf("step %d\n%s", res.Step, done), res.Step
}

// maxUIOutput caps the outline fed back to the model. It is larger than
// maxToolOutput because an outline cut in the middle loses the controls, so
// it is cut at the end, on a line.
const maxUIOutput = 16000

// verifierUILimit is how many elements one read lists for the model.
const verifierUILimit = 200

// uiResult reads the UI tree and returns its outline, both brains' text.
func uiResult(ctx context.Context, mgr *machine.Manager, runID, app string) (string, int) {
	tree, err := mgr.UI(ctx, runID, machine.HolderVerifier, app, verifierUILimit)
	if err != nil {
		return "error: " + err.Error() + ". Take a machine_screenshot and use the positions it describes instead.", tree.Step
	}
	out := tree.Outline()
	if len(out) > maxUIOutput {
		cut := strings.LastIndexByte(out[:maxUIOutput], '\n')
		out = out[:cut+1] + "(cut here: call machine_ui with app to read one application)\n"
	}
	return fmt.Sprintf("step %d\n%s", tree.Step, out), tree.Step
}

// click aims at element, from the latest UI read, or at x,y. It is both
// brains' machine_click.
func click(ctx context.Context, mgr *machine.Manager, runID string, element int, x, y *float64, button string, clicks int) (string, int) {
	done := ""
	if element != 0 {
		e, err := mgr.ElementCenter(runID, machine.HolderVerifier, element, 0)
		if err != nil {
			return "error: " + err.Error(), 0
		}
		x, y = &e.X, &e.Y
		done = fmt.Sprintf("clicked [%d] %s in %s at (%.3f, %.3f)", e.ID, e.Name(), e.App, e.X, e.Y)
	}
	if x == nil || y == nil {
		return "error: machine_click needs an element id from machine_ui, or both x and y", 0
	}
	if done == "" {
		done = fmt.Sprintf("clicked (%.3f, %.3f)", *x, *y)
	}
	return postInput(ctx, mgr, runID, done,
		machine.InputAction{Type: "click", X: x, Y: y, Button: button, Clicks: clicks})
}

func keyLabel(key string, mods []string) string {
	if len(mods) == 0 {
		return key
	}
	return strings.Join(mods, "+") + "+" + key
}

// execResultText is the command result both brains report.
func execResultText(res machine.ExecResult) string {
	return fmt.Sprintf("step %d\nexit code %d\nstdout:\n%s\nstderr:\n%s",
		res.Step, res.ExitCode, clamp(res.Stdout), clamp(res.Stderr))
}

// clamp keeps both ends of long output: the command near the start, the
// error near the end.
func clamp(s string) string {
	if len(s) <= maxToolOutput {
		return s
	}
	half := maxToolOutput / 2
	return s[:half] + "\n...[middle removed]...\n" + s[len(s)-half:]
}
