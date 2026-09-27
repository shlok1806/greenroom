package report

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtureRun is a real recorded run (a Companion change verified on a Greenroom machine), copied
// from ~/.greenroom/runs with long outputs cut, host paths neutralised and the one evidence
// screenshot shrunk. Its verdict is a pass with three checks, accepted by the coder after a dispute.
const fixtureRun = "20260926-231011-600cf88cfbdbcba8"

var fixtureModels = Models{Brain: "nvidia/nemotron-3-ultra-550b-a55b", Vision: "meta/muse-glimmer-30b"}

// fixtureInput reads the fixture's conversation and adds the finish the coding agent would have
// sent before the machine was destroyed, at a fixed time so the goldens are stable.
func fixtureInput(t *testing.T, links Links) Input {
	t.Helper()
	dir := filepath.Join("testdata", fixtureRun)
	store, err := session.Open(dir, session.DefaultMaxDisputes)
	if err != nil {
		t.Fatal(err)
	}
	msgs := store.After(0)
	at := time.Date(2026, 9, 26, 23, 56, 0, 0, time.UTC)
	f := session.Finish{Outcome: session.OutcomeVerified, At: at,
		Summary: "The stop card for a verifier reply that hit its limit now has a Continue button, and the runs list says why a run has no verdict.",
		Ref:     &session.Ref{Branch: "rsi/stop-limit-card", Commit: "e4e87f6", PR: "https://github.com/shlok1806/greenroom/pull/161"}}
	msgs = append(msgs, session.Message{Seq: len(msgs) + 1, At: at, From: session.System, Kind: session.Event,
		Text: session.FinishText(f), Finish: &f})
	return Input{Dir: dir, Messages: msgs, Verdict: store.Verdict(), Models: fixtureModels, Links: links}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/report -update to write it)", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the golden file; rerun with -update if the change is meant.\ngot:\n%s", name, got)
	}
}

