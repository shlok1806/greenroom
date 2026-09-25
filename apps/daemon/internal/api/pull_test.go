package api

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// fakeGuestHome makes a fresh HOME, which the fake tart runs the pull's probe and tar from.
func fakeGuestHome(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for name, body := range files {
		p := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// pull GETs the pull route and returns the response with its body read to the end, so the trailer is in.
func pull(t *testing.T, base, runID string, q url.Values, header http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/runs/"+runID+"/pull?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if h := header.Get("Host"); h != "" {
		req.Host = h
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func members(t *testing.T, data []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[hdr.Name] = string(b)
	}
}

func TestPullStreamsTheGuestPathAsAGzippedTarAndRecordsTheStep(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	fakeGuestHome(t, map[string]string{"work/app/a.txt": "alpha", "work/app/sub/b.txt": "beta", "work/app/.git/HEAD": "ref"})

	res, body := pull(t, h.url, runID, url.Values{"src": {"~/work/app"}, "exclude": {".git"}}, nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("status %d, type %q: %s", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	if got := res.Trailer.Get(pullErrorTrailer); got != "" {
		t.Errorf("error trailer %q on a good pull", got)
	}
	got := members(t, body)
	if got["./a.txt"] != "alpha" || got["./sub/b.txt"] != "beta" {
		t.Errorf("members = %v", got)
	}
	if _, ok := got["./.git/HEAD"]; ok {
		t.Error("the excluded .git was sent")
	}
	step, err := strconv.Atoi(res.Header.Get(pullStepHeader))
	if err != nil || step == 0 {
		t.Fatalf("step header %q", res.Header.Get(pullStepHeader))
	}
	var steps []machine.Step
	h.get("/api/runs/"+runID+"/steps", &steps)
	last := steps[len(steps)-1]
	if last.Tool != "machine_pull" || last.Seq != step || last.Error != "" {
		t.Errorf("last step = %+v, want machine_pull %d", last, step)
	}

	res, body = pull(t, h.url, runID, url.Values{"src": {"work/app/a.txt"}}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("file pull: %d %s", res.StatusCode, body)
	}
	if got := members(t, body); len(got) != 1 || got["./a.txt"] != "alpha" {
		t.Errorf("file pull members = %v, want the one file", got)
	}
}

func TestPullRefusesBadRequests(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	fakeGuestHome(t, nil)
	for _, tc := range []struct {
		name  string
		runID string
		q     url.Values
		code  int
		want  string
	}{
		{"no src", runID, url.Values{}, http.StatusBadRequest, "src is required"},
		{"missing src", runID, url.Values{"src": {"nothing"}}, http.StatusNotFound, "does not exist in the guest"},
		{"unknown run", "nope", url.Values{"src": {"x"}}, http.StatusNotFound, "no run"},
	} {
		res, body := pull(t, h.url, tc.runID, tc.q, nil)
		if res.StatusCode != tc.code || !strings.Contains(string(body), tc.want) {
			t.Errorf("%s: %d %s, want %d with %q", tc.name, res.StatusCode, body, tc.code, tc.want)
		}
	}
	if err := h.mgr.Destroy(t.Context(), runID); err != nil {
		t.Fatal(err)
	}
	if res, body := pull(t, h.url, runID, url.Values{"src": {"x"}}, nil); res.StatusCode != http.StatusConflict {
		t.Errorf("destroyed run: %d %s, want 409", res.StatusCode, body)
	}
}

// A tar that fails once the archive has begun cannot change the status; the trailer says so.
func TestPullNamesALateFailureInTheTrailer(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	home := fakeGuestHome(t, map[string]string{"work/app/ok.txt": "fine", "work/app/secret.txt": "x"})
	if err := os.Chmod(filepath.Join(home, "work/app/secret.txt"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, "work/app/secret.txt"), 0o644) })
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	res, body := pull(t, h.url, runID, url.Values{"src": {"work/app"}}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if got := res.Trailer.Get(pullErrorTrailer); !strings.Contains(got, "tar in the guest") || strings.ContainsAny(got, "\r\n") {
		t.Errorf("trailer = %q, want tar's failure on one line", got)
	}
}

// The route is behind the same bearer check as every other through the public host.
func TestPullNeedsTheTokenThroughThePublicHost(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	fakeGuestHome(t, map[string]string{"f.txt": "hi"})
	const host, token = "gr.example.com", "0123456789abcdef0123456789abcdef"
	ts := httptest.NewServer(Guard(New(h.mgr, session.NewRegistry(h.mgr.Root, 2), slog.New(slog.NewTextHandler(io.Discard, nil))), host, token))
	t.Cleanup(ts.Close)

	q := url.Values{"src": {"f.txt"}}
	if res, _ := pull(t, ts.URL, runID, q, http.Header{"Host": {host}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", res.StatusCode)
	}
	res, body := pull(t, ts.URL, runID, q, http.Header{"Host": {host}, "Authorization": {"Bearer " + token}})
	if res.StatusCode != http.StatusOK || members(t, body)["./f.txt"] != "hi" {
		t.Errorf("with the token: %d, members %v", res.StatusCode, members(t, body))
	}
}
