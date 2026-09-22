package verifier

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// execExitSeq queues one exit code per "run" via the fake tart's exec-codes.
func execExitSeq(t *testing.T, control string, codes string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, "exec-codes"), []byte(codes), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManualRunsCommandsAndSummarizesExitCodes(t *testing.T) {
	mgr, runID, control := ready(t)
	execExitSeq(t, control, "0\n3\n")
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "run echo hi\nrun exit 3"})

	m := NewManual(mgr, testLog())
	res, err := m.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Reply {
		t.Errorf("Ended = %q, want reply", res.Ended)
	}

	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 2 {
		t.Fatalf("got %d progress messages, want 2: %+v", len(progress), progress)
	}
	for _, p := range progress {
		if p.Step <= 0 {
			t.Errorf("progress step = %d, want > 0: %+v", p.Step, p)
		}
	}

	last := lastMessage(t, store)
	if last.Kind != session.Reply {
		t.Fatalf("last message = %+v, want a reply", last)
	}
	if !strings.Contains(last.Text, "0") || !strings.Contains(last.Text, "3") {
		t.Errorf("reply %q does not mention both exit codes", last.Text)
	}
}

func TestManualScreenshotProducesAProgressWithAPath(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "screenshot"})

	m := NewManual(mgr, testLog())
	res, err := m.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Reply {
		t.Errorf("Ended = %q, want reply", res.Ended)
	}

	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 1 {
		t.Fatalf("got %d progress messages, want 1: %+v", len(progress), progress)
	}
	if !strings.Contains(progress[0].Text, ".png") {
		t.Errorf("progress %q does not name a png path", progress[0].Text)
	}
}

func TestManualVerdictCitesTheStepsItRan(t *testing.T) {
	mgr, runID, control := ready(t)
	execExitSeq(t, control, "0")
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "run echo hi\nverdict pass it works"})

	m := NewManual(mgr, testLog())
	res, err := m.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Verdict {
		t.Errorf("Ended = %q, want verdict", res.Ended)
	}

	last := lastMessage(t, store)
	if last.Kind != session.Verdict || last.Verdict != "pass" {
		t.Fatalf("last message = %+v, want a pass verdict", last)
	}
	if last.Text != "it works" {
		t.Errorf("verdict summary = %q, want %q", last.Text, "it works")
	}
	if len(last.Evidence) == 0 {
		t.Errorf("verdict %+v has no evidence although a step ran", last)
	}
}

func TestManualClicksAndRecordsOneStep(t *testing.T) {
	mgr, runID, control := ready(t)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "click 0.25 0.5\nverdict pass clicked it"})

	m := NewManual(mgr, testLog())
	res, err := m.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Verdict {
		t.Errorf("Ended = %q, want verdict", res.Ended)
	}

	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 1 {
		t.Fatalf("got %d progress messages, want 1: %+v", len(progress), progress)
	}
	if !strings.HasPrefix(progress[0].Text, "machine_click") || progress[0].Step <= 0 {
		t.Errorf("progress = %+v, want a machine_click step", progress[0])
	}
	if !strings.Contains(testsupport.Calls(t, control), "greenroom-input") {
		t.Error("the click never reached the guest")
	}

	if _, held := mgr.ControlState(runID); held {
		t.Error("the lease is still held after the turn; a human could not take the screen")
	}
}

func TestManualTypeKeyAndScroll(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: strings.Join([]string{
		"type hello world",
		"key a cmd shift",
		"scroll 0 -120",
		"verdict pass typed, pressed and scrolled",
	}, "\n")})

	m := NewManual(mgr, testLog())
	res, err := m.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Verdict {
		t.Errorf("Ended = %q, want verdict", res.Ended)
	}

	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 3 {
		t.Fatalf("got %d progress messages, want 3: %+v", len(progress), progress)
	}
	wantPrefixes := []string{"machine_type", "machine_key", "machine_scroll"}
	for i, want := range wantPrefixes {
		if !strings.HasPrefix(progress[i].Text, want) || progress[i].Step <= 0 {
			t.Errorf("progress[%d] = %+v, want a %s step", i, progress[i], want)
		}
	}
	if !strings.Contains(progress[1].Text, "cmd+shift+a") {
		t.Errorf("key progress %q does not name the modifiers", progress[1].Text)
	}

	last := lastMessage(t, store)
	if last.Kind != session.Verdict || len(last.Evidence) != 3 {
		t.Fatalf("verdict = %+v, want evidence for all three steps", last)
	}
}

