package main

import (
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

// A web page can reach 127.0.0.1 through DNS rebinding (foreign Host) or a cross-site request
// (foreign Origin). Every route must refuse both, and still serve a native client.
func TestRoutesRefuseWhatAWebPageCanSend(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(routes(mgr, session.NewRegistry(mgr.Root, 2), "img", log))
	t.Cleanup(ts.Close)

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	for _, tc := range []struct {
		method, path, host, origin, body string
		want                             int
	}{
		{"POST", "/mcp", "", "", initialize, http.StatusOK},
		{"POST", "/mcp", "evil.example:7777", "", initialize, http.StatusForbidden},
		{"POST", "/mcp", "", "https://evil.example", initialize, http.StatusForbidden},
		{"GET", "/api/runs", "", "", "", http.StatusOK},
		{"GET", "/api/runs", "evil.example", "", "", http.StatusForbidden},
		{"POST", "/api/runs/x/destroy", "", "https://evil.example", "", http.StatusForbidden},
		{"GET", "/healthz", "rebound.example:7777", "", "", http.StatusForbidden},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			req.Host = tc.host
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s %s Host=%q Origin=%q: %d, want %d", tc.method, tc.path, tc.host, tc.origin, res.StatusCode, tc.want)
		}
	}
}

// Without a verifier every message that starts a turn is told nobody will answer it, not only
// the task, or a client waits for ever on a later human note or coder dispute (#79).
func TestNoVerifierAnswersEveryTurnWithANotice(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	reg := session.NewRegistry(mgr.Root, 2)
	bridgeLifecycle(mgr, reg, false)
	if err := os.MkdirAll(filepath.Join(mgr.Root, "runs", "r1"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := reg.Get("r1")
	if err != nil {
		t.Fatal(err)
	}
	notices := func() []string {
		var out []string
		for _, m := range store.After(0) {
			if m.From == session.System && strings.HasPrefix(m.Text, noVerifierNotice) {
				out = append(out, strings.TrimPrefix(m.Text, noVerifierNotice))
			}
		}
		return out
	}
	waitFor := func(want []string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for strings.Join(notices(), ",") != strings.Join(want, ",") {
			if time.Now().After(deadline) {
				t.Fatalf("notices %q, want %q", notices(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	appendMsg := func(m session.Message) session.Message {
		t.Helper()
		out, err := store.Append(m)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	appendMsg(session.Message{From: session.Coder, Kind: session.Task, Text: "build"})
	waitFor([]string{"task"})
	appendMsg(session.Message{From: session.Human, Kind: session.Note, Text: "is it done?"})
	waitFor([]string{"task", "note"})
	appendMsg(session.Message{From: session.Coder, Kind: session.Note, Text: "context only"})
	verdict := appendMsg(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "wrong scheme"})
	appendMsg(session.Message{From: session.Coder, Kind: session.Dispute, ReplyTo: verdict.Seq, Text: "you used Debug"})
	waitFor([]string{"task", "note", "dispute"})
}
