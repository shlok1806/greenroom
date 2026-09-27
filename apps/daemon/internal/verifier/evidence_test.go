package verifier

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// groceriesUI is issue #116's app: two fields holding typed text, Add, and a Milk checkbox.
const groceriesUI = `{"app":{"name":"Groceries","pid":9},"apps":["Groceries"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXTextField","label":"Item","value":"Apples","depth":0,"frame":{"x":380,"y":303,"w":68,"h":20}},
{"role":"AXTextField","label":"Price","value":"2.00","depth":0,"frame":{"x":508,"y":303,"w":46,"h":20}},
{"role":"AXButton","title":"Add","depth":0,"frame":{"x":592,"y":303,"w":46,"h":20}},
{"role":"AXCheckBox","title":"Milk","identifier":"item-Milk","value":"0","depth":0,"frame":{"x":474,"y":339,"w":20,"h":20}},
{"role":"AXStaticText","value":"In cart: $0.00","depth":0,"frame":{"x":400,"y":400,"w":120,"h":20}}]}`

// Issue #116: a click after text entry was lost, the tree still showed Milk unchecked, and the
// verdict said it was checked. Now the click's result says nothing changed, and a pass resting on
// it is refused and not posted; the model has to look again.
func TestIssue116ALostClickCannotCarryAPass(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, groceriesUI) // never changes: the click is lost
	look := lastStep(t, mgr, runID) + 1
	click, effect := look+1, look+2
	claim := verdictOf("pass", "Milk is checked and In cart shows $1.50 (step 5).",
		answer("milk", "pass", []int{effect}, click))
	model := &scriptedModel{replies: []string{
		declared("milk"),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 4}),
		claim,
		toolCall("machine_ui", map[string]any{}),
		verdictOf("fail", "Clicking Milk (step 5) left it unchecked (step 7).", answer("milk", "fail", []int{look + 3}, click)),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check Milk and report In cart. I fixed the total, it shows $1.50 now.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 5 {
		t.Fatalf("%d progress messages, want declare, ui, click, the refused verdict, ui", len(prog))
	}
	if !strings.Contains(prog[2].Text, "effect: no change detected") {
		t.Errorf("the lost click's result = %q, want no change detected", prog[2].Text)
	}
	refused := prog[3].Text
	for _, want := range []string{"report_verdict", "error: report_verdict refused; nothing was posted",
		`check "milk" (effect)`, "no change detected"} {
		if !strings.Contains(refused, want) {
			t.Errorf("refusal = %q, want %q", refused, want)
		}
	}
	verdicts := messagesOfKind(store, session.Verdict)
	if len(verdicts) != 1 || verdicts[0].Verdict != "fail" {
		t.Fatalf("verdicts = %+v, want only the fail that the fresh look supports", verdicts)
	}
	if !strings.Contains(model.request(t, 5), `check \"milk\" (effect)`) {
		t.Error("the model never saw which rule its pass broke")
	}
}

