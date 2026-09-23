package verifier

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// scriptedModel is a chat endpoint that replays replies and records requests.
type scriptedModel struct {
	mu       sync.Mutex
	replies  []string // raw JSON bodies, one per call
	requests []map[string]any
	vision   string
	visions  int

	// failures is how many requests get a 500 first; they consume no reply.
	failures int
	failed   int

	// onReasoning runs just before the nth (1-based) reasoning reply is
	// served: the one moment a test knows the verifier is mid-step.
	onReasoning func(n int)
}

func (s *scriptedModel) start(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.failed < s.failures {
			s.failed++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"Internal server error"}}`)
			return
		}
		s.requests = append(s.requests, body)

		// An image request is the vision model.
		if isVisionRequest(body) {
			s.visions++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":`+quote(s.vision)+`}}],"usage":{}}`)
			return
		}
		n := len(s.requests) - s.visions - 1
		if n >= len(s.replies) {
			// Not a 500, so the client does not retry the test's own mistake.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"the test ran out of scripted replies"}}`)
			return
		}
		if s.onReasoning != nil {
			s.onReasoning(n + 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, s.replies[n])
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// request returns the nth (1-based) request as JSON text.
func (s *scriptedModel) request(t *testing.T, n int) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 1 || n > len(s.requests) {
		t.Fatalf("the model was called %d times, want at least %d", len(s.requests), n)
	}
	b, _ := json.Marshal(s.requests[n-1])
	return string(b)
}

