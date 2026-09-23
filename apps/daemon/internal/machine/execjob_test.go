package machine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
