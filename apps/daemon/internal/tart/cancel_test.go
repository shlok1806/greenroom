package tart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cancelled exec gets SIGINT first, which tart turns into a clean close of its gRPC call
// (issue #186, daemon ADR 0002). It does not stop the control socket's fd leak.
func TestACancelledExecIsInterruptedBeforeItIsKilled(t *testing.T) {
	marks := filepath.Join(t.TempDir(), "marks")
	c, _ := argsBin(t, `trap 'echo interrupted >> '`+marks+`'; exit 130' INT
echo started >> '`+marks+`'
while :; do sleep 0.05; done`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Exec(ctx, "vm", "true")
		done <- err
	}()
	waitForFile(t, marks, "started")
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want the context's error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Exec did not return after its context was cancelled")
	}
	if took := time.Since(start); took >= execInterruptWait {
		t.Errorf("Exec took %s to return: tart was killed, not interrupted", took)
	}
	if got, _ := os.ReadFile(marks); !strings.Contains(string(got), "interrupted") {
		t.Errorf("tart never saw SIGINT: %q", got)
	}
}

// A tart that ignores SIGINT is still killed, execInterruptWait later.
func TestACancelledExecThatIgnoresTheInterruptIsKilled(t *testing.T) {
	orig := execInterruptWait
	execInterruptWait = 200 * time.Millisecond
	t.Cleanup(func() { execInterruptWait = orig })
	marks := filepath.Join(t.TempDir(), "marks")
	// The ignored SIGINT survives exec, so sleep ignores it too.
	c, _ := argsBin(t, `trap '' INT; echo started >> '`+marks+`'; exec sleep 30`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Exec(ctx, "vm", "true")
		done <- err
	}()
	waitForFile(t, marks, "started")
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want the context's error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a tart that ignores SIGINT was never killed")
	}
	if took := time.Since(start); took < execInterruptWait {
		t.Errorf("Exec returned after %s, before the interrupt wait of %s", took, execInterruptWait)
	}
}

func waitForFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got, _ := os.ReadFile(path); strings.Contains(string(got), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never said %q", path, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
