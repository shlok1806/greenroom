package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

type sendResult struct {
	Seq int       `json:"seq"`
	At  time.Time `json:"at"`
}

type transcriptResult struct {
	Messages []session.Message    `json:"messages"`
	Last     int                  `json:"last"`
	Verdict  session.VerdictState `json:"verdict"`
}

// store opens the run's conversation so a test can play the verifier or a human.
func (h *harness) store(runID string) *session.Store {
	h.t.Helper()
	s, err := h.reg.Get(runID)
	if err != nil {
		h.t.Fatalf("open conversation: %v", err)
	}
	return s
}

func TestAgentSendTaskThenTranscriptThenAnEmptyWait(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	var sent sendResult
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Build the app and check it launches."}, &sent)
	if sent.Seq != 1 {
		t.Errorf("seq = %d, want 1 for the first message", sent.Seq)
	}
	if sent.At.IsZero() {
		t.Error("the message came back without a timestamp")
	}

	var tr transcriptResult
	h.call("agent_transcript", map[string]any{"runId": runID}, &tr)
	if len(tr.Messages) != 1 || tr.Messages[0].Kind != session.Task || tr.Messages[0].From != session.Coder {
		t.Fatalf("transcript = %+v, want one task from the coder", tr.Messages)
	}
	if tr.Last != 1 || tr.Verdict.Status != session.None {
		t.Errorf("last = %d, verdict = %+v, want 1 and no verdict", tr.Last, tr.Verdict)
	}

	// Nothing answers, so the wait times out empty rather than failing.
	started := time.Now()
	var waited transcriptResult
	h.call("agent_wait", map[string]any{"runId": runID, "after": 1, "timeoutSeconds": 1}, &waited)
	if len(waited.Messages) != 0 {
		t.Errorf("wait returned %+v, want nothing", waited.Messages)
	}
	if waited.Last != 1 {
		t.Errorf("last = %d, want 1", waited.Last)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Errorf("the wait took %v, want about a second", elapsed)
	}
}

func TestAgentWaitUnblocksWhenTheVerifierSpeaks(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Build it."}, nil)

	store := h.store(runID)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = store.Append(session.Message{From: session.Verifier, Kind: session.Question, Text: "Which scheme should I build?"})
	}()

	started := time.Now()
	var got transcriptResult
	h.call("agent_wait", map[string]any{"runId": runID, "after": 1, "timeoutSeconds": 30}, &got)
	if len(got.Messages) != 1 || got.Messages[0].Kind != session.Question {
		t.Fatalf("wait returned %+v, want the verifier's question", got.Messages)
	}
	if got.Last != 2 {
		t.Errorf("last = %d, want 2", got.Last)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Errorf("the wait blocked for %v although a message arrived at once", elapsed)
	}

	var sent sendResult
	h.call("agent_send", map[string]any{"runId": runID, "kind": "answer", "text": "The Debug scheme.", "replyTo": got.Messages[0].Seq}, &sent)
	if sent.Seq != 3 {
		t.Errorf("seq = %d, want 3", sent.Seq)
	}
}

// Issue #46: agent_wait returned on the first new message, so every verifier progress line cost the
// coding agent a whole turn. Progress alone keeps it waiting; the batch comes back when the turn ends.
func TestAgentWaitGathersProgressUntilTheTurnEnds(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Check the window title."}, nil)

	store := h.store(runID)
	go func() {
		for _, m := range []session.Message{
			{From: session.Verifier, Kind: session.Progress, Text: "machine_ui {}"},
			{From: session.Verifier, Kind: session.Progress, Text: "machine_click {}"},
			{From: session.Verifier, Kind: session.Verdict, Verdict: "pass", Text: "the title is right"},
		} {
			time.Sleep(100 * time.Millisecond)
			_, _ = store.Append(m)
		}
	}()

	var got transcriptResult
	h.call("agent_wait", map[string]any{"runId": runID, "after": 1, "timeoutSeconds": 20}, &got)
	if len(got.Messages) != 3 || got.Messages[2].Kind != session.Verdict || got.Last != 4 {
		t.Fatalf("wait returned %d messages ending at %d, want both progress lines and the verdict in one call: %+v",
			len(got.Messages), got.Last, got.Messages)
	}
}

