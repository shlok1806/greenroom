package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// The toolkit's tools (daemon ADR 0006 point 9) through a real MCP client, on the real manager
// with the fake tart's fake guest agent answering each op from a canned agent-<op>.json.

// newToolkitHarness is newHarness on a daemon run with -desktop-toolkit.
func newToolkitHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessOn(t, []machine.Option{machine.WithDesktopToolkit(true)})
}

// can writes the fake agent's canned answer to op: a result, or {"error": ...} when body has one.
func (h *harness) can(op, body string) {
	h.t.Helper()
	if !strings.HasPrefix(strings.TrimSpace(body), `{"error"`) {
		body = `{"result":` + body + `}`
	}
	if err := os.WriteFile(filepath.Join(h.control, "agent-"+op+".json"), []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// toolkitTools are the tools only a -desktop-toolkit daemon has.
var toolkitTools = []string{"machine_snapshot", "machine_find", "machine_press", "machine_set_value"}

// listed is the harness's tools by name.
func (h *harness) listed() map[string]*mcp.Tool {
	h.t.Helper()
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func TestTheToolkitsToolsExistOnlyWithTheToolkit(t *testing.T) {
	off, on := newHarness(t).listed(), newToolkitHarness(t)
	tools := on.listed()
	for _, name := range toolkitTools {
		if off[name] != nil {
			t.Errorf("%s exists without the toolkit", name)
		}
		if tools[name] == nil || tools[name].Description == "" {
			t.Errorf("%s is missing with the toolkit", name)
		}
	}
	if len(tools) != len(off)+len(toolkitTools) {
		t.Errorf("the toolkit has %d tools, want the %d without it plus %d", len(tools), len(off), len(toolkitTools))
	}
	if d := tools["machine_ui"].Description; !strings.Contains(d, "Prefer machine_snapshot") {
		t.Errorf("machine_ui does not point at machine_snapshot: %q", d)
	}
	if got := on.session.InitializeResult().Instructions; !strings.Contains(got, "machine_snapshot") || strings.Contains(got, "machine_ui to find") {
		t.Errorf("the instructions do not point at the toolkit: %q", got)
	}
	if got := instructions(false); strings.Contains(got, "machine_snapshot") || !strings.Contains(got, "machine_ui to find controls") {
		t.Errorf("the instructions without the toolkit changed: %q", got)
	}
}

const snapshotJSON = `{"screen":{"width":1024,"height":768},"frontmost":{"name":"Navlab","pid":7},"app":{"name":"Navlab","pid":7},
"nodes":[{"ref":"e1","role":"Window","name":"Navlab","frame":[0,0,400,300],"vis":[0,0,400,300]},
{"ref":"e4","role":"Button","name":"Open run","frame":[10,10,80,20],"vis":[10,10,80,20],"depth":1,"window":"e1"}]}`

// callText runs a tool that must succeed and returns its text and structured result.
func (h *harness) callText(name string, args map[string]any) (string, map[string]any) {
	h.t.Helper()
	var out map[string]any
	res := h.call(name, args, &out)
	return text(res), out
}

// toolError runs a tool that must fail and returns its error text.
func (h *harness) toolError(name string, args map[string]any) string {
	h.t.Helper()
	res := h.raw(name, args)
	if !res.IsError {
		h.t.Fatalf("%s %v succeeded: %s", name, args, text(res))
	}
	return text(res)
}

// A read-only toolkit tool answers with its step and text, and its structured result; a bad
// argument is refused with words that say what to send.
func TestSnapshotAndFind(t *testing.T) {
	h := newToolkitHarness(t)
	runID := h.ready()
	h.can("snapshot", snapshotJSON)
	got, out := h.callText("machine_snapshot", map[string]any{"runId": runID, "app": "Navlab"})
	if !strings.HasPrefix(got, "step ") || !strings.Contains(got, `e4 Button "Open run"`) {
		t.Errorf("text:\n%s", got)
	}
	if nodes, _ := out["nodes"].([]any); len(nodes) != 2 || out["step"] == nil {
		t.Errorf("structured %v", out)
	}
	if msg := h.toolError("machine_snapshot", map[string]any{"runId": runID, "ref": "17"}); !strings.Contains(msg, "not a ref") {
		t.Errorf("a bare number as ref: %s", msg)
	}

	h.can("find", `{"matches":[{"ref":"e4","role":"Button","name":"Open run","window":"e1"}],"searched":2}`)
	got, out = h.callText("machine_find", map[string]any{"runId": runID, "text": "Open"})
	if !strings.Contains(got, `1 match for "Open"`) || !strings.Contains(got, "e4") {
		t.Errorf("find text:\n%s", got)
	}
	if m, _ := out["matches"].([]any); len(m) != 1 {
		t.Errorf("find structured %v", out)
	}
	if msg := h.toolError("machine_find", map[string]any{"runId": runID, "text": ""}); !strings.Contains(msg, "text: missing") {
		t.Errorf("an empty find: %s", msg)
	}
}