// Input on an open task waits for the checks; looking does not. The refusal is an ordinary
// error (it records no step and posts nothing to the guest), and after declare_checks the same
// click runs.
func TestInputIsRefusedUntilChecksAreDeclared(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	model := &scriptedModel{replies: []string{
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 1}),
		declared("tip"),
		toolCall("machine_click", map[string]any{"element": 1}),
		verdictOf("inconclusive", "25% was clicked; its result was not read."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click 25%.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 4 {
		t.Fatalf("%d progress messages, want 4", len(prog))
	}
	if prog[0].Step == 0 || strings.HasPrefix(splitResult(prog[0].Text), "error:") {
		t.Errorf("the look before any checks = %+v, want it to run", prog[0])
	}
	if prog[1].Step != 0 || splitResult(prog[1].Text) != declareFirst {
		t.Errorf("the click before any checks = %+v, want the declare-first refusal", prog[1])
	}
	if len(prog[2].Checks) != 1 || prog[2].Checks[0].ID != "tip" {
		t.Errorf("declaration = %+v, want it to carry the check", prog[2])
	}
	if !strings.Contains(prog[3].Text, "clicked [1]") {
		t.Errorf("the click after declaring = %q, want it to land", prog[3].Text)
	}
	steps, err := machine.ReadSteps(mgr.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	inputs := 0
	for _, st := range steps {
		if st.Tool == "machine_input" {
			inputs++
		}
	}
	if inputs != 1 {
		t.Errorf("%d input steps, want only the declared click", inputs)
	}
	// A refusal repeated is a repeat (issue #125) like any error.
	seen := repeats{}
	call := nim.ToolCall{Name: "machine_click", Arguments: `{"element":1}`}
	seen.record(call, declareFirst)
	if n := seen.record(call, declareFirst); n != 2 {
		t.Errorf("a repeated declare-first refusal counts %d, want 2", n)
	}
}

func splitResult(text string) string {
	_, _, result := splitProgress(text)
	return result
}

// Declaring again replaces the list: a verdict answering the old list is refused, and one
// answering the new list is posted with the new checks and their criteria.
func TestDeclaringAgainReplacesTheChecks(t *testing.T) {
	mgr, runID, _ := ready(t)
	build := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("build", "launch"),
		toolCall("machine_exec", map[string]any{"command": "swift build"}),
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{
			{"id": "build", "criterion": "swift build exits 0."},
		}}),
		verdictOf("pass", "It built (step 2).", answer("build", "pass", []int{build}), answer("launch", "pass", []int{build})),
		verdictOf("pass", "It built (step 2).", answer("build", "pass", []int{build})),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Build it.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)
	if refused := prog[len(prog)-1].Text; !strings.Contains(refused, `check "launch" (unknown id)`) {
		t.Errorf("refusal = %q, want the replaced check named", refused)
	}
	got := lastMessage(t, store)
	if got.Kind != session.Verdict || got.Verdict != "pass" || len(got.Checks) != 1 ||
		got.Checks[0].Criterion != "swift build exits 0." {
		t.Fatalf("verdict = %+v, want the pass with the new list's criterion", got)
	}
}

// The coordinator's ask: a client reading only the verdict message sees each check's criterion,
// filled by the daemon from the declaration, since the model sends only ids.
func TestAPostedVerdictCarriesEachDeclaredCriterion(t *testing.T) {
	mgr, runID, _ := ready(t)
	build := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{
			{"id": "build", "criterion": "swift build exits 0."},
			{"id": "tests", "criterion": "swift test reports no failures."},
		}}),
		toolCall("machine_exec", map[string]any{"command": "swift build && swift test"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Both hold (step 2).",
			"checks": []map[string]any{answer("tests", "pass", []int{build}), answer("build", "pass", []int{build})}}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Build and test it.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	got := lastMessage(t, store)
	if got.Kind != session.Verdict || len(got.Checks) != 2 {
		t.Fatalf("last = %+v, want the verdict with two checks", got)
	}
	// In declared order, each with its criterion, applied kind (ADR 0027), status, evidence and
	// what was observed.
	want := []session.Check{
		{ID: "build", Criterion: "swift build exits 0.", Kinds: []string{session.CheckValue}, Status: "pass", Evidence: []int{build}, Observed: "The build looked as described."},
		{ID: "tests", Criterion: "swift test reports no failures.", Kinds: []string{session.CheckValue}, Status: "pass", Evidence: []int{build}, Observed: "The tests looked as described."},
	}
	a, _ := json.Marshal(got.Checks)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("checks = %s, want %s", a, b)
	}
	if v := store.Verdict(); len(v.Checks) != 2 {
		t.Errorf("verdict state = %+v, want its checks", v)
	}
}

// A screen handover after the evidence makes it stale: the pass is refused until the verifier
// looks again, and then posts.
func TestAPassNeedsEvidenceNewerThanTheLatestHandover(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentSelectedUI)
	look := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("tip"),
		toolCall("machine_ui", map[string]any{}),
		verdictOf("pass", "25% is selected (step 2).", answer("tip", "pass", []int{look})),
		toolCall("machine_ui", map[string]any{}),
		verdictOf("pass", "25% is selected (step 3).", answer("tip", "pass", []int{look + 1})),
	}}
	model.onReasoning = func(n int) {
		if n == 3 { // a person takes the screen and gives it back after the look
			if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
				t.Errorf("TakeControl: %v", err)
			}
			if _, _, err := mgr.ReleaseControl(runID, "human"); err != nil {
				t.Errorf("ReleaseControl: %v", err)
			}
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check 25% is selected.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)
	if refused := prog[2].Text; !strings.Contains(refused, `check "tip" (freshness): the screen changed hands`) {
		t.Errorf("refusal = %q, want the handover named", refused)
	}
	if got := store.Verdict(); got.Verdict != "pass" || got.Checks[0].Evidence[0] != look+1 {
		t.Errorf("verdict = %+v, want the pass on the look after the handover", got)
	}
}

