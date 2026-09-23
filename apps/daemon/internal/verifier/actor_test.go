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

// Issue #33: after the machine is destroyed the actor is gone, so a task was accepted and nothing
// ever answered it, and agent_wait looped forever. Whatever would start a turn now gets an event
// saying nothing will answer; a note that starts none gets nothing.
func TestAMessageOnADestroyedRunIsToldNothingWillAnswer(t *testing.T) {
	mgr, runID, _ := ready(t)
	v := newVerifier(t, mgr, (&scriptedModel{}).start(t))
	reg := session.NewRegistry(mgr.Root, 2)
	actors := NewActors(v, mgr, reg)
	if err := mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if actors.Running(runID) {
		t.Fatal("the actor outlived its machine")
	}
	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	post(t, store, session.Message{From: session.Coder, Kind: session.Note, Text: "for the record"})
	postTask(t, store, "run echo hi")
	deadline := time.Now().Add(5 * time.Second)
	for !eventSaying(store, "nothing will answer") {
		if time.Now().After(deadline) {
			t.Fatalf("a task on a destroyed run got no answer at all; transcript: %+v", store.After(0))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(messagesOfKind(store, session.Event)); n != 1 {
		t.Errorf("%d events, want one, for the task and not the note; transcript: %+v", n, store.After(0))
	}
	last := lastMessage(t, store)
	if last.From != session.System || !strings.Contains(last.Text, "destroyed") {
		t.Errorf("last message = %+v, want the system saying the machine was destroyed", last)
	}
}

// blockedBrain takes a turn that only ends when the actor is stopped.
type blockedBrain struct{ started chan struct{} }

func (b blockedBrain) Turn(ctx context.Context, _ string, _ *session.Store) (TurnResult, error) {
	close(b.started)
	<-ctx.Done()
	return TurnResult{}, ctx.Err()
}

// Issue #33 again: a turn cut short by the destroy posted nothing, so agent_wait looped forever.
func TestATurnCutShortByDestroyIsToldNothingWillAnswer(t *testing.T) {
	mgr, runID, _ := ready(t)
	brain := blockedBrain{started: make(chan struct{})}
	reg := session.NewRegistry(mgr.Root, 2)
	NewActors(brain, mgr, reg)
	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	postTask(t, store, "run echo hi")
	select {
	case <-brain.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never started")
	}
	if err := mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	var notices []string
	for _, m := range messagesOfKind(store, session.Event) {
		if m.From == session.System && strings.Contains(m.Text, "nothing will answer") {
			notices = append(notices, m.Text)
		}
	}
	want := "this run's machine was destroyed, so the verifier has stopped and nothing will answer this task"
	if len(notices) != 1 || notices[0] != want {
		t.Errorf("notices = %q, want one %q; transcript: %+v", notices, want, store.After(0))
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
	// Issue #45: "send another message" led a coder to send a note, which never starts a turn, and
	// wait forever. The event names what does.
	if !eventSaying(store, "send a task, answer or dispute to try again") || !eventSaying(store, "a coding agent's note does not start a turn") {
		t.Errorf("the give-up event does not say which messages restart the verifier; transcript: %+v", store.After(0))
	}
	// It gave up on the turn, not on the run: the next message still works.
	if !actors.Running(runID) {
		t.Error("the actor died with the turn")
	}
}
