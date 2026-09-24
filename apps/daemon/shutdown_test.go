package main

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// Issue #98: with a Companion's event stream open, a stop waited the full 5 s shutdown timeout,
// exited "context deadline exceeded", and a restart in that window was refused by the root lock.
func TestAStopWithAnEventStreamOpenIsQuickAndCleanAndFreesTheRoot(t *testing.T) {
	root := t.TempDir()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	args := []string{"-addr", addr, "-root", root, "-tart", "/usr/bin/false", "-verifier", "manual",
		"-env-file", filepath.Join(t.TempDir(), "none.env")}

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveUntil(ctx, args) }()

	var stream *http.Response
	for deadline := time.Now().Add(5 * time.Second); ; {
		stream, err = http.Get("http://" + addr + "/api/events")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never served: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = stream.Body.Close() }()

	stopped := time.Now()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v, want a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return")
	}
	if took := time.Since(stopped); took > 2*time.Second {
		t.Errorf("the stop took %s with an event stream open, want under 2 s", took)
	}
	release, err := lockRoot(root)
	if err != nil {
		t.Fatalf("the root is still locked after the stop: %v", err)
	}
	release()
}
