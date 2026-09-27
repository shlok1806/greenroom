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

	"github.com/shlok1806/greenroom/apps/daemon/internal/api"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/report"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// reportFixture is the recorded run internal/report's goldens use.
const reportFixture = "20260926-231011-600cf88cfbdbcba8"

// ADR 0034: GET /api/runs/{id}/report is behind api.Guard like every route. Through the public
// host it needs the token, and its screenshot links go through the artifact route there, which
// the same token opens; locally they are paths on this host.
func TestRoutesServeARunReportLocallyAndToThePublicHostWithTheToken(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	copyDir(t, filepath.Join("internal", "report", "testdata", reportFixture), mgr.RunDir(reportFixture))
	const public, token = "gr.example.com", "0123456789abcdef0123456789abcdef"
	models := report.Models{Brain: "reasoner", Vision: "eyes"}
	ts := httptest.NewServer(routes(mgr, session.NewRegistry(mgr.Root, 2), "img", public, token, t.TempDir(), api.Version{}, models, log))
	t.Cleanup(ts.Close)

	get := func(path, host, auth string) (int, string, string) {
		t.Helper()
		req, err := http.NewRequest("GET", ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Type"), string(body)
	}
	path := "/api/runs/" + reportFixture + "/report"
	artifact := "/api/runs/" + reportFixture + "/artifacts/107-screenshot.png"

	if code, _, _ := get(path, public, ""); code != http.StatusUnauthorized {
		t.Errorf("public host without the token: %d, want 401", code)
	}
	if code, _, _ := get(path, public, "wrong-token-wrong-token-wrong-token"); code != http.StatusUnauthorized {
		t.Errorf("public host with a wrong token: %d, want 401", code)
	}
	if code, _, _ := get(path, "evil.example", token); code != http.StatusForbidden {
		t.Errorf("a foreign Host: %d, want 403", code)
	}

	code, ct, md := get(path, public, token)
	if code != http.StatusOK || !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("public host with the token: %d %s\n%s", code, ct, md)
	}
	link := "https://" + public + artifact
	for _, want := range []string{"## Greenroom: Not finished", "- PASS **run-b-card-after** (visual)",
		"[step 107 (screenshot)](<" + link + ">)", "brain `reasoner`, describer `eyes`"} {
		if !strings.Contains(md, want) {
			t.Errorf("public report lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, mgr.Root) {
		t.Error("a report through the public host names a path on the daemon's host")
	}
	// The link opens with the same token, which is what greenroom connect sends.
	if code, ct, _ := get(artifact, public, token); code != http.StatusOK || ct != "image/png" {
		t.Errorf("the linked artifact through the public host: %d %s, want 200 image/png", code, ct)
	}

	code, ct, body := get(path+"?format=json", "", "")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("loopback json: %d %s", code, ct)
	}
	var rep report.Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatal(err)
	}
	if got, want := rep.Verdict.Checks[0].Evidence[1].Screenshot.Link, filepath.Join(mgr.RunDir(reportFixture), "107-screenshot.png"); got != want {
		t.Errorf("loopback link = %q, want the host path %q", got, want)
	}

	if _, _, body := get(path+"?embed=true", "", ""); !strings.Contains(body, "](data:image/png;base64,") {
		t.Error("embed=true does not embed the screenshot")
	}
	if code, _, _ := get(path+"?format=html", "", ""); code != http.StatusBadRequest {
		t.Errorf("format=html: %d, want 400", code)
	}
	if code, _, _ := get(path+"?embed=maybe", "", ""); code != http.StatusBadRequest {
		t.Errorf("embed=maybe: %d, want 400", code)
	}
	if code, _, _ := get("/api/runs/20990101-000000-0000000000000000/report", "", ""); code != http.StatusNotFound {
		t.Errorf("an unknown run: %d, want 404", code)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
