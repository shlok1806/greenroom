package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
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
	if !conversationSays(h, runID, "gave the screen back after 1 action") {
		t.Error("the release did not say what was done")
	}
	var detail RunDetail
	h.get("/api/runs/"+runID, &detail)
	if detail.Machine != nil && detail.Machine.Control != nil {
		t.Errorf("the lease survived the release: %+v", detail.Machine.Control)
	}
}

// Issue #57: a lease that lapsed left no trace, so the transcript read "took control", "took
// control", "gave the screen back after 0 actions". The lapse is recorded with what was done under
// it, when the human takes the screen again or lets go of a lapsed lease.
func TestALapsedLeaseIsRecordedWithItsActions(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	// A 3 s lease, not 1 s: on a loaded host the input posted right after taking control can arrive
	// after a 1 s lease has already lapsed.
	h.postJSON("/api/runs/"+runID+"/control", map[string]any{"ttlSeconds": 3}, nil)
	h.postJSON("/api/runs/"+runID+"/input", map[string]any{
		"actions": []machine.InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}},
	}, nil)
	time.Sleep(3300 * time.Millisecond)

	h.postJSON("/api/runs/"+runID+"/control", map[string]any{"ttlSeconds": 3}, nil)
	time.Sleep(3300 * time.Millisecond)
	if code, body := h.status(http.MethodDelete, "/api/runs/"+runID+"/control", nil); code != http.StatusOK {
		t.Fatalf("release: status %d: %s", code, body)
	}

	var handovers []string
	for _, m := range h.store(runID).After(0) {
		if strings.Contains(m.Text, "control of the screen") || strings.Contains(m.Text, "gave the screen back") {
			handovers = append(handovers, m.Text)
		}
	}
	want := []string{
		"human took control of the screen",
		"human lost control of the screen after 1 action: the lease lapsed with no input or renewal for 3 s",
		"human took control of the screen",
		"human lost control of the screen after 0 actions: the lease lapsed with no input or renewal for 3 s",
	}
	if strings.Join(handovers, "\n") != strings.Join(want, "\n") {
		t.Fatalf("handovers:\n%s\nwant:\n%s", strings.Join(handovers, "\n"), strings.Join(want, "\n"))
	}
}

// Only a human lease is announced: the human letting go after the verifier's lease lapsed must not
// say a human lost control.
func TestReleasingAnotherSeatsLapsedLeaseSaysNothing(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if _, _, err := h.mgr.TakeControl(runID, machine.HolderVerifier, time.Second); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if code, body := h.status(http.MethodDelete, "/api/runs/"+runID+"/control", nil); code != http.StatusOK {
		t.Fatalf("release: status %d: %s", code, body)
	}
	for _, m := range h.store(runID).After(0) {
		if strings.HasPrefix(m.Text, "human") && (strings.Contains(m.Text, "control of the screen") || strings.Contains(m.Text, "gave the screen back")) {
			t.Errorf("posted %q for the verifier's lease", m.Text)
		}
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

func TestAFailedWarmUpKeepsALeaseTheHumanAlreadyHeld(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if _, _, err := h.mgr.TakeControl(runID, humanSeat, 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	testsupport.Flag(t, h.control, "fail-input-install")
	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/control", nil); code != http.StatusConflict {
		t.Fatalf("a failed warm-up answered %d: %s", code, body)
	}
	if c, held := h.mgr.ControlState(runID); !held || c.Holder != humanSeat {
		t.Errorf("the human lost a lease they already held: %+v held=%v", c, held)
	}
}

func TestAFailedWarmUpReleasesAFreshLease(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	testsupport.Flag(t, h.control, "fail-input-install")
	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/control", nil); code != http.StatusConflict {
		t.Fatalf("a failed warm-up answered %d: %s", code, body)
	}
	if c, held := h.mgr.ControlState(runID); held {
		t.Errorf("a lease taken by the failed request is still held: %+v", c)
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
	// The API's seat is always "human", so another holder must come from the manager.
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

func conversationSays(h *harness, runID, text string) bool {
	return conversationCount(h, runID, text) > 0
}

func conversationCount(h *harness, runID, text string) int {
	h.t.Helper()
	n := 0
	for _, m := range h.store(runID).After(0) {
		if strings.Contains(m.Text, text) {
			n++
		}
	}
	return n
}

// Issue #57, remainder: when the Companion holding the lease dies, nothing touched the lease
// again, so no lapse was posted and the run detail kept showing it as held. The lapse is posted
// when the lease runs out, and the detail stops showing it.
func TestALeaseNobodyRenewsIsRecordedWhenItLapses(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.postJSON("/api/runs/"+runID+"/control", map[string]any{"ttlSeconds": 1}, nil)
	deadline := time.Now().Add(5 * time.Second)
	for conversationCount(h, runID, "human lost control of the screen") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no lapse was posted for a lease nobody renewed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var d RunDetail
	h.get("/api/runs/"+runID, &d)
	if d.Machine != nil && d.Machine.Control != nil {
		t.Errorf("the run detail still shows the lapsed lease: %+v", d.Machine.Control)
	}
	// A retake afterwards posts no second lapse for the same lease.
	h.take(runID)
	if n := conversationCount(h, runID, "human lost control of the screen"); n != 1 {
		t.Errorf("%d lapse events, want 1", n)
	}
}

// Issue #100: two Companions share the human seat; one's Give Back was undone by the other's
// next renewal, which took a fresh lease. A renewal never takes a lease; it says the screen went.
func TestARenewalAfterAGiveBackIsRefused(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.take(runID)
	if code, body := h.status(http.MethodDelete, "/api/runs/"+runID+"/control", nil); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, body)
	}
	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/control", map[string]any{"renew": true})
	if code != http.StatusConflict || !strings.Contains(body, "given back") {
		t.Fatalf("renewal after a give back = %d %s, want 409 saying the screen was given back", code, body)
	}
	if n := conversationCount(h, runID, "took control"); n != 1 {
		t.Errorf("%d takes posted, want 1: the renewal must not take the screen", n)
	}
	// A renewal of a live lease still renews.
	h.take(runID)
	if code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/control", map[string]any{"renew": true}); code != http.StatusOK {
		t.Fatalf("renewal of a held lease = %d %s", code, body)
	}
}
