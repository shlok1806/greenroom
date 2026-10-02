package verifier

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// promptScreen is the screen issue #283 measured on a real guest: the Apple Events prompt for an
// app under test, drawn by UserNotificationCenter at layer 8.
const (
	promptScreen = `{"windows":[{"owner":"UserNotificationCenter","name":"","layer":8,"alpha":1,"x":382,"y":118,"width":260,"height":256,"pid":1132}],
 "apps":[{"name":"Finder","bundleId":"com.apple.finder","pid":391}]}`
	promptText = "1132\t“tart-guest-agent” wants access to control “TestApp”. Allowing control will provide access " +
		"to documents and data in “TestApp”, and to perform actions within that app. \n"
)

// Issue #283: an app raises a permission prompt while the verifier's machine_exec runs. The call
// comes back early, stopped, with the prompt's text and what to do; the verifier then approves
// the app (ADR 0044) and its command runs.
func TestAPromptStopsTheVerifiersCommandAndTheApprovalProceeds(t *testing.T) {
	mgr, runID, control := ready(t, machine.WithPromptLook(100*time.Millisecond))
	writeControlFile(t, control, "exec-sleep", "30")
	writeControlFile(t, control, "desktop.json", promptScreen)
	writeControlFile(t, control, "prompt-text", promptText)
	osascript := `osascript -e 'tell application id "com.example.testapp" to count windows'`
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": osascript}),
		toolCall("machine_approve_control", map[string]any{"app": "work/TestApp.app"}),
		toolCall("machine_exec", map[string]any{"command": osascript}),
		toolCall("reply", map[string]any{"text": "TestApp answered after the approval."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "count TestApp's windows with osascript"})

	// Once the first command is stopped the prompt has gone (timed out), so the command after the
	// approval runs on a clean screen and ends by itself.
	go func() {
		for i := 0; i < 4000; i++ {
			if calls, _ := os.ReadFile(filepath.Join(control, "calls.log")); strings.Contains(string(calls), "greenroom-exec-stop") {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		_ = os.Remove(filepath.Join(control, "desktop.json"))
		_ = os.Remove(filepath.Join(control, "exec-sleep"))
	}()
	started := time.Now()
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if took := time.Since(started); took > 20*time.Second {
		t.Errorf("the turn took %s; the blocked command should come back at the first look", took)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 3 {
		t.Fatalf("%d progress messages, want the stopped exec, the approval and the exec after it", len(prog))
	}
	for _, want := range []string{"stopped:", "UserNotificationCenter", "wants access to control “TestApp”",
		"machine_approve_control", "sleep "} {
		if !strings.Contains(prog[0].Text, want) {
			t.Errorf("the stopped command's result lacks %q:\n%s", want, prog[0].Text)
		}
	}
	if !strings.Contains(prog[1].Text, "kTCCServiceAppleEvents") {
		t.Errorf("the approval did not run after the stop:\n%s", prog[1].Text)
	}
	if strings.Contains(prog[2].Text, "stopped:") || !strings.Contains(prog[2].Text, "exit code 0") {
		t.Errorf("the command after the approval did not run to its end:\n%s", prog[2].Text)
	}
	if req := model.request(t, 2); !strings.Contains(req, "stopped: greenroom ended this command") {
		t.Errorf("the model never read the stop: %s", truncateFor(req))
	}
}

func writeControlFile(t *testing.T, control, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPromptWaitCoversThePromptsLifetime(t *testing.T) {
	for _, c := range []struct {
		seconds float64
		want    int
	}{{15.4, 115}, {0, 130}, {200, 10}} {
		if got := promptWait(c.seconds); got != c.want {
			t.Errorf("promptWait(%v) = %d, want %d", c.seconds, got, c.want)
		}
	}
}

func TestTheSystemPromptSendsTheVerifierToMachineApproveControl(t *testing.T) {
	for _, want := range []string{"machine_approve_control", "TCC.db"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
}
