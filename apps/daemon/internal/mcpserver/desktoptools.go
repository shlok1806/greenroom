package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// The desktop toolkit's tools (daemon ADR 0006 point 9), only with serve -desktop-toolkit. The
// manager does the work and internal/desktop the wording; these map arguments and results. Each
// returns its text (first line "step N") and its structured result. Their structured results
// carry the agent's own shapes, so they declare no output schema.

// addDesktopTools adds the toolkit's tools when the manager drives machines through the guest
// agent; without it the tool list is exactly what it was before the toolkit.
func addDesktopTools(s *mcp.Server, mgr *machine.Manager) {
	if !mgr.DesktopToolkit() {
		return
	}
	addTreeTools(s, mgr)
	addActionTools(s, mgr)
	addWaitTools(s, mgr)
}

// deskText is a toolkit tool's result: its text under the step it recorded.
func deskText(step int, text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("step %d\n%s", step, text)}}}
}

// uiDescriptionToolkit is machine_ui's description when the toolkit's tools exist: the same read,
// pointing at machine_snapshot (docs/21 section 7.5).
const uiDescriptionToolkit = "Read the accessibility tree of the frontmost application (or a named one) as numbered elements with " +
	"centers as fractions of the screen, for machine_click. Prefer machine_snapshot: its refs outlive reads, it " +
	"flags covered, offscreen and clipped elements, and the actions take its refs. It only reads; it needs no " +
	"control of the screen."

func addTreeTools(s *mcp.Server, mgr *machine.Manager) {
	type snapshotIn struct {
		RunID    string   `json:"runId" jsonschema:"runId from machine_create"`
		App      string   `json:"app,omitempty" jsonschema:"Application name or bundle id. Default: the frontmost application."`
		Window   string   `json:"window,omitempty" jsonschema:"One window only: its ref (such as e1) or its title."`
		Ref      string   `json:"ref,omitempty" jsonschema:"Only the subtree under this ref."`
		Mode     string   `json:"mode,omitempty" jsonschema:"interactive (default: controls and text), all, or text."`
		Limit    int      `json:"limit,omitempty" jsonschema:"Most elements to list. Default 250, max 1000."`
		FullText []string `json:"fullText,omitempty" jsonschema:"Refs whose long text is sent whole instead of cut at 240 characters."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_snapshot",
		Description: "Read an app's windows as one element a line, each with a ref (e17) that stays the same while " +
			"the element lives, its state, and flags from real hit-testing: covered by what, offscreen in which scroll " +
			"area, clipped, not drawn. The top lines name the frontmost app, the focus, and anything that needs " +
			"handling first (a sheet, alert, menu or another app's window over it). Take one before acting, then act " +
			"by ref with machine_press, machine_type, machine_set_value, machine_key and machine_scroll. It only " +
			"reads; it needs no control of the screen.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotIn) (*mcp.CallToolResult, any, error) {
		snap, err := mgr.Snapshot(ctx, in.RunID, machine.HolderCoder, desktop.SnapshotArgs{App: in.App, Window: in.Window,
			Ref: in.Ref, Mode: desktop.SnapshotMode(in.Mode), Limit: in.Limit, FullText: in.FullText})
		if err != nil {
			return nil, nil, err
		}
		return deskText(snap.Step, snap.Text), snap, nil
	})

	type findIn struct {
		RunID            string `json:"runId" jsonschema:"runId from machine_create"`
		Text             string `json:"text" jsonschema:"Text to find in names, values, descriptions and help: a substring, or /regex/."`
		Role             string `json:"role,omitempty" jsonschema:"Only elements of this role, such as Button or TextField."`
		App              string `json:"app,omitempty" jsonschema:"Application name or bundle id. Default: the frontmost application."`
		IncludeOffscreen *bool  `json:"includeOffscreen,omitempty" jsonschema:"Also match rows scrolled out of view. Default true."`
		Limit            int    `json:"limit,omitempty" jsonschema:"Most matches. Default 50, max 200."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_find",
		Description: "Find elements by text in an app's whole tree, rows scrolled out of view included, and return " +
			"them with their refs and where they are (in which window, offscreen in which scroll area). Use it " +
			"instead of reading a long snapshot for one label. It only reads.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findIn) (*mcp.CallToolResult, any, error) {
		f, err := mgr.Find(ctx, in.RunID, machine.HolderCoder, desktop.FindArgs{Text: in.Text, Role: in.Role, App: in.App,
			IncludeOffscreen: in.IncludeOffscreen, Limit: in.Limit})
		if err != nil {
			return nil, nil, err
		}
		return deskText(f.Step, f.Text), f, nil
	})
}
