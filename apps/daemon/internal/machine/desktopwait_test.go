package machine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
)

// A wait and an expectation record what they observed and how long it took; an expectation also
// saves a crop of its target, and a crop that fails never fails it.
func TestWaitsAndExpectationsRecordTheirObservations(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "waitFor", `{"satisfied":true,"elapsedMs":3100,"node":{"ref":"e62","role":"StaticText","name":"Result","value":"42"},"value":"42"}`)
	w, err := mgr.WaitFor(context.Background(), mc.RunID, HolderVerifier,
		desktop.WaitArgs{Target: desktop.WaitTarget{Ref: "e62"}, State: desktop.WaitValue, Value: &desktop.ValueMatch{Op: desktop.OpEquals, Expected: "42"}})
	if err != nil {
		t.Fatal(err)
	}
	if !w.Satisfied || !strings.Contains(w.Text, "matched after 3.1 s") {
		t.Errorf("wait %+v\n%s", w.WaitResult, w.Text)
	}
	ws := stepsOf(t, mgr, mc.RunID)[w.Step]
	if out := mustJSON(t, ws.Output); ws.Tool != "machine_wait_for" || !strings.Contains(out, `"elapsedMs":3100`) || !strings.Contains(out, `"value":"42"`) {
		t.Errorf("wait step %+v", ws)
	}

	can(t, control, "expect", `{"passed":false,"observed":"41","elapsedMs":2000,"node":{"ref":"e62","role":"StaticText","name":"Result","value":"41"}}`)
	e, err := mgr.Expect(context.Background(), mc.RunID, HolderVerifier,
		desktop.ExpectArgs{Target: desktop.WaitTarget{Ref: "e62"}, Property: desktop.PropValue, Expected: "42"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Passed || !strings.Contains(e.Text, `FAILED after 2.0 s (observed "41")`) {
		t.Errorf("expect %+v\n%s", e.ExpectResult, e.Text)
	}
	if want := filepath.Join(mc.Dir, fmt.Sprintf("%03d-expect.jpg", e.Step)); e.Crop != want {
		t.Errorf("crop %q, want %q", e.Crop, want)
	}
	if data, err := os.ReadFile(e.Crop); err != nil || len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Errorf("the crop is not a JPEG: %v", err)
	}
	if cap := agentRequests(t, control, "capture"); len(cap) == 0 || !strings.Contains(mustJSON(t, cap[len(cap)-1]["args"]), `"ref":"e62"`) {
		t.Errorf("the crop's capture %v", cap)
	}
	es := stepsOf(t, mgr, mc.RunID)[e.Step]
	out := mustJSON(t, es.Output)
	for _, want := range []string{`"expected":"42"`, `"observed":"41"`, `"elapsedMs":2000`, `"passed":false`, `"crop":`} {
		if !strings.Contains(out, want) {
			t.Errorf("expect step lacks %s: %s", want, out)
		}
	}
	if es.Error != "" {
		t.Errorf("a failed expectation is a failed step: %q", es.Error)
	}

	if err := os.Remove(filepath.Join(control, "shot.b64")); err != nil {
		t.Fatal(err)
	}
	e2, err := mgr.Expect(context.Background(), mc.RunID, HolderVerifier,
		desktop.ExpectArgs{Target: desktop.WaitTarget{Ref: "e62"}, Property: desktop.PropValue, Expected: "42"})
	if err != nil || e2.Crop != "" {
		t.Fatalf("an expectation whose crop failed: %+v, %v", e2, err)
	}
	if out := mustJSON(t, stepsOf(t, mgr, mc.RunID)[e2.Step].Output); !strings.Contains(out, "cropError") {
		t.Errorf("the failed crop is not said: %s", out)
	}
}

// machine_screenshot with a ref is a crop, a look and a step; without one it is the whole screen.
func TestAScreenshotCropsToARef(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	data, shot, err := mgr.ScreenshotOf(context.Background(), mc.RunID, HolderVerifier, desktop.ShotArgs{Ref: "e62"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || shot.Ref != "e62" || shot.Path == "" || shot.Width != 64 {
		t.Fatalf("shot %+v", shot)
	}
	caps := agentRequests(t, control, "capture")
	if last := mustJSON(t, caps[len(caps)-1]["args"]); !strings.Contains(last, `"ref":"e62"`) {
		t.Errorf("capture args %s", last)
	}
	if s := stepsOf(t, mgr, mc.RunID)[shot.Step]; s.Tool != "machine_screenshot" || s.By != HolderVerifier ||
		!strings.Contains(mustJSON(t, s.Input), `"ref":"e62"`) {
		t.Errorf("step %+v", s)
	}
	if _, whole, err := mgr.ScreenshotOf(context.Background(), mc.RunID, HolderVerifier, desktop.ShotArgs{}); err != nil || whole.Ref != "" || whole.Width != 64 {
		t.Errorf("the whole screen: %+v %v", whole, err)
	}
}
