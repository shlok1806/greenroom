package machine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #29: however much a command prints, what is kept stays bounded, keeps
// the ends, and never splits a character.
func TestHeadTailKeepsTheEndsOfAHugeOutputInBoundedMemory(t *testing.T) {
	var h headTail
	chunk := []byte(strings.Repeat("é", 16*1024)) // 2-byte runes, so cuts can land mid-character
	_, _ = h.Write([]byte("START"))
	for i := 0; i < 400; i++ { // about 13 MB
		_, _ = h.Write(chunk)
		if c := cap(h.tail); c > 4*ExecTailLimit+len(chunk) {
			t.Fatalf("the tail buffer grew to %d bytes", c)
		}
	}
	_, _ = h.Write([]byte("END"))
	text, total, truncated := h.result()
	if want := int64(5 + 400*len(chunk) + 3); total != want || !truncated {
		t.Fatalf("total %d truncated %v, want %d and true", total, truncated, want)
	}
	if !strings.HasPrefix(text, "START") || !strings.HasSuffix(text, "END") || !utf8.ValidString(text) {
		t.Errorf("kept text lost an end or split a character: %.20q ... %.20q", text, text[len(text)-20:])
	}
	if len(text) > ExecHeadLimit+ExecTailLimit+100 {
		t.Errorf("kept %d bytes", len(text))
	}
}

func TestHeadTailKeepsAnOutputAtTheLimitWhole(t *testing.T) {
	for _, n := range []int{0, 1, ExecHeadLimit, ExecHeadLimit + ExecTailLimit} {
		var h headTail
		body := strings.Repeat("a", n)
		_, _ = h.Write([]byte(body))
		if text, total, truncated := h.result(); text != body || total != int64(n) || truncated {
			t.Errorf("%d bytes: kept %d, total %d, truncated %v; want all of it", n, len(text), total, truncated)
		}
	}
	var h headTail
	_, _ = h.Write([]byte(strings.Repeat("a", ExecHeadLimit+ExecTailLimit+1)))
	if _, _, truncated := h.result(); !truncated {
		t.Error("one byte over the limit is not marked truncated")
	}
}

// Destroying a machine ends a command still running on it, so no tart exec outlives it.
func TestDestroyEndsARunningCommand(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "exec-sleep"), []byte("30"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := mgr.ExecStart(context.Background(), mc.RunID, "sleep 30", "", time.Minute)
	if err != nil || !st.Running {
		t.Fatalf("ExecStart: %+v, %v", st, err)
	}
	live, err := mgr.get(mc.RunID) // mc is a snapshot without its handles
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.Lock()
	j := live.execs[st.ExecID]
	mgr.mu.Unlock()
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-j.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the command's tart exec outlived its machine")
	}
	if j.err == nil {
		t.Error("a command ended by destroy reported success")
	}
}

// A command still running when ExecWait's own wait elapses is exactly the shape of a stall
// on a system prompt (ADR 0038, issue #252): the exact Calculator repro blocks osascript on
// "tart-guest-agent wants access to control Calculator" with nothing to answer it. ExecWait
// takes a look and reports what is on screen, without touching it.
func TestExecWaitLooksAtTheScreenWhenACommandIsStillRunning(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "exec-sleep"), []byte("5"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "desktop.json"), []byte(promptAndTerminal), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := mgr.ExecStart(context.Background(), mc.RunID, "osascript -e 'tell application \"Calculator\" to activate'", "", time.Minute)
	if err != nil || !st.Running {
		t.Fatalf("ExecStart: %+v, %v", st, err)
	}
	got, err := mgr.ExecWait(context.Background(), mc.RunID, st.ExecID, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Running {
		t.Fatal("the command finished before its own sleep; the test cannot exercise the look")
	}
	if got.Desktop == nil || got.Desktop.Clean {
		t.Fatalf("a running command with a prompt on screen reported no desktop finding: %+v", got.Desktop)
	}
	if s := strings.Join(got.Desktop.Findings(), "\n"); !strings.Contains(s, "UserNotificationCenter") {
		t.Errorf("findings do not name the prompt: %s", s)
	}
	live, err := mgr.get(mc.RunID)
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.Lock()
	desktop := live.Desktop
	mgr.mu.Unlock()
	if desktop == nil || desktop.Clean {
		t.Error("the machine's own Desktop field was not updated, so machine_wait would not see it either")
	}
	if calls := testsupport.Calls(t, control); strings.Contains(calls, "pkill") || strings.Contains(calls, "killall") ||
		strings.Contains(calls, "to quit") {
		t.Errorf("the look closed or killed something instead of only reporting it:\n%s", calls)
	}
}

// A command that returns within its wait is not stalled, so the extra look never runs, and a
// clean desktop never sets Desktop even when it is checked.
func TestExecWaitDoesNotLookAtAFinishedCommand(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr) // boot's own desktop check already logged one --desktop read
	before := strings.Count(testsupport.Calls(t, control), "--desktop")

	st, err := mgr.ExecStart(context.Background(), mc.RunID, "echo hi", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.ExecWait(context.Background(), mc.RunID, st.ExecID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Running {
		t.Fatal("echo did not finish within a second")
	}
	if got.Desktop != nil {
		t.Errorf("a finished command carries a desktop finding: %+v", got.Desktop)
	}
	if after := strings.Count(testsupport.Calls(t, control), "--desktop"); after != before {
		t.Errorf("a command that finished within its wait was still looked at (%d --desktop reads before, %d after)", before, after)
	}
}