// A pass whose action's effect check found a change can cite that read as its evidence.
func TestAClickWithAnEffectCarriesAPass(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	look := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("tip"),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 1}),
		verdictOf("pass", "25% is selected (step 4).", answer("tip", "pass", []int{look + 2}, look+1)),
	}}
	selectOnClick(t, model, control, 3)
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Select 25%.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	click := messagesOfKind(store, session.Progress)[2].Text
	want := "machine_ui step 4 read the UI after this input.\neffect: 1 change\n  [1] RadioButton/Segment \"25%\": now selected"
	if !strings.Contains(click, strings.Replace(want, "step 4", "step "+itoa(look+2), 1)) {
		t.Errorf("click result = %q, want %q", click, want)
	}
	if got := store.Verdict(); got.Verdict != "pass" {
		t.Errorf("verdict = %+v, want pass", got)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// With no usable tree the effect is unknown and the model is told to take a screenshot; with no
// earlier read to compare it says so.
func TestTheEffectIsUnknownWithoutATree(t *testing.T) {
	mgr, runID, control := ready(t)
	model := &scriptedModel{replies: []string{
		declared("tip"),
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}), // the fake app lists no elements
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		verdictOf("inconclusive", "Nothing could be read."),
	}}
	model.onReasoning = func(n int) {
		if n == 3 {
			putUI(t, control, segmentUI) // a tree appears, but there was no earlier one to compare
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click it.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)
	if !strings.HasSuffix(prog[1].Text, "effect: unknown (no UI tree). Take a machine_screenshot to see what this input did.") {
		t.Errorf("result = %q, want the unknown effect", prog[1].Text)
	}
	if !strings.Contains(prog[2].Text, "effect: unknown (no earlier machine_ui read to compare)") {
		t.Errorf("result = %q, want no earlier read named", prog[2].Text)
	}
}

func TestDescribeEffect(t *testing.T) {
	el := func(id int, role, title, value string, selected bool, x float64) machine.UIElement {
		return machine.UIElement{ID: id, Role: role, Title: title, Value: value, Selected: selected, X: x, Y: 0.5}
	}
	before := machine.UITree{App: "Groceries", Elements: []machine.UIElement{
		el(1, "TextField", "Item", "Apples", false, 0.1),
		el(2, "Button", "Add", "", false, 0.2),
		el(3, "StaticText", "", "No items", false, 0.3),
	}}
	cases := []struct {
		name  string
		after machine.UITree
		want  []string
	}{
		{"no change", before, []string{machine.EffectNone, "effect: no change detected. If you expected a change, the input may have been lost"}},
		{"changed", machine.UITree{App: "Groceries", Elements: []machine.UIElement{
			el(1, "TextField", "Item", "", false, 0.1),
			el(2, "Button", "Add", "", false, 0.2),
			el(3, "Row", "Apples", "", false, 0.3),
		}}, []string{machine.EffectChanged, "effect: 3 changes", `[1] TextField "Item": value "Apples" -> ""`, `+ [3] Row "Apples"`,
			`- StaticText value="No items"`, "Element ids changed: call machine_ui before clicking by id."}},
		{"moved", machine.UITree{App: "Groceries", Elements: []machine.UIElement{
			el(1, "TextField", "Item", "Apples", false, 0.15),
			el(2, "Button", "Add", "", false, 0.25),
			el(3, "StaticText", "", "No items", false, 0.35),
		}}, []string{machine.EffectChanged, "effect: 1 change", "3 elements moved"}},
		{"another app", machine.UITree{App: "Finder"}, []string{machine.EffectChanged, "effect: the frontmost app is now Finder (was Groceries)"}},
		// ADR 0027: text that stops being drawn is a change, and so is text that starts.
		{"hidden", machine.UITree{App: "Groceries", Elements: []machine.UIElement{
			el(1, "TextField", "Item", "Apples", false, 0.1),
			el(2, "Button", "Add", "", false, 0.2),
			{ID: 3, Role: "StaticText", Value: "No items", X: 0.3, Y: 0.5, Rendered: machine.RenderedBlank},
		}}, []string{machine.EffectChanged, "effect: 1 change", `[3] StaticText: now not drawn`}},
	}
	for _, c := range cases {
		kind, got := describeEffect(before, c.after)
		if kind != c.want[0] {
			t.Errorf("%s: kind = %q, want %q", c.name, kind, c.want[0])
		}
		for _, w := range c.want[1:] {
			if !strings.Contains(got, w) {
				t.Errorf("%s: effect = %q, want %q", c.name, got, w)
			}
		}
	}
	// Capped: a whole screen changing lists a few changes and the rest as a count.
	var many []machine.UIElement
	for i := 1; i <= 20; i++ {
		many = append(many, el(i, "Cell", "row "+itoa(i), "", false, 0.1))
	}
	_, got := describeEffect(machine.UITree{App: "A"}, machine.UITree{App: "A", Elements: many})
	if !strings.Contains(got, "effect: 20 changes") || !strings.Contains(got, "and 12 more") || strings.Count(got, "\n  + ") != maxEffectChanges {
		t.Errorf("capped effect = %q", got)
	}
	for _, c := range []struct {
		tree machine.UITree
		err  error
		prev bool
		want string
	}{
		{machine.UITree{}, errTest("no accessibility"), true, effectNoTree},
		{machine.UITree{App: "Groceries"}, nil, true, effectNoTree},
		{before, nil, false, "effect: unknown (no earlier machine_ui read to compare)"},
	} {
		if kind, text := judgeEffect(before, c.prev, c.tree, c.err); kind != machine.EffectUnknown || !strings.HasPrefix(text, c.want) {
			t.Errorf("judgeEffect = %q, %q, want unknown %q", kind, text, c.want)
		}
	}
}

