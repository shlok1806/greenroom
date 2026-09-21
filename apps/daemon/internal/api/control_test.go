package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

func frac(v float64) *float64 { return &v }

// take asks for the screen and fails the test if the daemon refuses.
func (h *harness) take(runID string) controlOut {
	h.t.Helper()
	var out controlOut
	h.postJSON("/api/runs/"+runID+"/control", map[string]any{}, &out)
	return out
}

func (h *harness) postJSON(path string, in, out any) {
	h.t.Helper()
	res, body := h.do(http.MethodPost, path, in)
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		h.t.Fatalf("POST %s: status %d: %s", path, res.StatusCode, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			h.t.Fatalf("POST %s: decode: %v (%s)", path, err, body)
		}
	}
}

// --- taking and giving back the screen (ADR 0009) ---

func TestTakingControlReportsTheScreenAndTellsTheConversation(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	out := h.take(runID)
	if out.Control == nil || out.Control.Holder != "human" {
		t.Fatalf("control = %+v, want a human lease", out.Control)
	}
	if out.Screen == nil || out.Screen.Width != 1024 || out.Screen.Height != 768 {
		t.Fatalf("screen = %+v, want the guest's 1024x768", out.Screen)
	}

	// The coder learns on its next agent_wait that someone else is driving.
	if !conversationSays(h, runID, "took control") {
		t.Error("taking control left no message in the conversation")
	}

	// The lease is visible to a window that reconnects.
	var detail RunDetail
	h.get("/api/runs/"+runID, &detail)
	if detail.Machine == nil || detail.Machine.Control == nil {
		t.Fatalf("the run detail does not carry the lease: %+v", detail.Machine)
	}
}

func TestTakingControlTwiceAnnouncesItOnce(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.take(runID)
	h.take(runID)
	if n := conversationCount(h, runID, "took control"); n != 1 {
		t.Errorf("the conversation holds %d handover messages, want 1", n)
	}
}

func TestReleasingControlReportsWhatWasDone(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.take(runID)

	var res machine.InputResult
	h.postJSON("/api/runs/"+runID+"/input", map[string]any{
		"actions": []machine.InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}},
	}, &res)
	if res.Actions != 1 {
		t.Errorf("input reported %d actions, want 1", res.Actions)
	}

	if code, body := h.status(http.MethodDelete, "/api/runs/"+runID+"/control", nil); code != http.StatusOK {
		t.Fatalf("release: status %d: %s", code, body)
	}
	if !conversationSays(h, runID, "gave the screen back after 1 actions") {
		t.Error("the release did not say what was done")
	}
	var detail RunDetail
	h.get("/api/runs/"+runID, &detail)
	if detail.Machine != nil && detail.Machine.Control != nil {
		t.Errorf("the lease survived the release: %+v", detail.Machine.Control)
	}
}

func TestInputWithoutControlIsRefused(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/input", map[string]any{
		"actions": []machine.InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}},
	})
	if code != http.StatusConflict {
		t.Fatalf("input without a lease answered %d: %s", code, body)
	}
	if !strings.Contains(body, "take control") {
		t.Errorf("the refusal does not say what to do: %s", body)
	}
}

func TestInputWithNoActionsIsRefused(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.take(runID)
	if code, _ := h.status(http.MethodPost, "/api/runs/"+runID+"/input", map[string]any{"actions": []any{}}); code != http.StatusBadRequest {
		t.Errorf("an empty batch answered %d, want 400", code)
	}
}

func TestControlOfAMachineThatIsGone(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/destroy", nil); code != http.StatusOK {
		t.Fatalf("destroy: %d %s", code, body)
	}
	if code, _ := h.status(http.MethodPost, "/api/runs/"+runID+"/control", nil); code != http.StatusConflict {
		t.Errorf("taking control of a destroyed machine answered %d, want 409", code)
	}
}

func TestControlOfAnUnknownRun(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.status(http.MethodPost, "/api/runs/nope/control", nil); code != http.StatusNotFound {
		t.Errorf("an unknown run answered %d, want 404", code)
	}
}

func TestTheScreenIsLockedToOneHolder(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	// The verifier, not the companion, takes it first: the API's own seat is
	// always "human", so this is the only way to stand in another's shoes.
	if _, _, err := h.mgr.TakeControl(runID, "verifier", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/control", nil)
	if code != http.StatusLocked {
		t.Fatalf("a taken screen answered %d: %s", code, body)
	}
}

func TestInputFailureLeavesTheReasonInTheBody(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.take(runID)
	testsupport.Flag(t, h.control, "input-down")
	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/input", map[string]any{
		"actions": []machine.InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}},
	})
	if code != http.StatusInternalServerError {
		t.Fatalf("a refusing guest answered %d: %s", code, body)
	}
	if !strings.Contains(body, "this machine refused the event") {
		t.Errorf("the guest's own words are missing: %s", body)
	}
}

// --- helpers ---

func conversationMessages(h *harness, runID string) []session.Message {
	h.t.Helper()
	return h.store(runID).After(0)
}

func conversationSays(h *harness, runID, text string) bool {
	return conversationCount(h, runID, text) > 0
}

func conversationCount(h *harness, runID, text string) int {
	h.t.Helper()
	n := 0
	for _, m := range conversationMessages(h, runID) {
		if strings.Contains(m.Text, text) {
			n++
		}
	}
	return n
}
