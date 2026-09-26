package verifier

import (
	"context"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// toolCallRaw is toolCall with the arguments exactly as the model wrote them.
func toolCallRaw(name, args string) string {
	return `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":` +
		quote(name) + `,"arguments":` + quote(args) + `}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
}

// Issue #125: the verifier sent machine_type with no text about 6 times in a row. The second
// identical failure is told so, and the third ends the turn with a question naming the call.
func TestARepeatedFailingCallIsWarnedThenEndsTheTurnWithAQuestion(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the split.")
	post(t, store, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "Each pays is $45.00, not $53.10."})
	model := &scriptedModel{replies: []string{
		declared("each-pays"),
		toolCallRaw("machine_type", `{"text": "", "note": "bill"}`),
		// The same call with its keys in another order and other spacing.
		toolCallRaw("machine_type", `{"note":"bill","text":""}`),
		toolCallRaw("machine_type", "{\n  \"text\": \"\",\n  \"note\": \"bill\"\n}"),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "Could not type."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	postTask(t, store, "Type the bill and report Each pays.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(model.request(t, 3), "You already made this exact call") {
		t.Error("the first failure was already called a repeat")
	}
	if !strings.Contains(model.request(t, 4), "You already made this exact call and it failed the same way") {
		t.Error("the second identical failure was not told it repeats")
	}
	if res.Ended != session.Question || res.Steps != 4 || model.calls() != 4 {
		t.Fatalf("ended %q after %d steps and %d calls, want a question after the third failure", res.Ended, res.Steps, model.calls())
	}
	last := lastMessage(t, store)
	for _, want := range []string{"machine_type", `{"note":"bill","text":""}`, "needs text", "3 times", "verdict still stands"} {
		if !strings.Contains(last.Text, want) {
			t.Errorf("question = %q, want it to contain %q", last.Text, want)
		}
	}
	if got := store.Verdict(); got.Verdict != "fail" {
		t.Errorf("verdict = %+v, want the standing fail kept", got)
	}
	if n := len(messagesOfKind(store, session.Progress)); n != 4 {
		t.Errorf("%d progress messages, want the declaration and the 3 failures", n)
	}
}

// A different call in between does not reset a count, and different calls are never repeats.
func TestDifferentFailingCallsAreNotARepeat(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		declared("bill"),
		toolCall("machine_type", map[string]any{"text": ""}),
		toolCall("machine_key", map[string]any{"key": ""}),
		toolCall("machine_type", map[string]any{"text": ""}),
		toolCall("machine_key", map[string]any{"key": ""}),
		toolCall("machine_type", map[string]any{"value": "160"}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "Could not type."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Type the bill.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict {
		t.Fatalf("ended %q, want the model's verdict: no call failed 3 times", res.Ended)
	}
	// Each request carries the whole turn, so it counts every warning so far: the second
	// machine_type and the second machine_key are repeats, the machine_type with value is not.
	for n, want := range map[int]int{3: 0, 4: 0, 5: 1, 6: 2, 7: 2} {
		if got := strings.Count(model.request(t, n), "You already made this exact call"); got != want {
			t.Errorf("request %d carries %d repeat warnings, want %d", n, got, want)
		}
	}
	// The wrong field name is named in the error, so the model can correct it.
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 6 || !strings.Contains(prog[5].Text, "this call sent: value") {
		t.Errorf("last progress = %q, want the error to name the field that arrived", prog[len(prog)-1].Text)
	}
}

func TestRepeatsCountsIdenticalFailuresAndASuccessClearsThem(t *testing.T) {
	r := repeats{}
	a := nim.ToolCall{Name: "machine_click", Arguments: `{"element": 5}`}
	b := nim.ToolCall{Name: "machine_click", Arguments: `{"element": 6}`}
	const refused = "error: element 5 is not in your latest machine_ui"
	if n := r.record(a, refused); n != 1 {
		t.Fatalf("first failure = %d, want 1", n)
	}
	if n := r.record(b, refused); n != 1 {
		t.Errorf("another call's failure = %d, want 1", n)
	}
	if n := r.record(a, refused); n != 2 {
		t.Errorf("second identical failure = %d, want 2", n)
	}
	if n := r.record(a, "error: another reason"); n != 1 {
		t.Errorf("a different error = %d, want 1", n)
	}
	if n := r.record(a, "step 7\nclicked [5] OK"); n != 0 {
		t.Errorf("a success = %d, want 0", n)
	}
	if n := r.record(a, refused); n != 1 {
		t.Errorf("a failure after the success = %d, want the count restarted at 1", n)
	}
	if n := r.record(b, refused); n != 2 {
		t.Errorf("the other call = %d, want its count kept at 2", n)
	}
	for i := 0; i < 3; i++ {
		if n := r.record(a, screenTakenResult(errTest("human holds the screen"))); n != 0 {
			t.Errorf("a screen-taken refusal = %d, want it left to the #97 rule", n)
		}
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestCanonicalArgsIgnoresKeyOrderAndSpacing(t *testing.T) {
	for _, args := range []string{`{"x":0.5,"y":0.25}`, `{ "y": 0.25, "x": 0.5 }`, "{\n\"y\":0.25,\n\"x\":0.50}"} {
		if got := canonicalArgs(args); got != `{"x":0.5,"y":0.25}` {
			t.Errorf("canonicalArgs(%q) = %q", args, got)
		}
	}
	if got := canonicalArgs(""); got != "{}" {
		t.Errorf("empty arguments = %q, want {}", got)
	}
	if got := canonicalArgs(" not json "); got != "not json" {
		t.Errorf("broken arguments = %q, want them kept as sent", got)
	}
}

// Issue #125: a model typing a number sent {"text": 160}, which a string field refused as no text.
func TestMachineTypeTypesANumberAsWritten(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		declared("bill"),
		toolCallRaw("machine_type", `{"text": 160}`),
		toolCallRaw("machine_key", `{"key": 5}`),
		verdictOf("inconclusive", "Typed 160; the result was not read."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Type 160.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
	if len(prog) != 2 || !strings.Contains(prog[0].Text, `typed "160"`) || !strings.Contains(prog[1].Text, "pressed 5") {
		t.Fatalf("progress = %+v, want 160 typed and 5 pressed", prog)
	}
}

// A pretty-printed call is projected back on one line, as valid JSON.
func TestProgressTextKeepsPrettyPrintedArgumentsOnOneLine(t *testing.T) {
	call := nim.ToolCall{Name: "machine_type", Arguments: "{\n  \"text\": \"160\"\n}"}
	name, args, result := splitProgress(progressText(call, "step 4\ntyped \"160\""))
	if name != "machine_type" || args != `{"text":"160"}` || result != "step 4\ntyped \"160\"" {
		t.Errorf("split = %q %q %q", name, args, result)
	}
}