// reviewTranscript is a task, its declared checks, and progress for reviewSteps.
func reviewTranscript(ids ...string) []session.Message {
	var checks []session.Check
	for _, id := range ids {
		checks = append(checks, session.Check{ID: id, Criterion: "The " + id + " holds."})
	}
	p := func(step int, tool, result string) session.Message {
		return session.Message{From: session.Verifier, Kind: session.Progress, Step: step, Text: tool + " {}\n" + result}
	}
	return []session.Message{
		{From: session.Coder, Kind: session.Task, Text: "Check it."},
		{From: session.Verifier, Kind: session.Progress, Text: "declare_checks {}\nDeclared.", Checks: checks},
		p(3, "machine_ui", "step 3\nApp: Groceries"),
		p(5, "machine_click", "step 5\nclicked (0.5, 0.5)\nmachine_ui step 6 read the UI after this input.\n"+effectNone+"."),
		p(7, "machine_ui", "step 7\nApp: Groceries"),
		p(8, "machine_click", "step 8\nclicked (0.5, 0.5)\nmachine_ui step 9 read the UI after this input.\neffect: 1 change\n  [4] CheckBox \"Milk\": value \"0\" -> \"1\""),
		p(10, "machine_exec", "error: tart failed"),
		p(11, "machine_screenshot", "step 11\nThe screen shows: Groceries"),
	}
}

// reviewSteps is a run's step records for reviewVerdict: the verifier's look (3), a coder's
// look (4), a click with no effect (5, read 6), a look (7), a click that changed something (8,
// read 9), a failed exec (10) and a screenshot (11).
func reviewSteps() []machine.Step {
	v := machine.HolderVerifier
	return []machine.Step{
		{Seq: 1, Tool: "machine_create"},
		{Seq: 2, Tool: "machine_boot"},
		{Seq: 3, Tool: "machine_ui", By: v},
		{Seq: 4, Tool: "machine_ui", By: machine.HolderCoder},
		{Seq: 5, Tool: "machine_input", By: v},
		{Seq: 6, Tool: "machine_ui", By: v, Effect: &machine.StepEffect{Of: 5, Kind: machine.EffectNone}},
		{Seq: 7, Tool: "machine_ui", By: v},
		{Seq: 8, Tool: "machine_input", By: v},
		{Seq: 9, Tool: "machine_ui", By: v, Effect: &machine.StepEffect{Of: 8, Kind: machine.EffectChanged}},
		{Seq: 10, Tool: "machine_exec", By: v, Error: "tart failed"},
		{Seq: 11, Tool: "machine_screenshot", By: v},
	}
}

func reviewArgs(verdict string, checks ...map[string]any) verdictCall {
	b, _ := json.Marshal(map[string]any{"verdict": verdict, "summary": "s", "checks": checks})
	return parseVerdict(string(b))
}

