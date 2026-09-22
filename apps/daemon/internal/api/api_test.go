package api

import (
	"bufio"
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
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

type harness struct {
	t       *testing.T
	url     string
	mgr     *machine.Manager
	reg     *session.Registry
	control string
}

// newHarness serves the real API over HTTP with a fake tart behind it, the
// way the daemon does. No VM is involved.
//
// The frame recorder is off by default (extra can turn it back on): a test
// of the routes around it should not also, incidentally, be a test of it,
// racing frame capture subprocesses against every other test's assertions.
func newHarness(t *testing.T, extra ...machine.Option) *harness {
	t.Helper()
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := append([]machine.Option{
		machine.WithTartBin(bin), machine.WithReadyTimeout(10 * time.Second),
		machine.WithSSHProbe(func(context.Context, string, string) error { return nil }),
		machine.WithFrameInterval(0),
	}, extra...)
	mgr, err := machine.NewManager(root, log, opts...)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	reg := session.NewRegistry(mgr.Root, 2)
	ts := httptest.NewServer(New(mgr, reg, log))
	t.Cleanup(ts.Close)
	return &harness{t: t, url: ts.URL, mgr: mgr, reg: reg, control: control}
}

// create starts a machine and returns its run id while it is still booting.
func (h *harness) create() string {
	h.t.Helper()
	mc, err := h.mgr.Create(context.Background(), "ghcr.io/example/base:latest", false)
	if err != nil {
		h.t.Fatalf("create: %v", err)
	}
	return mc.RunID
}

func (h *harness) ready() string {
	h.t.Helper()
	runID := h.create()
	mc, err := h.mgr.Wait(context.Background(), runID, 10*time.Second)
	if err != nil {
		h.t.Fatalf("wait: %v", err)
	}
	if mc.Status != machine.Ready {
		h.t.Fatalf("machine is %s, want ready (error %q)", mc.Status, mc.Error)
	}
	return runID
}

func (h *harness) store(runID string) *session.Store {
	h.t.Helper()
	s, err := h.reg.Get(runID)
	if err != nil {
		h.t.Fatalf("open conversation: %v", err)
	}
	return s
}

// get calls the API and decodes a successful body into out.
func (h *harness) get(path string, out any) {
	h.t.Helper()
	res, body := h.do(http.MethodGet, path, nil)
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("GET %s: status %d: %s", path, res.StatusCode, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			h.t.Fatalf("GET %s: decode: %v (%s)", path, err, body)
		}
	}
}

func (h *harness) do(method, path string, in any) (*http.Response, []byte) {
	h.t.Helper()
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			h.t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, h.url+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return res, data
}

// status runs a request and returns only its status code and body.
func (h *harness) status(method, path string, in any) (int, string) {
	h.t.Helper()
	res, body := h.do(method, path, in)
	return res.StatusCode, string(body)
}

