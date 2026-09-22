package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
