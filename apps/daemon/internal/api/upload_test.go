package api

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// put sends body as a PUT with the given content type and returns the status and body.
func (h *harness) put(path, contentType string, body []byte) (int, string) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPut, h.url+path, bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(data)
}

// fakeRsync puts an rsync on PATH that records its arguments and the file it was given, and prints stats.
func fakeRsync(t *testing.T) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		"echo 'Number of files: 2 (reg: 1, dir: 1)'\necho 'Total transferred file size: 5 bytes'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

// A remote client's machine_sync: its project arrives as a gzipped tar and reaches the guest
// through the same Manager.Sync, from a staging directory the destroy removes (ADR 0021).
func TestUploadSyncUnpacksAndSyncsFromStaging(t *testing.T) {
	argsFile := fakeRsync(t)
	h := newHarness(t)
	runID := h.ready()
	mtime := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	body := tarball(t, entry{name: "Package.swift", body: "swift", mtime: mtime})

	code, resp := h.put("/api/runs/"+runID+"/sync?name=myapp", "application/gzip", body)
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, resp)
	}
	var res machine.SyncResult
	if err := json.Unmarshal([]byte(resp), &res); err != nil {
		t.Fatalf("decode %s: %v", resp, err)
	}
	if res.Dest != "work/myapp" {
		t.Errorf("dest %q, want work/myapp: the default follows the name as it does the local source's basename", res.Dest)
	}
	if !strings.Contains(res.Summary, "Number of files") {
		t.Errorf("summary %q, want rsync's stats", res.Summary)
	}
	staging := filepath.Join(h.mgr.Root, "uploads", runID, "myapp")
	st, err := os.Stat(filepath.Join(staging, "Package.swift"))
	if err != nil || !st.ModTime().Equal(mtime) {
		t.Fatalf("staged file = %v, %v; want it with mtime %v", st, err, mtime)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("rsync never ran: %v", err)
	}
	if !strings.Contains(string(args), "\n"+staging+"/\n") {
		t.Errorf("rsync did not copy from the staging directory %s:\n%s", staging, args)
	}

	// A repeat upload replaces what was staged, so a file deleted on the client goes too.
	body = tarball(t, entry{name: "Sources/main.swift", body: "print(1)"})
	if code, resp := h.put("/api/runs/"+runID+"/sync?name=myapp&dest=~/work/other", "application/gzip; charset=binary", body); code != http.StatusOK {
		t.Fatalf("second upload: %d %s", code, resp)
	} else if !strings.Contains(resp, `"dest":"work/other"`) {
		t.Errorf("second upload ignored dest: %s", resp)
	}
	if _, err := os.Stat(filepath.Join(staging, "Package.swift")); err == nil {
		t.Error("the first upload's file is still staged")
	}

	if code, _ := h.status(http.MethodPost, "/api/runs/"+runID+"/destroy", nil); code != http.StatusOK {
		t.Fatalf("destroy: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(h.mgr.Root, "uploads", runID)); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the destroyed run's uploads are still there")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, resp := h.put("/api/runs/"+runID+"/sync", "application/gzip", body); code != http.StatusConflict {
		t.Errorf("upload to a destroyed run: %d %s, want 409", code, resp)
	}
	if _, err := os.Stat(filepath.Join(h.mgr.Root, "uploads", runID)); err == nil {
		t.Error("an upload to a destroyed run left a staging directory")
	}
}

func TestUploadSyncRefusesBadRequests(t *testing.T) {
	fakeRsync(t)
	h := newHarness(t)
	runID := h.ready()
	good := tarball(t, entry{name: "a", body: "x"})
	path := "/api/runs/" + runID + "/sync"
	for _, tc := range []struct {
		name, path, contentType string
		body                    []byte
		want                    int
	}{
		{"no such run", "/api/runs/nope/sync", "application/gzip", good, http.StatusNotFound},
		{"JSON body", path, "application/json", []byte(`{}`), http.StatusUnsupportedMediaType},
		{"form body", path, "application/x-www-form-urlencoded", good, http.StatusUnsupportedMediaType},
		{"no content type", path, "", good, http.StatusUnsupportedMediaType},
		{"name with a slash", path + "?name=a/b", "application/gzip", good, http.StatusBadRequest},
		{"name climbing", path + "?name=..", "application/gzip", good, http.StatusBadRequest},
		{"not gzip", path, "application/gzip", []byte("plain"), http.StatusBadRequest},
		{"traversal", path, "application/gzip", tarball(t, entry{name: "../x", body: "x"}), http.StatusBadRequest},
		{"escaping link", path, "application/gzip", tarball(t, entry{name: "l", typ: tar.TypeSymlink, link: "../../.."}), http.StatusBadRequest},
		{"dest outside the home", path + "?dest=/etc", "application/gzip", good, http.StatusBadRequest},
		{"dest climbing", path + "?dest=work/../..", "application/gzip", good, http.StatusBadRequest},
	} {
		if code, resp := h.put(tc.path, tc.contentType, tc.body); code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, code, resp, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(h.mgr.Root, "uploads", runID, "project", "a")); err == nil {
		t.Error("a refused upload left files staged")
	}
	// Only PUT to this one route takes gzip; every other write still must be JSON.
	if code, _ := h.send(http.MethodPost, path, "application/gzip", string(good), ""); code != http.StatusUnsupportedMediaType && code != http.StatusMethodNotAllowed {
		t.Errorf("POST gzip to sync: %d, want 415 or 405", code)
	}
	if code, _ := h.send(http.MethodPost, "/api/runs/"+runID+"/messages", "application/gzip", string(good), ""); code != http.StatusUnsupportedMediaType {
		t.Errorf("gzip to messages: %d, want 415", code)
	}
}

func TestUploadSyncCapsTheBody(t *testing.T) {
	fakeRsync(t)
	h := newHarness(t)
	runID := h.ready()
	body := tarball(t, entry{name: "a", body: strings.Repeat("x", 4000)}, entry{name: "b", body: strings.Repeat("y", 4000)})

	oldBytes, oldLimits := maxUploadBytes, uploadLimits
	t.Cleanup(func() { maxUploadBytes, uploadLimits = oldBytes, oldLimits })

	uploadLimits = untarLimits{bytes: 5000, entries: 100}
	if code, resp := h.put("/api/runs/"+runID+"/sync", "application/gzip", body); code != http.StatusRequestEntityTooLarge {
		t.Errorf("past the unpacked cap: %d %s, want 413", code, resp)
	}
	uploadLimits = untarLimits{bytes: 1 << 20, entries: 1}
	if code, resp := h.put("/api/runs/"+runID+"/sync", "application/gzip", body); code != http.StatusRequestEntityTooLarge {
		t.Errorf("past the entry cap: %d %s, want 413", code, resp)
	}
	uploadLimits = oldLimits
	maxUploadBytes = int64(len(body) / 2)
	if code, resp := h.put("/api/runs/"+runID+"/sync", "application/gzip", body); code != http.StatusRequestEntityTooLarge {
		t.Errorf("past the body cap: %d %s, want 413", code, resp)
	}
}

// A daemon that stopped before a destroy leaves that run's uploads; the next one removes them.
func TestNewSweepsUploadsOfRunsWithNoMachine(t *testing.T) {
	h := newHarness(t)
	stale := filepath.Join(h.mgr.Root, "uploads", "gone", "project")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = New(h.mgr, h.reg, h.mgr.Log)
	if _, err := os.Stat(filepath.Dir(stale)); !os.IsNotExist(err) {
		t.Errorf("uploads of a run with no machine survived a restart: %v", err)
	}
}
