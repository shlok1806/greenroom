package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/summary"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// listed is the run list's row for runID.
func (h *harness) listed(runID string) RunSummary {
	h.t.Helper()
	var runs []RunSummary
	h.get("/api/runs", &runs)
	for _, r := range runs {
		if r.RunID == runID {
			return r
		}
	}
	h.t.Fatalf("run %s is not in /api/runs", runID)
	return RunSummary{}
}

// reportMarkdown is the run's report as Markdown.
func (h *harness) reportMarkdown(runID string) string {
	h.t.Helper()
	res, err := http.Get(h.url + "/api/runs/" + runID + "/report?format=md")
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK {
		h.t.Fatalf("report: %d %v %s", res.StatusCode, err, body)
	}
	return string(body)
}

// A create whose clone fails leaves a run that every surface calls a failure: the list says
// failed, the summary Did not start, the report too (issue #286, root ADR 0049). It keeps the
// name its create was given.
func TestACreateWhoseCloneFailedIsListedAsFailedEverywhere(t *testing.T) {
	h := newHarness(t)
	testsupport.Flag(t, h.control, "fail-clone")
	if _, err := h.mgr.CreateLabeled(context.Background(), "greenroom-lean-a",
		machine.Label{Name: "TipSplit: round each share", Source: "claude-code"}); err == nil {
		t.Fatal("create succeeded with fail-clone")
	}
	var runs []RunSummary
	h.get("/api/runs", &runs)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want the failed create's run", runs)
	}
	r := runs[0]
	if r.Status != "failed" || r.StartError == "" || r.DestroyedAt == nil {
		t.Errorf("listed status %q startError %q destroyedAt %v; want failed, the clone's error and an end", r.Status, r.StartError, r.DestroyedAt)
	}
	s := h.summary(r.RunID)
	if s.State != summary.DidNotStart || s.Status != "Did not start" || s.Tone != summary.ToneFail || s.Group != summary.Done {
		t.Errorf("summary %s %q %s %s; want did-not-start, fail, done", s.State, s.Status, s.Tone, s.Group)
	}
	if s.Detail != "The Mac could not be created." || s.Name != "TipSplit: round each share" || s.Source != "Claude Code" {
		t.Errorf("detail %q name %q source %q", s.Detail, s.Name, s.Source)
	}
	if b := h.board(); b.Groups[2].Count != 1 || b.Groups[2].Runs[0].State != summary.DidNotStart {
		t.Errorf("board done = %+v, want the run there as did-not-start", b.Groups[2])
	}
	if md := h.reportMarkdown(r.RunID); !strings.HasPrefix(md, "## Greenroom: Did not start\n") || !strings.Contains(md, r.StartError) {
		t.Errorf("report:\n%s", md)
	}
}

// A boot that fails is failed while its machine is held and stays failed once it is gone, where
// it used to read finished.
func TestABootThatFailedStaysFailedAfterItsMachineIsGone(t *testing.T) {
	h := newHarness(t, machine.WithReadyTimeout(2*time.Second))
	testsupport.Flag(t, h.control, "fail-keyinstall")
	runID := h.create()
	if _, err := h.mgr.Wait(context.Background(), runID, 30*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if r := h.listed(runID); r.Status != "failed" || r.StartError == "" {
		t.Errorf("held: status %q startError %q, want failed with the boot's error", r.Status, r.StartError)
	}
	if s := h.summary(runID); s.State != summary.DidNotStart || s.Detail != "The Mac did not finish starting." {
		t.Errorf("held: summary %s %q", s.State, s.Detail)
	}
	if err := h.mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if r := h.listed(runID); r.Status != "failed" || r.StartError == "" {
		t.Errorf("gone: status %q startError %q, want failed with the boot's error", r.Status, r.StartError)
	}
	s := h.summary(runID)
	if s.State != summary.DidNotStart || s.Machine.Ended != "The Mac did not start." {
		t.Errorf("gone: summary %s, machine %+v", s.State, s.Machine)
	}
}

// A run whose Mac was ready and then went is finished, as before.
func TestARunThatStartedIsStillListedAsFinishedOnceItsMachineIsGone(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if err := h.mgr.Destroy(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if r := h.listed(runID); r.Status != "finished" || r.StartError != "" {
		t.Errorf("status %q startError %q, want finished with none", r.Status, r.StartError)
	}
	if s := h.summary(runID); s.State != summary.Stopped {
		t.Errorf("summary %s, want stopped", s.State)
	}
}
