package mcpserver

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWaitForAndExpect(t *testing.T) {
	h := newToolkitHarness(t)
	runID := h.ready()
	h.putShot()
	h.can("waitFor", `{"satisfied":true,"elapsedMs":3100,"node":{"ref":"e62","role":"StaticText","name":"Result"}}`)
	got, out := h.callText("machine_wait_for", map[string]any{"runId": runID, "target": map[string]any{"text": "Result"}})
	if !strings.Contains(got, `e62 StaticText "Result" appeared after 3.1 s`) || out["satisfied"] != true {
		t.Errorf("wait:\n%s\n%v", got, out)
	}
	h.can("waitFor", `{"satisfied":true,"elapsedMs":10,"node":{"ref":"e62","role":"StaticText","name":"Result","value":"42"},"value":"42"}`)
	got, _ = h.callText("machine_wait_for", map[string]any{"runId": runID, "target": "e62", "state": "value",
		"value": map[string]any{"op": "equals", "expected": "42"}})
	if !strings.Contains(got, `value equals "42": matched`) {
		t.Errorf("a value wait:\n%s", got)
	}
	if msg := h.toolError("machine_wait_for", map[string]any{"runId": runID, "target": map[string]any{}}); !strings.Contains(msg, "target: missing") {
		t.Errorf("a wait with no target: %s", msg)
	}

	h.can("expect", `{"passed":true,"observed":"42","elapsedMs":120,"node":{"ref":"e62","role":"StaticText","name":"Result","value":"42"}}`)
	got, out = h.callText("machine_expect", map[string]any{"runId": runID, "target": "e62", "property": "value", "expected": "42"})
	if !strings.Contains(got, `expect e62 StaticText "Result" value equals "42": passed after 0.1 s (observed "42")`) || out["passed"] != true {
		t.Errorf("expect:\n%s\n%v", got, out)
	}
	if crop, _ := out["crop"].(string); !strings.HasSuffix(crop, "-expect.jpg") {
		t.Errorf("no crop: %v", out)
	}
	if msg := h.toolError("machine_expect", map[string]any{"runId": runID, "target": "e62", "property": ""}); !strings.Contains(msg, "property: missing") {
		t.Errorf("an expectation with no property: %s", msg)
	}
}

// machine_screenshot crops with the toolkit, and its old call is the whole screen as before.
func TestScreenshotCropsAndKeepsItsOldCall(t *testing.T) {
	h := newToolkitHarness(t)
	runID := h.ready()
	h.putShot()
	var whole map[string]any
	res := h.call("machine_screenshot", map[string]any{"runId": runID}, &whole)
	if _, ok := res.Content[0].(*mcp.ImageContent); !ok || whole["ref"] != nil || whole["width"] != float64(8) {
		t.Errorf("the old call: %v", whole)
	}
	var crop map[string]any
	res = h.call("machine_screenshot", map[string]any{"runId": runID, "ref": "e62"}, &crop)
	if _, ok := res.Content[0].(*mcp.ImageContent); !ok || crop["ref"] != "e62" || crop["path"] == nil {
		t.Errorf("the crop: %v", crop)
	}
	if msg := h.toolError("machine_screenshot", map[string]any{"runId": runID, "ref": "e62", "region": []float64{0, 0, 0.5, 0.5}}); !strings.Contains(msg, "not several") {
		t.Errorf("two crops: %s", msg)
	}
	if d := h.listed()["machine_screenshot"].Description; !strings.Contains(d, "crop it to an element (ref)") {
		t.Errorf("description %q", d)
	}
}
