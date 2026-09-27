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
	// visionReplies, when set, answer the vision calls in order before vision does.
	visionReplies []string

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
			answer := s.vision
			if s.visions < len(s.visionReplies) {
				answer = s.visionReplies[s.visions]
			}
			s.visions++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":`+quote(answer)+`}}],"usage":{}}`)
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
	// A live machine keeps writing into its run directory, racing t.TempDir's cleanup under load.
	// Registered after the TempDir, so it runs before the removal.
	t.Cleanup(func() {
		for _, mc := range mgr.List() {
			_, _ = mgr.Wait(context.Background(), mc.RunID, 15*time.Second)
			_ = mgr.Destroy(context.Background(), mc.RunID)
		}
	})
	mc, err := mgr.Create(context.Background(), "img")
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

	mc, err := mgr.Create(context.Background(), "img")
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
	build := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("build"),
		toolCall("machine_exec", map[string]any{"command": "swift build"}),
		verdictOf("pass", "The build succeeded and the app launched.", answer("build", "pass", []int{build})),
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
	if len(prog) != 2 {
		t.Fatalf("%d progress messages, want the declaration and the command", len(prog))
	}
	if len(prog[0].Checks) != 1 || prog[0].Checks[0].ID != "build" || prog[0].Step != 0 {
		t.Errorf("declaration = %+v, want the one check and no step", prog[0])
	}
	if prog[1].Step != build {
		t.Errorf("progress step = %d, want %d, the step it recorded", prog[1].Step, build)
	}
	if !strings.HasPrefix(prog[1].Text, "machine_exec") {
		t.Errorf("progress text = %q, want it to name the tool", truncateFor(prog[1].Text))
	}
	if c := last.Checks; len(c) != 1 || c[0].Status != "pass" || c[0].Criterion == "" || c[0].Evidence[0] != build {
		t.Errorf("verdict checks = %+v, want the answered check with its criterion", c)
	}

	if !strings.Contains(testsupport.ExecStdin(t, control), "swift build") {
		t.Error("the command never reached the machine")
	}
	if second := model.request(t, 3); !strings.Contains(second, "exit code") {
		t.Errorf("the tool result never reached the model: %s", truncateFor(second))
	}
}

