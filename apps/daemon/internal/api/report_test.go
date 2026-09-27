package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// ADR 0031: a finished run says so in the run list and the run detail, from its finish event,
// before the manifest mirror is written; an unfinished one carries an explicit null (list).
func TestAFinishedRunSaysSoInTheListAndTheDetail(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	var runs []RunSummary
	h.get("/api/runs", &runs)
	if findRun(t, runs, runID).Finish != nil {
		t.Fatal("an unfinished run reads as finished")
	}
	_, raw := h.do(http.MethodGet, "/api/runs", nil)
	if !strings.Contains(string(raw), `"finish":null`) {
		t.Errorf("the run list does not say finish is null: %s", raw)
	}

	f := session.Finish{Outcome: session.OutcomeUnverified, Summary: "Shipped without a verdict.", Ref: &session.Ref{PR: "#12"}}
	if _, err := h.store(runID).Append(session.Message{From: session.System, Kind: session.Event, Text: session.FinishText(f), Finish: &f}); err != nil {
		t.Fatal(err)
	}
	h.get("/api/runs", &runs)
	if got := findRun(t, runs, runID).Finish; got == nil || got.Outcome != session.OutcomeUnverified || got.Ref.PR != "#12" || got.At.IsZero() {
		t.Errorf("list finish = %+v", got)
	}
	var d RunDetail
	h.get("/api/runs/"+runID, &d)
	if d.Finish == nil || d.Finish.Summary != "Shipped without a verdict." {
		t.Errorf("detail finish = %+v", d.Finish)
	}

	code, body := h.status(http.MethodGet, "/api/runs/"+runID+"/report", nil)
	if code != http.StatusOK || !strings.Contains(body, "## Greenroom: Unverified") || !strings.Contains(body, "PR `#12`") {
		t.Errorf("report: %d\n%s", code, body)
	}
}
