package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/api"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/report"
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
	ts := httptest.NewServer(routes(mgr, session.NewRegistry(mgr.Root, 2), "img", "", "", t.TempDir(), api.Version{}, report.Models{}, log))
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

// Tunnel traffic reaches /mcp from 127.0.0.1 with the public Host. The SDK's own DNS-rebinding
// check refuses exactly that, so with a public host api.Guard must be the only Host check, or
// every remote agent gets 403 at initialize while /api and /healthz work.
func TestRoutesServeMCPToThePublicHostWithTheToken(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	const public, token = "gr.example.com", "0123456789abcdef0123456789abcdef"
	ts := httptest.NewServer(routes(mgr, session.NewRegistry(mgr.Root, 2), "img", public, token, t.TempDir(), api.Version{}, report.Models{}, log))
	t.Cleanup(ts.Close)

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	for _, tc := range []struct {
		host, auth string
		want       int
	}{
		{public, "Bearer " + token, http.StatusOK},
		{public, "", http.StatusUnauthorized},
		{"", "", http.StatusOK},
		{"evil.example", "Bearer " + token, http.StatusForbidden},
	} {
		req, err := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(initialize))
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			req.Host = tc.host
		}
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("POST /mcp Host=%q auth=%t: %d, want %d (%s)", tc.host, tc.auth != "", res.StatusCode, tc.want, body)
		}
	}
}

// ADR 0021: the daemon serves its own client installer, to loopback and to the public host
// without a token, and nothing else in <root>/dist's neighbourhood.
func TestRoutesServeTheInstallFiles(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	dist := t.TempDir()
	for name, body := range map[string]string{"install.sh": "#!/bin/sh\necho hi\n", "Greenroom.zip": "PK", "greenroom": "\xcf\xfa\xed\xfe"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dist, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	const public, token = "gr.example.com", "0123456789abcdef0123456789abcdef"
	ts := httptest.NewServer(routes(mgr, session.NewRegistry(mgr.Root, 2), "img", public, token, dist, api.Version{}, report.Models{}, log))
	t.Cleanup(ts.Close)

	for _, tc := range []struct {
		method, path, host string
		want               int
		contentType, body  string
	}{
		{"GET", "/install.sh", "", http.StatusOK, "text/x-shellscript; charset=utf-8", "#!/bin/sh\necho hi\n"},
		{"GET", "/install.sh", public, http.StatusOK, "text/x-shellscript; charset=utf-8", "#!/bin/sh\necho hi\n"},
		{"HEAD", "/install.sh", public, http.StatusOK, "text/x-shellscript; charset=utf-8", ""},
		{"GET", "/dl/Greenroom.zip", public, http.StatusOK, "application/zip", "PK"},
		{"GET", "/dl/greenroom", public, http.StatusOK, "application/octet-stream", "\xcf\xfa\xed\xfe"},
		{"GET", "/dl/missing", public, http.StatusNotFound, "", ""},
		{"GET", "/dl/sub", public, http.StatusNotFound, "", ""},
		{"GET", "/dl/..%2finstall.sh", "", http.StatusNotFound, "", ""},
		{"POST", "/install.sh", public, http.StatusUnauthorized, "", ""},
		{"GET", "/healthz", public, http.StatusUnauthorized, "", ""},
		{"GET", "/api/runs", public, http.StatusUnauthorized, "", ""},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			req.Host = tc.host
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s %s Host=%q: %d, want %d (%s)", tc.method, tc.path, tc.host, res.StatusCode, tc.want, body)
			continue
		}
		if tc.want != http.StatusOK {
			continue
		}
		if got := res.Header.Get("Content-Type"); got != tc.contentType {
			t.Errorf("%s %s: Content-Type %q, want %q", tc.method, tc.path, got, tc.contentType)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s %s: Cache-Control %q, want no-store", tc.method, tc.path, got)
		}
		if string(body) != tc.body {
			t.Errorf("%s %s: body %q, want %q", tc.method, tc.path, body, tc.body)
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

// ADR 0033: the daemon says which build it is, behind Guard like every route (the public host
// needs the token), and offers no way to update it: only GET answers.
func TestRoutesServeTheVersionReadOnly(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	const public, token = "gr.example.com", "0123456789abcdef0123456789abcdef"
	ver := buildVersion()
	ver.Checkout = "/src/greenroom"
	ts := httptest.NewServer(routes(mgr, session.NewRegistry(mgr.Root, 2), "img", public, token, t.TempDir(), ver, report.Models{}, log))
	t.Cleanup(ts.Close)

	for _, tc := range []struct {
		method, host, auth, origin string
		want                       int
	}{
		{"GET", "", "", "", http.StatusOK},
		{"GET", public, "Bearer " + token, "", http.StatusOK},
		{"GET", public, "", "", http.StatusUnauthorized},
		{"GET", public, "Bearer wrong-token-wrong-token-wrong-token", "", http.StatusUnauthorized},
		{"GET", "", "", "https://evil.example", http.StatusForbidden},
		{"GET", "evil.example", "", "", http.StatusForbidden},
		{"POST", "", "", "", http.StatusNotFound}, // no route: /api/ answers it
		{"POST", public, "Bearer " + token, "", http.StatusNotFound},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+"/api/version", nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			req.Host = tc.host
		}
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s /api/version Host=%q auth=%t Origin=%q: %d, want %d (%s)", tc.method, tc.host, tc.auth != "", tc.origin, res.StatusCode, tc.want, body)
			continue
		}
		if res.StatusCode != http.StatusOK {
			continue
		}
		var got api.Version
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Checkout != "/src/greenroom" || got.Verifier != "none" || got.InputHelper != machine.InputHelperVersion() ||
			got.ImageRecipe != machine.ImageRecipeVersion() || got.Version == "" {
			t.Errorf("version = %+v", got)
		}
	}
}

// The checkout comes from the environment install.sh writes into the launchd job.
func TestTheVersionNamesTheRecordedCheckout(t *testing.T) {
	t.Setenv("GREENROOM_CHECKOUT", " /src/greenroom \n")
	if got := buildVersion().Checkout; got != "/src/greenroom" {
		t.Fatalf("checkout %q", got)
	}
	t.Setenv("GREENROOM_CHECKOUT", "")
	if got := buildVersion().Checkout; got != "" {
		t.Fatalf("checkout %q with none recorded", got)
	}
}
