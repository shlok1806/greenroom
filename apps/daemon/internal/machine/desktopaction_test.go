package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

const pressJSON = `{"target":{"ref":"e11","role":"RadioButton","name":"20%","frame":[30,20,10,8],"vis":[30,20,10,8]},
"point":[35,24],"tried":[[35,24]],"via":"pointer","checks":[{"check":"visible","ms":0},{"check":"stable","ms":120}],"waitedMs":150,
"before":{"app":{"name":"Navlab"},"nodes":[{"ref":"e11","role":"RadioButton","name":"20%"},{"ref":"e62","role":"StaticText","name":"Tip","value":"$15.12"}]},
"after":{"app":{"name":"Navlab"},"nodes":[{"ref":"e11","role":"RadioButton","name":"20%","states":["selected"]},{"ref":"e62","role":"StaticText","name":"Tip","value":"$24.00"}]},
"settled":true,"settledMs":300}`

// A bad action argument is refused before the lease is taken: no step, no request, no lease.
func TestABadActionArgumentTakesNoLease(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	before := len(stepsOf(t, mgr, mc.RunID))
	if _, err := mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "17"}); err == nil ||
		!strings.Contains(err.Error(), "machine_ui's element numbers are not refs") {
		t.Fatalf("got %v", err)
	}
	if _, err := mgr.Type(context.Background(), mc.RunID, HolderCoder, desktop.TypeArgs{Ref: "e4"}); err == nil {
		t.Fatal("a type with no text was not refused")
	}
	if n := len(stepsOf(t, mgr, mc.RunID)); n != before {
		t.Errorf("recorded %d steps", n-before)
	}
	if n := len(testsupport.ControlLines(t, control, "agent-requests")); n != 0 {
		t.Errorf("%d requests reached the agent", n)
	}
	if _, held := mgr.ControlState(mc.RunID); held {
		t.Error("a refused argument left a lease")
	}
}

// An action is one call and its own effect: the step records the effect of itself (no UI read
// follows), the resolved target, the point as fractions of the screen and the checks' waits, and
// the lease was the caller's for the call only.
func TestAnActionIsItsOwnEffect(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "press", pressJSON)
	res, err := mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "e11"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Effect.Kind != desktop.EffectChanged || res.Point == nil || res.Point[0] != 0.547 || res.Point[1] != 0.5 {
		t.Fatalf("result %+v", res)
	}
	for _, want := range []string{`pressed e11 RadioButton "20%" at (0.547, 0.500)`, "effect: 2 changes", `"$15.12" -> "$24.00"`} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, res.Text)
		}
	}
	req := agentRequests(t, control, "press")
	if len(req) != 1 || req[0]["reader"] != HolderCoder || req[0]["input"] != true {
		t.Fatalf("requests %v", req)
	}
	step := stepsOf(t, mgr, mc.RunID)[res.Step]
	if step.Tool != "machine_press" || step.By != HolderCoder || step.Effect == nil || step.Effect.Of != res.Step ||
		step.Effect.Kind != EffectChanged {
		t.Fatalf("step %+v effect %+v", step, step.Effect)
	}
	out, _ := json.Marshal(step.Output)
	for _, want := range []string{`"ref":"e11","role":"RadioButton"`, `"point":[0.547,0.5]`, `"check":"stable","ms":120`, `"waitedMs":150`, `effect: 2 changes`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("evidence lacks %s: %s", want, out)
		}
	}
	if _, held := mgr.ControlState(mc.RunID); held {
		t.Error("the action's per-call lease outlived it")
	}
	if c := mustGet(t, mgr, mc.RunID).input.handovers.Load(); c == 0 {
		t.Error("the coder's per-call lease is not counted as a handover")
	}
}