func (s *scriptedModel) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func isVisionRequest(body map[string]any) bool {
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		parts, ok := mm["content"].([]any)
		if !ok {
			continue
		}
		for _, p := range parts {
			if pm, ok := p.(map[string]any); ok && pm["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// toolCall builds a reply in which the model asks for one function.
func toolCall(name string, args map[string]any) string {
	a, _ := json.Marshal(args)
	return `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":` +
		quote(name) + `,"arguments":` + quote(string(a)) + `}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
}

func prose(text string) string {
	return `{"choices":[{"message":{"role":"assistant","content":` + quote(text) + `}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`
}

// ready brings up a fake machine and returns the manager and its runId.
func ready(t *testing.T) (*machine.Manager, string, string) {
	t.Helper()
	bin, control := testsupport.FakeTart(t)
	mgr, err := machine.NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		machine.WithTartBin(bin), machine.WithReadyTimeout(10*time.Second), machine.WithFrameInterval(0),
		machine.WithSSHProbe(func(context.Context, string, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	mc, err := mgr.Create(context.Background(), "img", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 20*time.Second)
	if err != nil || got.Status != machine.Ready {
		t.Fatalf("machine not ready: %+v %v", got, err)
	}
	return mgr, mc.RunID, control
}

// failed brings up a machine whose tart run exits before the guest is up.
func failed(t *testing.T) (*machine.Manager, string) {
	t.Helper()
	bin, control := testsupport.FakeTart(t)
	testsupport.Flag(t, control, "fail-run")
	mgr, err := machine.NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		machine.WithTartBin(bin), machine.WithReadyTimeout(2*time.Second),
		machine.WithFrameInterval(0),
		machine.WithSSHProbe(func(context.Context, string, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	// Wait returns before the failed VM is stopped and deleted; "failed" is
	// emitted after, so waiting for it keeps that cleanup from racing TempDir.
	done := make(chan struct{})
	var once sync.Once
	stop := mgr.Listen(func(ev machine.LifecycleEvent) {
		if ev.Kind == "failed" {
			once.Do(func() { close(done) })
		}
	})
	t.Cleanup(stop)

	mc, err := mgr.Create(context.Background(), "img", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 20*time.Second)
	if err != nil || got.Status != machine.Failed {
		t.Fatalf("machine did not fail: %+v %v", got, err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the manager never finished cleaning up the failed machine")
	}
	return mgr, mc.RunID
}

func newVerifier(t *testing.T, mgr *machine.Manager, url string) *Verifier {
	t.Helper()
	v, err := New(mgr, Config{
		BaseURL: url, APIKey: "test-key", Model: "reasoner", VisionModel: "eyes",
		MaxSteps: 6, Budget: 30 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// openStore opens a run's conversation the way the daemon does.
func openStore(t *testing.T, mgr *machine.Manager, runID string) *session.Store {
	t.Helper()
	store, err := session.Open(mgr.RunDir(runID), 2)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func postTask(t *testing.T, store *session.Store, text string) session.Message {
	t.Helper()
	return post(t, store, session.Message{From: session.Coder, Kind: session.Task, Text: text})
}

func post(t *testing.T, store *session.Store, m session.Message) session.Message {
	t.Helper()
	got, err := store.Append(m)
	if err != nil {
		t.Fatalf("append %s: %v", m.Kind, err)
	}
	return got
}

func messagesOfKind(store *session.Store, k session.Kind) []session.Message {
	var out []session.Message
	for _, m := range store.After(0) {
		if m.Kind == k {
			out = append(out, m)
		}
	}
	return out
}

func lastMessage(t *testing.T, store *session.Store) session.Message {
	t.Helper()
	all := store.After(0)
	if len(all) == 0 {
		t.Fatal("the conversation is empty")
	}
	return all[len(all)-1]
}

func TestNewRequiresAKeyAndAModel(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(nil, Config{Model: "m"}, log); err == nil {
		t.Error("New accepted an empty API key")
	}
	if _, err := New(nil, Config{APIKey: "k"}, log); err == nil {
		t.Error("New accepted an empty model")
	}
}

func TestTurnRunsACommandThenPostsAVerdict(t *testing.T) {
	mgr, runID, control := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "swift build"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "The build succeeded and the app launched."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Build the app and say whether it works.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Verdict {
		t.Errorf("ended = %q, want %q", res.Ended, session.Verdict)
	}
	if res.Tokens == 0 {
		t.Error("the turn counts no tokens")
	}

	last := lastMessage(t, store)
	if last.Kind != session.Verdict || last.From != session.Verifier {
		t.Fatalf("the transcript ends with a %s from %s, want a verdict from the verifier", last.Kind, last.From)
	}
	if last.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass", last.Verdict)
	}
	if !strings.Contains(last.Text, "build succeeded") {
		t.Errorf("summary = %q", last.Text)
	}
	if got := store.Verdict(); got.Verdict != "pass" || got.Status != session.Proposed {
		t.Errorf("verdict state = %+v, want a proposed pass", got)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 {
		t.Fatalf("%d progress messages, want 1", len(prog))
	}
	if prog[0].Step <= 0 {
		t.Errorf("progress step = %d, want the step it recorded", prog[0].Step)
	}
	if !strings.HasPrefix(prog[0].Text, "machine_exec") {
		t.Errorf("progress text = %q, want it to name the tool", truncateFor(prog[0].Text))
	}

	if !strings.Contains(testsupport.Calls(t, control), "swift build") {
		t.Error("the command never reached the machine")
	}
	if second := model.request(t, 2); !strings.Contains(second, "exit code") {
		t.Errorf("the tool result never reached the model: %s", truncateFor(second))
	}
}

func TestTurnClicksAtAFractionAndRecordsOneStep(t *testing.T) {
	mgr, runID, control := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_click", map[string]any{"x": 0.25, "y": 0.5}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Clicked the button."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click the button at a quarter across, halfway down.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Verdict {
		t.Errorf("Ended = %q, want verdict", res.Ended)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 {
		t.Fatalf("%d progress messages, want 1", len(prog))
	}
	if !strings.HasPrefix(prog[0].Text, "machine_click") || prog[0].Step <= 0 {
		t.Errorf("progress = %+v, want a machine_click step", prog[0])
	}
	if !strings.Contains(testsupport.Calls(t, control), "greenroom-input") {
		t.Error("the click never reached the guest")
	}

	// The lease is per call: a human can take the screen back right away.
	if _, held := mgr.ControlState(runID); held {
		t.Error("the lease is still held after the turn; a human could not take the screen")
	}
}

func TestTurnTypesAndScrolls(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_type", map[string]any{"text": "hello"}),
		toolCall("machine_key", map[string]any{"key": "a", "mods": []string{"cmd"}}),
		toolCall("machine_scroll", map[string]any{"deltaY": -120.0}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Typed, pressed and scrolled."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Type, press command-A, then scroll up.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 3 {
		t.Fatalf("%d progress messages, want 3", len(prog))
	}
	wantPrefixes := []string{"machine_type", "machine_key", "machine_scroll"}
	for i, want := range wantPrefixes {
		if !strings.HasPrefix(prog[i].Text, want) || prog[i].Step <= 0 {
			t.Errorf("progress[%d] = %+v, want a %s step", i, prog[i], want)
		}
	}
	if !strings.Contains(prog[1].Text, "cmd+a") {
		t.Errorf("key progress %q does not name the modifier", prog[1].Text)
	}
}

func TestTurnComposesADragWithMachineInput(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_input", map[string]any{"actions": []map[string]any{
			{"type": "down", "x": 0.1, "y": 0.1},
			{"type": "move", "x": 0.5, "y": 0.5},
			{"type": "up", "x": 0.5, "y": 0.5},
		}}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Dragged it."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Drag the item across the screen.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 {
		t.Fatalf("a 3-action drag posted %d steps, want 1", len(prog))
	}
	if !strings.HasPrefix(prog[0].Text, "machine_input") {
		t.Errorf("progress = %+v, want a machine_input step", prog[0])
	}

	// One machine_input step however many actions the batch held.
	steps, err := machine.ReadSteps(mgr.RunDir(runID))
	if err != nil {
		t.Fatalf("ReadSteps: %v", err)
	}
	var found int
	for _, s := range steps {
		if s.Tool == "machine_input" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("machine_input steps = %d, want 1", found)
	}
}

func TestTurnComputerUseIsRefusedWhileAHumanHoldsTheScreen(t *testing.T) {
	mgr, runID, control := ready(t)
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	model := &scriptedModel{replies: []string{
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "Could not click; a human has the screen."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click the button.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 || !strings.Contains(prog[0].Text, "human") {
		t.Fatalf("progress = %+v, want an error naming the human", prog)
	}
	if strings.Contains(testsupport.Calls(t, control), "greenroom-input") {
		t.Error("a batch reached the guest while a human held the screen")
	}
	if c, held := mgr.ControlState(runID); !held || c.Holder != "human" {
		t.Errorf("control = %+v, want the human to still hold it", c)
	}
}

func TestTurnDescribesAScreenshotForABlindModel(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	model := &scriptedModel{
		vision: "Safari is frontmost showing github.com. A dialog covers the page: Your computer was restarted.",
		replies: []string{
			toolCall("machine_screenshot", map[string]any{}),
			toolCall("report_verdict", map[string]any{
				"verdict": "fail", "summary": "A system dialog covered the app.",
				"evidence": []string{"step 1", "screenshots/1.png"},
			}),
		},
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Look at the screen.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if model.visions != 1 {
		t.Errorf("the vision model was called %d times, want 1", model.visions)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 1 || prog[0].Step <= 0 {
		t.Fatalf("progress = %+v, want one message with a step", prog)
	}
	got := store.Verdict()
	if got.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", got.Verdict)
	}
	if len(got.Evidence) != 2 || got.Evidence[0] != "step 1" {
		t.Errorf("evidence = %v, want what the model cited", got.Evidence)
	}

	// The description, not the image, must reach the reasoning model.
	last := model.request(t, model.calls())
	if !strings.Contains(last, "Safari is frontmost") {
		t.Error("the screen description never reached the reasoning model")
	}
	if strings.Contains(last, "image_url") {
		t.Error("an image was sent to the reasoning model, which cannot accept one")
	}
}

func TestTurnKeepsGoingWhenTheEyesFail(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	// No vision model configured, so describing must fail.
	model := &scriptedModel{replies: []string{
		toolCall("machine_screenshot", map[string]any{}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "Could not see the screen."}),
	}}
	v, err := New(mgr, Config{
		BaseURL: model.start(t), APIKey: "k", Model: "reasoner",
		MaxSteps: 4, Budget: 30 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Look at the screen.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("a blind verifier must still finish: %v", err)
	}
	if res.Ended != session.Verdict {
		t.Errorf("ended = %q, want a verdict", res.Ended)
	}
	if got := store.Verdict(); got.Verdict != "inconclusive" {
		t.Errorf("verdict = %q, want inconclusive", got.Verdict)
	}
	if second := model.request(t, 2); !strings.Contains(second, "could not be described") {
		t.Error("the model was not told that the screenshot could not be described")
	}
}

func TestTurnEndsWhenTheVerifierAsksAQuestion(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("ask", map[string]any{"question": "Which scheme?"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Debug built cleanly."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Build the app.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Question {
		t.Errorf("ended = %q, want %q", res.Ended, session.Question)
	}
	q := lastMessage(t, store)
	if q.Kind != session.Question || q.From != session.Verifier || q.Text != "Which scheme?" {
		t.Fatalf("last message = %+v, want the verifier's question", q)
	}
	if got := store.Verdict(); got.Status != session.None {
		t.Errorf("verdict state = %+v, want none yet", got)
	}

	// Answering starts a second turn whose context is rebuilt from the file.
	post(t, store, session.Message{From: session.Coder, Kind: session.Answer, ReplyTo: q.Seq, Text: "Use the Debug scheme."})
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("second Turn: %v", err)
	}
	req := model.request(t, 2)
	for _, want := range []string{"Build the app.", "Which scheme?", "answers your question: Use the Debug scheme."} {
		if !strings.Contains(req, want) {
			t.Errorf("the rebuilt context is missing %q", want)
		}
	}
	if got := store.Verdict(); got.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass", got.Verdict)
	}
}

func TestDisputeReopensTheConversation(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("report_verdict", map[string]any{"verdict": "fail", "summary": "The build failed."}),
		toolCall("machine_exec", map[string]any{"command": "swift build -c release"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Release builds cleanly; I was wrong."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Build the app.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("first Turn: %v", err)
	}
	first := store.Verdict()
	if first.Verdict != "fail" {
		t.Fatalf("first verdict = %q, want fail", first.Verdict)
	}
	post(t, store, session.Message{From: session.Coder, Kind: session.Dispute, ReplyTo: first.Seq, Text: "you used Debug"})

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("second Turn: %v", err)
	}
	if req := model.request(t, 2); !strings.Contains(req, "disputes your verdict: you used Debug") {
		t.Errorf("the dispute never reached the model: %s", truncateFor(req))
	}
	got := store.Verdict()
	if got.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass", got.Verdict)
	}
	if got.Status != session.Proposed {
		t.Errorf("status = %q, want proposed", got.Status)
	}
	if got.Disputes != 1 {
		t.Errorf("disputes = %d, want 1", got.Disputes)
	}
}

// A daemon restarting between turns rebuilds context from the transcript.
func TestContextIsRebuiltAfterARestart(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("ask", map[string]any{"question": "Which scheme?"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Debug built cleanly."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Build the app.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("first Turn: %v", err)
	}
	q := lastMessage(t, store)

	reopened := openStore(t, mgr, runID)
	if reopened.Len() != store.Len() {
		t.Fatalf("the reopened conversation has %d messages, want %d", reopened.Len(), store.Len())
	}
	post(t, reopened, session.Message{From: session.Coder, Kind: session.Answer, ReplyTo: q.Seq, Text: "Use the Debug scheme."})
	if _, err := v.Turn(context.Background(), runID, reopened); err != nil {
		t.Fatalf("second Turn: %v", err)
	}
	req := model.request(t, 2)
	for _, want := range []string{"Build the app.", "Which scheme?", "answers your question: Use the Debug scheme."} {
		if !strings.Contains(req, want) {
			t.Errorf("the rebuilt context is missing %q", want)
		}
	}
}

func TestANoteSentMidTurnReachesTheModel(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "open -a Xcode"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "It launched."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Launch the app.")

	// The note lands while the verifier waits on its first model call.
	var once sync.Once
	model.onReasoning = func(n int) {
		if n != 1 {
			return
		}
		once.Do(func() {
			post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "ignore the GPU dialog"})
		})
	}

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if req := model.request(t, 2); !strings.Contains(req, "[while you were working] human says: ignore the GPU dialog") {
		t.Errorf("the note never reached the model: %s", truncateFor(req))
	}
}

// Prose is posted as a reply, never turned into a verdict.
func TestProseWithoutAToolCallBecomesAReply(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{prose("It looks fine to me.")}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check it.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Reply {
		t.Errorf("ended = %q, want %q", res.Ended, session.Reply)
	}
	last := lastMessage(t, store)
	if last.Kind != session.Reply || last.From != session.Verifier || last.Text != "It looks fine to me." {
		t.Fatalf("last message = %+v, want the prose as a reply from the verifier", last)
	}
	if got := store.Verdict(); got.Status != session.None {
		t.Errorf("verdict state = %+v, want no verdict from prose", got)
	}
	if model.calls() != 1 {
		t.Errorf("the model was called %d times, want 1: the loop must not nudge", model.calls())
	}
}

// A human note is answered, and the turn knows what the machine is doing.
func TestAHumanNoteIsAnsweredWithAReply(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("reply", map[string]any{"text": "Still booting, 40 seconds in."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	note := post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "how is it going?"})
	if !note.StartsTurn() {
		t.Fatal("a human note must start a turn")
	}

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Reply {
		t.Errorf("ended = %q, want %q", res.Ended, session.Reply)
	}
	last := lastMessage(t, store)
	if last.Kind != session.Reply || last.From != session.Verifier || last.Text != "Still booting, 40 seconds in." {
		t.Fatalf("last message = %+v, want the verifier's reply", last)
	}
	req := model.request(t, 1)
	for _, want := range []string{"human says: how is it going?", "Machine status: ready"} {
		if !strings.Contains(req, want) {
			t.Errorf("the first request is missing %q: %s", want, truncateFor(req))
		}
	}
}

// A run whose machine never booted still gets turns, and the model is told
// the machine is dead.
func TestAFailedMachineStillAnswersAHuman(t *testing.T) {
	mgr, runID := failed(t)
	model := &scriptedModel{replies: []string{
		toolCall("reply", map[string]any{"text": "The machine never booted, so I could not start."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	reg := session.NewRegistry(mgr.Root, 2)
	actors := NewActors(v, mgr, reg)
	if !actors.Running(runID) {
		t.Fatal("a failed machine's run lost its actor; nobody can ask what happened")
	}
	t.Cleanup(func() { actors.Stop(runID) })

	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "what happened?"})

	reply := waitForKind(t, store, session.Reply, 5*time.Second)
	if reply.From != session.Verifier || !strings.Contains(reply.Text, "never booted") {
		t.Errorf("reply = %+v, want the verifier's answer", reply)
	}
	if req := model.request(t, 1); !strings.Contains(req, "Machine status: failed") {
		t.Errorf("the model was not told the machine failed: %s", truncateFor(req))
	}
	if !actors.Running(runID) {
		t.Error("the actor stopped after the failure; the run can no longer be talked to")
	}
}

func TestTurnStopsAtTheStepLimit(t *testing.T) {
	mgr, runID, _ := ready(t)
	replies := make([]string, 8)
	for i := range replies {
		replies[i] = toolCall("machine_exec", map[string]any{"command": "echo again"})
	}
	model := &scriptedModel{replies: replies}
	v := newVerifier(t, mgr, model.start(t)) // MaxSteps 6
	store := openStore(t, mgr, runID)
	postTask(t, store, "Loop forever.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps != 6 {
		t.Errorf("steps = %d, want the limit of 6", res.Steps)
	}
	last := lastMessage(t, store)
	if last.Kind != session.Reply {
		t.Fatalf("last message = %+v, want a reply", last)
	}
	if !strings.Contains(last.Text, "used all 6 tool calls") || !strings.Contains(last.Text, "continue from here") {
		t.Errorf("reply = %q", last.Text)
	}
	if n := len(messagesOfKind(store, session.Progress)); n != 6 {
		t.Errorf("%d progress messages, want 6", n)
	}
}

// Running out of wall-clock budget ends the turn with a reply, not an error,
// like running out of steps.
func TestTurnStopsWhenTheBudgetRunsOut(t *testing.T) {
	mgr, runID, _ := ready(t)
	replies := make([]string, 8)
	for i := range replies {
		replies[i] = toolCall("machine_exec", map[string]any{"command": "echo again"})
	}
	model := &scriptedModel{replies: replies}
	v, err := New(mgr, Config{
		BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner", VisionModel: "eyes",
		MaxSteps: 100, Budget: 1 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Loop forever.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Ended != session.Reply {
		t.Errorf("Ended = %q, want reply", res.Ended)
	}

	last := lastMessage(t, store)
	if last.Kind != session.Reply {
		t.Fatalf("last message = %+v, want a reply", last)
	}
	if !strings.Contains(last.Text, "ran out of time after") || !strings.Contains(last.Text, "Send another message and I will continue") {
		t.Errorf("reply = %q", last.Text)
	}
}

func TestTurnReportsAnEndpointFailure(t *testing.T) {
	mgr, runID, _ := ready(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer ts.Close()
	v := newVerifier(t, mgr, ts.URL)
	store := openStore(t, mgr, runID)
	postTask(t, store, "Anything.")

	_, err := v.Turn(context.Background(), runID, store)
	if err == nil {
		t.Fatal("Turn returned no error although the endpoint rejected the key")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to carry the status", err)
	}
	found := false
	for _, m := range messagesOfKind(store, session.Event) {
		if m.From == session.System && strings.Contains(m.Text, "verifier turn failed") {
			found = true
		}
	}
	if !found {
		t.Error("the failure was never posted to the conversation")
	}
}

// A turn cancelled because its actor stopped (the machine was destroyed)
// leaves the transcript alone.
func TestACancelledTurnPostsNothing(t *testing.T) {
	mgr, runID, _ := ready(t)
	hit := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		hit <- struct{}{}
		<-r.Context().Done()
	}))
	defer ts.Close()
	v := newVerifier(t, mgr, ts.URL)
	store := openStore(t, mgr, runID)
	postTask(t, store, "Anything.")
	before := store.Len()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-hit
		cancel()
	}()
	if _, err := v.Turn(ctx, runID, store); err == nil {
		t.Fatal("a cancelled turn returned no error")
	}
	if after := store.After(before); len(after) != 0 {
		t.Errorf("a cancelled turn posted %+v", after)
	}
}

func TestUnknownToolIsReportedToTheModel(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("delete_everything", map[string]any{}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "No such tool."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Try a tool that does not exist.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	if req := model.request(t, 2); !strings.Contains(req, "no tool named delete_everything") {
		t.Error("the loop did not tell the model that the tool does not exist")
	}
}

func TestParseVerdictFallsBackToInconclusive(t *testing.T) {
	for _, args := range []string{`{"verdict":"maybe","summary":"x"}`, `{}`, `not json`, `{"verdict":"PASS","summary":"y"}`} {
		got, summary, _ := parseVerdict(args)
		if summary == "" {
			t.Errorf("parseVerdict(%s) returned an empty summary", args)
		}
		want := "inconclusive"
		if args == `{"verdict":"PASS","summary":"y"}` {
			want = "pass"
		}
		if got != want {
			t.Errorf("parseVerdict(%s) = %q, want %q", args, got, want)
		}
	}
	got, summary, evidence := parseVerdict(`{"verdict":"fail","summary":"broken","evidence":["step 3","/tmp/a.png"]}`)
	if got != "fail" || summary != "broken" {
		t.Errorf("parseVerdict = %q, %q", got, summary)
	}
	if len(evidence) != 2 || evidence[1] != "/tmp/a.png" {
		t.Errorf("evidence = %v", evidence)
	}
}

func TestParseQuestion(t *testing.T) {
	if got := parseQuestion(`{"question":"  Which scheme? "}`); got != "Which scheme?" {
		t.Errorf("parseQuestion = %q", got)
	}
	if got := parseQuestion(`not json`); got == "" {
		t.Error("parseQuestion returned nothing for a broken argument")
	}
}

func TestProgressTextRoundTrips(t *testing.T) {
	args := `{"command": "swift build -c release", "cwd": "my app"}`
	result := "step 3\nexit code 0\nstdout:\nall good\nstderr:\n"
	name, gotArgs, gotResult := splitProgress(progressText(nim.ToolCall{Name: "machine_exec", Arguments: args}, result))
	if name != "machine_exec" {
		t.Errorf("name = %q", name)
	}
	if gotArgs != args {
		t.Errorf("arguments = %q, want %q", gotArgs, args)
	}
	if gotResult != result {
		t.Errorf("result = %q, want %q", gotResult, result)
	}
	// A tool with no arguments must still project as valid JSON.
	if _, empty, _ := splitProgress(progressText(nim.ToolCall{Name: "machine_screenshot"}, "step 1")); empty != "{}" {
		t.Errorf("empty arguments = %q, want {}", empty)
	}
}

func TestClampKeepsBothEndsOfLongOutput(t *testing.T) {
	long := strings.Repeat("a", 100) + strings.Repeat("b", maxToolOutput) + strings.Repeat("c", 100)
	got := clamp(long)
	if len(got) > maxToolOutput+64 {
		t.Errorf("clamped output is %d characters, want about %d", len(got), maxToolOutput)
	}
	if !strings.HasPrefix(got, "aaa") || !strings.HasSuffix(got, "ccc") {
		t.Error("clamp dropped one of the ends")
	}
	short := "just a line"
	if clamp(short) != short {
		t.Error("clamp changed short output")
	}
}

func TestToJPEGShrinksALargeScreenshot(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2048, 1536))
	img.Set(10, 10, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	jpg, err := toJPEG(buf.Bytes())
	if err != nil {
		t.Fatalf("toJPEG: %v", err)
	}
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		t.Fatal("the output is not a JPEG")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != maxVisionWidth {
		t.Errorf("width = %d, want %d", cfg.Width, maxVisionWidth)
	}
	if _, err := toJPEG([]byte("not a png")); err == nil {
		t.Error("toJPEG accepted something that is not a PNG")
	}
}

// writeShot gives the fake tart a real PNG to hand back as a screenshot.
func writeShot(t *testing.T, control string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString(buf.Bytes())
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(enc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func truncateFor(s string) string {
	if len(s) < 300 {
		return s
	}
	return s[:300]
}

const segmentUI = `{"app":{"name":"TipSplit","pid":7},"apps":["TipSplit"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXRadioButton","subrole":"AXSegment","label":"25%","depth":0,"frame":{"x":586,"y":347,"w":48,"h":24}}]}`

func putUI(t *testing.T, control, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, "ui.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ADR 0012: a text-only model aims from the UI tree. It reads the tree, sees
// the element's center, and clicks it by id; the click lands on that center.
func TestTurnReadsTheUITreeAndClicksAnElement(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	model := &scriptedModel{replies: []string{
		toolCall("machine_ui", map[string]any{"app": "TipSplit"}),
		toolCall("machine_click", map[string]any{"element": 1}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Clicked 25%."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click 25%.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	// The second model request carries the tree the first asked for.
	if req := model.request(t, 2); !strings.Contains(req, `[1] RadioButton/Segment label=\"25%\" center (0.596, 0.467)`) {
		t.Errorf("the model never saw the element's center:\n%s", req)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 2 || !strings.Contains(prog[1].Text, `clicked [1] RadioButton/Segment "25%" at (0.596, 0.467)`) {
		t.Fatalf("progress = %+v, want the click to name the element it hit", prog)
	}
	if !strings.Contains(testsupport.Calls(t, control), "--ui-base64") {
		t.Error("the tree was never read from the guest")
	}
}

func TestTurnClickByElementNeedsATree(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_click", map[string]any{"element": 3}),
		toolCall("reply", map[string]any{"text": "I need to read the UI first."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click it.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if req := model.request(t, 2); !strings.Contains(req, "call machine_ui first") {
		t.Errorf("the model was not told to read the tree first:\n%s", req)
	}
}

// The model is told to obey a coder's constraints and to aim from the tree;
// both were missing when a demo verifier read the source and clicked blind.
func TestSystemPromptBindsConstraintsAndAimsFromTheTree(t *testing.T) {
	for _, want := range []string{"hard rules", "do not rebuild or relaunch", "Before any click, call machine_ui", "wallpaper"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	if !strings.Contains(visionPrompt, "approximate center as fractions") {
		t.Error("the vision prompt does not ask for positions")
	}
}

// assistantProse returns the text of every assistant message in the nth
// request that has content, i.e. that the model could imitate as prose.
func assistantProse(t *testing.T, model *scriptedModel, n int) []string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(model.request(t, n)), &req); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range req.Messages {
		if s, _ := m.Content.(string); m.Role == "assistant" && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Regression (demo run 20260923-005718): a fail was accepted, the coder fixed
// the code and sent a new task, and the model answered "[I reported verdict
// pass] ..." in prose, imitating how its past verdict was projected. That was
// posted as a reply, so the run kept the accepted fail. A past verdict is now
// a report_verdict call in the context, and the new pass supersedes the fail.
func TestANewTaskAfterAnAcceptedVerdictGetsARealVerdict(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("report_verdict", map[string]any{"verdict": "fail", "summary": "Each pays shows $0.00.", "evidence": []string{"step 3"}}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Each pays shows $48.00 now."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the total.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	fail := store.Verdict()
	post(t, store, session.Message{From: session.Coder, Kind: session.Accept, ReplyTo: fail.Seq})
	post(t, store, session.Message{From: session.Coder, Kind: session.Note, Text: "Fixed the rounding."})
	postTask(t, store, "Check the total again.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}

	for _, prose := range assistantProse(t, model, 2) {
		if strings.Contains(prose, "verdict") {
			t.Errorf("the past verdict reached the model as prose it can imitate: %q", prose)
		}
	}
	if req := model.request(t, 2); !strings.Contains(req, `\"verdict\":\"fail\"`) || !strings.Contains(req, `"name":"report_verdict"`) {
		t.Errorf("the past verdict is not a report_verdict call in the context:\n%s", truncateFor(req))
	}
	got := store.Verdict()
	if got.Verdict != "pass" || got.Status != session.Proposed || got.Seq == fail.Seq {
		t.Fatalf("verdict = %+v, want a new proposed pass", got)
	}
	post(t, store, session.Message{From: session.Coder, Kind: session.Accept, ReplyTo: got.Seq})
	if store.Verdict().Status != session.Accepted {
		t.Errorf("the coder could not accept the new pass: %+v", store.Verdict())
	}
}

// A model that still writes a verdict as prose is told to call the tool, once.
func TestAProseVerdictIsSentBackForTheTool(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		prose("[I reported verdict pass] Each pays is $48.00."),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Each pays is $48.00."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the total.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict || store.Verdict().Verdict != "pass" {
		t.Errorf("ended %q with verdict %+v, want a recorded pass", res.Ended, store.Verdict())
	}
	if !strings.Contains(model.request(t, 2), "Call report_verdict") {
		t.Error("the model was not told to call report_verdict")
	}
	for _, m := range store.After(0) {
		if m.Kind == session.Reply {
			t.Errorf("the prose verdict was posted as a reply: %q", m.Text)
		}
	}
}

func TestProseVerdictMatchesOnlyVerdictShapes(t *testing.T) {
	for text, want := range map[string]bool{
		"[I reported verdict pass] ok":   true,
		"  [I report a verdict fail] no": true,
		"[I asked] Which scheme?":        true,
		"Verdict: pass. It works.":       true,
		"**Verdict:** fail":              true,
		"The build is still running.":    false,
		"I will report a verdict soon.":  false,
		"The verdict depends on the OS.": false,
	} {
		if got := proseVerdict.MatchString(text); got != want {
			t.Errorf("proseVerdict(%q) = %v, want %v", text, got, want)
		}
	}
}