// Every rule of ADR 0024 point 3, each refused with a message naming the rule and the check.
func TestReviewVerdictRefusesEachBrokenRule(t *testing.T) {
	cases := []struct {
		name     string
		ids      []string
		handover int
		call     verdictCall
		want     string // "" for a verdict that holds
	}{
		{"a pass that holds", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{9}, 8)), ""},
		{"a fail that holds", []string{"milk"}, 0, reviewArgs("fail", answer("milk", "fail", []int{6}, 5)), ""},
		{"no checks declared", nil, 0, reviewArgs("pass"), "checks: no checks are declared"},
		{"unanswered", []string{"milk", "total"}, 0, reviewArgs("pass", answer("milk", "pass", []int{9}, 8)),
			`check "total" (answered): not answered`},
		{"unknown id", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{9}, 8), answer("eggs", "pass", []int{9})),
			`check "eggs" (unknown id): it was not declared; the declared ids are "milk"`},
		{"not recorded", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{40})),
			`check "milk" (cited step): evidence step 40 is not a step you recorded in this run`},
		{"an action as evidence", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{8})),
			`check "milk" (kind): evidence step 8 is a machine_input; evidence must be an observation`},
		{"someone else's look", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{4})),
			`check "milk" (cited step): evidence step 4 was recorded by the coder, not by you`},
		{"a look as an action", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{9}, 7)),
			`check "milk" (kind): action step 7 is a machine_ui; actions must be inputs`},
		{"a failed observation", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{10})),
			`check "milk" (kind): evidence step 10 failed, so it observed nothing`},
		{"not a step number", []string{"milk"}, 0, reviewArgs("pass", map[string]any{"id": "milk", "status": "pass", "evidence": []any{"the tree"}}),
			`check "milk" (cited step): "the tree" is not a step number`},
		{"stale against an action", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{7}, 8)),
			`check "milk" (freshness): no evidence step comes after action step 8`},
		{"stale against a handover", []string{"milk"}, 10, reviewArgs("pass", answer("milk", "pass", []int{9}, 8)),
			`check "milk" (freshness): the screen changed hands after step 10`},
		{"no evidence", []string{"milk"}, 0, reviewArgs("fail", answer("milk", "fail", nil)),
			`check "milk" (freshness): no evidence steps`},
		{"a lost action", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{6}, 5)),
			`check "milk" (effect): an action in it found no change detected (effect check at step 6)`},
		{"a lost action looked at again", []string{"milk"}, 0, reviewArgs("pass", answer("milk", "pass", []int{7}, 5)), ""},
		{"a pass with an unchecked check", []string{"milk", "total"}, 0,
			reviewArgs("pass", answer("milk", "pass", []int{9}, 8), answer("total", "unchecked", nil)),
			`check "total" (pass): its status is unchecked; a pass needs every check pass`},
		{"a fail with no failing check", []string{"milk"}, 0, reviewArgs("fail", answer("milk", "pass", []int{9}, 8)),
			"fail: no check is fail with valid evidence"},
		{"a bad status", []string{"milk"}, 0, reviewArgs("fail", answer("milk", "broken", []int{9})),
			`check "milk" (status): "broken" is not pass, fail or unchecked`},
		{"a step in evidence", []string{"milk"}, 0, parseVerdict(`{"verdict":"fail","summary":"s","evidence":["step 9"],` +
			`"checks":[{"id":"milk","status":"fail","evidence":[9],"actions":[8]}]}`), `evidence: "step 9" is a step`},
	}
	for _, c := range cases {
		r := reviewVerdict(c.call, reviewTranscript(c.ids...), reviewSteps(), c.handover)
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
		if msg := refusal(r.problems); !strings.HasPrefix(msg, "error: report_verdict refused; nothing was posted") {
			t.Errorf("%s: refusal = %q", c.name, msg)
		}
	}
}