// setManifestSteps rewrites one run's manifest step count, which is how a
// daemon that died mid-run, or an older daemon, left it.
func (h *harness) setManifestSteps(runID string, steps int) {
	h.t.Helper()
	path := filepath.Join(h.mgr.RunDir(runID), "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	var man map[string]any
	if err := json.Unmarshal(data, &man); err != nil {
		h.t.Fatal(err)
	}
	man["steps"] = steps
	data, err = json.Marshal(man)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func findRun(t *testing.T, runs []RunSummary, runID string) RunSummary {
	t.Helper()
	for _, r := range runs {
		if r.RunID == runID {
			return r
		}
	}
	t.Fatalf("run %s is not in the list of %d", runID, len(runs))
	return RunSummary{}
}

func (h *harness) putShot() {
	h.t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	img.Set(2, 2, color.RGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		h.t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString(buf.Bytes())
	if err := os.WriteFile(filepath.Join(h.control, "shot.b64"), []byte(enc), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// --- runs ---

func TestRunsListFollowsTheMachineFromBootingToReady(t *testing.T) {
	h := newHarness(t)
	runID := h.create()

	var runs []RunSummary
	h.get("/api/runs", &runs)
	if len(runs) != 1 || runs[0].RunID != runID {
		t.Fatalf("runs = %+v, want the one run %s", runs, runID)
	}
	if runs[0].Status != string(machine.Booting) {
		t.Errorf("status = %q, want booting", runs[0].Status)
	}
	if runs[0].DestroyedAt != nil {
		t.Errorf("destroyedAt = %v, want null while the machine lives", runs[0].DestroyedAt)
	}
	if runs[0].Image == "" || runs[0].CreatedAt.IsZero() {
		t.Errorf("run = %+v, want an image and a creation time", runs[0])
	}

	if _, err := h.mgr.Wait(context.Background(), runID, 10*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	h.get("/api/runs", &runs)
	if runs[0].Status != string(machine.Ready) {
		t.Fatalf("status = %q, want ready", runs[0].Status)
	}
	if runs[0].IP == "" {
		t.Errorf("a ready machine has no ip: %+v", runs[0])
	}
	// Nothing has been said in the conversation, but the run has recorded
	// its boot, and that is activity: lastActivity follows the evidence.
	if runs[0].LastActivity.Before(runs[0].CreatedAt) {
		t.Errorf("lastActivity = %v, before the run was created at %v", runs[0].LastActivity, runs[0].CreatedAt)
	}
}

func TestRunDetailCarriesTheManifestTheMachineAndTheLiveVerdict(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)
	if _, err := store.Append(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "pass", Text: "the app builds"}); err != nil {
		t.Fatal(err)
	}

	var d RunDetail
	h.get("/api/runs/"+runID, &d)
	if d.RunID != runID || d.MachineName == "" {
		t.Fatalf("detail = %+v, want the run's manifest", d)
	}
	if d.Machine == nil || d.Machine.Status != machine.Ready {
		t.Fatalf("machine = %+v, want the live ready machine", d.Machine)
	}
	if d.Verdict == nil || d.Verdict.Verdict != "pass" || d.Verdict.Status != session.Proposed {
		t.Errorf("verdict = %+v, want the proposed pass from the conversation", d.Verdict)
	}
}

func TestStepsAreTheRunsEvidence(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.putShot()
	if _, _, err := h.mgr.Screenshot(context.Background(), runID); err != nil {
		t.Fatalf("screenshot: %v", err)
	}

	var steps []machine.Step
	h.get("/api/runs/"+runID+"/steps", &steps)
	if len(steps) == 0 || steps[len(steps)-1].Tool != "machine_screenshot" {
		t.Fatalf("steps = %+v, want the screenshot last", steps)
	}
}

// The list's step count is read from steps.jsonl, never from the manifest.
// A run was found reporting steps: 0 in the list while /steps answered with
// six records for the same run: the manifest's Steps is the highest number
// handed out, not a count, so a daemon stopped between begin and complete,
// or an older daemon that wiped the manifest on reattach, leaves the two
// apart for the life of the run. The evidence on disk is what the product
// sells, so the summary counts it.
func TestTheRunListCountsTheStepsOnDiskNotTheManifest(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	var steps []machine.Step
	h.get("/api/runs/"+runID+"/steps", &steps)
	if len(steps) == 0 {
		t.Fatal("the run recorded no steps, so this test cannot see the regression")
	}

	for _, manifestSteps := range []int{0, len(steps) + 40} {
		h.setManifestSteps(runID, manifestSteps)

		var runs []RunSummary
		h.get("/api/runs", &runs)
		got := findRun(t, runs, runID)
		if got.Steps != len(steps) {
			t.Errorf("the list says %d steps with a manifest claiming %d; /steps has %d",
				got.Steps, manifestSteps, len(steps))
		}
		var d RunDetail
		h.get("/api/runs/"+runID, &d)
		if d.Steps != len(steps) {
			t.Errorf("the detail says %d steps with a manifest claiming %d; /steps has %d",
				d.Steps, manifestSteps, len(steps))
		}
	}
}

// A run that nobody talks to is still working. lastActivity used to follow
// the conversation alone, so a run driven entirely by an agent was dated
// from its creation while its steps piled up, and the list read it as
// abandoned.
func TestLastActivityFollowsTheStepsAndNotJustTheConversation(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	// A step taken after the machine was ready: the create and boot steps are
	// both dated from the moment the run began, so neither of them can tell a
	// lastActivity that follows the steps from one that does not.
	h.putShot()
	if _, _, err := h.mgr.Screenshot(context.Background(), runID); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	var steps []machine.Step
	h.get("/api/runs/"+runID+"/steps", &steps)
	if len(steps) == 0 {
		t.Fatal("the run recorded no steps, so this test cannot see the regression")
	}
	last := steps[len(steps)-1].At

	var runs []RunSummary
	h.get("/api/runs", &runs)
	got := findRun(t, runs, runID)
	if got.LastActivity.Before(last) {
		t.Errorf("lastActivity is %v, behind the last step at %v", got.LastActivity, last)
	}
}

// A run whose conversation has reached no verdict answers with no verdict,
// in the list and in the detail alike. RunDetail.Verdict shadows the
// manifest's field of the same name, so the detail used to answer with an
// empty verdict object where the list answered null.
func TestARunWithNoVerdictSaysSoTheSameWayEverywhere(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	var runs []RunSummary
	h.get("/api/runs", &runs)
	if got := findRun(t, runs, runID); got.Verdict != nil {
		t.Errorf("the list invented a verdict: %+v", got.Verdict)
	}
	var d RunDetail
	h.get("/api/runs/"+runID, &d)
	if d.Verdict != nil {
		t.Errorf("the detail invented a verdict: %+v", d.Verdict)
	}
}

// --- the conversation ---

func TestMessagesReturnOnlyWhatIsAfterN(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)
	for _, text := range []string{"first", "second", "third"} {
		if _, err := store.Append(session.Message{From: session.Coder, Kind: session.Note, Text: text}); err != nil {
			t.Fatal(err)
		}
	}

	var all transcriptOut
	h.get("/api/runs/"+runID+"/messages", &all)
	if len(all.Messages) != 3 || all.Last != 3 {
		t.Fatalf("messages = %d, last = %d, want 3 and 3", len(all.Messages), all.Last)
	}

	var rest transcriptOut
	h.get("/api/runs/"+runID+"/messages?after=2", &rest)
	if len(rest.Messages) != 1 || rest.Messages[0].Text != "third" || rest.Last != 3 {
		t.Fatalf("after=2 gave %+v with last %d", rest.Messages, rest.Last)
	}

	var none transcriptOut
	h.get("/api/runs/"+runID+"/messages?after=3", &none)
	if len(none.Messages) != 0 || none.Last != 3 {
		t.Fatalf("after=3 gave %+v with last %d, want nothing and 3", none.Messages, none.Last)
	}
}

func TestAHumanNoteReachesSomeoneAlreadyWaiting(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)

	// This is agent_wait's side of the same store: the coder is blocked when
	// the human speaks from the app.
	got := make(chan []session.Message, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		got <- store.Wait(ctx, 0)
	}()
	time.Sleep(20 * time.Millisecond)

	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/messages",
		map[string]any{"kind": "note", "text": "the dialog is the GPU bug, ignore it"})
	if code != http.StatusCreated {
		t.Fatalf("post: status %d: %s", code, body)
	}

	select {
	case msgs := <-got:
		if len(msgs) != 1 || msgs[0].From != session.Human || msgs[0].Kind != session.Note {
			t.Fatalf("the waiter got %+v, want one note from the human", msgs)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiter never saw the human's note")
	}
}

func TestAnAnswerWithoutAReplyToIsRefused(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/messages",
		map[string]any{"kind": "answer", "text": "the scheme is Debug"})
	if code != http.StatusBadRequest {
		t.Fatalf("status %d: %s, want 400", code, body)
	}
	if !strings.Contains(body, "error") {
		t.Errorf("body = %s, want an error object", body)
	}
}

func TestAHumanClosesAContestedVerdict(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)
	// Two coder disputes exhaust the budget; the third verdict is contested
	// and only the human can close it (ADR 0006).
	for i := 0; i < 2; i++ {
		v, err := store.Append(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "the build broke"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Append(session.Message{From: session.Coder, Kind: session.Dispute, Text: "wrong command", ReplyTo: v.Seq}); err != nil {
			t.Fatal(err)
		}
	}
	v, err := store.Append(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "still broken"})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Verdict().Status; got != session.Contested {
		t.Fatalf("verdict status = %q, want contested", got)
	}

	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/messages",
		map[string]any{"kind": "dispute", "text": "it built here", "replyTo": v.Seq})
	if code != http.StatusCreated {
		t.Fatalf("status %d: %s, want 201: a human may close a contested verdict", code, body)
	}
	if got := store.Verdict().Status; got != session.Rejected {
		t.Errorf("verdict status = %q, want rejected after the human disputed it", got)
	}
}

