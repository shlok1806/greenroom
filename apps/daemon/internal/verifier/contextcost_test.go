package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

func outlineTurn(step int, name, args, body string) []nim.Message {
	return toolTurn(fmt.Sprintf("outline%d", step), name, args, fmt.Sprintf("step %d\n%s", step, body))
}

func TestContextSharesOnlyIdenticalObservations(t *testing.T) {
	body := strings.Repeat("[1] StaticText value=Doing (1), drawn at (0.4, 0.5)\n", 60)
	msgs := []nim.Message{{Role: "system", Content: "Keep all evidence."}}
	for i := 1; i <= 30; i++ {
		msgs = append(msgs, outlineTurn(i, "machine_ui", "{}", body)...)
	}
	// A transient state is still needed even though it was superseded.
	msgs = append(msgs, outlineTurn(31, "machine_ui", "{}", strings.ReplaceAll(body, "Doing (1)", "Doing (0)"))...)
	before, _ := json.Marshal(msgs)
	out := compactContext(msgs)
	// Measure the actual wire request, including tools, rather than Go's message struct.
	model := &scriptedModel{replies: []string{prose("done"), prose("done")}}
	client := nim.New(model.start(t), "k")
	for _, history := range [][]nim.Message{msgs, out} {
		if _, _, err := client.Chat(context.Background(), "reasoner", history, tools); err != nil {
			t.Fatal(err)
		}
	}
	wireBefore, wireAfter := model.request(t, 1), model.request(t, 2)
	if len(wireAfter)*10 >= len(wireBefore)*3 {
		t.Fatalf("repeated-outline request bytes = %d -> %d; want at least 70%% saving", len(wireBefore), len(wireAfter))
	}
	t.Logf("Repeated-outline fixture: %d -> %d request bytes (%.1f%% reduction)", len(wireBefore), len(wireAfter), 100*(1-float64(len(wireAfter))/float64(len(wireBefore))))
	if !strings.Contains(out[2].Content, "step 1\n[Identical machine_ui") || !strings.Contains(out[2].Content, "step 30") {
		t.Fatalf("earlier observation lost original step or complete copy: %s", out[2].Content)
	}
	if out[len(out)-3].Content != fmt.Sprintf("step 30\n%s", body) || !strings.Contains(out[len(out)-1].Content, "Doing (0)") {
		t.Fatal("latest identical outline or distinct transient state was dropped")
	}
	unchanged, _ := json.Marshal(msgs)
	if string(before) != string(unchanged) {
		t.Fatal("request compaction mutated the live history")
	}
	if !reflect.DeepEqual(compactContext(out), out) {
		t.Fatal("compaction must be idempotent")
	}
}

func TestContextRetainsDifferencesAndToolProtocol(t *testing.T) {
	body := strings.Repeat("[1] Button Add enabled center=(0.4, 0.5)\n", 70)
	for _, tc := range []struct{ name, tool, args, result string }{
		{"arguments", "machine_ui", `{"app":"Other"}`, body},
		{"refs", "machine_ui", "{}", strings.ReplaceAll(body, "[1]", "[2]")},
		{"covered", "machine_ui", "{}", body + "[covered]"},
		{"effect", "machine_ui", "{}", body + "effect: changed after step 2"},
		{"snapshot vs UI", "machine_snapshot", "{}", body},
		{"screenshot", "machine_screenshot", "{}", body},
		{"command", "machine_exec", "{}", body},
		{"malformed header", "machine_ui", "{}", body},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := outlineTurn(1, "machine_ui", "{}", body)
			msgs = append(msgs, outlineTurn(2, tc.tool, tc.args, tc.result)...)
			if tc.name == "malformed header" {
				msgs[3].Content = "step 02\n" + body
			}
			if got := compactContext(msgs); !reflect.DeepEqual(got, msgs) {
				t.Fatalf("distinct or ineligible observation changed: %+v", got)
			}
		})
	}
}

// Tool-call ids can be reused by the endpoint in later assistant messages.
func TestCompactionMatchesThePrecedingCallWhenIDsRepeat(t *testing.T) {
	body := strings.Repeat("The same output can come from different tools.\n", 70)
	msgs := toolTurn("reused", "machine_exec", "{}", "step 1\n"+body)
	msgs = append(msgs, toolTurn("reused", "machine_ui", "{}", "step 2\n"+body)...)
	if got := compactContext(msgs); !reflect.DeepEqual(got, msgs) {
		t.Fatal("a repeated call id made an exec output look like a UI observation")
	}
}

