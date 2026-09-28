package verifier

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

// The desktop toolkit's tools (daemon ADR 0006 point 9): with -desktop-toolkit the verifier gets
// the same tools as MCP, with the same names and arguments, beside the old ones (wave 4 removes
// those). Both brains run them through deskTool, so a Manual verifier makes the same calls.

// toolkitLooks are the toolkit tools that only read: observations for the verdict review.
var toolkitLooks = []string{"machine_snapshot", "machine_find"}

// isToolkitTool reports whether name is one of the toolkit's tools.
func isToolkitTool(name string) bool { return slices.Contains(toolkitLooks, name) }

// toolkitToolDefs are the toolkit tools as the reasoning model sees them.
var toolkitToolDefs = []nim.Tool{
	{
		Name: "machine_snapshot",
		Description: "Read an app's windows as one element a line, each with a ref (e17) that stays the same while " +
			"the element lives, its state, and flags from real hit-testing: covered by what, offscreen, clipped, not " +
			"drawn. The top lines name the frontmost app, the focus, and anything to handle first (a sheet, alert, " +
			"menu, or another app's window over it).",
		Schema: object(map[string]any{
			"app":      str("Optional application name or bundle id. Default: the frontmost application."),
			"window":   str("Optional: one window only, by its ref or its title."),
			"ref":      str("Optional: only the subtree under this ref."),
			"mode":     str("interactive (default: controls and text), all, or text."),
			"limit":    integer("Most elements to list. Default 250, max 1000."),
			"fullText": strList("Refs whose long text is sent whole instead of cut at 240 characters."),
		}),
	},
	{
		Name: "machine_find",
		Description: "Find elements by text in an app's whole tree, rows scrolled out of view included, and return " +
			"their refs and where they are. Use it instead of reading a long snapshot for one label.",
		Schema: object(map[string]any{
			"text":             str("Text to find in names, values, descriptions and help: a substring, or /regex/."),
			"role":             str("Optional: only elements of this role, such as Button."),
			"app":              str("Optional application name or bundle id. Default: the frontmost application."),
			"includeOffscreen": map[string]any{"type": "boolean", "description": "Also match rows scrolled out of view. Default true."},
			"limit":            integer("Most matches. Default 50, max 200."),
		}, "text"),
	},
}

// toolsFor is the verifier's tool list: with the toolkit, its tools too.
func toolsFor(toolkit bool) []nim.Tool {
	if !toolkit {
		return tools
	}
	return withToolkitDefs(tools, toolkitToolDefs, toolkitActionDefs)
}

// maxSnapshotOutput caps a snapshot's outline fed back to the model, cut on a line.
const maxSnapshotOutput = 16000

// deskTool runs one toolkit tool call as the verifier and returns the result text and its step (0
// when none was recorded). Arguments are the MCP tools' own, decoded and checked by
// internal/desktop.
func deskTool(ctx context.Context, mgr *machine.Manager, runID string, call nim.ToolCall) (result string, step int) {
	args := []byte(call.Arguments)
	switch call.Name {
	case "machine_snapshot":
		var a desktop.SnapshotArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_snapshot: " + err.Error(), 0
		}
		snap, err := mgr.Snapshot(ctx, runID, machine.HolderVerifier, a)
		if err != nil {
			return "error: " + err.Error(), snap.Step
		}
		return fmt.Sprintf("step %d\n%s", snap.Step, cutOnLine(snap.Text, maxSnapshotOutput,
			"(cut here: pass window or ref to read less)\n")), snap.Step

	case "machine_find":
		var a desktop.FindArgs
		if err := desktop.DecodeArgs(args, &a); err != nil {
			return "error: machine_find: " + err.Error(), 0
		}
		f, err := mgr.Find(ctx, runID, machine.HolderVerifier, a)
		if err != nil {
			return "error: " + err.Error(), f.Step
		}
		return fmt.Sprintf("step %d\n%s", f.Step, f.Text), f.Step
	}
	return "error: no tool named " + call.Name, 0
}

// cutOnLine cuts s to at most n bytes at a line end, and says so with note.
func cutOnLine(s string, n int, note string) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], '\n')
	return s[:cut+1] + note
}

// toolkitPromptLines are the system prompt's lines on the toolkit (docs/21 section 7.2), kept short
// and in writingRules' voice.
var toolkitPromptLines = []string{
	"- Start with machine_snapshot and read its attention line first: a sheet, alert or menu listed there comes before anything else.",
	"- Use machine_find to find one element by its text instead of reading a long snapshot.",
	"- Act by ref with machine_press, machine_type (with ref, replace and submit), machine_key and machine_scroll. They wait for the element and refuse, saying why, when it cannot be used.",
	"- An action's result is its effect. Do not take a snapshot or a screenshot to see whether it worked.",
	"- A point needs a reason: press at x and y only for content with no ref, such as a canvas.",
	"- machine_set_value is for setup only, never for the input a check is about.",
}

// systemPromptFor is the verifier's system prompt: with the toolkit, a section on its tools goes
// before the rules. The coordinate advice above it stays for the old tools.
func systemPromptFor(toolkit bool) string {
	if !toolkit {
		return systemPrompt
	}
	section := "With the desktop toolkit (refs such as e17):\n" + strings.Join(toolkitPromptLines, "\n") + "\n\n"
	return strings.Replace(systemPrompt, "\nRules:\n", "\n"+section+"Rules:\n", 1)
}
