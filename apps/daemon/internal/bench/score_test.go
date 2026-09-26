package bench

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// res builds a result: the case's kind decides what it expects.
func res(caseID string, trial int, kind, split, ending, verdict string) Result {
	r := Result{Case: caseID, Trial: trial, Kind: kind, Split: split, Ending: ending, Verdict: verdict,
		RunDir: "/runs/" + caseID + "-" + string(rune('0'+trial)), StartedAt: time.Date(2026, 9, 25, 12, trial, 0, 0, time.UTC)}
	switch kind {
	case KindCorrect:
		r.Expected = ExpectPass
	case KindMutant:
		r.Expected, r.Family = ExpectFail, "dead_control"
	default:
		r.Expected = AskOrInconclusive
	}
	return r
}

func TestComputeCountsFalsePassesAndFailsPerTrialAndPerCase(t *testing.T) {
	results := []Result{
		res("m1", 1, KindMutant, SplitDev, EndVerdict, "fail"),
		res("m1", 2, KindMutant, SplitDev, EndVerdict, "pass"), // a false pass
		res("m1", 3, KindMutant, SplitDev, EndVerdict, "fail"),
		res("m2", 1, KindMutant, SplitDev, EndVerdict, "inconclusive"),
		res("m2", 2, KindMutant, SplitDev, EndModelError, ""), // not an answer: out of the rates
		res("m2", 3, KindMutant, SplitDev, EndQuestion, ""),
		res("c1", 1, KindCorrect, SplitDev, EndVerdict, "pass"),
		res("c1", 2, KindCorrect, SplitDev, EndVerdict, "fail"), // a false fail
		res("c1", 3, KindCorrect, SplitDev, EndLimit, ""),
		res("i1", 1, KindInfra, SplitDev, EndQuestion, ""),
		res("i1", 2, KindInfra, SplitDev, EndVerdict, "pass"),
		res("i1", 3, KindInfra, SplitDev, EndSetupError, ""),
	}
	m := Compute(results)
	if m.Trials != 12 || m.Answered != 10 {
		t.Errorf("trials %d answered %d, want 12 and 10 (all but the model and setup errors)", m.Trials, m.Answered)
	}
	// Right: m1#1, m1#3, c1#1, i1#1.
	if m.Right != 4 {
		t.Errorf("right = %d, want 4", m.Right)
	}
	if m.FalsePass.K != 1 || m.FalsePass.N != 5 {
		t.Errorf("false pass = %d/%d, want 1/5", m.FalsePass.K, m.FalsePass.N)
	}
	if want := UpperBound(1, 5, Confidence); m.FalsePass.Upper != want {
		t.Errorf("false pass upper = %v, want %v", m.FalsePass.Upper, want)
	}
	if m.FalsePassCases.K != 1 || m.FalsePassCases.N != 2 {
		t.Errorf("false pass cases = %d/%d, want 1/2", m.FalsePassCases.K, m.FalsePassCases.N)
	}
	if m.FalseFail.K != 1 || m.FalseFail.N != 3 {
		t.Errorf("false fail = %d/%d, want 1/3 (the limit counts in n)", m.FalseFail.K, m.FalseFail.N)
	}
	if m.AbstainBroken.K != 2 || m.AbstainBroken.N != 5 {
		t.Errorf("abstain on broken = %d/%d, want 2/5", m.AbstainBroken.K, m.AbstainBroken.N)
	}
	if m.AbstainInfra.K != 1 || m.AbstainInfra.N != 2 {
		t.Errorf("abstain on infra = %d/%d, want 1/2", m.AbstainInfra.K, m.AbstainInfra.N)
	}
	// Verdict or question, of the 11 trials whose turn ran: all but the model error and the limit.
	if m.Obtained.K != 9 || m.Obtained.N != 11 {
		t.Errorf("obtained = %d/%d, want 9/11", m.Obtained.K, m.Obtained.N)
	}
	// Four cases of three trials: none agrees throughout, none is right throughout.
	if m.Consistent.K != 0 || m.Consistent.N != 4 || m.AllRight.K != 0 || m.MinTrials != 3 {
		t.Errorf("pass^k = %+v / %+v (min %d), want 0/4 and 0/4, k=3", m.Consistent, m.AllRight, m.MinTrials)
	}
}

func TestComputeTimesAndTokensPerVerdictAndRefusals(t *testing.T) {
	var results []Result
	for i, s := range []float64{100, 200, 300, 400} {
		r := res("m", i+1, KindMutant, SplitDev, EndVerdict, "fail")
		r.Seconds, r.Tokens = s, int(s)*10
		results = append(results, r)
	}
	q := res("m", 5, KindMutant, SplitDev, EndQuestion, "")
	q.Seconds, q.RefusedVerdicts = 9999, 2 // a question is not a verdict: out of the time percentiles
	results = append(results, q)
	m := Compute(results)
	if m.SecondsP50 != 200 || m.SecondsP95 != 400 || m.TokensP50 != 2000 || m.TokensP95 != 4000 {
		t.Errorf("p50/p95 = %v/%v s, %v/%v tokens", m.SecondsP50, m.SecondsP95, m.TokensP50, m.TokensP95)
	}
	if m.RefusedVerdicts.K != 2 || m.RefusedVerdicts.N != 6 {
		t.Errorf("refused = %d/%d, want 2 of 6 attempts", m.RefusedVerdicts.K, m.RefusedVerdicts.N)
	}
	if m.Consistent.K != 0 || m.AllRight.K != 0 {
		t.Error("a case with a question among fails is neither consistent nor all right")
	}
}

