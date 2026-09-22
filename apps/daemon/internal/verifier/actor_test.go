package verifier

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// noRetryWaits keeps both retry schedules (client and actor) but zeroes them.
func noRetryWaits(t *testing.T) {
	t.Helper()
	backoff, delays := nim.RetryBackoff, TurnRetryDelays
	nim.RetryBackoff = []time.Duration{0, 0, 0, 0}
	TurnRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { nim.RetryBackoff, TurnRetryDelays = backoff, delays })
}

// eventSaying reports whether a system event contains text.
func eventSaying(store *session.Store, text string) bool {
	for _, m := range messagesOfKind(store, session.Event) {
		if m.From == session.System && strings.Contains(m.Text, text) {
			return true
		}
	}
	return false
}

// waitForKind polls the transcript for the first message of kind k.
func waitForKind(t *testing.T, store *session.Store, k session.Kind, within time.Duration) session.Message {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if msgs := messagesOfKind(store, k); len(msgs) > 0 {
			return msgs[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s message after %v; transcript: %+v", k, within, store.After(0))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestActorTakesATurnWhenATaskArrivesAndStopsWithTheMachine(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "swift build"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "The build succeeded."}),
	}}
	v := newVerifier(t, mgr, model.start(t))

	// The machine already exists, so the actor must come from mgr.List().
	reg := session.NewRegistry(mgr.Root, 2)
	actors := NewActors(v, mgr, reg)
	if !actors.Running(runID) {
		t.Fatal("no actor for a machine that existed before the verifier started")
	}

	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	postTask(t, store, "Build the app.")

	verdict := waitForKind(t, store, session.Verdict, 5*time.Second)
	if verdict.From != session.Verifier || verdict.Verdict != "pass" {
		t.Errorf("verdict = %+v, want a pass from the verifier", verdict)
	}

	if err := mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// Stop runs inside the destroyed listener, so it is done by now.
	if actors.Running(runID) {
		t.Error("the actor outlived its machine")
	}
}

// A coder note is context and must not cost a model call.
func TestACoderNoteDoesNotStartATurn(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{}
	v := newVerifier(t, mgr, model.start(t))
	reg := session.NewRegistry(mgr.Root, 2)
	actors := NewActors(v, mgr, reg)
	t.Cleanup(func() { actors.Stop(runID) })

	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	post(t, store, session.Message{From: session.Coder, Kind: session.Note, Text: "the build needs Xcode 16"})

	time.Sleep(300 * time.Millisecond)
	if model.calls() != 0 {
		t.Errorf("the model was called %d times for a coder note, want 0", model.calls())
	}
	for _, m := range store.After(0) {
		if m.From == session.Verifier {
			t.Fatalf("the verifier answered a coder note: %+v", m)
		}
	}
}

func TestLastUnansweredFindsTheTurnStillOwed(t *testing.T) {
	task := session.Message{Seq: 1, From: session.Coder, Kind: session.Task}
	question := session.Message{Seq: 2, From: session.Verifier, Kind: session.Question}
	verdict := session.Message{Seq: 2, From: session.Verifier, Kind: session.Verdict}
	dispute := session.Message{Seq: 3, From: session.Coder, Kind: session.Dispute}
	answer := session.Message{Seq: 3, From: session.Coder, Kind: session.Answer}
	note := session.Message{Seq: 4, From: session.Human, Kind: session.Note}
	reply := session.Message{Seq: 5, From: session.Verifier, Kind: session.Reply}

	cases := []struct {
		name string
		msgs []session.Message
		want int
	}{
		{"empty", nil, 0},
		{"a task nobody answered", []session.Message{task}, 1},
		{"a task the verifier questioned", []session.Message{task, question}, 0},
		{"a verdict under dispute", []session.Message{task, verdict, dispute}, 3},
		{"a question that was answered", []session.Message{task, question, answer}, 3},
		{"a human note nobody answered", []session.Message{task, verdict, note}, 4},
		{"a human note the verifier replied to", []session.Message{task, verdict, note, reply}, 0},
	}
	for _, c := range cases {
		if got := lastUnanswered(c.msgs); got != c.want {
			t.Errorf("%s: lastUnanswered = %d, want %d", c.name, got, c.want)
		}
	}
}

// A turn that fails on a bad minute is retried until the task is answered.
func TestActorRetriesATurnTheEndpointCouldNotServe(t *testing.T) {
	noRetryWaits(t)
	mgr, runID, _ := ready(t)
	// Five failures exhaust the client's schedule; only the actor's retry helps.
	model := &scriptedModel{failures: 5, replies: []string{
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "The build succeeded."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	reg := session.NewRegistry(mgr.Root, 2)
	actors := NewActors(v, mgr, reg)
	t.Cleanup(func() { actors.Stop(runID) })

	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	postTask(t, store, "Build the app.")

	verdict := waitForKind(t, store, session.Verdict, 10*time.Second)
	if verdict.Verdict != "pass" {
		t.Errorf("verdict = %+v, want the pass from the retried turn", verdict)
	}
	if !eventSaying(store, "verifier retrying the turn (attempt 2 of 4)") {
		t.Errorf("the retry was never announced; transcript: %+v", store.After(0))
	}
	if eventSaying(store, "gave up") {
		t.Error("the actor gave up although the retry worked")
	}
	for _, m := range store.After(0) {
		if m.From == session.Human {
			t.Fatal("the turn only succeeded because a human spoke again")
		}
	}
}

// An endpoint down for good ends in a visible "gave up" event.
func TestActorGivesUpAndSaysSo(t *testing.T) {
	noRetryWaits(t)
	mgr, runID, _ := ready(t)
	model := &scriptedModel{failures: 1000}
	v := newVerifier(t, mgr, model.start(t))
	reg := session.NewRegistry(mgr.Root, 2)
	actors := NewActors(v, mgr, reg)
	t.Cleanup(func() { actors.Stop(runID) })

	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	postTask(t, store, "Build the app.")

	deadline := time.Now().Add(10 * time.Second)
	for !eventSaying(store, "verifier gave up on this turn after 4 attempts") {
		if time.Now().After(deadline) {
			t.Fatalf("the actor never said it gave up; transcript: %+v", store.After(0))
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"attempt 2 of 4", "attempt 3 of 4", "attempt 4 of 4"} {
		if !eventSaying(store, want) {
			t.Errorf("no %q event; transcript: %+v", want, store.After(0))
		}
	}
	// It gave up on the turn, not on the run: the next message still works.
	if !actors.Running(runID) {
		t.Error("the actor died with the turn")
	}
}
