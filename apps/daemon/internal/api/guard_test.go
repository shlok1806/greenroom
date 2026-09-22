package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalOnlyRefusesAForeignHost(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for host, want := range map[string]int{
		"127.0.0.1:7777":        http.StatusNoContent,
		"127.0.0.1":             http.StatusNoContent,
		"localhost:7777":        http.StatusNoContent,
		"[::1]:7777":            http.StatusNoContent,
		"[::1]":                 http.StatusNoContent,
		"127.0.0.2:7777":        http.StatusNoContent,
		"evil.example:7777":     http.StatusForbidden,
		"evil.example":          http.StatusForbidden,
		"localhost.evil.com":    http.StatusForbidden,
		"192.168.1.10:7777":     http.StatusForbidden,
		"0.0.0.0:7777":          http.StatusForbidden,
		"":                      http.StatusForbidden,
		"127.0.0.1.nip.io:7777": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		LocalOnly(ok).ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: status %d, want %d", host, rec.Code, want)
		}
	}
}

func TestLocalOnlyRefusesAForeignOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for origin, want := range map[string]int{
		"":                       http.StatusNoContent,
		"http://localhost:3000":  http.StatusNoContent,
		"http://127.0.0.1:7777":  http.StatusNoContent,
		"http://[::1]:7777":      http.StatusNoContent,
		"https://evil.example":   http.StatusForbidden,
		"null":                   http.StatusForbidden,
		"http://localhost.evil":  http.StatusForbidden,
		"http://192.168.1.10:80": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Host = "127.0.0.1:7777"
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		LocalOnly(ok).ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Origin %q: status %d, want %d", origin, rec.Code, want)
		}
	}
}

func TestLoopbackAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7777": true,
		"localhost:7777": true,
		"[::1]:7777":     true,
		":7777":          false,
		"0.0.0.0:7777":   false,
		"10.0.0.2:7777":  false,
	} {
		if got := LoopbackAddr(addr); got != want {
			t.Errorf("LoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestBareName(t *testing.T) {
	for name, want := range map[string]bool{
		"run-1": true, "a.jpg": true, "": false, ".": false, "..": false, "../x": false, "a/b": false, `a\b`: false,
	} {
		if got := bareName(name); got != want {
			t.Errorf("bareName(%q) = %v, want %v", name, got, want)
		}
	}
}

func (h *harness) send(method, path, contentType, body, origin string) (int, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.url+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode, res.Status
}

func TestAWriteMustBeJSON(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	path := "/api/runs/" + runID + "/messages"
	for _, ct := range []string{"application/x-www-form-urlencoded", "text/plain", "multipart/form-data; boundary=x"} {
		if code, status := h.send(http.MethodPost, path, ct, `{"kind":"note","text":"json-only note"}`, ""); code != http.StatusUnsupportedMediaType {
			t.Errorf("POST as %s: %s, want 415", ct, status)
		}
	}
	if code, status := h.send(http.MethodPost, path, "", `{"kind":"note","text":"json-only note"}`, ""); code != http.StatusUnsupportedMediaType {
		t.Errorf("POST with a body and no Content-Type: %s, want 415", status)
	}
	if code, status := h.send(http.MethodPost, path, "application/json; charset=utf-8", `{"kind":"note","text":"json-only note"}`, ""); code != http.StatusOK && code != http.StatusCreated {
		t.Errorf("POST as JSON: %s, want success", status)
	}
	if conversationCount(h, runID, "json-only note") != 1 {
		t.Error("only the JSON note should have reached the conversation")
	}
}

func TestABodylessWriteWorksFromANativeClientButNotABrowser(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	// A browser's cross-site POST always carries Origin, even with no body.
	if code, status := h.send(http.MethodPost, "/api/runs/"+runID+"/destroy", "", "", "https://evil.example"); code != http.StatusForbidden {
		t.Fatalf("cross-site destroy: %s, want 403", status)
	}
	if !h.mgr.Live(runID) {
		t.Fatal("the cross-site destroy went through")
	}
	if code, status := h.send(http.MethodDelete, "/api/runs/"+runID+"/control", "", "", ""); code != http.StatusOK {
		t.Errorf("body-less DELETE control: %s, want 200", status)
	}
	if code, status := h.send(http.MethodPost, "/api/runs/"+runID+"/destroy", "", "", ""); code != http.StatusOK {
		t.Fatalf("body-less destroy: %s, want 200", status)
	}
	if h.mgr.Live(runID) {
		t.Error("the machine is still live after destroy")
	}
}