func TestCoverageMatchesMustCheckAgainstTheChecks(t *testing.T) {
	r := res("m", 1, KindMutant, SplitDev, EndVerdict, "fail")
	r.MustCheck = []string{"Each pays reads $48.00 for Bill 120, 20% tip, 3 people", "Reset returns Bill to 84.00", "Tip stays $15.12"}
	r.DeclaredChecks = json.RawMessage(`[{"id":"c1","criterion":"Each pays shows $48.00 with bill 120, tip 20%, people 3"},
		{"id":"c2","criterion":"Pressing Reset puts the bill back to 84.00"}]`)
	r.Checks = json.RawMessage(`[{"id":"c1","status":"fail","evidence":[4],"observed":"showed $8.00"}]`)
	covered, total, ok := coverage(r)
	if !ok || covered != 2 || total != 3 {
		t.Errorf("coverage = %d/%d (%v), want 2/3", covered, total, ok)
	}
	r.DeclaredChecks, r.Checks = nil, nil
	if _, _, ok := coverage(r); ok {
		t.Error("a result without checks has no coverage")
	}
	m := Compute([]Result{r})
	if m.WithChecks != 0 || m.Coverage.N != 0 {
		t.Errorf("checks counted without any: %+v", m)
	}
}

func TestLatestKeepsTheLastResultOfEachPair(t *testing.T) {
	first := res("a", 1, KindMutant, SplitDev, EndSetupError, "")
	again := res("a", 1, KindMutant, SplitDev, EndVerdict, "fail")
	other := res("b", 1, KindMutant, SplitDev, EndVerdict, "pass")
	got := Latest([]Result{first, other, again})
	if len(got) != 2 || got[0].Ending != EndVerdict || got[1].Case != "b" {
		t.Errorf("Latest = %+v", got)
	}
	d := done([]Result{first})
	if d[first.key()] {
		t.Error("a setup error counts as done; a resume must retry it")
	}
	if !done([]Result{first, again})[first.key()] {
		t.Error("a verdict after the setup error is done")
	}
}

func TestReadResultsSkipsATornLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.jsonl")
	line, _ := json.Marshal(res("a", 1, KindMutant, SplitDev, EndVerdict, "fail"))
	if err := os.WriteFile(path, append(append(line, '\n'), []byte(`{"case":"b","tri`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResults(path)
	if err != nil || len(got) != 1 || got[0].Case != "a" {
		t.Fatalf("ReadResults = %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("{bad}\n"+string(line)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResults(path); err == nil {
		t.Error("a bad line that ends in a newline is damage, not a torn write")
	}
}

func TestReportShowsTheHeadlineAndEveryWrongResultWithItsRunDirectory(t *testing.T) {
	results := []Result{
		res("m1", 1, KindMutant, SplitDev, EndVerdict, "pass"),
		res("m2", 1, KindMutant, SplitHoldout, EndVerdict, "fail"),
		res("c1", 1, KindCorrect, SplitDev, EndVerdict, "pass"),
		res("i1", 1, KindInfra, SplitDev, EndVerdict, "fail"),
		res("i2", 1, KindInfra, SplitHoldout, EndTimeout, ""),
	}
	results[0].Text = "Each pays | reads $48.00\nas asked."
	results[0].Model, results[0].Image = "some/model", "greenroom-lean-a"
	results[4].Error = "no end of the verifier's turn after 30m0s"
	out := Report("/tmp/r.jsonl", results, time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC), ReportOptions{})
	for _, want := range []string{
		"# Verifier bench report",
		"`some/model`", "`greenroom-lean-a`",
		"False pass rate (pass on a broken build): **1/2 (50.0%), upper 97.5%**",
		"| False pass rate, per trial | 1/1 (100.0%), upper 100.0% | 0/1 (0.0%), upper 95.0% | 1/2 (50.0%), upper 97.5% |",
		"## Wrong results (2)",
		"| m1 | 1 | mutant | fail | pass | Each pays \\| reads $48.00 as asked. | `/runs/m1-1` |",
		"| i1 | 1 | infra | ask_or_inconclusive | fail |",
		"## No answer: model errors, timeouts and setup errors (1)",
		"| i2 | 1 | timeout | no end of the verifier's turn after 30m0s | `/runs/i2-1` |",
		"| dead_control | 2 | 1 | 1 | 0 | 0 |",
		"n/a (one trial per case)",
		"n/a (no checks recorded)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Error("the report has an em dash")
	}
}

func TestWithTiersPrefersTheCaseFilesAndKeepsTheRecordedTierOfAGoneCase(t *testing.T) {
	old := res("m1", 1, KindMutant, SplitDev, EndVerdict, "fail") // recorded before tiers: none
	retagged := res("m2", 1, KindMutant, SplitDev, EndVerdict, "fail")
	retagged.Tier = TierSimple // simple when it ran, untagged since
	gone := res("m3", 1, KindMutant, SplitDev, EndVerdict, "fail")
	gone.Tier = TierSimple
	results := []Result{old, retagged, gone}
	out, matched := WithTiers(results, []Case{{ID: "m1", Tier: TierSimple}, {ID: "m2"}})
	if matched != 2 {
		t.Errorf("matched %d, want 2", matched)
	}
	for i, want := range []string{TierSimple, "", TierSimple} {
		if out[i].Tier != want {
			t.Errorf("%s: tier %q, want %q", out[i].Case, out[i].Tier, want)
		}
	}
	if results[0].Tier != "" {
		t.Error("WithTiers changed its input")
	}
}

// A results line from before tiers has no tier field and reads as not tiered.
func TestAResultWithNoTierReadsAsNotTiered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.jsonl")
	line := `{"case":"m1","trial":1,"kind":"mutant","split":"dev","expected":"fail","ending":"verdict","verdict":"fail","startedAt":"2026-09-25T12:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := ReadResults(path)
	if err != nil || len(rs) != 1 || rs[0].Tier != "" {
		t.Fatalf("read %+v, %v; want one result with no tier", rs, err)
	}
	out := Report(path, rs, time.Now(), ReportOptions{})
	if !strings.Contains(out, "No simple cases in these results.") {
		t.Errorf("an untiered result was scored as simple:\n%s", out)
	}
}

func TestReportHasATierSectionAndScoresOneTierOnRequest(t *testing.T) {
	simple := func(r Result) Result { r.Tier = TierSimple; return r }
	results := []Result{
		simple(res("m1", 1, KindMutant, SplitDev, EndVerdict, "pass")),
		simple(res("m1", 2, KindMutant, SplitDev, EndVerdict, "pass")),
		simple(res("m1", 3, KindMutant, SplitDev, EndVerdict, "pass")),
		simple(res("c1", 1, KindCorrect, SplitHoldout, EndVerdict, "pass")),
		simple(res("c1", 2, KindCorrect, SplitHoldout, EndVerdict, "pass")),
		simple(res("c1", 3, KindCorrect, SplitHoldout, EndVerdict, "pass")),
		res("m2", 1, KindMutant, SplitDev, EndVerdict, "fail"),
		res("m2", 2, KindMutant, SplitDev, EndVerdict, "fail"),
		res("m2", 3, KindMutant, SplitDev, EndVerdict, "pass"),
	}
	now := time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC)
	out := Report("/tmp/r.jsonl", results, now, ReportOptions{TierSource: "from the case files"})
	for _, want := range []string{
		"- Tiers: from the case files.",
		"Simple tier only: **3/3 (100.0%), upper 100.0%**, per case 1/1 (100.0%), upper 100.0%.",
		"## By tier",
		"| Metric | simple dev | simple holdout | simple | all |",
		"| False pass rate, per trial | 3/3 (100.0%), upper 100.0% | n/a (0 trials) | 3/3 (100.0%), upper 100.0% | 4/6 (66.7%), upper 93.7% |",
		"| pass^k: same outcome in every trial | 1/1 (100.0%) of cases, k=3 | 1/1 (100.0%) of cases, k=3 | 2/2 (100.0%) of cases, k=3 | 2/3 (66.7%) of cases, k=3 |",
		"| pass^k: right in every trial | 0/1 (0.0%) of cases, k=3 | 1/1 (100.0%) of cases, k=3 | 1/2 (50.0%) of cases, k=3 | 1/3 (33.3%) of cases, k=3 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q\n%s", want, out)
		}
	}

	only := Report("/tmp/r.jsonl", results, now, ReportOptions{Tier: TierSimple})
	for _, want := range []string{
		"- Tier: only `simple` cases",
		"(9 lines, 6 case and trial pairs, 2 cases)",
		"False pass rate (pass on a broken build): **3/3 (100.0%), upper 100.0%**",
	} {
		if !strings.Contains(only, want) {
			t.Errorf("simple-only report lacks %q\n%s", want, only)
		}
	}
	if strings.Contains(only, "## By tier") || strings.Contains(only, "m2") {
		t.Errorf("a simple-only report shows other cases or a tier section:\n%s", only)
	}
}

func TestRateValueWithNoTrialsIsNaN(t *testing.T) {
	if !math.IsNaN(rate(0, 0).Value()) || fmtRate(rate(0, 0)) != "n/a (0 trials)" {
		t.Error("a rate over no trials must read n/a")
	}
}
