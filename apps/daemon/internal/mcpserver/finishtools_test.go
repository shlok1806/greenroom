package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/report"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

type finishResult struct {
	Finish       session.Finish `json:"finish"`
	Destroyed    bool           `json:"destroyed"`
	DestroyError string         `json:"destroyError"`
	Report       report.Report  `json:"report"`
}

// verdict plays the coder's task and the verifier's verdict on it, and returns the verdict's seq.
func (h *harness) verdict(runID, verdict string, checks []session.Check) int {
	h.t.Helper()
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Check the total reads $48.00."}, nil)
	m, err := h.store(runID).Append(session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: verdict,
		Text: "The total reads $48.00.", Checks: checks})
	if err != nil {
		h.t.Fatal(err)
	}
	return m.Seq
}

func passChecks(evidence int) []session.Check {
	return []session.Check{{ID: "total", Criterion: "The total reads $48.00", Kinds: []string{"visual"}, Status: "pass",
		Evidence: []int{evidence}, Observed: "The total reads $48.00."}}
}

// refused runs run_finish and requires a tool error containing want.
func (h *harness) refused(args map[string]any, want string) {
	h.t.Helper()
	res := h.raw("run_finish", args)
	if !res.IsError {
		h.t.Fatalf("run_finish %v succeeded, want a refusal naming %q", args, want)
	}
	if !strings.Contains(text(res), want) {
		h.t.Errorf("refusal = %q, want it to say %q", text(res), want)
	}
}

func finishArgs(runID, outcome string) map[string]any {
	return map[string]any{"runId": runID, "outcome": outcome, "summary": "Each pays now shows the split total."}
}

func (h *harness) finishes(runID string) []session.Message {
	var out []session.Message
	for _, m := range h.store(runID).After(0) {
		if m.Finish != nil {
			out = append(out, m)
		}
	}
	return out
}

func TestVerifiedNeedsAnAcceptedPass(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	h.refused(finishArgs(runID, "verified"), "has no verdict")

	fail := h.verdict(runID, "fail", nil)
	h.refused(finishArgs(runID, "verified"), "is fail, not pass")
	h.call("agent_send", map[string]any{"runId": runID, "kind": "accept", "replyTo": fail}, nil)
	h.refused(finishArgs(runID, "verified"), "is fail, not pass") // an accepted fail is still a fail

	pass := h.verdict(runID, "pass", nil)
	h.refused(finishArgs(runID, "verified"), "is proposed, not accepted")

	// Two disputes contest it; only a human can accept it now.
	h.call("agent_send", map[string]any{"runId": runID, "kind": "dispute", "replyTo": pass, "text": "Wrong window."}, nil)
	pass = h.verdict(runID, "pass", nil)
	h.call("agent_send", map[string]any{"runId": runID, "kind": "dispute", "replyTo": pass, "text": "Still wrong."}, nil)
	h.verdict(runID, "pass", nil)
	h.refused(finishArgs(runID, "verified"), "only a human can accept it")

	if got := h.finishes(runID); len(got) != 0 {
		t.Fatalf("a refused finish was recorded: %+v", got)
	}
	man, _ := machine.ReadManifest(h.mgr.RunDir(runID))
	if man.Finish != nil {
		t.Errorf("a refused finish reached the manifest: %+v", man.Finish)
	}
	if !h.mgr.Live(runID) {
		t.Error("a refused finish destroyed the machine")
	}
}

