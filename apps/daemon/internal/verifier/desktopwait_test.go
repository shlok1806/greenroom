package verifier

import (
	"context"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

const expectPassedJSON = `{"passed":true,"observed":"$48.00","elapsedMs":120,"node":{"ref":"e62","role":"StaticText","name":"Total","value":"$48.00"}}`

// An expectation after an action is a check's evidence; so is a wait.
func TestAnExpectationAndAWaitAreEvidence(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "press", verifierPressJSON)
	can(t, control, "expect", expectPassedJSON)
	can(t, control, "waitFor", `{"satisfied":true,"elapsedMs":800,"node":{"ref":"e62","role":"StaticText","name":"Total","value":"$48.00"},"value":"$48.00"}`)
	first := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("total", "waited"),
		toolCall("machine_press", map[string]any{"ref": "e4"}),
		toolCall("machine_wait_for", map[string]any{"target": "e62", "state": "value", "value": map[string]any{"op": "equals", "expected": "$48.00"}}),
		toolCall("machine_expect", map[string]any{"target": "e62", "property": "value", "expected": "$48.00"}),
		verdictOf("pass", "The total shows $48.00 (step 3).",
			answer("total", "pass", []int{first + 2}, first), answer("waited", "pass", []int{first + 1}, first)),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Press Add and check the total.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if !strings.Contains(progress[3].Text, `value equals "$48.00": passed`) || !strings.Contains(progress[2].Text, "matched after 0.8 s") {
		t.Errorf("progress:\n%s\n%s", progress[2].Text, progress[3].Text)
	}
	if got := lastMessage(t, store); got.Kind != session.Verdict || got.Verdict != "pass" {
		t.Fatalf("got %+v, want the pass posted", got)
	}
}

// A screenshot cropped to a ref with a question goes to the describer with the question, and is
// a visual check's evidence.
func TestACroppedScreenshotAsksTheDescriberTheQuestion(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	writeShot(t, control)
	model := &scriptedModel{vision: "The Tapped button is green.", replies: []string{
		toolCall("machine_screenshot", map[string]any{"ref": "e41", "question": "what color is the Tapped button"}),
		verdictOf("inconclusive", "Looked."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Is the button green?")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if len(progress) == 0 || !strings.Contains(progress[0].Text, "The crop shows:\nThe Tapped button is green.") {
		t.Fatalf("progress %+v", progress)
	}
	if !strings.Contains(model.request(t, 2), "what color is the Tapped button") {
		t.Error("the describer was not asked the question")
	}
	steps, _ := mgr.Steps(runID)
	if s := steps[len(steps)-1]; s.Tool != "machine_screenshot" || s.Error != "" || s.By != "verifier" ||
		s.ScreenshotDescription == nil || s.ScreenshotDescription.Text != model.vision {
		t.Errorf("step %+v", s)
	}
}

func TestTheToolkitPromptSaysHowToGetEvidence(t *testing.T) {
	for _, want := range []string{"Use machine_expect for a check's evidence", "Use machine_wait_for for anything that takes time",
		"Take screenshots only for visual checks, cropped to the element with ref, with a question"} {
		if !strings.Contains(systemPromptFor(true), want) {
			t.Errorf("the toolkit prompt lacks %q", want)
		}
	}
	names := strings.Join(toolNames(toolsFor(true)), ",")
	for _, want := range []string{"machine_wait_for", "machine_expect"} {
		if !strings.Contains(names, want) {
			t.Errorf("%s is not offered", want)
		}
	}
}

func TestManualWaitsAndExpects(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "expect", expectPassedJSON)
	can(t, control, "waitFor", `{"satisfied":true,"elapsedMs":800,"node":{"ref":"e62","role":"StaticText","name":"Total"}}`)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "waitfor e62\nexpect e62 value $48.00"})
	if _, err := NewManual(mgr, testLog()).Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 2 || !strings.Contains(progress[0].Text, "appeared after 0.8 s") ||
		!strings.Contains(progress[1].Text, `value equals "$48.00": passed`) {
		t.Fatalf("progress %+v", progress)
	}
}
