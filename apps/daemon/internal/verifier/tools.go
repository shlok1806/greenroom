package verifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

const maxToolOutput = 6000 // characters of guest output fed back to the model

const humanDriving = "A human may be driving the machine; if so this comes back as an error naming them, and the machine is unharmed. Then ask them for the screen or reply that you are waiting; never retry at once, and never report a verdict because of it."

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
			"button":  str("left (default), right, or middle. Any other name is an error."),
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
					"button": str("left (default), right, or middle. For click, down and up. Any other name is an error."),
					"clicks": integer("2 for a double click. For click, down and up."),
					"deltaX": num("For scroll. Positive scrolls right."),
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
		Schema:      object(map[string]any{"text": str("What you want to say: the finding first, in 1 to 3 plain sentences.")}, "text"),
	},
	{
		Name:        "ask",
		Description: "Ask the coder or the human for something you need and cannot find out yourself. Ends your turn; you continue when someone answers.",
		Schema:      object(map[string]any{"question": str("What you need to know, in one sentence. Add the reason only when the answer depends on it.")}, "question"),
	},
	{
		Name: "declare_checks",
		Description: "Declare the acceptance checks you derived from the task, before any input: each one an " +
			"outcome the task claims, what should be true after the actions it describes, that you can observe on " +
			"the machine. A setup step or an action the task tells you to do is not a check; an intermediate state " +
			"is one only when the task claims something about it. Input tools are refused on a task until you " +
			"declare. Declaring again replaces the list until your first input; after it, a new declaration must " +
			"keep every check and its kinds, and may only add. Your verdict answers each check by id. Each check has " +
			"a kind: value (a text, number or state; the default), visual (only for a claim about how it looks or " +
			"whether it can be seen: needs a machine_screenshot after its actions) or " +
			"timing (it happens within some seconds of its last action: needs an observation that started in time); " +
			"a check can be visual and timing. greenroom adds a kind when the criterion's words claim appearance or " +
			"speed, and says so.",
		Schema: object(map[string]any{
			"checks": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": 12,
				"items": object(map[string]any{
					"id":        str("A short unique id, like \"total\" or \"tip-25\"."),
					"criterion": str("What you will observe when it holds, in one sentence, like \"Each pays shows $48.00 for $160, 20%, 4 people\"."),
					"kinds": map[string]any{"type": "array", "items": map[string]any{"type": "string",
						"enum": []string{session.CheckValue, session.CheckVisual, session.CheckTiming}},
						"description": "[\"value\"] (default), or visual, timing or both: a check that is both needs both kinds of evidence."},
					"within": num("Timing checks only: seconds from the end of the last action, at least 2 (default 2)."),
				}, "id", "criterion"),
				"description": "1 to 12 checks.",
			},
		}, "checks"),
	},
	{
		Name: "report_verdict",
		Description: "End the turn with a verdict that answers every declared check. It is a proposal: the coder " +
			"or a human may accept or dispute it. greenroom checks the evidence and refuses a verdict that does " +
			"not hold: a pass needs every check pass; a fail needs a failing check with evidence, and its other " +
			"answers that do not hold are posted unchecked with the reason; inconclusive marks what you could not " +
			"show unchecked.",
		Schema: object(map[string]any{
			"verdict": map[string]any{
				"type":        "string",
				"enum":        []string{"pass", "fail", "inconclusive"},
				"description": "pass if every check passed, fail if the thing under test is broken, inconclusive if you could not tell.",
			},
			"summary": str("The result and its evidence in 2 or 3 short sentences, citing steps as 'step 4'. For a fail, name the cause."),
			"checks": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"id":     str("The id of a declared check."),
					"status": map[string]any{"type": "string", "enum": []string{"pass", "fail", "unchecked"}},
					"evidence": map[string]any{"type": "array", "items": map[string]any{"type": "integer"},
						"description": "Step numbers of your observations (machine_ui, machine_screenshot, machine_exec) that show the result, at least one after every step in actions."},
					"actions": map[string]any{"type": "array", "items": map[string]any{"type": "integer"},
						"description": "Step numbers of the inputs this check depends on; empty for a check that only looks."},
					"observed": str("What the evidence showed, in one sentence."),
				}, "id", "status", "evidence", "actions", "observed"),
				"description": "One answer per declared check.",
			},
			"evidence": strList("Artifact paths only, such as the full path of a machine_screenshot. Cite steps in checks."),
		}, "verdict", "summary", "checks"),
	},
}

