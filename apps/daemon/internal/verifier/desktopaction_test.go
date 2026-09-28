package verifier

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// The toolkit's actions for the verifier (daemon ADR 0006 points 8 and 12): an action's effect is
// on its own step, and the verdict review reads it there.

// toolkitReviewSteps is a run with the toolkit: a snapshot (3), a press that changed nothing (4),
// one that changed something (5), a snapshot (6), a press after which the app was gone (7), a
// coder's press (8).
func toolkitReviewSteps() []machine.Step {
	v := machine.HolderVerifier
	self := func(seq int, kind string) *machine.StepEffect { return &machine.StepEffect{Of: seq, Kind: kind} }
	return []machine.Step{
		{Seq: 1, Tool: "machine_create"},
		{Seq: 2, Tool: "machine_boot"},
		{Seq: 3, Tool: "machine_snapshot", By: v},
		{Seq: 4, Tool: "machine_press", By: v, Effect: self(4, machine.EffectNone)},
		{Seq: 5, Tool: "machine_press", By: v, Effect: self(5, machine.EffectChanged)},
		{Seq: 6, Tool: "machine_snapshot", By: v},
		{Seq: 7, Tool: "machine_press", By: v, Effect: self(7, machine.EffectQuit)},
		{Seq: 8, Tool: "machine_press", By: machine.HolderCoder, Effect: &machine.StepEffect{Of: 8, Kind: machine.EffectChanged}},
	}
}

// The no-change and quit rules work for toolkit actions as for the old effect read, and an action
// is never an observation otherwise.
func TestTheReviewReadsAToolkitActionsEffectFromItsOwnStep(t *testing.T) {
	for _, c := range []struct {
		name string
		call verdictCall
		want string // "" for a verdict that holds
	}{
		{"a pass on a snapshot after the press", reviewArgs("pass", answer("milk", "pass", []int{6}, 5)), ""},
		{"a press as a pass's evidence", reviewArgs("pass", answer("milk", "pass", []int{5}, 5)),
			`check "milk" (kind): evidence step 5 is a machine_press, an action; its own effect is evidence only for a fail`},
		{"a fail on a press that changed nothing", reviewArgs("fail", answer("milk", "fail", []int{4}, 4)), ""},
		{"a pass after a press that changed nothing, not looked at again", reviewArgs("pass", answer("milk", "pass", []int{3}, 4)),
			`check "milk" (freshness): no evidence step comes after action step 4`},
		{"a fail on a press after which the app quit", reviewArgs("fail", answer("milk", "fail", []int{7}, 7)), ""},
		{"the quit press only in actions", reviewArgs("fail", answer("milk", "fail", []int{6}, 7)),
			`check "milk" (quit): action step 7 made the app quit, as its own result says: cite step 7 as evidence too`},
		{"the quit press only in evidence", reviewArgs("fail", answer("milk", "fail", []int{7})),
			`check "milk" (quit): evidence step 7 is the action after which the app quit: cite step 7 in actions too`},
		{"a quit as a pass's evidence", reviewArgs("pass", answer("milk", "pass", []int{7}, 7)),
			`check "milk" (kind): evidence step 7 is a machine_press, an action`},
		{"the coder's press", reviewArgs("pass", answer("milk", "pass", []int{6}, 8)),
			`check "milk" (cited step): action step 8 was recorded by the coder, not by you`},
		{"a look as an action names the toolkit's inputs", reviewArgs("pass", answer("milk", "pass", []int{6}, 3)),
			"actions must be inputs (machine_press, machine_type"},
	} {
		r := reviewVerdict(c.call, reviewTranscript("milk"), toolkitReviewSteps(), 0)
		joined := strings.Join(r.problems, "\n")
		if c.want == "" {
			if len(r.problems) > 0 {
				t.Errorf("%s: refused: %s", c.name, joined)
			}
			continue
		}
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems = %q, want %q", c.name, joined, c.want)
		}
	}
}

const verifierPressJSON = `{"target":{"ref":"e4","role":"Button","name":"Add"},"point":[50,20],"tried":[[50,20]],"via":"pointer",
"before":{"app":{"name":"TipSplit"},"nodes":[{"ref":"e62","role":"StaticText","name":"Total","value":"$0.00"}]},
"after":{"app":{"name":"TipSplit"},"nodes":[{"ref":"e62","role":"StaticText","name":"Total","value":"$48.00"}]},
"settled":true,"settledMs":300}`

const verifierDeadPressJSON = `{"target":{"ref":"e5","role":"Button","name":"Clear done"},"point":[50,40],"via":"pointer",
"before":{"app":{"name":"TipSplit"},"nodes":[{"ref":"e5","role":"Button","name":"Clear done"}]},
"after":{"app":{"name":"TipSplit"},"nodes":[{"ref":"e5","role":"Button","name":"Clear done"}]},"settled":true,"settledMs":300}`