func TestNewTaskOmitsOnlySupersededLargeResults(t *testing.T) {
	body := strings.Repeat("Old observation still stored in transcript.\n", 100)
	msgs := []session.Message{
		{Seq: 1, From: session.Coder, Kind: session.Task, Text: "Old task."},
		{Seq: 2, From: session.Verifier, Kind: session.Progress, Step: 4, Text: "machine_ui {}\nstep 4\n" + body},
		{Seq: 3, From: session.Verifier, Kind: session.Question, Text: "Which scheme?"},
		{Seq: 4, From: session.Human, Kind: session.Answer, Text: "Debug."},
		{Seq: 5, From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "Old failure.", Checks: []session.Check{{ID: "old", Status: "fail", Evidence: []int{4}}}},
		{Seq: 6, From: session.Human, Kind: session.Dispute, Text: "Investigate."},
		{Seq: 7, From: session.System, Kind: session.Event, Control: session.ControlReturned, Text: "Screen returned."},
		{Seq: 8, From: session.Coder, Kind: session.Task, Text: "New task."},
		{Seq: 9, From: session.Verifier, Kind: session.Progress, Step: 5, Text: "machine_ui {}\nstep 5\n" + body},
	}
	before, _ := json.Marshal(msgs)
	out := project(msgs)
	if !strings.Contains(out[3].Content, "Earlier task's large tool output omitted") || !strings.HasPrefix(out[3].Content, "step 4\n") {
		t.Fatalf("superseded observation not clearly marked: %+v", out[3])
	}
	if !strings.Contains(out[len(out)-1].Content, body) {
		t.Fatal("current-task observation was omitted")
	}
	encoded, _ := json.Marshal(out)
	for _, want := range []string{"Old task.", "New task.", "Which scheme?", "Debug.", "Old failure.", "Investigate.", returnedAdvice, claimLabel, "report_verdict", "machine_ui"} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("decision/control/tool protocol lost %q", want)
		}
	}
	// With no newer task, a question/answer/dispute must retain its evidence.
	if got, _ := json.Marshal(project(msgs[:7])); !strings.Contains(string(got), "Old observation still stored") {
		t.Fatal("same-task evidence disappeared on a continuation")
	}
	after, _ := json.Marshal(msgs)
	if string(before) != string(after) {
		t.Fatal("projection changed the stored transcript")
	}
}

func TestNormalAndClosingRequestsCompactCopies(t *testing.T) {
	for _, closing := range []bool{false, true} {
		t.Run(fmt.Sprintf("closing=%t", closing), func(t *testing.T) {
			mgr, runID, control := ready(t)
			var elements []map[string]any
			for i := 0; i < 40; i++ {
				elements = append(elements, map[string]any{"role": "AXButton", "label": fmt.Sprintf("Button %d with a detailed and unchanged label", i), "frame": map[string]int{"x": 10, "y": 10 + i*15, "w": 200, "h": 15}})
			}
			ui, _ := json.Marshal(map[string]any{"app": map[string]any{"name": "Board", "pid": 7}, "screen": map[string]int{"width": 1024, "height": 768}, "elements": elements})
			putUI(t, control, string(ui))
			model := &scriptedModel{replies: []string{toolCall("machine_ui", map[string]any{}), toolCall("machine_ui", map[string]any{}), verdictOf("inconclusive", "Nothing was checked.")}}
			v := newVerifier(t, mgr, model.start(t))
			if closing {
				v.cfg.MaxSteps = 2
			}
			store := openStore(t, mgr, runID)
			postTask(t, store, "Look twice.")
			if _, err := v.Turn(context.Background(), runID, store); err != nil {
				t.Fatal(err)
			}
			if request := model.request(t, 3); !strings.Contains(request, "Identical machine_ui observation") || !strings.Contains(request, "Button 39") {
				t.Fatalf("third reasoning request must retain latest outline and compact older one: %s", truncateFor(request))
			}
			progress := messagesOfKind(store, session.Progress)
			if len(progress) != 2 {
				t.Fatalf("progress = %d, want both observations", len(progress))
			}
			for _, p := range progress {
				if !strings.Contains(p.Text, "Button 39") || strings.Contains(p.Text, "Identical machine_ui observation") {
					t.Fatal("request-only compaction contaminated recorded progress")
				}
			}
		})
	}
}