// runTool executes one machine tool call and returns what the model should see, plus the step
// it recorded (0 if none). An input that ran ends with its effect (effectCheck, ADR 0024), and,
// when it is a click on a control that changed nothing before in this turn, says so (dead,
// ADR 0029).
func (v *Verifier) runTool(ctx context.Context, runID string, call nim.ToolCall, dead *deadControls) (result string, step int) {
	if v.mgr.DesktopToolkit() && isToolkitAction(call) {
		// Its result is its own effect (daemon ADR 0006 point 8): no UI read follows it.
		if why := unusable(ctx, v.mgr, runID); why != "" {
			return "error: the machine is not usable: " + why, 0
		}
		result, step, kind, ref := deskAction(ctx, v.mgr, runID, call)
		if step != 0 && !strings.HasPrefix(result, "error:") && call.Name == "machine_press" && ref != "" {
			if hint := dead.record(&target{key: "ref\x00" + ref, name: ref, self: true}, kind, step); hint != "" {
				result += "\n" + hint
			}
		}
		return result, step
	}
	if !isInputTool(call.Name) {
		return v.machineTool(ctx, runID, call)
	}
	prev, hadPrev := v.mgr.LastUI(runID, machine.HolderVerifier)
	target := clickTarget(call, prev, hadPrev)
	inputAt := time.Now()
	result, step = v.machineTool(ctx, runID, call)
	if step == 0 || strings.HasPrefix(result, "error:") {
		return result, step
	}
	effect, kind, read := v.effectCheck(ctx, runID, step, inputAt, prev, hadPrev)
	result += "\n" + effect
	if hint := dead.record(target, kind, read); hint != "" {
		result += "\n" + hint
	}
	return result, step
}

// machineTool executes one machine tool call.
func (v *Verifier) machineTool(ctx context.Context, runID string, call nim.ToolCall) (result string, step int) {
	switch call.Name {
	case "machine_exec", "machine_screenshot", "machine_ui",
		"machine_click", "machine_type", "machine_key", "machine_scroll", "machine_input":
		// Still costs a step, so a model retrying a booting machine cannot spin.
		if why := unusable(ctx, v.mgr, runID); why != "" {
			return "error: the machine is not usable: " + why, 0
		}
	default:
		if isToolkitTool(call.Name) && v.mgr.DesktopToolkit() {
			if why := unusable(ctx, v.mgr, runID); why != "" {
				return "error: the machine is not usable: " + why, 0
			}
			return deskTool(ctx, v.mgr, runID, call)
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
			return `error: machine_exec needs a command: pass the shell command to run in "command", like {"command": "ls"}` +
				argsProblem(args, err), 0
		}
		res, err := v.mgr.ExecAs(ctx, runID, machine.HolderVerifier, in.Command, in.Cwd, execTimeout)
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		return execResultText(res), res.Step

	case "machine_screenshot":
		if v.mgr.DesktopToolkit() && desktop.ToolkitCall(call.Name, args) {
			return v.toolkitScreenshot(ctx, runID, call)
		}
		png, shot, err := v.mgr.ScreenshotAs(ctx, runID, machine.HolderVerifier)
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
			return "error: machine_click needs element, an id from your latest machine_ui, or x and y as numbers " +
				"from 0 to 1" + argsProblem(args, err), 0
		}
		return click(ctx, v.mgr, runID, in.Element, in.X, in.Y, in.Button, in.Clicks)

	case "machine_type":
		var in struct {
			Text looseString `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil || in.Text == "" {
			return `error: machine_type needs text: pass the characters to type in "text", like {"text": "hello"}; ` +
				"to press a key such as return or tab, use machine_key" + argsProblem(args, err), 0
		}
		text := string(in.Text)
		return postInput(ctx, v.mgr, runID, fmt.Sprintf("typed %q", text),
			machine.InputAction{Type: "type", Text: text})

	case "machine_key":
		var in struct {
			Key  looseString `json:"key"`
			Mods []string    `json:"mods"`
		}
		if err := json.Unmarshal(args, &in); err != nil || in.Key == "" {
			return `error: machine_key needs a key: pass one key name in "key", like {"key": "return"} or ` +
				`{"key": "a", "mods": ["cmd"]}; to type text, use machine_type` + argsProblem(args, err), 0
		}
		return postInput(ctx, v.mgr, runID, "pressed "+keyLabel(string(in.Key), in.Mods),
			machine.InputAction{Type: "key", Key: string(in.Key), Mods: in.Mods})

	case "machine_scroll":
		var in struct {
			X      *float64 `json:"x"`
			Y      *float64 `json:"y"`
			DeltaX float64  `json:"deltaX"`
			DeltaY float64  `json:"deltaY"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "error: machine_scroll needs deltaX or deltaY as numbers of points, like {\"deltaY\": 200} to " +
				"scroll down" + argsProblem(args, err), 0
		}
		return postInput(ctx, v.mgr, runID, fmt.Sprintf("scrolled (deltaX %.0f, deltaY %.0f)", in.DeltaX, in.DeltaY),
			machine.InputAction{Type: "scroll", X: in.X, Y: in.Y, DeltaX: in.DeltaX, DeltaY: in.DeltaY})

	case "machine_input":
		// The tool schema mirrors machine.InputAction's JSON tags.
		var in struct {
			Actions []machine.InputAction `json:"actions"`
		}
		// Unknown fields are an error the model can correct: "element" inside a batch was dropped
		// and the click went to the pointer (issue #85). machine_click takes an element.
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return "error: machine_input: " + err.Error() + " (each action takes type, x, y, button, clicks, deltaX, deltaY, text, key, mods, ms; to click an element, call machine_click with element)", 0
		}
		if len(in.Actions) == 0 {
			return "error: machine_input needs a non-empty actions array", 0
		}
		return postInput(ctx, v.mgr, runID, fmt.Sprintf("posted %d actions", len(in.Actions)), in.Actions...)

	default:
		return "error: no tool named " + call.Name, 0
	}
}

