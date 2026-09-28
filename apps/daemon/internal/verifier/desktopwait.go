package verifier

import (
	"context"
	"fmt"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

// The toolkit's waits and evidence for the verifier (daemon ADR 0006, docs/21 section 5.5): a
// wait in the guest, an assertion recorded as its own observation, and a screenshot cropped to
// the element a visual check is about, described with the question the check asks.

// toolkitWaitDefs are machine_wait_for and machine_expect as the reasoning model sees them.
var toolkitWaitDefs = []nim.Tool{
	{
		Name: "machine_wait_for",
		Description: "Wait in the machine until an element, a window or an app reaches a state, or the app goes idle, " +
			"and return how long it took, the value, and what changed meanwhile. Use it for anything that takes time; " +
			"never sleep or poll with screenshots. A wait that runs out is a result, not an error.",
		Schema: object(map[string]any{
			"target": map[string]any{"description": "A ref (\"e62\"), {\"text\": \"Done\", \"role\": \"Button\"}, {\"app\": \"TipSplit\"}, {\"window\": \"Settings\"} or \"idle\"."},
			"state":  str("appears (default), disappears, enabled, disabled, focused, changes, or value (with value)."),
			"value": object(map[string]any{
				"op":       str("equals, contains or matches."),
				"expected": str("The text to compare with."),
			}, "op", "expected"),
			"timeoutMs": integer("How long to wait. Default 10000, max 40000."),
		}, "target"),
	},
	{
		Name: "machine_expect",
		Description: "Assert what an element, window or app shows, waiting up to timeoutMs for it to hold. It records " +
			"what was expected, what was observed (exact text), when, and a crop of the target: cite its step as a " +
			"check's evidence. A failed expectation is a result, and evidence for a fail.",
		Schema: object(map[string]any{
			"target":    map[string]any{"description": "A ref (\"e62\"), {\"text\": \"Total\", \"role\": \"StaticText\"}, {\"window\": \"Settings\"} or {\"app\": \"TipSplit\"}."},
			"property":  str("value, name, exists, visible, enabled, selected or count."),
			"op":        str("equals (default), contains or matches for value and name; equals, atLeast or atMost for count."),
			"expected":  map[string]any{"description": "A string for value and name, true or false for the flags (default true), a whole number for count."},
			"timeoutMs": integer("How long it may take to hold. Default 2000, max 40000."),
		}, "target", "property"),
	},
}

// screenshotToolkitDef is machine_screenshot with the toolkit's crops and a question.
var screenshotToolkitDef = nim.Tool{
	Name: "machine_screenshot",
	Description: "Capture the machine's screen, or a crop of it, and return a written description and the path of " +
		"the saved image. Only for visual checks: crop it to the element with ref and ask what the check needs " +
		"with question; text and values come exact from machine_snapshot and machine_expect.",
	Schema: object(map[string]any{
		"ref":      str("Crop to this element and a margin around it."),
		"window":   str("Crop to this window: its ref or its title."),
		"region":   map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "description": "Crop to [x, y, w, h], fractions of the screen."},
		"question": str("What the describer should answer about the image, such as \"what color is the Tapped button\"."),
	}),
}

// deskWait runs a toolkit wait or expectation as the verifier: the result text and its step.
func deskWait(ctx context.Context, mgr *machine.Manager, runID string, call nim.ToolCall) (result string, step int) {
	args := []byte(call.Arguments)
	switch call.Name {
	case "machine_wait_for":
		var a desktop.WaitArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_wait_for: " + err.Error(), 0
		}
		w, err := mgr.WaitFor(ctx, runID, machine.HolderVerifier, a)
		if err != nil {
			return "error: " + err.Error(), w.Step
		}
		return fmt.Sprintf("step %d\n%s", w.Step, w.Text), w.Step
	case "machine_expect":
		var a desktop.ExpectArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_expect: " + err.Error(), 0
		}
		e, err := mgr.Expect(ctx, runID, machine.HolderVerifier, a)
		if err != nil {
			return "error: " + err.Error(), e.Step
		}
		return fmt.Sprintf("step %d\n%s", e.Step, e.Text), e.Step
	}
	return "error: no tool named " + call.Name, 0
}

// shotQuestion is the describer's prompt for a screenshot asked with a question: the question
// first, then the usual description.
func shotQuestion(question string) string {
	return "First answer this question about the image in one or two sentences: " + strings.TrimSpace(question) +
		"\nThen:\n" + visionPrompt
}

// toolkitScreenshot is machine_screenshot with the toolkit's arguments: a crop, and a question for
// the describer.
func (v *Verifier) toolkitScreenshot(ctx context.Context, runID string, call nim.ToolCall) (string, int) {
	var in struct {
		desktop.ShotArgs
		Question string `json:"question"`
	}
	if err := desktop.DecodeArgs([]byte(call.Arguments), &in); err != nil {
		return "error: machine_screenshot: " + err.Error(), 0
	}
	png, shot, err := v.mgr.ScreenshotOf(ctx, runID, machine.HolderVerifier, in.ShotArgs)
	if err != nil {
		return "error: " + err.Error(), shot.Step
	}
	prompt := visionPrompt
	if strings.TrimSpace(in.Question) != "" {
		prompt = shotQuestion(in.Question)
	}
	what := "The screen shows"
	if in.Crops() {
		what = "The crop shows"
	}
	desc, err := v.describeWith(ctx, png, prompt)
	if err != nil {
		return fmt.Sprintf("step %d\nThe screenshot was saved to %s but it could not be described: %v", shot.Step, shot.Path, err), shot.Step
	}
	return fmt.Sprintf("step %d\n%s:\n%s\n\nThe image is saved at %s (cite this path in a verdict's evidence)",
		shot.Step, what, desc, shot.Path), shot.Step
}