// --- artifacts ---

func TestArtifactsServeARunFileAndRefuseEverythingElse(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.mgr.RunDir(runID), "001-screenshot.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	res, body := h.do(http.MethodGet, "/api/runs/"+runID+"/artifacts/001-screenshot.png", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type = %q, want image/png", ct)
	}
	if !bytes.Equal(body, buf.Bytes()) {
		t.Errorf("the bytes served are not the file on disk")
	}

	for _, name := range []string{"..%2Fx", "a/b", "..%2f..%2fstate.json"} {
		code, got := h.status(http.MethodGet, "/api/runs/"+runID+"/artifacts/"+name, nil)
		if code != http.StatusBadRequest {
			t.Errorf("artifact %q: status %d: %s, want 400", name, code, got)
		}
	}
	if code, _ := h.status(http.MethodGet, "/api/runs/"+runID+"/artifacts/nope.png", nil); code != http.StatusNotFound {
		t.Errorf("a missing artifact gave %d, want 404", code)
	}
}

// --- controls ---

func TestScreenshotRecordsAStepAndTellsTheConversation(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.putShot()
	store := h.store(runID)

	var out struct {
		Step  int    `json:"step"`
		Path  string `json:"path"`
		Bytes int    `json:"bytes"`
	}
	res, body := h.do(http.MethodPost, "/api/runs/"+runID+"/screenshot", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Step == 0 || out.Bytes == 0 || !strings.HasSuffix(out.Path, ".png") {
		t.Fatalf("screenshot = %+v, want a step, a png path and some bytes", out)
	}
	if _, err := os.Stat(out.Path); err != nil {
		t.Errorf("the png is not on disk: %v", err)
	}

	msgs := store.After(0)
	if len(msgs) != 1 || msgs[0].From != session.System || msgs[0].Kind != session.Event ||
		!strings.Contains(msgs[0].Text, "human took a screenshot") {
		t.Fatalf("conversation = %+v, want the screenshot event", msgs)
	}
}

func TestDestroyTellsTheConversationAndTheRunFinishes(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	store := h.store(runID)

	code, body := h.status(http.MethodPost, "/api/runs/"+runID+"/destroy", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	msgs := store.After(0)
	if len(msgs) != 1 || msgs[0].Text != "human destroyed the machine" {
		t.Fatalf("conversation = %+v, want the destroy event", msgs)
	}

	var runs []RunSummary
	h.get("/api/runs", &runs)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want the finished run still listed", runs)
	}
	if runs[0].Status != statusFinished {
		t.Errorf("status = %q, want finished", runs[0].Status)
	}
	if runs[0].DestroyedAt == nil {
		t.Errorf("destroyedAt is null on a destroyed run: %+v", runs[0])
	}
	if runs[0].Messages != 1 {
		t.Errorf("messages = %d, want 1", runs[0].Messages)
	}
}

func TestAnUnknownRunIs404Everywhere(t *testing.T) {
	h := newHarness(t)
	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/runs/nope", nil},
		{http.MethodGet, "/api/runs/nope/steps", nil},
		{http.MethodGet, "/api/runs/nope/messages", nil},
		{http.MethodGet, "/api/runs/nope/artifacts/shot.png", nil},
		{http.MethodPost, "/api/runs/nope/messages", map[string]any{"kind": "note", "text": "hello"}},
		{http.MethodPost, "/api/runs/nope/screenshot", nil},
		{http.MethodPost, "/api/runs/nope/destroy", nil},
	}
	for _, r := range routes {
		code, body := h.status(r.method, r.path, r.body)
		if code != http.StatusNotFound {
			t.Errorf("%s %s: status %d: %s, want 404", r.method, r.path, code, body)
		}
		var e struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error == "" {
			t.Errorf("%s %s: body = %s, want an error object", r.method, r.path, body)
		}
	}
}

// --- events ---

func TestEventStreamDeliversMessagesAndStepsThenEndsWithTheRequest(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.putShot()
	store := h.store(runID) // subscribe the registry to this run before listening

	old := Heartbeat
	Heartbeat = 50 * time.Millisecond
	t.Cleanup(func() { Heartbeat = old })

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/api/events?runId="+runID, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("cache control = %q, want no-cache", cc)
	}

	names := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				names <- name
			}
		}
	}()

	if _, err := store.Append(session.Message{From: session.Coder, Kind: session.Note, Text: "watch this"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.mgr.Screenshot(context.Background(), runID); err != nil {
		t.Fatalf("screenshot: %v", err)
	}

	want := map[string]bool{"message": false, "step": false}
	deadline := time.After(10 * time.Second)
	for !want["message"] || !want["step"] {
		select {
		case name := <-names:
			if _, ok := want[name]; ok {
				want[name] = true
			}
		case <-deadline:
			t.Fatalf("the stream delivered %v, want both a message and a step", want)
		}
	}

	// Cancelling the request must end the handler, not leak its goroutine.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end when the request was cancelled")
	}
}
