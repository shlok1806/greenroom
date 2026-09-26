package verifier

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// ADR 0029: an input that changes nothing is evidence. Replays todolist-clear-done-no-action
// from the second simple-tier run: Clear done has no action. The verifier clicked it 3 to 12
// times (26 to 40 steps, up to 424k tokens), each click "succeeding", so the #125 guard never
// fired; one trial's fail citing those reads was refused for want of a screenshot.

// clearDoneUI is TodoList with Milk, Bread and Eggs added and Milk and Eggs ticked. Clear done does
// nothing, so the tree never changes.
const clearDoneUI = `{"app":{"name":"TodoList","pid":9},"apps":["Finder","TodoList"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXTextField","label":"New item","identifier":"new-item","depth":0,"frame":{"x":324,"y":283,"w":260,"h":24}},
{"role":"AXButton","title":"Add","depth":0,"frame":{"x":600,"y":283,"w":60,"h":24}},
{"role":"AXCheckBox","title":"Milk","value":"1","depth":0,"frame":{"x":324,"y":330,"w":120,"h":20}},
{"role":"AXCheckBox","title":"Bread","value":"0","depth":0,"frame":{"x":324,"y":356,"w":120,"h":20}},
{"role":"AXCheckBox","title":"Eggs","value":"1","depth":0,"frame":{"x":324,"y":382,"w":120,"h":20}},
{"role":"AXButton","title":"Clear done","depth":0,"frame":{"x":340,"y":525,"w":100,"h":24}},
{"role":"AXStaticText","value":"3 items, 2 done","identifier":"summary","depth":0,"frame":{"x":324,"y":560,"w":140,"h":20}}]}`

func TestBenchCaseDeadClearDoneIsAGroundedFailInTwoClicks(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, clearDoneUI)
	look := lastStep(t, mgr, runID) + 1
	click1, read1, click2, read2 := look+1, look+2, look+3, look+4
	model := &scriptedModel{replies: []string{
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{
			{"id": "only-bread", "criterion": "After pressing Clear done, only Bread remains", "kinds": []string{"visual"}},
			{"id": "summary", "criterion": "The summary reads 1 item, 0 done after Clear done"},
		}}),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 6}),
		toolCall("machine_click", map[string]any{"x": 0.38, "y": 0.699}), // the same button, by position
		verdictOf("fail", "Clear done changed nothing (steps 5 and 7).",
			said(answer("only-bread", "fail", []int{read1, read2}, click1, click2), "Milk, Bread and Eggs are still listed."),
			said(answer("summary", "fail", []int{read2}, click1, click2), "The summary still reads 3 items, 2 done.")),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, `I added a Clear done button to TodoList (running on screen). It removes every ticked item. `+
		`Press Clear done: only Bread should remain and the summary should read "1 item, 0 done".`)
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}

	if sys := deliveredText(t, model, 1, "system"); !strings.Contains(sys, "that is a fail: cite the click in actions and its effect read in evidence. Do not keep clicking it.") {
		t.Error("the prompt does not say a control that changes nothing is a fail")
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 4 {
		t.Fatalf("%d progress messages, want declare, ui and two clicks", len(prog))
	}
	if strings.Contains(prog[2].Text, "changed nothing") {
		t.Errorf("the first click that changed nothing already gets the hint: %q", prog[2].Text)
	}
	hint := fmt.Sprintf(`This is the second time this control (Button "Clear done") changed nothing. If the task says `+
		`it should change something, that is evidence for fail: cite the effect reads (steps %d and %d). Otherwise, try `+
		`something different or ask.`, read1, read2)
	if !strings.Contains(prog[3].Text, "effect: no change detected") || !strings.Contains(prog[3].Text, hint) {
		t.Errorf("the second click's result =\n%s\nwant the hint %q", prog[3].Text, hint)
	}
	verdicts := messagesOfKind(store, session.Verdict)
	if len(verdicts) != 1 || verdicts[0].Verdict != "fail" {
		t.Fatalf("verdicts = %+v, want the fail resting on the no-change reads", verdicts)
	}
	for _, c := range verdicts[0].Checks {
		if c.Status != session.CheckFail {
			t.Errorf("check %q = %s, want fail", c.ID, c.Status)
		}
	}
	if res.Steps != 5 {
		t.Errorf("the turn took %d model steps, want 5", res.Steps)
	}
}

