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
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// scriptedModel is a chat endpoint that replays a list of replies. It records
// every request so a test can assert what the loop sent.
type scriptedModel struct {
	mu       sync.Mutex
	replies  []string // raw JSON bodies, one per call
	requests []map[string]any
	vision   string
	visions  int
}

func (s *scriptedModel) start(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		s.mu.Lock()
		defer s.mu.Unlock()
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
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"the test ran out of scripted replies"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, s.replies[n])
	}))
	t.Cleanup(ts.Close)
	return ts.URL
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
		machine.WithTartBin(bin), machine.WithReadyTimeout(10*time.Second),
		machine.WithSSHProbe(func(context.Context, string) error { return nil }))
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

func TestNewRequiresAKeyAndAModel(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(nil, Config{Model: "m"}, log); err == nil {
		t.Error("New accepted an empty API key")
	}
	if _, err := New(nil, Config{APIKey: "k"}, log); err == nil {
		t.Error("New accepted an empty model")
	}
}

func TestVerifierRunsCommandsThenReportsAVerdict(t *testing.T) {
	mgr, runID, control := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "swift build"}),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "The build succeeded and the app launched."}),
	}}
	v := newVerifier(t, mgr, model.start(t))

	rep, err := v.Run(context.Background(), runID, "Build the app and say whether it works.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass", rep.Verdict)
	}
	if !strings.Contains(rep.Summary, "build succeeded") {
		t.Errorf("summary = %q", rep.Summary)
	}
	if rep.Steps != 2 {
		t.Errorf("steps = %d, want 2", rep.Steps)
	}
	if rep.Tokens == 0 {
		t.Error("the report counts no tokens")
	}
	if rep.Seconds < 0 {
		t.Errorf("seconds = %v", rep.Seconds)
	}
	// The command must have reached the guest.
	if !strings.Contains(testsupport.Calls(t, control), "swift build") {
		t.Error("the command never reached the machine")
	}
	// The second request must carry the tool result.
	if len(model.requests) < 2 {
		t.Fatalf("the model was called %d times, want 2", len(model.requests))
	}
	second, _ := json.Marshal(model.requests[1])
	if !strings.Contains(string(second), "exit code") {
		t.Errorf("the tool result never reached the model: %s", truncateFor(string(second)))
	}
}

func TestVerifierDescribesAScreenshotForABlindModel(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	model := &scriptedModel{
		vision: "Safari is frontmost showing github.com. A dialog covers the page: Your computer was restarted.",
		replies: []string{
			toolCall("machine_screenshot", map[string]any{}),
			toolCall("report_verdict", map[string]any{"verdict": "fail", "summary": "A system dialog covered the app."}),
		},
	}
	v := newVerifier(t, mgr, model.start(t))

	rep, err := v.Run(context.Background(), runID, "Look at the screen.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if model.visions != 1 {
		t.Errorf("the vision model was called %d times, want 1", model.visions)
	}
	if len(rep.Evidence) != 1 || !strings.HasSuffix(rep.Evidence[0], ".png") {
		t.Errorf("evidence = %v, want one png path", rep.Evidence)
	}
	if _, err := os.Stat(rep.Evidence[0]); err != nil {
		t.Errorf("the evidence file does not exist: %v", err)
	}
	// The description, not the image, must reach the reasoning model.
	second, _ := json.Marshal(model.requests[len(model.requests)-1])
	if !strings.Contains(string(second), "Safari is frontmost") {
		t.Error("the screen description never reached the reasoning model")
	}
	if strings.Contains(string(second), "image_url") {
		t.Error("an image was sent to the reasoning model, which cannot accept one")
	}
}

func TestVerifierKeepsGoingWhenTheEyesFail(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	// No vision model configured, so describing must fail.
	v, err := New(mgr, Config{
		BaseURL: (&scriptedModel{replies: []string{
			toolCall("machine_screenshot", map[string]any{}),
			toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "Could not see the screen."}),
		}}).start(t), APIKey: "k", Model: "reasoner", MaxSteps: 4, Budget: 30 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := v.Run(context.Background(), runID, "Look at the screen.")
	if err != nil {
		t.Fatalf("a blind verifier must still finish: %v", err)
	}
	if rep.Verdict != "inconclusive" {
		t.Errorf("verdict = %q, want inconclusive", rep.Verdict)
	}
	if len(rep.Evidence) != 1 {
		t.Errorf("the screenshot must still be kept as evidence, got %v", rep.Evidence)
	}
}

func TestVerifierAsksForAVerdictWhenTheModelAnswersInProse(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		prose("It looks fine to me."),
		toolCall("report_verdict", map[string]any{"verdict": "pass", "summary": "Fine."}),
	}}
	v := newVerifier(t, mgr, model.start(t))

	rep, err := v.Run(context.Background(), runID, "Check it.")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass after the nudge", rep.Verdict)
	}
	last, _ := json.Marshal(model.requests[1])
	if !strings.Contains(string(last), "report_verdict now") {
		t.Error("the loop never asked the model for a verdict")
	}
}

func TestVerifierStopsAtTheStepLimit(t *testing.T) {
	mgr, runID, _ := ready(t)
	replies := make([]string, 8)
	for i := range replies {
		replies[i] = toolCall("machine_exec", map[string]any{"command": "echo again"})
	}
	model := &scriptedModel{replies: replies}
	v := newVerifier(t, mgr, model.start(t)) // MaxSteps 6

	rep, err := v.Run(context.Background(), runID, "Loop forever.")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Steps != 6 {
		t.Errorf("steps = %d, want the limit of 6", rep.Steps)
	}
	if rep.Verdict != "inconclusive" {
		t.Errorf("verdict = %q, want inconclusive", rep.Verdict)
	}
	if !strings.Contains(rep.Summary, "without reporting a verdict") {
		t.Errorf("summary = %q", rep.Summary)
	}
}

func TestVerifierReportsAnEndpointFailure(t *testing.T) {
	mgr, runID, _ := ready(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer ts.Close()
	v := newVerifier(t, mgr, ts.URL)

	_, err := v.Run(context.Background(), runID, "Anything.")
	if err == nil {
		t.Fatal("Run returned no error although the endpoint rejected the key")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to carry the status", err)
	}
}

func TestUnknownToolIsReportedToTheModel(t *testing.T) {
	mgr, runID, _ := ready(t)
	model := &scriptedModel{replies: []string{
		toolCall("delete_everything", map[string]any{}),
		toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "No such tool."}),
	}}
	v := newVerifier(t, mgr, model.start(t))
	if _, err := v.Run(context.Background(), runID, "Try a tool that does not exist."); err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(model.requests[1])
	if !strings.Contains(string(second), "no tool named delete_everything") {
		t.Error("the loop did not tell the model that the tool does not exist")
	}
}

func TestParseVerdictFallsBackToInconclusive(t *testing.T) {
	for _, args := range []string{`{"verdict":"maybe","summary":"x"}`, `{}`, `not json`, `{"verdict":"PASS","summary":"y"}`} {
		v, _ := parseVerdict(args)
		switch args {
		case `{"verdict":"PASS","summary":"y"}`:
			if v != "pass" {
				t.Errorf("parseVerdict(%s) = %q, want pass", args, v)
			}
		default:
			if v != "inconclusive" {
				t.Errorf("parseVerdict(%s) = %q, want inconclusive", args, v)
			}
		}
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
