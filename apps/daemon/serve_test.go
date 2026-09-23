package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pendingRoot is a root holding one undestroyed run whose last message, a human note, has
// no reply yet: any verifier that starts on this root answers it at once.
func pendingRoot(t *testing.T) (root, conversation string) {
	t.Helper()
	root = t.TempDir()
	run := filepath.Join(root, "runs", "20260923-000000-qa00000000000063")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"runId":"20260923-000000-qa00000000000063","image":"x","createdAt":"2026-09-23T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(run, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	conversation = filepath.Join(run, "conversation.jsonl")
	note := `{"seq":1,"at":"2026-09-23T00:00:01Z","from":"human","kind":"note","text":"screenshot"}` + "\n"
	if err := os.WriteFile(conversation, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, conversation
}

// serveAsync runs serve and returns its error, or fails the test if it is still serving after d.
func serveAsync(t *testing.T, d time.Duration, args ...string) error {
	t.Helper()
	args = append(args, "-tart", "/usr/bin/false", "-verifier", "manual", "-open-viewer=false",
		"-env-file", filepath.Join(t.TempDir(), "none.env"))
	errCh := make(chan error, 1)
	go func() { errCh <- serve(args) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(d):
		t.Fatalf("serve %v is still running after %s; it should have refused to start", args, d)
		return nil
	}
}

func assertUntouched(t *testing.T, conversation string) {
	t.Helper()
	time.Sleep(300 * time.Millisecond) // an actor started by mistake answers within milliseconds
	b, err := os.ReadFile(conversation)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("a daemon that never served wrote into the transcript (%d lines):\n%s", n, b)
	}
}

// Issue #63: a daemon that cannot bind its address must fail before it starts a verifier, or
// it answers live runs behind the running daemon's back and duplicates seq numbers.
func TestServeThatCannotBindTouchesNothing(t *testing.T) {
	root, conversation := pendingRoot(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = busy.Close() })

	err = serveAsync(t, 5*time.Second, "-addr", busy.Addr().String(), "-root", root)
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("serve on a taken address = %v, want a bind error", err)
	}
	assertUntouched(t, conversation)
	if _, err := os.Stat(filepath.Join(root, "state.json")); err == nil {
		t.Fatal("a daemon that never served wrote state.json")
	}
}

// Issue #63: two daemons on one root, even on different ports, would share its machines and transcripts.
func TestServeRefusesARootAnotherDaemonHolds(t *testing.T) {
	root, conversation := pendingRoot(t)
	release, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	err = serveAsync(t, 5*time.Second, "-addr", "127.0.0.1:0", "-root", root)
	if err == nil || !strings.Contains(err.Error(), "another greenroom daemon") || !strings.Contains(err.Error(), root) {
		t.Fatalf("serve on a locked root = %v, want it refused naming the root", err)
	}
	assertUntouched(t, conversation)
}

func TestLockRootIsReleasedForTheNextDaemon(t *testing.T) {
	root := t.TempDir()
	release, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockRoot(root); err == nil {
		t.Fatal("a second lock on the same root succeeded")
	}
	release()
	again, err := lockRoot(root)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}