// Progress with no end still comes back at the timeout, so a caller sees the turn is alive.
func TestAgentWaitReturnsGatheredProgressAtTheTimeout(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Build it."}, nil)
	if _, err := h.store(runID).Append(session.Message{From: session.Verifier, Kind: session.Progress, Text: "machine_exec {}"}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var got transcriptResult
	h.call("agent_wait", map[string]any{"runId": runID, "after": 1, "timeoutSeconds": 1}, &got)
	if len(got.Messages) != 1 || got.Messages[0].Kind != session.Progress || got.Last != 2 {
		t.Fatalf("wait returned %+v, want the progress line", got.Messages)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond {
		t.Errorf("the wait returned after %v on a progress line; it should keep waiting for the turn to end", elapsed)
	}
}

func TestAgentSendRejectsRepliesThatPointNowhere(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Build it."}, nil)

	res := h.raw("agent_send", map[string]any{"runId": runID, "kind": "answer", "text": "The Debug scheme."})
	if !res.IsError {
		t.Fatal("an answer with no replyTo was accepted")
	}
	if !strings.Contains(text(res), "replies to") {
		t.Errorf("the error does not say what is missing: %q", text(res))
	}

	// The task is not a verdict, so accepting it must be refused by seq.
	res = h.raw("agent_send", map[string]any{"runId": runID, "kind": "accept", "replyTo": 1})
	if !res.IsError {
		t.Fatal("accept of a task was accepted")
	}
	if !strings.Contains(text(res), "not a verdict") {
		t.Errorf("the error does not say what message 1 is: %q", text(res))
	}
}

func TestTheThirdDisputeIsRefusedAsContested(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Build it."}, nil)
	store := h.store(runID)

	propose := func() int {
		t.Helper()
		m, err := store.Append(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "The build failed."})
		if err != nil {
			t.Fatalf("append verdict: %v", err)
		}
		return m.Seq
	}

	// Two disputes are the coder's budget.
	for i := 0; i < 2; i++ {
		seq := propose()
		h.call("agent_send", map[string]any{"runId": runID, "kind": "dispute", "text": "You built the wrong scheme.", "replyTo": seq}, nil)
	}
	seq := propose()
	res := h.raw("agent_send", map[string]any{"runId": runID, "kind": "dispute", "text": "Still wrong.", "replyTo": seq})
	if !res.IsError {
		t.Fatal("a third dispute was accepted")
	}
	if !strings.Contains(text(res), session.ErrContested.Error()) {
		t.Errorf("the coder is not told the verdict is contested: %q", text(res))
	}

	var tr transcriptResult
	h.call("agent_transcript", map[string]any{"runId": runID}, &tr)
	if tr.Verdict.Status != session.Contested {
		t.Errorf("status = %q, want contested", tr.Verdict.Status)
	}
	if tr.Verdict.Disputes != 2 {
		t.Errorf("disputes = %d, want 2", tr.Verdict.Disputes)
	}
}

func TestDestroyIsAnnouncedByTheLifecycleBridge(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	// machine_destroy posts nothing itself; subscribe the way main.go's lifecycle bridge does.
	store := h.store(runID)
	posted := make(chan struct{})
	stop := h.mgr.Listen(func(ev machine.LifecycleEvent) {
		if ev.Kind != "destroyed" {
			return
		}
		go func() {
			_, _ = store.Append(session.Message{From: session.System, Kind: session.Event, Text: "machine destroyed"})
			close(posted)
		}()
	})
	defer stop()

	before := store.Len()
	h.call("machine_destroy", map[string]any{"runId": runID}, nil)
	select {
	case <-posted:
	case <-time.After(5 * time.Second):
		t.Fatal("the manager never announced the destroy")
	}

	var tr transcriptResult
	h.call("agent_transcript", map[string]any{"runId": runID, "after": before}, &tr)
	found := false
	for _, m := range tr.Messages {
		if m.Kind == session.Event && m.From == session.System && m.Text == "machine destroyed" {
			found = true
		}
	}
	if !found {
		t.Errorf("the destroy is not in the conversation: %+v", tr.Messages)
	}
	if want := tr.Messages[len(tr.Messages)-1].Seq; tr.Last != want {
		t.Errorf("last = %d, want the seq of the last message, %d", tr.Last, want)
	}
}

func TestAgentToolsRefuseARunIdThatLeavesRuns(t *testing.T) {
	h := newHarness(t)
	// Real directories where "." and "../x" would land, so only validation can refuse them.
	for _, dir := range []string{filepath.Join(h.root, "runs"), filepath.Join(h.root, "x")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, runID := range []string{"../x", ".", "..", `..\x`, "a/b"} {
		for tool, args := range map[string]map[string]any{
			"agent_send":       {"runId": runID, "kind": "task", "text": "escape"},
			"agent_wait":       {"runId": runID, "timeoutSeconds": 1},
			"agent_transcript": {"runId": runID},
		} {
			if res := h.raw(tool, args); !res.IsError {
				t.Errorf("%s accepted runId %q", tool, runID)
			}
		}
	}
	err := filepath.WalkDir(h.root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "conversation.jsonl" {
			t.Errorf("a conversation was written at %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