// An inconclusive is always allowed; its answers are made honest: a check not answered and a
// pass that breaks a rule are posted as unchecked, saying why, and an unknown id is dropped.
func TestAnInconclusiveNamesWhatWasNotChecked(t *testing.T) {
	r := reviewVerdict(reviewArgs("inconclusive", answer("milk", "pass", []int{6}, 5), answer("eggs", "pass", []int{9})),
		reviewTranscript("milk", "total"), reviewSteps(), 0)
	if len(r.problems) != 0 {
		t.Fatalf("an inconclusive was refused: %v", r.problems)
	}
	c := r.msg.Checks
	if len(c) != 2 || c[0].ID != "milk" || c[0].Status != "unchecked" || !strings.Contains(c[0].Observed, "Not verified: effect: an action") ||
		c[1].ID != "total" || c[1].Status != "unchecked" || c[1].Observed != "Not answered." || c[1].Criterion != "The total holds." {
		t.Errorf("checks = %+v, want milk and total unchecked with reasons", c)
	}
	// With no checks declared it is allowed too, with none.
	if r := reviewVerdict(reviewArgs("inconclusive"), reviewTranscript(), reviewSteps(), 0); len(r.problems) != 0 || len(r.msg.Checks) != 0 {
		t.Errorf("review = %+v, want an inconclusive with no checks", r)
	}
}

// Checks answer the newest task: a declaration made before it does not count.
func TestChecksBelongToTheNewestTask(t *testing.T) {
	msgs := reviewTranscript("milk")
	if declaredChecks(msgs) == nil || inputRefusal(nim.ToolCall{Name: "machine_click"}, msgs) != "" {
		t.Fatal("the declared checks were not found")
	}
	msgs = append(msgs, session.Message{From: session.Human, Kind: session.Task, Text: "Now check eggs."})
	if declaredChecks(msgs) != nil || inputRefusal(nim.ToolCall{Name: "machine_click"}, msgs) != declareFirst {
		t.Error("checks declared before the newest task still count")
	}
	if inputRefusal(nim.ToolCall{Name: "machine_ui"}, msgs) != "" || inputRefusal(nim.ToolCall{Name: "machine_exec"}, msgs) != "" {
		t.Error("looking was refused")
	}
	// No task open (the verdict answered it): input is not gated.
	msgs = append(msgs, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "inconclusive", Text: "x"})
	if inputRefusal(nim.ToolCall{Name: "machine_click"}, msgs) != "" {
		t.Error("input was refused with no task open")
	}
}

func TestParseDeclaredChecks(t *testing.T) {
	many := make([]map[string]any, 13)
	for i := range many {
		many[i] = map[string]any{"id": itoa(i), "criterion": "c"}
	}
	for _, c := range []struct {
		args any
		want string
	}{
		{map[string]any{}, "declare_checks needs checks: 1 to 12"},
		{map[string]any{"checks": many}, "this call sent 13"},
		{map[string]any{"checks": []map[string]any{{"id": "a", "criterion": "x"}, {"id": "a", "criterion": "y"}}}, `check id "a" appears twice`},
		{map[string]any{"checks": []map[string]any{{"id": "a", "criterion": " "}}}, `check "a" needs a criterion`},
		{map[string]any{"checks": []map[string]any{{"id": strings.Repeat("x", 41), "criterion": "y"}}}, "check 1 needs an id of 1 to 40 characters"},
		{"not an object", "your arguments did not parse"},
		{map[string]any{"checks": []map[string]any{{"id": "a", "criterion": "x", "kinds": []string{"visual", "speed"}}}}, `check "a" kind "speed" is not value, visual or timing`},
		{map[string]any{"checks": []map[string]any{{"id": "a", "criterion": "x", "kind": "timing", "within": 0}}}, `check "a" within must be seconds, above 0`},
		{map[string]any{"checks": []map[string]any{{"id": "a", "criterion": "x", "kind": "timing", "within": 601}}}, "at most 600"},
		{map[string]any{"checks": []map[string]any{{"id": "a", "criterion": "x", "kinds": []string{"visual"}, "within": 3}}}, `check "a" is not timing; within is for a timing check only`},
	} {
		b, _ := json.Marshal(c.args)
		checks, _, problem := parseDeclaredChecks(string(b))
		if checks != nil || !strings.HasPrefix(problem, "error: declare_checks") || !strings.Contains(problem, c.want) {
			t.Errorf("parseDeclaredChecks(%s) = %v, %q, want %q", b, checks, problem, c.want)
		}
	}
	checks, _, problem := parseDeclaredChecks(`{"checks":[{"id":7,"criterion":" Total is $48.00 "}]}`)
	if problem != "" || len(checks) != 1 || checks[0].ID != "7" || checks[0].Criterion != "Total is $48.00" ||
		!slices.Equal(checks[0].Kinds, []string{session.CheckValue}) {
		t.Errorf("parseDeclaredChecks = %+v, %q", checks, problem)
	}
}