func TestAVerifiedFinishAfterAnAcceptedPassRecordsDestroysAndReports(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.putShot()
	var shot machine.Shot
	h.call("machine_screenshot", map[string]any{"runId": runID}, &shot)
	pass := h.verdict(runID, "pass", passChecks(shot.Step))
	h.call("agent_send", map[string]any{"runId": runID, "kind": "accept", "replyTo": pass}, nil)

	args := finishArgs(runID, "verified")
	args["ref"] = map[string]any{"branch": "fix/split", "commit": "abc1234", "pr": "https://github.com/o/r/pull/7"}
	var out finishResult
	res := h.call("run_finish", args, &out)

	if out.Finish.Outcome != session.OutcomeVerified || out.Finish.At.IsZero() || out.Finish.Ref == nil || out.Finish.Ref.PR != "https://github.com/o/r/pull/7" {
		t.Errorf("finish = %+v", out.Finish)
	}
	if !out.Destroyed || h.mgr.Live(runID) {
		t.Errorf("destroyed = %v, live = %v; destroy defaults to true", out.Destroyed, h.mgr.Live(runID))
	}
	events := h.finishes(runID)
	if len(events) != 1 || events[0].From != session.System || events[0].Kind != session.Event ||
		!strings.HasPrefix(events[0].Text, "run finished: verified.") || !events[0].Finish.At.Equal(out.Finish.At) {
		t.Fatalf("finish events = %+v", events)
	}
	man, err := machine.ReadManifest(h.mgr.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	if man.Finish == nil || man.Finish.Outcome != session.OutcomeVerified || man.Finish.Summary != "Each pays now shows the split total." {
		t.Errorf("manifest finish = %+v", man.Finish)
	}
	if man.DestroyedAt == nil {
		t.Error("the manifest does not record the destroy")
	}

	rep := out.Report
	if rep.Finish == nil || rep.Verdict == nil || rep.Verdict.Status != session.Accepted || len(rep.Verdict.Checks) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	ev := rep.Verdict.Checks[0].Evidence[0]
	if ev.Screenshot == nil || !strings.HasPrefix(ev.Screenshot.Link, h.mgr.RunDir(runID)) {
		t.Errorf("evidence = %+v, want the screenshot's path in the run directory (local caller)", ev)
	}
	md := text(res)
	for _, want := range []string{"## Greenroom: Verified", "- PASS **total** (visual)", "branch `fix/split`"} {
		if !strings.Contains(md, want) {
			t.Errorf("run_finish text lacks %q:\n%s", want, md)
		}
	}
}

func TestARunFinishesOnce(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	args := finishArgs(runID, "unverified")
	args["destroy"] = false
	var out finishResult
	h.call("run_finish", args, &out)
	if out.Destroyed || !h.mgr.Live(runID) {
		t.Errorf("destroy false destroyed the machine")
	}
	h.refused(finishArgs(runID, "abandoned"), "already finished as unverified")
	if got := h.finishes(runID); len(got) != 1 {
		t.Errorf("finish events = %d, want 1", len(got))
	}
}

func TestFinishingWaitsForTheVerifiersTurn(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	// A turn the actor is running.
	h.mgr.SetVerifierTurn(runID, true)
	h.refused(finishArgs(runID, "unverified"), "the verifier is in a turn")
	h.mgr.SetVerifierTurn(runID, false)

	// A task nobody has answered yet: the turn is owed, even before the actor picks it up.
	sent := sendResult{}
	h.call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Check it."}, &sent)
	h.refused(finishArgs(runID, "abandoned"), "has not answered message 1")

	// Its answer, a question here, ends the turn; a coder's note owes nothing.
	if _, err := h.store(runID).Append(session.Message{From: session.Verifier, Kind: session.Question, Text: "Which scheme?"}); err != nil {
		t.Fatal(err)
	}
	h.call("agent_send", map[string]any{"runId": runID, "kind": "note", "text": "Never mind."}, nil)
	h.call("run_finish", finishArgs(runID, "abandoned"), nil)
}

func TestAFinishNeedsAKnownOutcomeAndASummary(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.refused(map[string]any{"runId": runID, "outcome": "done", "summary": "x"}, "outcome must be verified, unverified or abandoned")
	h.refused(map[string]any{"runId": runID, "outcome": "unverified", "summary": "  "}, "summary is required")
	h.refused(map[string]any{"runId": runID, "outcome": "unverified", "summary": strings.Repeat("x", session.MaxFinishSummary+1)}, "at most")
	h.refused(map[string]any{"runId": "../etc", "outcome": "unverified", "summary": "x"}, "no run")
	if got := h.finishes(runID); len(got) != 0 {
		t.Errorf("finish events = %+v", got)
	}
}

func TestRunReportReadsTheProofInEitherFormat(t *testing.T) {
	h := newHarnessWith(t, WithModels(report.Models{Brain: "reasoner", Vision: "eyes"}))
	runID := h.ready()
	h.putShot()
	var shot machine.Shot
	h.call("machine_screenshot", map[string]any{"runId": runID}, &shot)
	h.verdict(runID, "pass", passChecks(shot.Step))

	var rep report.Report
	res := h.call("run_report", map[string]any{"runId": runID}, &rep)
	md := text(res)
	for _, want := range []string{"## Greenroom: Not finished", "- **Verdict:** pass (message 2), proposed",
		"brain `reasoner`, describer `eyes`", "Every listed check was observed on this build"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if rep.Models.Brain != "reasoner" || rep.Finish != nil || rep.Verdict == nil {
		t.Errorf("structured report = %+v", rep)
	}

	res = h.call("run_report", map[string]any{"runId": runID, "format": "json", "embed": true}, &rep)
	var fromText report.Report
	if err := json.Unmarshal([]byte(text(res)), &fromText); err != nil {
		t.Fatalf("json text does not decode: %v", err)
	}
	if link := fromText.Verdict.Checks[0].Evidence[0].Screenshot.Link; !strings.HasPrefix(link, "data:image/png;base64,") {
		t.Errorf("embedded link = %.40q", link)
	}
	h.refusedBy("run_report", map[string]any{"runId": runID, "format": "html"}, "format must be md or json")

	// A report reads the record, so a finished and destroyed run still answers.
	h.call("agent_send", map[string]any{"runId": runID, "kind": "accept", "replyTo": 2}, nil)
	h.call("run_finish", finishArgs(runID, "verified"), nil)
	h.call("run_report", map[string]any{"runId": runID}, &rep)
	if rep.Finish == nil || rep.DestroyedAt == nil {
		t.Errorf("report after finish = %+v", rep)
	}
}

// Issue #154: a run records who verifies it when it is created, and its report names those,
// not whatever the daemon is configured with when the report is asked for.
func TestRunReportNamesTheModelsTheRunRecorded(t *testing.T) {
	h := newHarnessWith(t, WithModels(report.Models{Brain: "configured-now", Vision: "eyes-now", Source: report.SourceDaemon}))
	h.mgr.SetModels(machine.Models{Brain: machine.BrainNIM, Model: "nvidia/recorded", Vision: "meta/recorded-eyes"})
	runID := h.ready()

	var rep report.Report
	md := text(h.call("run_report", map[string]any{"runId": runID}, &rep))
	want := report.Models{Brain: "nvidia/recorded", Vision: "meta/recorded-eyes", Source: report.SourceRun}
	if rep.Models != want {
		t.Errorf("models = %+v, want %+v", rep.Models, want)
	}
	if line := "brain `nvidia/recorded`, describer `meta/recorded-eyes` (recorded with the run)"; !strings.Contains(md, line) {
		t.Errorf("markdown lacks %q:\n%s", line, md)
	}
}

func TestThroughThePublicHostTheReportLinksTheArtifactRoute(t *testing.T) {
	h := newHarnessWith(t, ForPublicHost("gr.example.com"))
	runID := h.ready()
	h.putShot()
	var shot machine.Shot
	h.call("machine_screenshot", map[string]any{"runId": runID}, &shot)
	h.verdict(runID, "pass", passChecks(shot.Step))
	var rep report.Report
	h.call("run_report", map[string]any{"runId": runID}, &rep)
	want := "https://gr.example.com/api/runs/" + runID + "/artifacts/" + shotName(shot.Path)
	if got := rep.Verdict.Checks[0].Evidence[0].Screenshot.Link; got != want {
		t.Errorf("link = %q, want %q: a host path means nothing on the caller's computer", got, want)
	}
}

func shotName(path string) string { return path[strings.LastIndex(path, "/")+1:] }

// refusedBy runs any tool and requires a tool error containing want.
func (h *harness) refusedBy(tool string, args map[string]any, want string) {
	h.t.Helper()
	res := h.raw(tool, args)
	if !res.IsError || !strings.Contains(text(res), want) {
		h.t.Errorf("%s %v = %q (error %v), want a refusal naming %q", tool, args, text(res), res.IsError, want)
	}
}