// Like the model brain, an empty type is refused before it reaches the machine.
func TestManualTypeNeedsText(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "type\nverdict fail nothing typed"})

	if _, err := NewManual(mgr, testLog()).Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 1 || progress[0].Step != 0 || !strings.Contains(progress[0].Text, "machine_type needs text") {
		t.Fatalf("progress = %+v, want one unrecorded refusal", progress)
	}
	if last := lastMessage(t, store); len(last.Evidence) != 0 {
		t.Errorf("verdict cites %v, want no steps", last.Evidence)
	}
}

func TestSummarizeRunsKeepsWholeRunes(t *testing.T) {
	for _, out := range []string{strings.Repeat("é", 150), "x" + strings.Repeat("é", 150)} {
		got := summarizeRuns(1, []int{0}, out)
		if !utf8.ValidString(got) {
			t.Errorf("summarizeRuns split a rune: %q", got)
		}
		if !strings.HasSuffix(got, "éé") {
			t.Errorf("summarizeRuns lost the tail: %q", got)
		}
	}
}

func TestManualComputerUseIsRefusedWhileAHumanHoldsTheScreen(t *testing.T) {
	mgr, runID, control := ready(t)
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "click 0.5 0.5\nverdict fail could not click"})

	m := NewManual(mgr, testLog())
	if _, err := m.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 1 || !strings.Contains(progress[0].Text, "human") {
		t.Fatalf("progress = %+v, want an error naming the human", progress)
	}
	if strings.Contains(testsupport.Calls(t, control), "greenroom-input") {
		t.Error("a batch reached the guest while a human held the screen")
	}
	if c, held := mgr.ControlState(runID); !held || c.Holder != "human" {
		t.Errorf("control = %+v, want the human to still hold it", c)
	}
}

func TestManualAsk(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "ask which scheme?"})

	m := NewManual(mgr, testLog())
	res, err := m.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Question {
		t.Errorf("Ended = %q, want question", res.Ended)
	}

	last := lastMessage(t, store)
	if last.Kind != session.Question || last.Text != "which scheme?" {
		t.Fatalf("last message = %+v, want the question", last)
	}
}

func TestManualHelp(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "help"})

	m := NewManual(mgr, testLog())
	res, err := m.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Reply {
		t.Errorf("Ended = %q, want reply", res.Ended)
	}

	last := lastMessage(t, store)
	if last.Kind != session.Reply || !strings.Contains(last.Text, "run <") {
		t.Fatalf("last message = %+v, want a reply describing the grammar", last)
	}
}

func TestManualUnrecognisedInstructionAlsoGetsHelp(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "frobnicate the widget"})

	m := NewManual(mgr, testLog())
	if _, err := m.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	last := lastMessage(t, store)
	if last.Kind != session.Reply || !strings.Contains(last.Text, "run <") {
		t.Fatalf("last message = %+v, want the help reply", last)
	}
}

// Each turn reads only the last turn-starting message.
func TestManualTurnsAreIndependent(t *testing.T) {
	mgr, runID, control := ready(t)
	execExitSeq(t, control, "0")
	store := openStore(t, mgr, runID)
	m := NewManual(mgr, testLog())

	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "run echo hi"})
	if _, err := m.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("first Turn: %v", err)
	}
	first := lastMessage(t, store)
	if first.Kind != session.Reply || !strings.Contains(first.Text, "ran 1 commands") {
		t.Fatalf("first turn reply = %+v, want a summary of one run", first)
	}

	post(t, store, session.Message{From: session.Coder, Kind: session.Task, Text: "help"})
	if _, err := m.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("second Turn: %v", err)
	}
	second := lastMessage(t, store)
	if second.Kind != session.Reply || second.Text != manualHelp {
		t.Fatalf("second turn reply = %+v, want the exact help text, unaffected by the first turn", second)
	}
}

func TestActorAnswersAHumanNoteWithManual(t *testing.T) {
	mgr, runID, _ := ready(t)
	reg := session.NewRegistry(mgr.Root, 2)
	actors := NewActors(NewManual(mgr, testLog()), mgr, reg)
	t.Cleanup(func() { actors.Stop(runID) })

	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "ask what should i test?"})

	question := waitForKind(t, store, session.Question, 5*time.Second)
	if question.From != session.Verifier || question.Text != "what should i test?" {
		t.Errorf("question = %+v, want the verifier asking back what was asked", question)
	}
}