// ADR 0024 point 5: the coder's task reaches the model labelled as unverified claims; a human's
// task and the coder's other messages do not carry the label.
func TestTheCodersTaskIsLabelledAsClaims(t *testing.T) {
	msgs := []session.Message{
		{Seq: 1, From: session.Coder, Kind: session.Task, Text: "I fixed it; Each pays shows $48.00 now."},
		{Seq: 2, From: session.Human, Kind: session.Task, Text: "Check the tip."},
	}
	var user []string
	for _, m := range project(msgs) {
		if m.Role == "user" {
			user = append(user, m.Content)
		}
	}
	if len(user) != 2 || !strings.Contains(user[0], "I fixed it; Each pays shows $48.00 now.\n"+claimLabel) {
		t.Fatalf("coder task = %q, want the claim label", user)
	}
	if strings.Contains(user[1], claimLabel) {
		t.Errorf("human task = %q, want no claim label", user[1])
	}
	late := projectLate(msgs[:1])
	if len(late) != 1 || !strings.Contains(late[0].Content, claimLabel) {
		t.Errorf("a mid-turn task = %+v, want the label too", late)
	}
	if !strings.Contains(systemPrompt, "unverified claims: turn them into checks, never cite them") {
		t.Error("the system prompt lacks the claim rule")
	}
}

// A past verdict is projected with its answered checks, as report_verdict takes them.
func TestAPastVerdictIsProjectedWithItsChecks(t *testing.T) {
	out := project([]session.Message{{Seq: 4, From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "x",
		Checks: []session.Check{{ID: "milk", Criterion: "c", Status: "fail", Evidence: []int{7}, Observed: "unchecked"}}}})
	args := out[1].ToolCalls[0].Arguments
	if want := `"checks":[{"actions":[],"evidence":[7],"id":"milk","observed":"unchecked","status":"fail"}]`; !strings.Contains(args, want) {
		t.Errorf("arguments = %s, want %s", args, want)
	}
}

// ADR 0024 point 7: the manual brain is a person's judgement. It acts with no declared checks, its
// input reports no effect, and its verdict posts with the free evidence list and no checks.
func TestTheManualBrainIsExempt(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click 25%.")
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "click 0.5 0.5\nverdict pass it clicked"})
	if _, err := NewManual(mgr, testLog()).Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 || !strings.Contains(prog[0].Text, "clicked (0.500, 0.500)") || strings.Contains(prog[0].Text, "effect:") {
		t.Errorf("progress = %+v, want the click with no refusal and no effect check", prog)
	}
	got := lastMessage(t, store)
	if got.Kind != session.Verdict || got.Verdict != "pass" || len(got.Checks) != 0 || len(got.Evidence) == 0 {
		t.Errorf("verdict = %+v, want the manual pass with its free evidence", got)
	}
}

// The review reads step records, never progress text: rewording every progress message, or
// dropping them, changes nothing (the coordinator's robustness ask).
func TestTheReviewDoesNotDependOnProgressWording(t *testing.T) {
	calls := []verdictCall{
		reviewArgs("pass", answer("milk", "pass", []int{9}, 8)),
		reviewArgs("pass", answer("milk", "pass", []int{6}, 5)),
		reviewArgs("fail", answer("milk", "fail", []int{4})),
		reviewArgs("inconclusive", answer("milk", "pass", []int{6}, 5)),
	}
	for i, call := range calls {
		want := reviewVerdict(call, reviewTranscript("milk"), reviewSteps(), 0)
		reworded := reviewTranscript("milk")
		for j := range reworded {
			if reworded[j].Kind == session.Progress {
				reworded[j].Text = "something else entirely\nerror: effect: no change detected"
			}
		}
		for name, msgs := range map[string][]session.Message{"reworded": reworded, "no progress text": reworded[:2]} {
			got := reviewVerdict(call, msgs, reviewSteps(), 0)
			a, _ := json.Marshal([]any{want.problems, want.msg})
			b, _ := json.Marshal([]any{got.problems, got.msg})
			if string(a) != string(b) {
				t.Errorf("call %d, %s: review = %s, want %s", i, name, b, a)
			}
		}
	}
}

