package verifier

import (
	"context"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// After the first input on a task, a declaration may add checks but never drop or weaken one:
// otherwise a model could shed the check it is failing and pass the rest. Before it, declaring
// again replaces the list (TestDeclaringAgainReplacesTheChecks).
func TestARedeclarationAfterAnInputKeepsEveryCheck(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, todoUI("Milk"))
	look := lastStep(t, mgr, runID) + 1
	click, read := look+1, look+2
	added := map[string]any{"id": "added", "criterion": "Milk appears in the list"}
	cleared := map[string]any{"id": "cleared", "criterion": "The New item field is empty after Add"}
	extra := map[string]any{"id": "extra", "criterion": "The Add button is still there"}
	model := &scriptedModel{replies: []string{
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{added, cleared}}),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 2}),
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{cleared}}),
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{added, cleared, extra}}),
		verdictOf("pass", "Milk was added and the field emptied (step 3).",
			answer("added", "pass", []int{read}, click), answer("cleared", "pass", []int{read}, click),
			answer("extra", "pass", []int{read}, click)),
	}}
	model.onReasoning = func(n int) {
		if n == 3 {
			putUI(t, control, todoUI("", "Milk"))
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Add Milk to TodoList (running on screen): it appears in the list and the field empties.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 5 {
		t.Fatalf("%d progress messages, want 5", len(prog))
	}
	refused := prog[3]
	if !strings.Contains(refused.Text, `error: declare_checks refused; the declared checks stand`) ||
		!strings.Contains(refused.Text, `"added" is missing`) || len(refused.Checks) != 0 {
		t.Errorf("the dropping declaration = %+v, want it refused naming the dropped check", refused)
	}
	if len(prog[4].Checks) != 3 {
		t.Errorf("the adding declaration = %+v, want it accepted with 3 checks", prog[4])
	}
	got := lastMessage(t, store)
	if got.Kind != session.Verdict || got.Verdict != "pass" || len(got.Checks) != 3 {
		t.Fatalf("verdict = %+v, want the pass answering all 3 checks", got)
	}
}

// Every way to lose a declared check after an input is refused, by name; adding and keeping is not.
func TestRedeclareRefusalNamesEachLostCheck(t *testing.T) {
	visual := session.Check{ID: "shown", Criterion: "Milk appears", Kinds: []string{session.CheckVisual}}
	timing := session.Check{ID: "fast", Criterion: "It updates", Kinds: []string{session.CheckTiming}, Within: 2}
	msgs := []session.Message{
		{From: session.Coder, Kind: session.Task, Text: "Check it."},
		{From: session.Verifier, Kind: session.Progress, Text: "declare_checks {}\nDeclared.", Checks: []session.Check{visual, timing}},
		{From: session.Verifier, Kind: session.Progress, Text: "machine_exec {}\nstep 4\nexit code 0", Step: 4},
	}
	value := session.Check{ID: "shown", Criterion: "Milk appears", Kinds: []string{session.CheckValue}}
	longer := timing
	longer.Within = 10
	reworded := visual
	reworded.Criterion = "Something appears"
	extra := session.Check{ID: "extra", Criterion: "The field empties", Kinds: []string{session.CheckValue}}
	if got := redeclareRefusal([]session.Check{value}, msgs); got != "" {
		t.Errorf("before any input: %q, want the list replaced freely", got)
	}
	msgs = append(msgs, session.Message{From: session.Verifier, Kind: session.Progress,
		Text: "machine_click {\"element\":2}\nstep 5\nclicked", Step: 5})
	for _, c := range []struct {
		checks []session.Check
		want   string
	}{
		{[]session.Check{timing}, `"shown" is missing`},
		{[]session.Check{value, timing}, `"shown" dropped a kind (it was visual)`},
		{[]session.Check{visual, longer}, `"fast" has a longer window (it was 2 s)`},
		{[]session.Check{reworded, timing}, `"shown" changed its criterion`},
		{[]session.Check{visual, timing, extra}, ""},
	} {
		got := redeclareRefusal(c.checks, msgs)
		if c.want == "" && got != "" || !strings.Contains(got, c.want) {
			t.Errorf("redeclare %+v = %q, want %q", c.checks, got, c.want)
		}
	}
}