// Each of the agent's errors becomes one a model can act on; a refusal is the step's error with
// the structured refusal in its output.
func TestAgentErrorsSayWhatToDoNext(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	for _, tc := range []struct {
		name, canned string
		want         []string
		is           error
	}{
		{name: "refused", canned: `{"error":{"code":"refused","message":"covered","detail":{"reason":"covered","by":{"ref":"e70","role":"List","name":"Runs","where":"window"},"waitedMs":5000}}}`,
			want: []string{`refused: e41 is covered by e70 List "Runs"`, "close or move it first"}, is: ErrActionRefused},
		{name: "stale", canned: `{"error":{"code":"stale_ref","message":"e41's window closed"}}`,
			want: []string{"e41's window closed", "take a new machine_snapshot"}, is: ErrStaleRef},
		{name: "not found", canned: `{"error":{"code":"not_found","message":"no app named Nope"}}`,
			want: []string{"no app named Nope", "machine_snapshot or machine_find"}},
		{name: "ambiguous", canned: `{"error":{"code":"ambiguous","message":"two windows fit","detail":{"candidates":[{"ref":"e1","role":"Window","name":"A"},{"ref":"e9","role":"Window","name":"B"}]}}}`,
			want: []string{"two windows fit", `e1 Window "A"`, `e9 Window "B"`, "name one by its ref"}},
		{name: "not responding", canned: `{"error":{"code":"not_responding","message":"Navlab does not answer","retryable":true}}`,
			want: []string{"not responding", "machine_exec", "machine_reboot"}},
		{name: "deadline", canned: `{"error":{"code":"deadline","message":"out of time","retryable":true}}`,
			want: []string{"machine_exec", "machine_reboot"}, is: ErrScreenNotAnswering},
	} {
		t.Run(tc.name, func(t *testing.T) {
			can(t, control, "press", tc.canned)
			res, err := mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "e41"})
			if err == nil {
				t.Fatal("no error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("errors.Is(%v, %v) is false", err, tc.is)
			}
			step := stepsOf(t, mgr, mc.RunID)[res.Step]
			if step.Tool != "machine_press" || step.Error != err.Error() || step.Effect != nil {
				t.Errorf("step %+v", step)
			}
			if tc.name == "refused" {
				out, _ := json.Marshal(step.Output)
				if !strings.Contains(string(out), `"reason":"covered"`) {
					t.Errorf("the refusal's class is not in the step: %s", out)
				}
			}
		})
	}
}

// #212's acceptance: a human takeover mid-action cancels the action, the next input is refused
// while the human holds the screen, and works after the give back.
func TestAHumanTakeoverMidActionEndsItAndHoldsTheScreen(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "press", pressJSON)
	if err := os.WriteFile(filepath.Join(control, "agent-press-sleep"), []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var actErr error
	var act DesktopAction
	wg.Add(1)
	go func() {
		defer wg.Done()
		act, actErr = mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "e11"})
	}()
	waitUntil(t, 3*time.Second, "the press reached the agent", func() bool { return len(agentRequests(t, control, "press")) == 1 })
	if c, held := mgr.ControlState(mc.RunID); !held || c.Holder != HolderCoder {
		t.Fatalf("the coder does not hold the screen during its action: %+v", c)
	}
	started := time.Now()
	lease, fresh, err := mgr.TakeControl(mc.RunID, "human", 0)
	if err != nil || !fresh || lease.Holder != "human" {
		t.Fatalf("the human take mid-action: %+v fresh %v, %v", lease, fresh, err)
	}
	wg.Wait()
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("the action ended %s after the take, want at once", took)
	}
	var taken *ScreenTakenError
	if !errors.As(actErr, &taken) || taken.Holder != "human" {
		t.Fatalf("the action ended with %v, want the screen taken by the human", actErr)
	}
	if s := stepsOf(t, mgr, mc.RunID)[act.Step]; s.Error == "" || !strings.Contains(s.Error, "human") {
		t.Errorf("the preempted step %+v", s)
	}
	if c, held := mgr.ControlState(mc.RunID); !held || c.Holder != "human" {
		t.Fatalf("the preempted call's release took the human's lease: %+v held %v", c, held)
	}
	if err := os.Remove(filepath.Join(control, "agent-press-sleep")); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "e11"}); !errors.Is(err, ErrScreenTaken) {
		t.Fatalf("an action while the human holds the screen: %v", err)
	}
	if n := len(agentRequests(t, control, "press")); n != 1 {
		t.Errorf("the refused action reached the agent (%d presses)", n)
	}
	if _, _, err := mgr.ReleaseControl(mc.RunID, "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "e11"}); err != nil {
		t.Fatalf("an action after the give back: %v", err)
	}
	got := testsupport.ControlLines(t, control, "agent-control")
	if len(got) < 2 || got[0] != `pause {"holder":"human"}` || got[1] != "resume {}" {
		t.Errorf("agent-control %q, want PAUSE for the human then RESUME", got)
	}
}

// Without a guest agent nothing changes: a human cannot take the screen from an agent's lease.
func TestWithoutTheToolkitAHumanStillCannotTakeAHeldScreen(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, HolderCoder, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); !errors.Is(err, ErrControlHeld) {
		t.Fatalf("got %v, want ErrControlHeld", err)
	}
}

