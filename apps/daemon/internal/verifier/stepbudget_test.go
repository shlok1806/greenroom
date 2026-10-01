package verifier

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

func budgetChecks(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("check-%d", i+1)
	}
	return ids
}

func TestMultiCheckTaskGetsRoomToFinishUnlessExplicitlyCapped(t *testing.T) {
	for _, cap := range []int{0, 40} {
		t.Run(fmt.Sprintf("configured-%d", cap), func(t *testing.T) {
			mgr, runID, control := ready(t)
			putUI(t, control, groceriesUI)
			ids := budgetChecks(10)
			replies := []string{declared(ids...)}
			var answers []map[string]any
			first := lastStep(t, mgr, runID) + 1
			for i, id := range ids {
				replies = append(replies, toolCall("machine_ui", map[string]any{}), toolCall("machine_key", map[string]any{"key": "tab"}), toolCall("machine_ui", map[string]any{}), toolCall("machine_key", map[string]any{"key": "tab"}), toolCall("machine_ui", map[string]any{}))
				base := first + i*7 // keys each record an input and its effect read
				answers = append(answers, answer(id, "pass", []int{base + 6}, base+1, base+4))
			}
			replies = append(replies, verdictOf("pass", "All ten results were observed.", answers...))
			if cap > 0 {
				replies = append(replies[:cap], verdictOf("inconclusive", "The last checks were not reached."))
			}
			model := &scriptedModel{replies: replies}
			v, err := New(mgr, Config{BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner", MaxSteps: cap}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			store := openStore(t, mgr, runID)
			postTask(t, store, "Check all ten UI results.")
			res, err := v.Turn(context.Background(), runID, store)
			if err != nil {
				t.Fatal(err)
			}
			got := store.Verdict()
			if cap == 0 {
				if res.Steps != 52 || model.calls() != 52 || got.Verdict != "pass" || len(got.Checks) != 10 {
					t.Fatalf("res=%+v calls=%d verdict=%+v", res, model.calls(), got)
				}
				for _, c := range got.Checks {
					if c.Status != session.CheckPass {
						t.Errorf("check=%+v", c)
					}
				}
			} else {
				if res.Steps != 40 || model.calls() != 41 || got.Verdict != "inconclusive" {
					t.Fatalf("explicit cap res=%+v calls=%d verdict=%+v", res, model.calls(), got)
				}
				if got := strings.Join(model.offered(t, 41), ","); got != "report_verdict,ask" {
					t.Errorf("closing tools=%s", got)
				}
			}
			if v.cfg.MaxSteps != DefaultMaxSteps {
				t.Errorf("shared cap changed=%d", v.cfg.MaxSteps)
			}
		})
	}
}

func TestChecklistBudgetIsBoundedAndReportsExpandedLimit(t *testing.T) {
	mgr, runID, _ := ready(t)
	replies := make([]string, maxChecklistSteps+1)
	for i := range replies {
		replies[i] = declared(budgetChecks(12)...)
	}
	model := &scriptedModel{replies: replies}
	v, err := New(mgr, Config{BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check twelve outcomes.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	last := lastMessage(t, store)
	if res.Steps != 108 || model.calls() != 109 || last.Stop != session.StopSteps || !strings.Contains(last.Text, "used all 108") {
		t.Fatalf("res=%+v calls=%d last=%+v", res, model.calls(), last)
	}
}

func TestChecklistBudgetUsesOnlyCurrentOpenTask(t *testing.T) {
	v := &Verifier{cfg: Config{MaxSteps: 40}, checklistSteps: true}
	msgs := []session.Message{{From: session.Coder, Kind: session.Task}, {From: session.Verifier, Kind: session.Progress, Checks: make([]session.Check, 12)}}
	if got := v.stepLimit(msgs); got != 108 {
		t.Errorf("resumed cap=%d", got)
	}
	msgs = append(msgs, session.Message{From: session.Coder, Kind: session.Task})
	if got := v.stepLimit(msgs); got != 40 {
		t.Errorf("new task inherited cap=%d", got)
	}
	msgs = append(msgs, msgs[1], session.Message{From: session.Verifier, Kind: session.Verdict})
	if got := v.stepLimit(msgs); got != 40 {
		t.Errorf("closed task cap=%d", got)
	}
	v.checklistSteps = false
	v.cfg.MaxSteps = 6
	if got := v.stepLimit(msgs[:2]); got != 6 {
		t.Errorf("explicit cap=%d", got)
	}
	v.checklistSteps = true
	v.cfg.MaxSteps = 40
	msgs[1].Checks = make([]session.Check, 1000)
	if got := v.stepLimit(msgs[:2]); got != 108 {
		t.Errorf("malformed transcript cap=%d", got)
	}
}

func TestInvalidDeclarationDoesNotExtendStepBudget(t *testing.T) {
	mgr, runID, _ := ready(t)
	replies := append(execs(39), declared(budgetChecks(13)...))
	replies = append(replies, prose("Could not finish."))
	model := &scriptedModel{replies: replies}
	v, err := New(mgr, Config{BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check results.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps != 40 || model.calls() != 41 || lastMessage(t, store).Stop != session.StopSteps {
		t.Fatalf("res=%+v calls=%d", res, model.calls())
	}
	if checks := declaredChecks(store.After(0)); len(checks) != 0 {
		t.Errorf("invalid checks accepted=%+v", checks)
	}
}

func TestExpandedStepBudgetStillStopsAtTimeBudget(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{prose("Too late."), verdictOf("inconclusive", "Time ran out.")}}
	model.onReasoning = func(n int) {
		if n == 1 {
			time.Sleep(600 * time.Millisecond)
		}
	}
	v, err := New(mgr, Config{BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner", Budget: 200 * time.Millisecond}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Finish twelve checks.")
	checks := make([]session.Check, 12)
	for i, id := range budgetChecks(12) {
		checks[i] = session.Check{ID: id, Criterion: "Result is correct"}
	}
	post(t, store, session.Message{From: session.Verifier, Kind: session.Progress, Checks: checks})
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls() != 2 || res.Ended != session.Verdict || !strings.Contains(model.request(t, 2), "out of time") {
		t.Fatalf("res=%+v calls=%d", res, model.calls())
	}
}