// looseString is a string argument that also takes a number, as written. A model typing a bill
// of 160 may send {"text": 160}, which a plain string field refused as no text at all (issue #125).
type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = looseString(str)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*s = looseString(n.String())
		return nil
	}
	return fmt.Errorf("want a string, got %s", clip(string(b), 80))
}

// argsProblem says what was wrong with a refused call's arguments, so a wrong field name or type
// is visible to the model and in the log: " (this call sent: value)" or the decode error.
func argsProblem(args []byte, err error) string {
	if err != nil {
		return " (your arguments did not parse: " + clip(err.Error(), 200) + ")"
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil {
		return ""
	}
	if len(fields) == 0 {
		return " (this call sent no fields)"
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return " (this call sent: " + strings.Join(names, ", ") + ")"
}

func (v *Verifier) describe(ctx context.Context, png []byte) (string, error) {
	return v.describeWith(ctx, png, visionPrompt)
}

// describeWith is describe with the describer asked prompt.
func (v *Verifier) describeWith(ctx context.Context, png []byte, prompt string) (string, error) {
	if v.cfg.VisionModel == "" {
		return "", errors.New("no vision model configured")
	}
	jpeg, err := toJPEG(png)
	if err != nil {
		return "", err
	}
	// The vision model sometimes answers with noise or nothing (see readableDescription); one
	// more try has been enough. A second such answer is an error, so the reasoning model is
	// told it could not see rather than handed noise or an empty description.
	for attempt := 0; ; attempt++ {
		text, err := v.llm.Describe(ctx, v.cfg.VisionModel, jpeg, prompt)
		if err != nil || readableDescription(text) {
			return text, err
		}
		if attempt == 1 {
			return "", errors.New("the vision model gave no readable description twice")
		}
	}
}

// minDescriptionLetters is the fewest letters a description may have. Every real answer names
// its five parts, so it has far more.
const minDescriptionLetters = 10

// readableDescription is false for the unusable answers describers have given (ADR 0030): a
// page of <unk> tokens (nano-omni), no text at all (nano-omni, kimi-k3 with thinking on, and
// muse-glimmer-30b when it spends its budget reasoning) and a line of punctuation such as
// "!!!!" (kimi-k3 with thinking off, 10 of 40 on 2026-09-26).
func readableDescription(text string) bool {
	if strings.Contains(text, "<unk>") {
		return false
	}
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
			if letters >= minDescriptionLetters {
				return true
			}
		}
	}
	return false
}

// screenTakenPrefix starts the tool result for input refused because someone else holds the
// screen; Turn counts these (issue #97).
const screenTakenPrefix = "error: the screen is taken: "

func screenTakenResult(err error) string {
	return screenTakenPrefix + err.Error() + ". Do not retry input now: it fails the same way while they hold it. " +
		"You may still look (machine_ui, machine_screenshot). End the turn with ask, asking them to give the screen " +
		"back, or reply that you are waiting. Never report a verdict because of this: it says nothing about the app."
}

// staleLookPrefix starts the tool result for input refused because the screen changed hands
// since the verifier last looked (issue #124). A look clears its repeat count (repeats.record).
const staleLookPrefix = "error: the screen changed hands since your last look"

func staleLookResult(err error) string {
	return "error: " + err.Error() + ". It may have changed: plan again from what you see, not from ids or " +
		"positions you read before."
}

// postInput posts one batch as the verifier and returns the tool result text,
// "step N\n<done>" on success, and the step it recorded.
func postInput(ctx context.Context, mgr *machine.Manager, runID, done string, actions ...machine.InputAction) (string, int) {
	// Per call, never per turn (ADR 0009, issue #11).
	res, err := mgr.InputAs(ctx, runID, machine.HolderVerifier, actions)
	if errors.Is(err, machine.ErrScreenTaken) {
		return screenTakenResult(err), res.Step
	}
	if errors.Is(err, machine.ErrStaleLook) {
		return staleLookResult(err), res.Step
	}
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
		done = fmt.Sprintf("clicked [%d] %s in %s at (%.3f, %.3f)", e.ID, e.Name(), e.App, e.X, e.Y)
		// Its app first, then the part of it that shows (daemon ADR 0009).
		return postInput(ctx, mgr, runID, done, machine.FocusThenClick(e, button, clicks)...)
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