// The coder is refused during a verifier turn, and the verifier after a handover until it looks
// again, where a snapshot is a look (issues #82, #124).
func TestActionsKeepTheLeaseRules(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "press", pressJSON)
	can(t, control, "snapshot", snapshotJSON)
	mgr.SetVerifierTurn(mc.RunID, true)
	if _, err := mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "e11"}); err == nil ||
		!strings.Contains(err.Error(), "agent_wait") {
		t.Fatalf("the coder mid verifier turn: %v", err)
	}
	mgr.SetVerifierTurn(mc.RunID, false)
	if _, err := mgr.Press(context.Background(), mc.RunID, HolderCoder, desktop.PressArgs{Ref: "e11"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Press(context.Background(), mc.RunID, HolderVerifier, desktop.PressArgs{Ref: "e11"}); !errors.Is(err, ErrStaleLook) ||
		!strings.Contains(err.Error(), "machine_snapshot") {
		t.Fatalf("the verifier after the coder's lease: %v", err)
	}
	if _, err := mgr.Snapshot(context.Background(), mc.RunID, HolderVerifier, desktop.SnapshotArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Press(context.Background(), mc.RunID, HolderVerifier, desktop.PressArgs{Ref: "e11"}); err != nil {
		t.Fatalf("the verifier after its snapshot: %v", err)
	}
}

// A secure field's typed text never reaches a step, whether the type worked or was refused
// (catalog I15; the agent redacts its own result, the daemon its record of the request).
func TestTypedSecretsNeverReachAStep(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "type", `{"target":{"ref":"e6","role":"TextField","name":"Password","states":["secret"],"chars":7},
"typed":"<secret, 7 chars>","readBack":"<secret, 7 chars>","readBackOK":true,"secret":true,
"before":{"nodes":[{"ref":"e6","role":"TextField","states":["secret"],"chars":0}]},"after":{"nodes":[{"ref":"e6","role":"TextField","states":["secret"],"chars":7}]},"settled":true}`)
	res, err := mgr.Type(context.Background(), mc.RunID, HolderCoder, desktop.TypeArgs{Ref: "e6", Text: "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "hunter2") || !strings.Contains(res.Text, "<secret, 7 chars>") {
		t.Errorf("text:\n%s", res.Text)
	}
	if req := agentRequests(t, control, "type"); len(req) != 1 || req[0]["args"].(map[string]any)["paceMs"] != float64(20) {
		t.Errorf("the type request %v does not carry the default pace", req)
	}
	can(t, control, "type", `{"error":{"code":"refused","message":"not editable","detail":{"reason":"not_editable"}}}`)
	if _, err := mgr.Type(context.Background(), mc.RunID, HolderCoder, desktop.TypeArgs{Ref: "e6", Text: "hunter2"}); !errors.Is(err, ErrActionRefused) {
		t.Fatalf("got %v", err)
	}
	data, err := os.ReadFile(filepath.Join(mc.Dir, "steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("hunter2")) {
		t.Errorf("a typed secret reached steps.jsonl:\n%s", data)
	}
}

// A scroll by ref is an action whose step records where the container went.
func TestAScrollByRefIsAnActionWithItsEffect(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "scroll", `{"container":{"ref":"e20","role":"ScrollArea","name":"Items"},"from":{"x":null,"y":0},"to":{"x":null,"y":1},
"atEnd":true,"steps":6,"via":"wheel","target":{"ref":"e45","role":"Button","name":"Details"},"visible":true}`)
	res, err := mgr.Scroll(context.Background(), mc.RunID, HolderCoder, desktop.ScrollArgs{Ref: "e20", To: desktop.ScrollTo{Ref: "e45"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, `scrolled e20 ScrollArea "Items" y 0% -> 100%`) || res.Effect.Kind != desktop.EffectChanged {
		t.Errorf("scroll %+v\n%s", res, res.Text)
	}
	if req := agentRequests(t, control, "scroll"); len(req) != 1 || !strings.Contains(mustJSON(t, req[0]["args"]), `"to":{"ref":"e45"}`) {
		t.Errorf("request %v", req)
	}
	if s := stepsOf(t, mgr, mc.RunID)[res.Step]; s.Tool != "machine_scroll" || s.Effect == nil || s.Effect.Of != res.Step {
		t.Errorf("step %+v", s)
	}
}
