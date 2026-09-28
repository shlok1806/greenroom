package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

const pressJSON = `{"target":{"ref":"e4","role":"Button","name":"Open run","frame":[10,10,80,20],"vis":[10,10,80,20]},
"point":[50,20],"tried":[[50,20]],"via":"pointer",
"before":{"app":{"name":"Navlab"},"nodes":[{"ref":"e4","role":"Button","name":"Open run"}]},
"after":{"app":{"name":"Navlab"},"nodes":[{"ref":"e4","role":"Button","name":"Open run"},{"ref":"e9","role":"Sheet","name":"Run"}]},
"settled":true,"settledMs":300}`

// agentOps is the ops the fake agent was asked for, in order.
func (h *harness) agentOps() []string {
	h.t.Helper()
	var out []string
	for _, line := range testsupport.ControlLines(h.t, h.control, "agent-requests") {
		var r struct {
			Op string `json:"op"`
		}
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, r.Op)
		}
	}
	return out
}

// Every action says its result is its effect, and the old tools point at the new ones.
func TestTheActionsSayTheirResultIsTheirEffect(t *testing.T) {
	tools := newToolkitHarness(t).listed()
	for _, name := range []string{"machine_press", "machine_set_value", "machine_type", "machine_key", "machine_scroll"} {
		if d := tools[name].Description; !strings.Contains(d, "The result is the action's effect") ||
			!strings.Contains(d, "Do not take a snapshot or a screenshot to see whether it worked") {
			t.Errorf("%s: %q", name, d)
		}
	}
	if !strings.Contains(tools["machine_click"].Description, "Prefer machine_press") ||
		!strings.Contains(tools["machine_input"].Description, "prefer machine_press") {
		t.Errorf("click %q, input %q", tools["machine_click"].Description, tools["machine_input"].Description)
	}
}

func TestPressAndSetValue(t *testing.T) {
	h := newToolkitHarness(t)
	runID := h.ready()
	h.can("press", pressJSON)
	got, out := h.callText("machine_press", map[string]any{"runId": runID, "ref": "e4"})
	for _, want := range []string{"step ", `pressed e4 Button "Open run"`, "effect: 1 change"} {
		if !strings.Contains(got, want) {
			t.Errorf("press text lacks %q:\n%s", want, got)
		}
	}
	if eff, _ := out["effect"].(map[string]any); eff["kind"] != "changed" {
		t.Errorf("structured %v", out)
	}
	if msg := h.toolError("machine_press", map[string]any{"runId": runID, "x": 0.5, "y": 0.5}); !strings.Contains(msg, "reason") {
		t.Errorf("a point with no reason: %s", msg)
	}

	h.can("setValue", `{"target":{"ref":"e7","role":"Slider","name":"Tip"},"readBack":"20","readBackOK":true,
"before":{"nodes":[{"ref":"e7","role":"Slider","value":"15"}]},"after":{"nodes":[{"ref":"e7","role":"Slider","value":"20"}]},"settled":true}`)
	got, _ = h.callText("machine_set_value", map[string]any{"runId": runID, "ref": "e7", "value": "20"})
	if !strings.Contains(got, `set e7 Slider "Tip" to "20" through accessibility`) {
		t.Errorf("set_value text:\n%s", got)
	}
	h.can("setValue", `{"error":{"code":"refused","message":"disabled","detail":{"reason":"disabled","waitedMs":5000}}}`)
	if msg := h.toolError("machine_set_value", map[string]any{"runId": runID, "ref": "e7", "value": "20"}); !strings.Contains(msg, "is disabled") {
		t.Errorf("a refused set_value: %s", msg)
	}
}

// machine_type, machine_key and machine_scroll keep their old calls: without a new argument they
// post through the old input path and answer as before.
func TestTheSharedToolsKeepTheirOldCalls(t *testing.T) {
	h := newToolkitHarness(t)
	runID := h.ready()
	var old map[string]any
	h.call("machine_type", map[string]any{"runId": runID, "text": "12"}, &old)
	h.call("machine_key", map[string]any{"runId": runID, "key": "s", "mods": []string{"cmd"}}, nil)
	h.call("machine_scroll", map[string]any{"runId": runID, "x": 0.5, "y": 0.5, "deltaY": 200}, nil)
	if old["actions"] != float64(1) || old["step"] == nil {
		t.Errorf("the old type answered %v", old)
	}
	for _, op := range h.agentOps() {
		if op == "type" || op == "key" || op == "scroll" {
			t.Errorf("an old call went to the toolkit's %s", op)
		}
	}

	h.can("type", `{"target":{"ref":"e4","role":"TextField","name":"Bill"},"typed":"12","readBack":"12","readBackOK":true,
"before":{"nodes":[{"ref":"e4","role":"TextField","value":""}]},"after":{"nodes":[{"ref":"e4","role":"TextField","value":"12"}]},"settled":true}`)
	got, _ := h.callText("machine_type", map[string]any{"runId": runID, "text": "12", "ref": "e4"})
	if !strings.Contains(got, `typed "12" into e4 TextField "Bill"; it shows "12"`) {
		t.Errorf("type by ref:\n%s", got)
	}
	if msg := h.toolError("machine_type", map[string]any{"runId": runID, "text": "", "ref": "e4"}); !strings.Contains(msg, "text: missing") {
		t.Errorf("an empty type: %s", msg)
	}

	h.can("key", `{"target":{"ref":"e4","role":"TextField","name":"Bill"},"before":{"nodes":[]},"after":{"nodes":[]},"settled":true,"settledMs":300}`)
	got, _ = h.callText("machine_key", map[string]any{"runId": runID, "key": "return", "ref": "e4"})
	if !strings.Contains(got, `pressed key return on e4 TextField "Bill"`) {
		t.Errorf("key by ref:\n%s", got)
	}
	if msg := h.toolError("machine_key", map[string]any{"runId": runID, "key": "s", "mods": []string{"hyper"}, "ref": "e4"}); !strings.Contains(msg, "unknown modifier") {
		t.Errorf("a bad modifier: %s", msg)
	}

	h.can("scroll", `{"container":{"ref":"e20","role":"ScrollArea","name":"Items"},"from":{"x":null,"y":0},"to":{"x":null,"y":1},"atEnd":true}`)
	got, _ = h.callText("machine_scroll", map[string]any{"runId": runID, "ref": "e20", "to": "bottom"})
	if !strings.Contains(got, `scrolled e20 ScrollArea "Items" y 0% -> 100%`) {
		t.Errorf("scroll by ref:\n%s", got)
	}
	if msg := h.toolError("machine_scroll", map[string]any{"runId": runID, "ref": "e20", "to": "bottom", "deltaY": 5}); !strings.Contains(msg, "not both") {
		t.Errorf("a mixed scroll: %s", msg)
	}
	if msg := h.toolError("machine_scroll", map[string]any{"runId": runID, "ref": "e20"}); !strings.Contains(msg, "to: missing") {
		t.Errorf("a scroll going nowhere: %s", msg)
	}
}