func TestTurnClicksAtAFractionAndRecordsOneStep(t *testing.T) {
	mgr, runID, control := ready(t)
	model := &scriptedModel{replies: []string{
		declared("button"),
		toolCall("machine_click", map[string]any{"x": 0.25, "y": 0.5}),
		verdictOf("inconclusive", "Clicked the button; the app has no UI tree to read."),
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

	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
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
		declared("text"),
		toolCall("machine_type", map[string]any{"text": "hello"}),
		toolCall("machine_key", map[string]any{"key": "a", "mods": []string{"cmd"}}),
		toolCall("machine_scroll", map[string]any{"deltaY": -120.0}),
		verdictOf("inconclusive", "Typed, pressed and scrolled."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Type, press command-A, then scroll up.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
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

// Issue #126: the model brain's empty machine_type gets the machine's own refusal, with no step.
func TestTurnEmptyTypeIsRefusedWithNoStep(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		declared("text"),
		toolCall("machine_type", map[string]any{"text": ""}),
		verdictOf("inconclusive", "Nothing was typed."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Type nothing.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
	if len(prog) != 1 || prog[0].Step != 0 || !strings.Contains(prog[0].Text, "type needs text: pass the characters to type") {
		t.Fatalf("progress = %+v, want one unrecorded type-needs-text refusal", prog)
	}
}

func TestTurnComposesADragWithMachineInput(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		declared("drag"),
		toolCall("machine_input", map[string]any{"actions": []map[string]any{
			{"type": "down", "x": 0.1, "y": 0.1},
			{"type": "move", "x": 0.5, "y": 0.5},
			{"type": "up", "x": 0.5, "y": 0.5},
		}}),
		verdictOf("inconclusive", "Dragged it."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Drag the item across the screen.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
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
		declared("button"),
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "Could not click; a human has the screen."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click the button.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
	if len(prog) != 1 || !strings.Contains(prog[0].Text, "human") {
		t.Fatalf("progress = %+v, want an error naming the human", prog)
	}
	// --json-base64 posts a batch; boot's own helper check runs the helper too.
	if strings.Contains(testsupport.Calls(t, control), "--json-base64") {
		t.Error("a batch reached the guest while a human held the screen")
	}
	if c, held := mgr.ControlState(runID); !held || c.Holder != "human" {
		t.Errorf("control = %+v, want the human to still hold it", c)
	}
}

func TestTurnDescribesAScreenshotForABlindModel(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	shot := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{
		vision: "Safari is frontmost showing github.com. A dialog covers the page: Your computer was restarted.",
		replies: []string{
			declared("screen"),
			toolCall("machine_screenshot", map[string]any{}),
			toolCall("report_verdict", map[string]any{
				"verdict": "fail", "summary": "A system dialog covered the app.",
				"checks":   []map[string]any{answer("screen", "fail", []int{shot})},
				"evidence": []string{"screenshots/1.png"},
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
	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
	if len(prog) != 1 || prog[0].Step != shot {
		t.Fatalf("progress = %+v, want one message with step %d", prog, shot)
	}
	got := store.Verdict()
	if got.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail", got.Verdict)
	}
	if len(got.Evidence) != 1 || got.Evidence[0] != "screenshots/1.png" {
		t.Errorf("evidence = %v, want the artifact path the model cited", got.Evidence)
	}
	if len(got.Checks) != 1 || got.Checks[0].Status != "fail" || got.Checks[0].Evidence[0] != shot {
		t.Errorf("checks = %+v, want the failing check with its screenshot step", got.Checks)
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

// The vision model once answered with a page of <unk> tokens; muse-glimmer-30b and kimi-k3
// sometimes answer with no text, and kimi-k3 with a line of "!" (ADR 0030). Each is retried
// once, and a second one reaches the reasoning model as an error, not as noise.
func TestDescribeRetriesAnUnreadableAnswerOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replies []string
		want    string
	}{
		{"then readable", []string{"<unk><unk><unk>", "3. Window text:\nEach pays: $48.00"}, "Each pays: $48.00"},
		{"twice", []string{"<unk><unk>", "<unk><unk>"}, "no readable description twice"},
		{"empty then readable", []string{"", "3. Window text:\nEach pays: $48.00"}, "Each pays: $48.00"},
		{"empty twice", []string{"", ""}, "no readable description twice"},
		{"punctuation then readable", []string{"1!!!!!!!!!!!!!!!!!!!!!", "3. Window text:\nEach pays: $48.00"}, "Each pays: $48.00"},
		{"punctuation twice", []string{"!!!!!!!!!!!!", "!!!!!!!!!!!!"}, "no readable description twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, runID, control := ready(t)
			writeShot(t, control)
			model := &scriptedModel{
				visionReplies: tc.replies,
				replies: []string{
					toolCall("machine_screenshot", map[string]any{}),
					toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "done"}),
				},
			}
			v := newVerifier(t, mgr, model.start(t))
			store := openStore(t, mgr, runID)
			postTask(t, store, "Look at the screen.")
			if _, err := v.Turn(context.Background(), runID, store); err != nil {
				t.Fatalf("Turn: %v", err)
			}
			if model.visions != 2 {
				t.Errorf("the vision model was called %d times, want 2", model.visions)
			}
			last := model.request(t, model.calls())
			if !strings.Contains(last, tc.want) {
				t.Errorf("the reasoning model never saw %q", tc.want)
			}
			if strings.Contains(last, "<unk>") {
				t.Error("unreadable tokens reached the reasoning model")
			}
		})
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
	build := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		toolCall("ask", map[string]any{"question": "Which scheme?"}),
		declared("build"),
		toolCall("machine_exec", map[string]any{"command": "swift build"}),
		verdictOf("pass", "Debug built cleanly (step 2).", answer("build", "pass", []int{build})),
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
	debug := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("build"),
		toolCall("machine_exec", map[string]any{"command": "swift build"}),
		verdictOf("fail", "The build failed.", answer("build", "fail", []int{debug})),
		toolCall("machine_exec", map[string]any{"command": "swift build -c release"}),
		verdictOf("pass", "Release builds cleanly; I was wrong.", answer("build", "pass", []int{debug + 1})),
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
	if req := model.request(t, 4); !strings.Contains(req, "disputes your verdict: you used Debug") {
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
		verdictOf("inconclusive", "The build was not run."),
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
		verdictOf("inconclusive", "It launched; nothing was checked on screen."),
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
	// The task is open, so the first prose is sent back once for a verdict (issue #89); a
	// model that answers in prose again is heard.
	model := &scriptedModel{replies: []string{prose("Looking."), prose("It looks fine to me.")}}
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
	if model.calls() != 2 {
		t.Errorf("the model was called %d times, want 2: one nudge for the open task, then the reply", model.calls())
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
	if !strings.Contains(last.Text, "ran out of time after") || !strings.Contains(last.Text, "Send a message and I will continue") {
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
		in := parseVerdict(args)
		if in.summary == "" {
			t.Errorf("parseVerdict(%s) returned an empty summary", args)
		}
		want := "inconclusive"
		if args == `{"verdict":"PASS","summary":"y"}` {
			want = "pass"
		}
		if in.verdict != want {
			t.Errorf("parseVerdict(%s) = %q, want %q", args, in.verdict, want)
		}
	}
	// ADR 0024: evidence is for artifact paths; a step there is a problem the review names, and
	// steps in checks take numbers, numeric strings and "step 4".
	in := parseVerdict(`{"verdict":"fail","summary":"broken","evidence":["step 3","/tmp/a.png"],` +
		`"checks":[{"id":"total","status":"FAIL","evidence":[4,"5","step 6"],"actions":["seven"],"observed":"x"}]}`)
	if in.verdict != "fail" || in.summary != "broken" {
		t.Errorf("parseVerdict = %q, %q", in.verdict, in.summary)
	}
	if len(in.paths) != 1 || in.paths[0] != "/tmp/a.png" {
		t.Errorf("paths = %v, want only the artifact path", in.paths)
	}
	if len(in.general) != 1 || !strings.Contains(in.general[0], `"step 3" is a step`) {
		t.Errorf("general problems = %v, want the step in evidence named", in.general)
	}
	c := in.checks[0]
	if c.ID != "total" || c.Status != "fail" || len(c.Evidence) != 3 || c.Evidence[2] != 6 || len(c.Actions) != 0 {
		t.Errorf("check = %+v", c)
	}
	if p := in.problems["total"]; len(p) != 1 || !strings.Contains(p[0], `"seven" is not a step number`) {
		t.Errorf("check problems = %v", p)
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
	look := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("tip"),
		toolCall("machine_ui", map[string]any{"app": "TipSplit"}),
		toolCall("machine_click", map[string]any{"element": 1}),
		verdictOf("pass", "25% is selected (step 3).", answer("tip", "pass", []int{look + 2}, look+1)),
	}}
	selectOnClick(t, model, control, 3)
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click 25%.")

	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	// The request after the read carries the tree it asked for.
	if req := model.request(t, 3); !strings.Contains(req, `[1] RadioButton/Segment label=\"25%\" center (0.596, 0.467)`) {
		t.Errorf("the model never saw the element's center:\n%s", req)
	}
	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
	if len(prog) != 2 || !strings.Contains(prog[1].Text, `clicked [1] RadioButton/Segment "25%" in TipSplit at (0.596, 0.467)`) {
		t.Fatalf("progress = %+v, want the click to name the element it hit", prog)
	}
	if got := store.Verdict(); got.Verdict != "pass" {
		t.Errorf("verdict = %+v, want the pass its effect check shows", got)
	}
	if !strings.Contains(testsupport.Calls(t, control), "--ui-base64") {
		t.Error("the tree was never read from the guest")
	}
}

func TestTurnClickByElementNeedsATree(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		declared("it"),
		toolCall("machine_click", map[string]any{"element": 3}),
		toolCall("ask", map[string]any{"question": "Which element is it? I need to read the UI first."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click it.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if req := model.request(t, 3); !strings.Contains(req, "call machine_ui first") {
		t.Errorf("the model was not told to read the tree first:\n%s", req)
	}
}

// deliveredText returns the text of every message with role in the nth
// (1-based) request, as the model received it.
func deliveredText(t *testing.T, model *scriptedModel, n int, role string) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(model.request(t, n)), &req); err != nil {
		t.Fatalf("request %d: %v", n, err)
	}
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role != role {
			continue
		}
		switch c := m.Content.(type) {
		case string:
			b.WriteString(c + "\n")
		case []any:
			for _, p := range c {
				if pm, ok := p.(map[string]any); ok && pm["type"] == "text" {
					text, _ := pm["text"].(string)
					b.WriteString(text + "\n")
				}
			}
		}
	}
	return b.String()
}

// The model is told to obey a coder's constraints and to aim from the tree;
// both were missing when a demo verifier read the source and clicked blind.
func TestTheDeliveredSystemPromptBindsConstraintsAndAimsFromTheTree(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		verdictOf("inconclusive", "Nothing was checked."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Use the UI only.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	system := deliveredText(t, model, 1, "system")
	for _, want := range []string{"hard rules", "do not rebuild or relaunch", "Before any click, call machine_ui", "wallpaper",
		// ADR 0024: checks before input, and the coder's claims are never evidence.
		"call declare_checks before your first input", "unverified claims", "A verdict rests only on observations you made",
		`"no change detected"`} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt the model received lacks %q:\n%s", want, system)
		}
	}
}

// Padded verifier prose made the Companion's transcript tiring to read. The
// model must receive the writing rules, with their examples, in its system prompt.
func TestTheDeliveredSystemPromptCarriesTheWritingRules(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		verdictOf("inconclusive", "Nothing was checked."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the total.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	system := deliveredText(t, model, 1, "system")
	for _, want := range []string{
		"How you write",
		"Lead with the finding",
		"One claim per sentence",
		"Cite evidence as step numbers",
		"Do not announce what you will do",
		"No adverbs or intensifiers",
		`No "not X but Y" contrasts`,
		"No em dashes",
		"active voice and plain numbers",
		"A verdict summary is 2 or 3 short sentences",
		"A reply is 1 to 3 sentences. A question is one sentence",
		`After: "Launching TipSplit to read both totals."`,
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt the model received lacks %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "\u2014") {
		t.Error("the system prompt that bans em dashes contains one")
	}
}

// A describer that named the window and "no error" but no values made the
// verifier retake the shot. The prompt sent with the image must ask for every
// visible string and for positions.
func TestTheDeliveredVisionPromptAsksForEveryVisibleString(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	model := &scriptedModel{
		vision: "3. Window text:\nEach pays: $48.00",
		replies: []string{
			toolCall("machine_screenshot", map[string]any{}),
			verdictOf("inconclusive", "Nothing was checked."),
		},
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Look at the screen.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if model.visions != 1 {
		t.Fatalf("the vision model was called %d times, want 1", model.visions)
	}
	vision := deliveredText(t, model, 2, "user")
	for _, want := range []string{
		"quote every piece of text visible in the frontmost window",
		"the contents of every field",
		"values, results, totals",
		"Never skip text",
		"approximate center as fractions",
	} {
		if !strings.Contains(vision, want) {
			t.Errorf("the vision prompt the model received lacks %q:\n%s", want, vision)
		}
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
	look := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("total"),
		toolCall("machine_ui", map[string]any{}),
		verdictOf("fail", "Each pays shows $0.00.", answer("total", "fail", []int{look})),
		declared("total"),
		toolCall("machine_ui", map[string]any{}),
		verdictOf("pass", "Each pays shows $48.00 now.", answer("total", "pass", []int{look + 1})),
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

	for _, prose := range assistantProse(t, model, 4) {
		if strings.Contains(prose, "verdict") {
			t.Errorf("the past verdict reached the model as prose it can imitate: %q", prose)
		}
	}
	if req := model.request(t, 4); !strings.Contains(req, `\"verdict\":\"fail\"`) || !strings.Contains(req, `"name":"report_verdict"`) ||
		!strings.Contains(req, `\"checks\":[{\"actions\":[],\"evidence\":[`) {
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
		prose("[I reported verdict inconclusive] Each pays was not read."),
		verdictOf("inconclusive", "Each pays was not read."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the total.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict || store.Verdict().Verdict != "inconclusive" {
		t.Errorf("ended %q with verdict %+v, want a recorded verdict", res.Ended, store.Verdict())
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

// On the last step there is no step left to answer a nudge, so the prose is
// posted as a reply rather than thrown away with the cap message in its place.
func TestAProseVerdictOnTheLastStepIsPostedAsAReply(t *testing.T) {
	mgr, runID, _ := ready(t)
	var replies []string
	for i := 0; i < 5; i++ {
		replies = append(replies, toolCall("machine_exec", map[string]any{"command": "true"}))
	}
	replies = append(replies, prose("Verdict: pass. Each pays is $48.00."))
	model := &scriptedModel{replies: replies}
	v := newVerifier(t, mgr, model.start(t)) // MaxSteps 6
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the total.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range store.After(0) {
		if m.Kind == session.Reply {
			got = append(got, m.Text)
		}
	}
	if res.Ended != session.Reply || len(got) != 1 || !strings.Contains(got[0], "Each pays is $48.00") {
		t.Errorf("ended %q with replies %q, want the model's prose as the one reply", res.Ended, got)
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

// truncatedReply is a reply the endpoint stopped at max_tokens: finish_reason length, no parsed tool call.
func truncatedReply(text string) string {
	return `{"choices":[{"message":{"role":"assistant","content":` + quote(text) +
		`},"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":1200}}`
}

// Issue #71: a reasoning model that spent its token budget thinking, or ran out mid tool call,
// came back cut off; the empty or half-written message was posted as the turn's reply and the
// verdict was lost. A cut-off step is sent back, and the model's next answer counts.
func TestACutOffStepIsRetriedNotPostedAsTheReply(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		truncatedReply(""),
		truncatedReply(`All checks pass.<tool_call>report_verdict<arg_key>evidence</arg_key><arg_value>["step 22", "st`),
		verdictOf("inconclusive", "The widths were not checked."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the pricing cards.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict || store.Verdict().Verdict != "inconclusive" {
		t.Fatalf("ended %q with verdict %+v, want the verdict the model meant", res.Ended, store.Verdict())
	}
	for _, m := range store.After(0) {
		if m.Kind == session.Reply {
			t.Errorf("a cut-off step was posted as a reply: %q", m.Text)
		}
	}
	if !strings.Contains(model.request(t, 2), "cut off") {
		t.Error("the model was not told its last message was cut off")
	}
}

// When every step is cut off, the turn says so in words; it never posts a fragment.
func TestATurnThatKeepsGettingCutOffSaysSo(t *testing.T) {
	mgr, runID, _ := ready(t)
	var replies []string
	for i := 0; i < 6; i++ {
		replies = append(replies, truncatedReply(`<tool_call>report_verdict<arg_key>evidence`))
	}
	model := &scriptedModel{replies: replies}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check it.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	last := lastMessage(t, store)
	if last.Kind != session.Reply || strings.Contains(last.Text, "<tool_call>") || !strings.Contains(last.Text, "cut off") {
		t.Fatalf("last message = %+v, want a reply saying the answers were cut off, with no fragment", last)
	}
}

// Reasoning models think inside the completion budget, so 1200 tokens was used up before any answer.
func TestChatLeavesRoomForAReasoningModel(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{verdictOf("inconclusive", "Nothing was checked.")}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check it.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(model.request(t, 1)), &body); err != nil {
		t.Fatal(err)
	}
	if n, _ := body["max_tokens"].(float64); n < 4096 {
		t.Errorf("max_tokens = %v, want at least 4096", body["max_tokens"])
	}
}

// Issue #85: an "element" inside a machine_input batch was dropped and the click went to the
// pointer, reported as "posted 3 actions". The model gets an error it can act on instead.
func TestMachineInputRefusesFieldsItDoesNotKnow(t *testing.T) {
	mgr, runID, control := ready(t)
	model := &scriptedModel{replies: []string{
		declared("todo"),
		toolCall("machine_input", map[string]any{"actions": []map[string]any{
			{"type": "click", "element": 2}, {"type": "type", "text": "Call mom"},
		}}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "Could not add."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Add a todo.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
	if len(prog) != 1 || !strings.Contains(prog[0].Text, `unknown field "element"`) || !strings.Contains(prog[0].Text, "machine_click") {
		t.Fatalf("progress = %+v, want an error naming the unknown field and machine_click", prog)
	}
	if strings.Contains(testsupport.Calls(t, control), "--json-base64") {
		t.Error("the batch was posted")
	}
}

// Issue #89: a human re-check arrived while the verifier checked the coder's newer task, and the
// turn ended in a reply, so the task never got a verdict. A turn with an open task is sent back
// once when it tries to end in a reply, and the verdict it then reports is the one recorded.
func TestATurnWithAnOpenTaskIsSentBackFromAReply(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	build := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "true"}),
		toolCall("reply", map[string]any{"text": "The first verdict was right for the old build; the fix works."}),
		declared("split"), // after the human's task, which the checks must cover
		verdictOf("pass", "The fixed build splits correctly.", answer("split", "pass", []int{build})),
	}}
	model.onReasoning = func(n int) {
		if n == 2 {
			// The Companion's Re-check lands mid-turn, as in the demo.
			_, _ = store.Append(session.Message{From: session.Human, Kind: session.Task,
				Text: "Re-check the fail verdict (message 1) before it is trusted: is it still right?"})
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	postTask(t, store, "Re-check the fixed build.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict || store.Verdict().Verdict != "pass" {
		t.Fatalf("ended %q with verdict %+v, want the pass for the open task", res.Ended, store.Verdict())
	}
	if replies := messagesOfKind(store, session.Reply); len(replies) != 0 {
		t.Errorf("the reply was posted although a task was open: %+v", replies)
	}
	if !strings.Contains(model.request(t, 3), "report_verdict") || !strings.Contains(model.request(t, 3), "still open") {
		t.Error("the model was not told the task is still open and needs report_verdict")
	}
}

// The nudge is once per turn: a model that replies again is heard, not looped.
func TestAnOpenTaskIsNudgedOnlyOnce(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("reply", map[string]any{"text": "The IP is 192.168.64.2."}),
		toolCall("reply", map[string]any{"text": "The IP is 192.168.64.2; there is nothing to judge."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "What is the machine's IP?")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Reply || lastMessage(t, store).Text != "The IP is 192.168.64.2; there is nothing to judge." {
		t.Fatalf("ended %q with %+v, want the second reply posted", res.Ended, lastMessage(t, store))
	}
}

// A turn with no open task (a human's question after a verdict) replies as before.
func TestANoteAfterAVerdictIsStillAnsweredWithAReply(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{toolCall("reply", map[string]any{"text": "It passed at step 3."})}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check it.")
	post(t, store, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "pass", Text: "fine"})
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "why did it pass?"})
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	if last := lastMessage(t, store); last.Kind != session.Reply || model.calls() != 1 {
		t.Fatalf("last = %+v after %d calls, want one reply", last, model.calls())
	}
}

// A reply sent back for an open task shares its message with other calls: every call id still
// gets a tool message in the next request, since a strict endpoint refuses an unanswered one.
func TestASentBackReplyAnswersEveryCallOfItsMessage(t *testing.T) {
	mgr, runID, _ := ready(t)
	reply, _ := json.Marshal(map[string]any{"text": "Looks fine."})
	exec, _ := json.Marshal(map[string]any{"command": "true"})
	both := `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[` +
		`{"id":"c1","type":"function","function":{"name":"reply","arguments":` + quote(string(reply)) + `}},` +
		`{"id":"c2","type":"function","function":{"name":"machine_exec","arguments":` + quote(string(exec)) + `}}` +
		`]}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
	model := &scriptedModel{replies: []string{
		both,
		verdictOf("inconclusive", "Nothing was checked."),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check it.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict {
		t.Fatalf("ended %q, want the verdict", res.Ended)
	}
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			Content    string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(model.request(t, 2)), &req); err != nil {
		t.Fatal(err)
	}
	answers := map[string]string{}
	for _, m := range req.Messages {
		if m.Role == "tool" {
			answers[m.ToolCallID] = m.Content
		}
	}
	if !strings.Contains(answers["c1"], "still open") {
		t.Errorf("the reply call got %q, want the open-task nudge", answers["c1"])
	}
	if !strings.Contains(answers["c2"], "Not run") {
		t.Errorf("the machine_exec call got %q, want it answered as dropped", answers["c2"])
	}
	if progress := messagesOfKind(store, session.Progress); len(progress) != 0 {
		t.Errorf("a dropped call ran: %+v", progress)
	}
}

// Issue #97: while a human held the screen the verifier retried clicks in a tight loop, then
// reported "inconclusive: a human is driving", which replaced its real fail verdict. The first
// refusal ends the turn with a question (issue #124), and a lease refusal never becomes a verdict.
func TestAHumanOnTheScreenEndsTheTurnWithAQuestionNotAVerdict(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the split.")
	post(t, store, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "Each pays is $45.00, not $53.10."})
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	replies := []string{declared("each-pays")}
	for i := 0; i < 5; i++ {
		replies = append(replies, toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}))
	}
	replies = append(replies, toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "A human is driving."}))
	model := &scriptedModel{replies: replies}
	v := newVerifier(t, mgr, model.start(t))
	postTask(t, store, "Click 20% and report Each pays.")

	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Question {
		t.Fatalf("ended %q, want a question asking for the screen", res.Ended)
	}
	if last := lastMessage(t, store); !strings.Contains(last.Text, "screen") {
		t.Errorf("question = %q, want it to ask for the screen", last.Text)
	}
	if got := store.Verdict(); got.Verdict != "fail" {
		t.Errorf("verdict = %+v, want the standing fail kept", got)
	}
	if last := lastMessage(t, store); !strings.Contains(last.Text, "verdict still stands") {
		t.Errorf("question = %q, want it to say the fail still stands", last.Text)
	}
	if n := len(messagesOfKind(store, session.Progress)) - 1; n != 1 { // less the declaration
		t.Errorf("%d clicks were tried while the human held the screen, want 1", n)
	}
	if model.calls() != 2 {
		t.Errorf("the model was asked %d times, want twice: the declaration, then the refused click ends the turn", model.calls())
	}
	if last := lastMessage(t, store); !strings.Contains(last.Text, "Press Give Back in the Companion and I will continue") ||
		strings.Contains(last.Text, "send a message") {
		t.Errorf("question = %q, want giving back to be enough", last.Text)
	}
}

// A model that reports inconclusive right after one refusal is turned into the same question.
func TestAnInconclusiveCausedByTheLeaseIsAQuestion(t *testing.T) {
	mgr, runID, _ := ready(t)
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	model := &scriptedModel{replies: []string{
		declared("it"),
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "A human is driving."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click it.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Question || store.Verdict().Status != session.None {
		t.Fatalf("ended %q with verdict %+v, want a question and no verdict", res.Ended, store.Verdict())
	}
	if last := lastMessage(t, store); strings.Contains(last.Text, "verdict still stands") {
		t.Errorf("question = %q, but there is no verdict to stand", last.Text)
	}
}

// A verdict the human rejected does not "still stand" in the question.
func TestTheQuestionDoesNotStandByARejectedVerdict(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the split.")
	fail := post(t, store, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "Each pays is $45.00, not $53.10."})
	post(t, store, session.Message{From: session.Human, Kind: session.Dispute, ReplyTo: fail.Seq, Text: "that is the right total"})
	if got := store.Verdict().Status; got != session.Rejected {
		t.Fatalf("status = %q, want rejected", got)
	}
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	model := &scriptedModel{replies: []string{
		declared("it"),
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "A human is driving."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	postTask(t, store, "Check it again.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	last := lastMessage(t, store)
	if res.Ended != session.Question || strings.Contains(last.Text, "verdict still stands") {
		t.Fatalf("ended %q with %q, want a question that does not claim the rejected verdict stands", res.Ended, last.Text)
	}
}

// Once the human gives the screen back mid-turn, an inconclusive is the model's own verdict.
func TestAnInconclusiveAfterTheScreenIsGivenBackIsAVerdict(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "true"}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "The build crashed on launch."}),
	}}
	model.onReasoning = func(n int) {
		switch n {
		case 1: // taken while the verifier works
			if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
				t.Errorf("TakeControl: %v", err)
			}
			if _, err := store.Append(session.Message{From: session.System, Kind: session.Event,
				Text: "human took control of the screen", Control: session.ControlTaken}); err != nil {
				t.Errorf("append: %v", err)
			}
		case 2:
			if _, _, err := mgr.ReleaseControl(runID, "human"); err != nil {
				t.Errorf("ReleaseControl: %v", err)
			}
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	postTask(t, store, "Launch it.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Verdict(); res.Ended != session.Verdict || got.Verdict != "inconclusive" || got.Summary != "The build crashed on launch." {
		t.Fatalf("ended %q with verdict %+v, want the model's inconclusive posted", res.Ended, got)
	}
}

// The coding agent holding the screen is asked to finish, not told to press Give Back.
func TestTheQuestionNamesTheCodingAgentWhenItHoldsTheScreen(t *testing.T) {
	mgr, runID, _ := ready(t)
	if _, _, err := mgr.TakeControl(runID, machine.HolderCoder, 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	model := &scriptedModel{replies: []string{
		declared("it"),
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click it.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	last := lastMessage(t, store)
	if res.Ended != session.Question || !strings.Contains(last.Text, "coding agent") || strings.Contains(last.Text, "Give Back") {
		t.Fatalf("ended %q with %q, want a question addressed to the coding agent", res.Ended, last.Text)
	}
}
