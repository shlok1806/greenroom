package verifier

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// execs is n scripted machine_exec calls, a model that never finishes.
func execs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = toolCall("machine_exec", map[string]any{"command": "echo again"})
	}
	return out
}

// offered lists the tools the nth (1-based) request offered.
func (s *scriptedModel) offered(t *testing.T, n int) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 1 || n > len(s.requests) {
		t.Fatalf("the model was called %d times, want at least %d", len(s.requests), n)
	}
	var names []string
	list, _ := s.requests[n-1]["tools"].([]any)
	for _, tool := range list {
		fn, _ := tool.(map[string]any)["function"].(map[string]any)
		name, _ := fn["name"].(string)
		names = append(names, name)
	}
	return names
}

// Issue #127: a turn that hits its step cap with a task open asks once for a verdict from the
// evidence it has, offering only the tools that end a turn.
func TestTheStepCapWithAnOpenTaskEndsInAClosingVerdict(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: append(execs(6), toolCall("report_verdict", map[string]any{
		"verdict": "inconclusive", "summary": "The picker opened (step 3); its selection was not checked.",
	}))}
	v := newVerifier(t, mgr, model.start(t)) // MaxSteps 6
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the picker.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict || res.Steps != 6 || model.calls() != 7 {
		t.Fatalf("ended %q after %d steps and %d calls, want a verdict from one closing call", res.Ended, res.Steps, model.calls())
	}
	if got := store.Verdict(); got.Verdict != "inconclusive" || !strings.Contains(got.Summary, "not checked") {
		t.Errorf("verdict = %+v, want the closing inconclusive", got)
	}
	closing := model.request(t, 7)
	if !strings.Contains(closing, "You are out of steps for this turn") {
		t.Error("the closing call did not say the turn is out of steps")
	}
	if got := strings.Join(model.offered(t, 7), ","); got != "report_verdict,ask" {
		t.Errorf("the closing call offered %s, want only report_verdict and ask", got)
	}
	if strings.Contains(model.request(t, 6), "out of steps") {
		t.Error("a step before the cap was told it is out of steps")
	}
}

// A closing call that fails or answers in prose leaves the old reply, marked as a stop.
func TestAClosingCallWithoutAVerdictFallsBackToTheMarkedReply(t *testing.T) {
	for name, closing := range map[string][]string{
		"fails": nil, // the scripted model runs out: a 400
		"prose": {prose("I think it works.")},
		"acts":  {toolCall("machine_exec", map[string]any{"command": "echo more"})},
	} {
		t.Run(name, func(t *testing.T) {
			mgr, runID, control := ready(t)
			model := &scriptedModel{replies: append(execs(6), closing...)}
			v := newVerifier(t, mgr, model.start(t))
			store := openStore(t, mgr, runID)
			postTask(t, store, "Check the picker.")

			res, err := v.Turn(context.Background(), runID, store)
			if err != nil {
				t.Fatal(err)
			}
			last := lastMessage(t, store)
			if res.Ended != session.Reply || last.Kind != session.Reply || last.Stop != session.StopSteps {
				t.Fatalf("ended %q with %+v, want the reply marked stop steps", res.Ended, last)
			}
			if !strings.Contains(last.Text, "used all 6 tool calls") {
				t.Errorf("reply = %q", last.Text)
			}
			if model.calls() != 7 {
				t.Errorf("%d model calls, want one closing call after the 6 steps", model.calls())
			}
			if strings.Contains(testsupport.Calls(t, control), "echo more") {
				t.Error("the closing call ran a machine tool")
			}
		})
	}
}

// Issue #127: at the budget the turn's context is dead, so the closing call runs on a fresh one.
func TestTheBudgetWithAnOpenTaskEndsInAClosingVerdict(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "echo slow"}),
		toolCall("report_verdict", map[string]any{"verdict": "fail", "summary": "The picker never opened (step 2)."}),
	}}
	// The first answer comes after the budget, so the turn's own context is gone before any step.
	model.onReasoning = func(n int) {
		if n == 1 {
			time.Sleep(600 * time.Millisecond)
		}
	}
	v, err := New(mgr, Config{
		BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner", VisionModel: "eyes",
		MaxSteps: 100, Budget: 200 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the picker.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if got := store.Verdict(); res.Ended != session.Verdict || got.Verdict != "fail" {
		t.Fatalf("ended %q with verdict %+v, want the closing fail", res.Ended, got)
	}
	closing := model.request(t, 2)
	if !strings.Contains(closing, "You are out of time for this turn") {
		t.Error("the closing call did not say the turn is out of time")
	}
	if strings.Contains(closing, "Machine status: gone") {
		t.Error("the dead turn context made the machine read as gone")
	}
}

// The budget reply carries stop time when the closing call gives nothing.
func TestTheBudgetReplyIsMarkedAsAStop(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: execs(8)}
	v, err := New(mgr, Config{
		BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner", VisionModel: "eyes",
		MaxSteps: 100, Budget: 1 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the picker.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	if last := lastMessage(t, store); last.Kind != session.Reply || last.Stop != session.StopTime ||
		!strings.Contains(last.Text, "ran out of time") {
		t.Fatalf("last = %+v, want the time reply marked stop time", last)
	}
}

// With no task open, the cap is only a reply: nothing is owed a verdict.
func TestTheStepCapWithoutAnOpenTaskMakesNoClosingCall(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: execs(8)}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "Poke around."})

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls() != 6 {
		t.Errorf("%d model calls, want the 6 steps and no closing call", model.calls())
	}
	if last := lastMessage(t, store); res.Ended != session.Reply || last.Stop != session.StopSteps {
		t.Fatalf("ended %q with %+v, want the reply marked stop steps", res.Ended, last)
	}
}

// A closing inconclusive about a screen someone else holds is the question asking for it (issue #97).
func TestAClosingInconclusiveWhileTheScreenIsTakenIsAQuestion(t *testing.T) {
	mgr, runID, _ := ready(t)
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	replies := append(execs(5), toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "A human is driving."}))
	model := &scriptedModel{replies: replies}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click it.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Question || store.Verdict().Status != session.None {
		t.Fatalf("ended %q with verdict %+v, want the screen question and no verdict", res.Ended, store.Verdict())
	}
	if last := lastMessage(t, store); !strings.Contains(last.Text, "screen") {
		t.Errorf("question = %q, want it to ask for the screen", last.Text)
	}
}
