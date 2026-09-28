package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// The toolkit's waits and evidence (daemon ADR 0006, docs/21 sections 5.5 and 5.7): a wait in
// the guest instead of screenshot polling, an assertion recorded as its own step, and screenshots
// cropped to what a check is about.

// screenshotDescription is machine_screenshot's description without the toolkit.
const screenshotDescription = "Capture the machine's screen. Returns a JPEG to look at, the path of the lossless PNG saved in " +
	"the run directory, and the image's size in pixels. scale is image pixels per desktop point: 1 on the " +
	"default image (1024x768), more on a HiDPI guest, where the image is larger than the desktop. Aim clicks as a fraction of this image " +
	"(x divided by width, y divided by height), never in pixels: machine_click takes 0 to 1."

// screenshotDescriptionToolkit is machine_screenshot's description beside the toolkit.
const screenshotDescriptionToolkit = "Capture the machine's screen, or crop it to an element (ref), a window (its ref or title) " +
	"or a region (fractions of the screen). Returns a JPEG to look at, the path of the lossless PNG saved in the run " +
	"directory, and its size in pixels. Use it to see how something looks; for what it says, machine_snapshot and " +
	"machine_expect read the exact text. scale is image pixels per desktop point."

func addWaitTools(s *mcp.Server, mgr *machine.Manager) {
	type waitIn struct {
		RunID     string `json:"runId" jsonschema:"runId from machine_create"`
		Target    any    `json:"target" jsonschema:"What to watch: a ref (\"e62\"), {\"text\": \"Done\", \"role\": \"Button\"}, {\"app\": \"TipSplit\"}, {\"window\": \"Settings\"} or \"idle\" (the app stops changing)."`
		State     string `json:"state,omitempty" jsonschema:"appears (default), disappears, enabled, disabled, focused, changes, or value (with value)."`
		Value     any    `json:"value,omitempty" jsonschema:"With state value: {\"op\": \"equals\", \"contains\" or \"matches\", \"expected\": \"42\"}."`
		TimeoutMs int    `json:"timeoutMs,omitempty" jsonschema:"How long to wait. Default 10000, max 40000."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_wait_for",
		Description: "Wait in the machine until an element, a window or an app reaches a state (appears, goes away, " +
			"becomes enabled, changes, shows a value) or the app goes idle, and return how long it took, its value, and " +
			"what changed meanwhile. Use it for anything that takes time instead of screenshots in a loop or sleeps. " +
			"A wait that runs out is a result, not an error. It only reads.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, any, error) {
		target, err := desktop.WaitTargetFrom(in.Target)
		if err != nil {
			return nil, nil, err
		}
		a := desktop.WaitArgs{Target: target, State: desktop.WaitState(in.State), TimeoutMs: in.TimeoutMs}
		if in.Value != nil {
			var v desktop.ValueMatch
			raw, _ := json.Marshal(in.Value)
			if err := desktop.DecodeArgs(raw, &v); err != nil {
				return nil, nil, err
			}
			a.Value = &v
		}
		res, err := mgr.WaitFor(ctx, in.RunID, machine.HolderCoder, a)
		if err != nil {
			return nil, nil, err
		}
		return deskText(res.Step, res.Text), res, nil
	})

	type expectIn struct {
		RunID     string `json:"runId" jsonschema:"runId from machine_create"`
		Target    any    `json:"target" jsonschema:"What to check: a ref (\"e62\"), {\"text\": \"Total\", \"role\": \"StaticText\"}, {\"window\": \"Settings\"} or {\"app\": \"TipSplit\"}."`
		Property  string `json:"property" jsonschema:"value, name, exists, visible, enabled, selected or count."`
		Op        string `json:"op,omitempty" jsonschema:"equals (default), contains or matches for value and name; equals, atLeast or atMost for count."`
		Expected  any    `json:"expected,omitempty" jsonschema:"A string for value and name, true or false for the flags (default true), a whole number for count."`
		TimeoutMs int    `json:"timeoutMs,omitempty" jsonschema:"How long it may take to hold. Default 2000, max 40000."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_expect",
		Description: "Assert what an element, window or app shows, waiting up to timeoutMs for it to hold, and record " +
			"the assertion as a step: what was expected, what was observed (exact text), when, and a crop of the " +
			"target. Use it for a check's evidence. A failed expectation is a result, not an error. It only reads.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in expectIn) (*mcp.CallToolResult, any, error) {
		target, err := desktop.WaitTargetFrom(in.Target)
		if err != nil {
			return nil, nil, err
		}
		res, err := mgr.Expect(ctx, in.RunID, machine.HolderCoder, desktop.ExpectArgs{Target: target,
			Property: desktop.ExpectProperty(in.Property), Op: desktop.ExpectOp(in.Op), Expected: in.Expected, TimeoutMs: in.TimeoutMs})
		if err != nil {
			return nil, nil, err
		}
		return deskText(res.Step, res.Text), res, nil
	})

	type shotIn struct {
		RunID  string    `json:"runId" jsonschema:"runId from machine_create"`
		Ref    string    `json:"ref,omitempty" jsonschema:"Crop to this element and a margin around it."`
		Window string    `json:"window,omitempty" jsonschema:"Crop to this window: its ref or its title."`
		Region []float64 `json:"region,omitempty" jsonschema:"Crop to [x, y, w, h], fractions of the screen 0 to 1."`
		Margin *int      `json:"margin,omitempty" jsonschema:"Points of margin around ref (default 24) or window (default 0)."`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "machine_screenshot", Description: screenshotDescriptionToolkit},
		func(ctx context.Context, req *mcp.CallToolRequest, in shotIn) (*mcp.CallToolResult, any, error) {
			if !desktop.ToolkitCall("machine_screenshot", req.Params.Arguments) {
				return screenshot(ctx, mgr, in.RunID)
			}
			data, shot, err := mgr.ScreenshotOf(ctx, in.RunID, machine.HolderCoder,
				desktop.ShotArgs{Ref: in.Ref, Window: in.Window, Region: in.Region, Margin: in.Margin})
			if err != nil {
				return nil, nil, err
			}
			return imageResult(data, shot)
		})
}

// screenshot is the whole-screen machine_screenshot, as it always was.
func screenshot(ctx context.Context, mgr *machine.Manager, runID string) (*mcp.CallToolResult, any, error) {
	data, shot, err := mgr.Screenshot(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	return imageResult(data, shot)
}

// imageResult is a screenshot's answer: the JPEG to look at and its metadata as text, with the
// metadata as the structured result.
func imageResult(pngBytes []byte, meta any) (*mcp.CallToolResult, any, error) {
	jpg, err := toJPEG(pngBytes)
	if err != nil {
		return nil, nil, err
	}
	text, _ := json.Marshal(meta)
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ImageContent{Data: jpg, MIMEType: "image/jpeg"},
		&mcp.TextContent{Text: string(text)},
	}}, meta, nil
}
