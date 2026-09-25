package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGuardRefusesAForeignHost(t *testing.T) {
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
		Guard(ok, "", "").ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: status %d, want %d", host, rec.Code, want)
		}
		if want == http.StatusForbidden && !strings.Contains(rec.Body.String(), "loopback") {
			t.Errorf("Host %q: body %q does not say loopback, which the Companion's advice keys off", host, rec.Body.String())
		}
	}
}

func TestGuardRefusesAForeignOrigin(t *testing.T) {
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
		Guard(ok, "gr.example.com", testToken).ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Origin %q: status %d, want %d", origin, rec.Code, want)
		}
	}
}

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

// Tunnel traffic arrives from loopback like local traffic; only its Host tells it apart (ADR 0021).
func TestGuardOnThePublicHost(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	const public = "gr.example.com"
	for _, tc := range []struct {
		name, method, path, host, auth, origin string
		guardHost, guardToken                  string
		want                                   int
	}{
		{"right token", "GET", "/api/runs", public, "Bearer " + testToken, "", public, testToken, http.StatusNoContent},
		{"mcp with token", "POST", "/mcp", public, "Bearer " + testToken, "", public, testToken, http.StatusNoContent},
		{"scheme in any case", "GET", "/api/runs", public, "bearer " + testToken, "", public, testToken, http.StatusNoContent},
		{"host with port", "GET", "/api/runs", public + ":443", "Bearer " + testToken, "", public, testToken, http.StatusNoContent},
		{"host in upper case", "GET", "/api/runs", "GR.Example.COM", "Bearer " + testToken, "", public, testToken, http.StatusNoContent},
		{"configured in upper case with a port", "GET", "/api/runs", public, "Bearer " + testToken, "", "GR.EXAMPLE.COM:443", testToken, http.StatusNoContent},
		{"no token", "GET", "/api/runs", public, "", "", public, testToken, http.StatusUnauthorized},
		{"wrong token", "GET", "/api/runs", public, "Bearer " + strings.Repeat("x", len(testToken)), "", public, testToken, http.StatusUnauthorized},
		{"token prefix", "GET", "/api/runs", public, "Bearer " + testToken[:32], "", public, testToken, http.StatusUnauthorized},
		{"token plus more", "GET", "/api/runs", public, "Bearer " + testToken + "x", "", public, testToken, http.StatusUnauthorized},
		{"no Bearer scheme", "GET", "/api/runs", public, testToken, "", public, testToken, http.StatusUnauthorized},
		{"Basic scheme", "GET", "/api/runs", public, "Basic " + testToken, "", public, testToken, http.StatusUnauthorized},
		{"Bearer and nothing", "GET", "/api/runs", public, "Bearer ", "", public, testToken, http.StatusUnauthorized},
		{"empty token configured", "GET", "/api/runs", public, "Bearer ", "", public, "", http.StatusUnauthorized},
		{"Origin with a good token", "GET", "/api/runs", public, "Bearer " + testToken, "https://" + public, public, testToken, http.StatusForbidden},
		{"Origin on an install file", "GET", "/install.sh", public, "", "https://evil.example", public, testToken, http.StatusForbidden},
		{"install.sh without token", "GET", "/install.sh", public, "", "", public, testToken, http.StatusNoContent},
		{"install.sh HEAD without token", "HEAD", "/install.sh", public, "", "", public, testToken, http.StatusNoContent},
		{"dl file without token", "GET", "/dl/greenroom", public, "", "", public, testToken, http.StatusNoContent},
		{"dl nested path needs token", "GET", "/dl/a/b", public, "", "", public, testToken, http.StatusUnauthorized},
		{"dl climbing needs token", "GET", "/dl/../api/runs", public, "", "", public, testToken, http.StatusUnauthorized},
		{"dl directory itself needs token", "GET", "/dl/", public, "", "", public, testToken, http.StatusUnauthorized},
		{"install.sh POST needs token", "POST", "/install.sh", public, "", "", public, testToken, http.StatusUnauthorized},
		{"dl PUT needs token", "PUT", "/dl/greenroom", public, "", "", public, testToken, http.StatusUnauthorized},
		{"other host with the token", "GET", "/api/runs", "evil.example", "Bearer " + testToken, "", public, testToken, http.StatusForbidden},
		{"sub-domain of the public host", "GET", "/api/runs", "x." + public, "Bearer " + testToken, "", public, testToken, http.StatusForbidden},
		{"public host not configured", "GET", "/install.sh", public, "", "", "", "", http.StatusForbidden},
		{"loopback needs no token", "GET", "/api/runs", "127.0.0.1:7777", "", "", public, testToken, http.StatusNoContent},
		{"loopback ignores a wrong token", "GET", "/api/runs", "localhost:7777", "Bearer nope", "", public, testToken, http.StatusNoContent},
	} {
		req := httptest.NewRequest(tc.method, "/", nil)
		req.URL.Path = tc.path
		req.Host = tc.host
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		Guard(ok, tc.guardHost, tc.guardToken).ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, strings.TrimSpace(rec.Body.String()))
		}
		if tc.want == http.StatusUnauthorized {
			if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("%s: WWW-Authenticate %q, want Bearer", tc.name, got)
			}
			if body := strings.TrimSpace(rec.Body.String()); body != "unauthorized: missing or wrong token" {
				t.Errorf("%s: body %q", tc.name, body)
			}
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