func TestTheReportOfARecordedRunMatchesItsGoldens(t *testing.T) {
	rep, err := Build(fixtureInput(t, Links{BaseURL: "https://gr.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "report.md", []byte(rep.Markdown()))
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "report.json", append(data, '\n'))
}

func TestTheReportResolvesEveryCheckAgainstTheStepLog(t *testing.T) {
	rep, err := Build(fixtureInput(t, Links{BaseURL: "https://gr.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Finish == nil || rep.Finish.Outcome != session.OutcomeVerified || rep.Finish.Ref.PR == "" {
		t.Fatalf("finish = %+v", rep.Finish)
	}
	if rep.TaskSeq != 68 || !strings.HasPrefix(rep.Task, "Thanks: your four passing checks") {
		t.Errorf("task = %d %.40q, want the task the verdict answers (68)", rep.TaskSeq, rep.Task)
	}
	v := rep.Verdict
	if v == nil || v.Seq != 91 || v.Verdict != "pass" || v.Status != session.Accepted || v.AcceptedBy != session.Coder || v.Disputes != 1 {
		t.Fatalf("verdict = %+v", v)
	}
	if len(v.Checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(v.Checks))
	}
	card := v.Checks[0]
	if card.ID != "run-b-card-after" || card.Kinds[0] != session.CheckVisual || len(card.Evidence) != 2 || len(card.Actions) != 2 {
		t.Fatalf("first check = %+v", card)
	}
	ui, shot := card.Evidence[0], card.Evidence[1]
	if ui.Step != 103 || ui.Tool != "machine_ui" || ui.Screenshot != nil || ui.At == nil {
		t.Errorf("evidence 103 = %+v, want a machine_ui step with no screenshot", ui)
	}
	want := "https://gr.example.com/api/runs/" + fixtureRun + "/artifacts/107-screenshot.png"
	if shot.Step != 107 || shot.Tool != "machine_screenshot" || shot.Screenshot == nil || shot.Screenshot.Link != want {
		t.Errorf("evidence 107 = %+v, want a screenshot linked at %s", shot, want)
	}
	if card.Actions[0].Tool != "machine_input" || card.Actions[0].By != "verifier" {
		t.Errorf("action 98 = %+v", card.Actions[0])
	}
	if len(v.Artifacts) != 1 || v.Artifacts[0].File != "107-screenshot.png" {
		t.Errorf("artifacts = %+v", v.Artifacts)
	}
	if rep.Models.Source != SourceDaemon {
		t.Errorf("models source = %q, want %q", rep.Models.Source, SourceDaemon)
	}
}

func TestLocallyAScreenshotIsItsPathOnThisHost(t *testing.T) {
	rep, err := Build(fixtureInput(t, Links{}))
	if err != nil {
		t.Fatal(err)
	}
	got := rep.Verdict.Checks[0].Evidence[1].Screenshot.Link
	if want := filepath.Join("testdata", fixtureRun, "107-screenshot.png"); got != want {
		t.Errorf("link = %q, want the file in the run directory %q (never the recorded host path)", got, want)
	}
	if !strings.Contains(rep.Markdown(), "[step 107 (screenshot)](<"+got+">)") {
		t.Errorf("markdown does not link the screenshot by path:\n%s", rep.Markdown())
	}
}

func TestEmbedPutsTheScreenshotInTheReport(t *testing.T) {
	rep, err := Build(fixtureInput(t, Links{BaseURL: "https://gr.example.com", Embed: true}))
	if err != nil {
		t.Fatal(err)
	}
	link := rep.Verdict.Checks[0].Evidence[1].Screenshot.Link
	if !strings.HasPrefix(link, "data:image/png;base64,iVBOR") {
		t.Fatalf("link = %.60q, want a PNG data URI", link)
	}
	if !strings.Contains(rep.Markdown(), "![step 107 (screenshot)](data:image/png;base64,") {
		t.Error("markdown does not show the embedded screenshot as an image")
	}
}

// writeRun makes a run directory with a manifest and the given step lines.
func writeRun(t *testing.T, steps ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "20260927-100000-0000000000000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	man := `{"runId":"20260927-100000-0000000000000001","image":"img","machineName":"m","createdAt":"2026-09-27T10:00:00Z","steps":3}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "steps.jsonl"), []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAFailWithAnUncheckedCheckSaysWhatWasNotObserved(t *testing.T) {
	dir := writeRun(t,
		`{"seq":1,"at":"2026-09-27T10:01:00Z","tool":"machine_input","by":"verifier","durationMs":5}`,
		`{"seq":2,"at":"2026-09-27T10:01:02Z","tool":"machine_screenshot","by":"verifier","output":{"path":"/elsewhere/002-screenshot.png"},"durationMs":5}`)
	// The screenshot the step names is missing from the run directory: no link, no guess.
	v := session.VerdictState{Seq: 2, Verdict: "fail", Status: session.Proposed, Summary: "Total is wrong.",
		Checks: []session.Check{
			{ID: "total", Criterion: "Total shows $48.00", Status: "fail", Evidence: []int{2}, Actions: []int{1}, Observed: "Total shows $0.00."},
			{ID: "fast", Criterion: "Total appears at once", Kinds: []string{"timing"}, Within: 2, Status: "unchecked", Observed: "Not answered."},
		}}
	msgs := []session.Message{
		{Seq: 1, From: session.Coder, Kind: session.Task, Text: "Check the total."},
		{Seq: 2, From: session.Verifier, Kind: session.Verdict, Verdict: "fail", Text: "Total is wrong.", Checks: v.Checks},
	}
	rep, err := Build(Input{Dir: dir, Messages: msgs, Verdict: v})
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown()
	for _, want := range []string{
		"## Greenroom: Not finished",
		"- **Outcome:** not finished",
		"- **Verdict:** fail (message 2), proposed",
		"- **Models:** no verifier configured",
		"- FAIL **total** (value): Total shows $48.00",
		"  - Evidence: step 2 (screenshot)\n",
		"- NOT CHECKED **fast** (timing within 2 s): Total appears at once",
		"A check listed as not checked was not observed",
		"> Check the total.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if rep.Verdict.Checks[0].Evidence[0].Screenshot != nil {
		t.Error("a screenshot missing from the run directory must not be linked")
	}
}

func TestARunWithNoVerdictSaysNothingWasChecked(t *testing.T) {
	dir := writeRun(t)
	f := session.Finish{Outcome: session.OutcomeAbandoned, Summary: "Gave up: the build needs a signing identity.", At: time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)}
	msgs := []session.Message{{Seq: 1, From: session.System, Kind: session.Event, Text: session.FinishText(f), Finish: &f}}
	rep, err := Build(Input{Dir: dir, Messages: msgs, Verdict: session.VerdictState{Status: session.None}, Models: Models{Brain: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown()
	for _, want := range []string{
		"## Greenroom: Abandoned\n\nGave up: the build needs a signing identity.",
		"- **Outcome:** abandoned, the work was given up 2026-09-27 11:00 UTC",
		"- **Verdict:** none",
		"- **Models:** brain `manual`, no describer",
		"No verdict: the verifier did not check this run",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	data, _ := json.Marshal(rep)
	if !strings.Contains(string(data), `"verdict":null`) {
		t.Errorf("json = %s, want an explicit null verdict", data)
	}
}

func TestAFinishIsReadFromTheManifestWhenTheConversationLacksIt(t *testing.T) {
	dir := writeRun(t)
	man := `{"runId":"r","createdAt":"2026-09-27T10:00:00Z","steps":0,"finish":{"outcome":"unverified","summary":"Shipped.","at":"2026-09-27T12:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Build(Input{Dir: dir, Verdict: session.VerdictState{Status: session.None}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Finish == nil || rep.Finish.Outcome != session.OutcomeUnverified {
		t.Fatalf("finish = %+v", rep.Finish)
	}
}

func TestMarkdownKeepsFieldsOnTheirLines(t *testing.T) {
	if got := code("a `b` c"); got != "``a `b` c``" {
		t.Errorf("code = %q", got)
	}
	if got := refLine(session.Ref{PR: "#12"}); got != "PR `#12`" {
		t.Errorf("a PR that is not a URL = %q", got)
	}
	if got := quote("one\n\ntwo </details>"); got != "> one\n>\n> two &lt;/details>" {
		t.Errorf("quote = %q", got)
	}
}
