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

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// execExitSeq queues exit codes for the fake tart's exec-codes control file:
// the first "run" instruction in a turn gets the first code, the second gets
// the second, and so on.
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

// Each turn reads only the last turn-starting message: nothing from an
// earlier turn leaks into the next one.
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