// A review after a restart reads the same records: a fresh Manager on the run's root, with the
// machine gone, still finds every step, its seat and the effect on its read.
func TestTheReviewReadsTheStepRecordsAfterARestart(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	look := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("tip"),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 1}),
		verdictOf("inconclusive", "Paused."),
	}}
	selectOnClick(t, model, control, 3)
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Select 25%.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	fresh, err := machine.NewManager(mgr.Root, testLog(), machine.WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	steps, err := fresh.Steps(runID)
	if err != nil {
		t.Fatal(err)
	}
	var read *machine.Step
	for i := range steps {
		if steps[i].Seq == look+2 {
			read = &steps[i]
		}
	}
	if read == nil || read.By != machine.HolderVerifier || read.Effect == nil || read.Effect.Of != look+1 ||
		read.Effect.Kind != machine.EffectChanged || !strings.Contains(read.Effect.Summary, "now selected") {
		t.Fatalf("effect read = %+v, want the verifier's read carrying the click's effect", read)
	}
	reopened := openStore(t, mgr, runID)
	call := reviewArgs("pass", answer("tip", "pass", []int{look + 2}, look+1))
	if r := reviewVerdict(call, reopened.After(0), steps, fresh.HandoverStep(runID)); len(r.problems) != 0 {
		t.Errorf("after a restart the review found %v, want the pass to hold", r.problems)
	}
	if _, err := fresh.Steps("../escape"); err == nil {
		t.Error("Steps read outside the runs directory")
	}
}

// Issue #153: a fail stands when at least one failing check is properly evidenced. The answers
// that break a rule are posted unchecked, each with its reason, a check not answered is
// unchecked too, and the summary lists them. A false pass is the costly error (ADR 0024); a
// grounded fail hidden behind two bad answers helps nobody (ADR 0031).
func TestAGroundedFailStandsOverBadAnswers(t *testing.T) {
	r := reviewVerdict(reviewArgs("fail",
		answer("milk", "fail", []int{6}, 5), // valid: its read found the click changed nothing
		answer("total", "fail", []int{4}),   // the coder's look
		answer("eggs", "pass", []int{7}, 8), // stale: no look after the click
	), reviewTranscript("milk", "total", "eggs", "bread"), reviewSteps(), 0)
	if len(r.problems) != 0 {
		t.Fatalf("a grounded fail was refused: %v", r.problems)
	}
	if r.msg.Verdict != "fail" {
		t.Fatalf("verdict = %q, want fail", r.msg.Verdict)
	}
	want := map[string]string{
		"milk":  "fail",
		"total": "unchecked Not verified: cited step: evidence step 4 was recorded by the coder, not by you",
		"eggs":  "unchecked Not verified: freshness: no evidence step comes after action step 8",
		"bread": "unchecked Not answered.",
	}
	if len(r.msg.Checks) != len(want) {
		t.Fatalf("checks = %+v, want %d", r.msg.Checks, len(want))
	}
	for _, c := range r.msg.Checks {
		got := c.Status
		if c.Status != session.CheckFail {
			got += " " + c.Observed
		}
		if !strings.HasPrefix(got, want[c.ID]) {
			t.Errorf("check %s = %q, want %q", c.ID, got, want[c.ID])
		}
		if c.Criterion != "The "+c.ID+" holds." {
			t.Errorf("check %s lost its criterion: %q", c.ID, c.Criterion)
		}
	}
	for _, s := range []string{"s\n\n[greenroom] Posted as fail on its evidenced failing checks. 3 answers did not hold " +
		"and are shown as unchecked:", `check "total" (cited step)`, `check "eggs" (freshness)`, `check "bread" (answered)`} {
		if !strings.Contains(r.msg.Text, s) {
			t.Errorf("summary = %q, want it to contain %q", r.msg.Text, s)
		}
	}

	// A fail whose only failing answer breaks a rule is still refused: nothing grounds it.
	r = reviewVerdict(reviewArgs("fail", answer("milk", "fail", []int{4}), answer("total", "unchecked", nil)),
		reviewTranscript("milk", "total"), reviewSteps(), 0)
	if !strings.Contains(strings.Join(r.problems, "\n"), "fail: no check is fail with valid evidence") {
		t.Errorf("problems = %q, want the ungrounded fail refused", r.problems)
	}
	// A pass is unchanged: one bad answer refuses it.
	r = reviewVerdict(reviewArgs("pass", answer("milk", "pass", []int{9}, 8), answer("total", "pass", []int{4})),
		reviewTranscript("milk", "total"), reviewSteps(), 0)
	if !strings.Contains(strings.Join(r.problems, "\n"), `check "total" (cited step)`) {
		t.Errorf("problems = %q, want the pass refused", r.problems)
	}
}