// A toolkit press answers with its own effect and no UI read follows it; a pass rests on a look
// after it. The declare-first gate applies to it as to any input.
func TestAToolkitPressIsItsOwnEffect(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "press", verifierPressJSON)
	can(t, control, "snapshot", toolkitSnapshot)
	first := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		toolCall("machine_press", map[string]any{"ref": "e4"}),
		declared("total"),
		toolCall("machine_press", map[string]any{"ref": "e4"}),
		toolCall("machine_snapshot", map[string]any{}),
		verdictOf("pass", "The total shows $48.00 (step 2).", answer("total", "pass", []int{first + 1}, first)),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Press Add and check the total.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if !strings.Contains(progress[0].Text, "declare_checks first") {
		t.Errorf("a press before declaring: %q", progress[0].Text)
	}
	if !strings.Contains(progress[2].Text, `"$0.00" -> "$48.00"`) || strings.Contains(progress[2].Text, "read the UI after this input") {
		t.Errorf("the press's result: %q", progress[2].Text)
	}
	if res.Ended != session.Verdict || lastMessage(t, store).Verdict != "pass" {
		t.Fatalf("ended %s: %+v", res.Ended, lastMessage(t, store))
	}
	steps, _ := mgr.Steps(runID)
	var tools []string
	for _, s := range steps[first-1:] {
		tools = append(tools, s.Tool)
	}
	if strings.Join(tools, ",") != "machine_press,machine_snapshot" {
		t.Errorf("steps after the declaration: %v, want the press and the snapshot, no effect read", tools)
	}
}

// A press that changes nothing twice gets the dead-control hint naming the presses, and a fail
// citing one holds (ADR 0029 through the toolkit).
func TestAToolkitPressThatChangesNothingIsEvidenceForAFail(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "press", verifierDeadPressJSON)
	first := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("cleared"),
		toolCall("machine_press", map[string]any{"ref": "e5"}),
		toolCall("machine_press", map[string]any{"ref": "e5"}),
		verdictOf("fail", "Clear done changed nothing (steps 1 and 2).", answer("cleared", "fail", []int{first + 1}, first+1)),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Clear the done items.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if !strings.Contains(progress[2].Text, "This is the second time this control (e5) changed nothing") ||
		!strings.Contains(progress[2].Text, "each of which is its own effect") {
		t.Errorf("the second press: %q", progress[2].Text)
	}
	if got := lastMessage(t, store); got.Kind != session.Verdict || got.Verdict != "fail" {
		t.Fatalf("got %+v, want the fail posted", got)
	}
}

// The old machine_type (text only) keeps its effect read with the toolkit on; with a ref it is the
// toolkit's type, with no read after it.
func TestTheOldTypeKeepsItsEffectRead(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "type", `{"target":{"ref":"e4","role":"TextField","name":"Bill"},"typed":"12","readBack":"12","readBackOK":true,
"before":{"nodes":[]},"after":{"nodes":[]},"settled":true}`)
	model := &scriptedModel{replies: []string{
		declared("bill"),
		toolCall("machine_type", map[string]any{"text": "12"}),
		toolCall("machine_type", map[string]any{"text": "12", "ref": "e4"}),
		verdictOf("inconclusive", "Typed."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Type the bill.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	steps, _ := mgr.Steps(runID)
	var tools []string
	for _, s := range steps {
		if s.By == machine.HolderVerifier {
			tools = append(tools, s.Tool)
		}
	}
	if strings.Join(tools, ",") != "machine_input,machine_ui,machine_type" {
		t.Errorf("verifier steps %v, want the old type with its read, then the toolkit's type alone", tools)
	}
}

// With the toolkit the model is offered the actions, and the shared tools with their new
// arguments; the prompt says an action's result is its effect.
func TestTheToolkitOffersItsActions(t *testing.T) {
	var typeDef map[string]any
	for _, tool := range toolsFor(true) {
		if tool.Name == "machine_type" {
			b, _ := json.Marshal(tool.Schema)
			_ = json.Unmarshal(b, &typeDef)
		}
	}
	if props, _ := typeDef["properties"].(map[string]any); props["ref"] == nil || props["paceMs"] == nil {
		t.Errorf("machine_type's toolkit schema %v", typeDef)
	}
	names := toolNames(toolsFor(true))
	for _, want := range []string{"machine_press", "machine_set_value"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("%s is not offered", want)
		}
	}
	for _, want := range []string{"An action's result is its effect. Do not take a snapshot or a screenshot to see whether it worked",
		"A point needs a reason", "machine_set_value is for setup only"} {
		if !strings.Contains(systemPromptFor(true), want) {
			t.Errorf("the toolkit prompt lacks %q", want)
		}
	}
}

func TestManualPressesAndSetsAValue(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "press", verifierPressJSON)
	can(t, control, "setValue", `{"target":{"ref":"e7","role":"Slider","name":"Tip"},"readBack":"20","readBackOK":true,"settled":true}`)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "press e4\nsetvalue e7 20"})
	if _, err := NewManual(mgr, testLog()).Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 2 || !strings.Contains(progress[0].Text, `pressed e4 Button "Add"`) ||
		!strings.Contains(progress[1].Text, `set e7 Slider "Tip" to "20"`) {
		t.Fatalf("progress %+v", progress)
	}
}
