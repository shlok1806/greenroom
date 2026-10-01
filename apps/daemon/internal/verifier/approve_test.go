package verifier

import (
	"context"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// The verifier approves an app its run built, through the same guest script as the coder's
// machine_approve_control, scoped to the guest home and recorded as its own step (ADR 0044,
// issue #269). Before this it had no way to, and hand-wrote TCC rows or stalled on the prompt.
func TestTheVerifierApprovesAnAppItsRunBuilt(t *testing.T) {
	mgr, runID, control := ready(t)
	approve := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		toolCall("machine_approve_control", map[string]any{"app": "work/TestApp.app"}),
		toolCall("reply", map[string]any{"text": "TestApp is approved (step 2)."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "approve the app you built"})

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if req := model.request(t, 1); !strings.Contains(req, `"name":"machine_approve_control"`) {
		t.Errorf("the model was not offered machine_approve_control: %s", truncateFor(req))
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 {
		t.Fatalf("%d progress messages, want the approval", len(prog))
	}
	if prog[0].Step != approve {
		t.Errorf("progress step = %d, want %d", prog[0].Step, approve)
	}
	for _, want := range []string{"machine_approve_control", "com.example.testapp", "kTCCServiceAppleEvents"} {
		if !strings.Contains(prog[0].Text, want) {
			t.Errorf("the approval's result lacks %q:\n%s", want, prog[0].Text)
		}
	}
	if !strings.Contains(testsupport.Calls(t, control), "sh work/TestApp.app home") {
		t.Errorf("the verifier's approval was not scoped to the guest home\n%s", testsupport.Calls(t, control))
	}
	steps, err := mgr.Steps(runID)
	if err != nil {
		t.Fatal(err)
	}
	if s := steps[len(steps)-1]; s.Seq != approve || s.Tool != "machine_approve_control" || s.By != machine.HolderVerifier {
		t.Errorf("last step = %d %s by %q, want %d machine_approve_control by the verifier", s.Seq, s.Tool, s.By, approve)
	}
}

// A refused approval says why, so the model can fix the path or ask, and is not an input:
// it needs no declared checks first.
func TestTheVerifiersApprovalNeedsAnAppAndIsNotAnInput(t *testing.T) {
	mgr, runID, control := ready(t)
	testsupport.Flag(t, control, "fail-tcc-grant")
	model := &scriptedModel{replies: []string{
		toolCall("machine_approve_control", map[string]any{}),
		toolCall("machine_approve_control", map[string]any{"app": "work/TestApp.app"}),
		verdictOf("inconclusive", "The app could not be approved."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Build TestApp and check its window title.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 2 {
		t.Fatalf("%d progress messages, want both approvals", len(prog))
	}
	if !strings.Contains(prog[0].Text, "error: machine_approve_control needs app") {
		t.Errorf("an approval with no app was not refused with the field to send:\n%s", prog[0].Text)
	}
	if strings.Contains(prog[1].Text, declareFirst) || !strings.Contains(prog[1].Text, "did not take") {
		t.Errorf("the approval was refused as an input, or its guest error is missing:\n%s", prog[1].Text)
	}
}

// Writing TCC.db by hand is what the verifier did without the tool (issue #269): the command
// never runs, and the refusal names the call to make instead.
func TestTheVerifierMayNotWriteTCCDatabasesThroughMachineExec(t *testing.T) {
	mgr, runID, control := ready(t)
	command := `sudo sqlite3 "/Library/Application Support/com.apple.TCC/TCC.db" "INSERT INTO access VALUES ('kTCCServiceAppleEvents')"`
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": command}),
		toolCall("reply", map[string]any{"text": "Using machine_approve_control instead."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "let osascript drive the app"})

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 {
		t.Fatalf("%d progress messages, want the refused command", len(prog))
	}
	if prog[0].Step != 0 || !strings.Contains(prog[0].Text, "error:") || !strings.Contains(prog[0].Text, "machine_approve_control") {
		t.Errorf("the TCC.db write was not refused with a pointer to machine_approve_control (step %d):\n%s", prog[0].Step, prog[0].Text)
	}
	if strings.Contains(testsupport.ExecStdin(t, control), "INSERT INTO access") {
		t.Error("the TCC.db write reached the guest")
	}
}

func TestTheSystemPromptSendsTheVerifierToMachineApproveControl(t *testing.T) {
	for _, want := range []string{"machine_approve_control", "TCC.db"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
}