// The tally keys on the control, by identity whether clicked by id or by a position inside it,
// or on the point when no control is under it; any input that changed something clears it.
func TestDeadControlsCountNoChangeClicksPerControl(t *testing.T) {
	tree := machine.UITree{App: "TodoList", Apps: []string{"TodoList"}, Elements: []machine.UIElement{
		{ID: 1, Role: "AXWindow", Title: "TodoList", X: 0.5, Y: 0.5, W: 0.6, H: 0.6},
		{ID: 2, Role: "AXTextField", Label: "New item", X: 0.44, Y: 0.38, W: 0.25, H: 0.03},
		{ID: 6, Role: "AXButton", Title: "Clear done", X: 0.38, Y: 0.7, W: 0.1, H: 0.03},
	}}
	click := func(args string) *target {
		return clickTarget(nim.ToolCall{Name: "machine_click", Arguments: args}, tree, true)
	}
	field, byID, byPoint := click(`{"element":2}`), click(`{"element":6}`), click(`{"x":0.39,"y":0.71}`)
	empty1, empty2 := click(`{"x":0.6,"y":0.6}`), click(`{"x":0.61,"y":0.6}`) // inside only the window
	batch := clickTarget(nim.ToolCall{Name: "machine_input",
		Arguments: `{"actions":[{"type":"sleep","ms":500},{"type":"click","x":0.38,"y":0.7}]}`}, tree, true)
	if byID == nil || byPoint == nil || batch == nil || !byID.same(*byPoint) || !byID.same(*batch) {
		t.Fatalf("a click by id, by position and in a batch on Clear done are not one control: %+v %+v %+v", byID, byPoint, batch)
	}
	if empty1 == nil || empty1.key != "" || !empty1.same(*empty2) || empty1.same(*byID) {
		t.Errorf("clicks on the window's empty space: %+v %+v, want the same point and not Clear done", empty1, empty2)
	}
	if typing := clickTarget(nim.ToolCall{Name: "machine_type", Arguments: `{"text":"Milk"}`}, tree, true); typing != nil {
		t.Errorf("typing has a click target: %+v", typing)
	}

	var d deadControls
	steps := []struct {
		t    *target
		kind string
		read int
		hint string // what the hint must contain, "" for none
	}{
		{field, machine.EffectNone, 8, ""},   // focusing the field
		{nil, machine.EffectChanged, 10, ""}, // typing: clears the tally
		{field, machine.EffectNone, 14, ""},  // focusing it again is the first since
		{byID, machine.EffectNone, 20, ""},   // Clear done, once
		{nil, machine.EffectUnknown, 22, ""}, // an unknown effect leaves the tally
		{byPoint, machine.EffectNone, 24, "second time this control (AXButton \"Clear done\") changed nothing"},
		{batch, machine.EffectNone, 26, "the third time"},
		{batch, machine.EffectNone, 28, "(steps 20, 24, 26 and 28)"},
		{byID, machine.EffectQuit, 30, ""},
		{byID, machine.EffectNone, 32, ""},
	}
	for i, s := range steps {
		got := d.record(s.t, s.kind, s.read)
		if s.hint == "" && got != "" || !strings.Contains(got, s.hint) {
			t.Errorf("input %d (read %d): hint = %q, want %q", i+1, s.read, got, s.hint)
		}
	}
}

// The review takes a no-change read as evidence for a fail when it follows the check's last
// action, whatever its kinds; never for a pass, and not after a later input changed the screen.
func TestReviewTakesANoChangeReadAsEvidenceForAFailOnly(t *testing.T) {
	v := machine.HolderVerifier
	steps := []machine.Step{
		{Seq: 3, Tool: "machine_ui", By: v},
		{Seq: 4, Tool: "machine_input", By: v},
		{Seq: 5, Tool: "machine_ui", By: v, Effect: &machine.StepEffect{Of: 4, Kind: machine.EffectNone}},
		{Seq: 6, Tool: "machine_input", By: v},
		{Seq: 7, Tool: "machine_ui", By: v, Effect: &machine.StepEffect{Of: 6, Kind: machine.EffectNone}},
		{Seq: 8, Tool: "machine_input", By: v},
		{Seq: 9, Tool: "machine_ui", By: v, Effect: &machine.StepEffect{Of: 8, Kind: machine.EffectChanged}},
	}
	visual := session.Check{ID: "bread", Criterion: "After Clear done, only Bread is shown", Kinds: []string{session.CheckVisual}}
	value := session.Check{ID: "bread", Criterion: "After Clear done, only Bread remains"}
	for _, c := range []struct {
		name  string
		check session.Check
		call  verdictCall
		want  string // "" for a verdict that holds
	}{
		{"a visual fail on two no-change reads", visual, reviewArgs("fail", answer("bread", "fail", []int{5, 7}, 4, 6)), ""},
		{"a visual fail on the last read alone", visual, reviewArgs("fail", answer("bread", "fail", []int{7}, 4, 6)), ""},
		{"a visual fail on an earlier read only", visual, reviewArgs("fail", answer("bread", "fail", []int{5}, 4, 6)),
			`check "bread" (freshness): no evidence step comes after action step 6`},
		{"a fail after a later input changed the screen", visual, reviewArgs("fail", answer("bread", "fail", []int{7}, 6, 8)),
			`check "bread" (freshness)`},
		{"a pass on the no-change read", value, reviewArgs("pass", answer("bread", "pass", []int{7}, 6)),
			`check "bread" (effect): an action in it found no change detected (effect check at step 7)`},
	} {
		r := reviewVerdict(c.call, kindTranscript(c.check), steps, 0)
		joined := strings.Join(r.problems, "\n")
		if c.want == "" && len(r.problems) > 0 {
			t.Errorf("%s: refused: %s", c.name, joined)
		}
		if c.want != "" && !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems = %q, want %q", c.name, joined, c.want)
		}
	}
}
