package bench

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Rate is k events in n trials with its exact upper bound (UpperBound).
type Rate struct {
	K, N  int
	Upper float64
}

func rate(k, n int) Rate { return Rate{K: k, N: n, Upper: UpperBound(k, n, Confidence)} }

// Value is k/n, or NaN with no trials.
func (r Rate) Value() float64 {
	if r.N == 0 {
		return math.NaN()
	}
	return float64(r.K) / float64(r.N)
}

// Metrics are ADR 0025's numbers for one slice of the results.
type Metrics struct {
	Trials, Answered, Right int

	FalsePass      Rate // pass verdicts on broken builds, per answered trial: the headline
	FalsePassCases Rate // broken cases with a pass in any trial
	FalseFail      Rate // fail verdicts on correct builds
	AbstainCorrect Rate // inconclusive or ask on correct builds
	AbstainBroken  Rate // inconclusive or ask on broken builds
	AbstainInfra   Rate // inconclusive or ask on infra cases (the right answer there)
	Obtained       Rate // turns ending in a verdict or a question, of every trial whose turn ran

	Consistent, AllRight Rate // pass^k: cases whose trials all agree, and all right
	MinTrials            int  // the fewest trials any case in the slice has

	Coverage        Rate // must_check items a trial's checks covered, over trials with checks
	WithChecks      int  // trials that carried ADR 0024 checks
	RefusedVerdicts Rate // report_verdict calls refused, of all attempted

	SecondsP50, SecondsP95 float64 // per verdict
	TokensP50, TokensP95   float64 // per verdict
}

// Compute scores results (the latest of each case and trial). Setup errors are the harness's
// and count nowhere but Trials.
func Compute(results []Result) Metrics {
	var m Metrics
	var fp, fpN, ff, ffN, abC, nC, abB, nB, abI, nI, obt, ran int
	var covered, mustTotal, refused, attempted int
	var secs, toks []float64
	brokenCase := map[string]bool{}
	brokenPassed := map[string]bool{}
	for _, r := range results {
		m.Trials++
		if r.Ending == EndSetupError {
			continue
		}
		ran++
		if r.Ending == EndVerdict || r.Ending == EndQuestion {
			obt++
		}
		refused += r.RefusedVerdicts
		attempted += r.RefusedVerdicts
		if r.Ending == EndVerdict {
			attempted++
			secs = append(secs, r.Seconds)
			toks = append(toks, float64(r.Tokens))
		}
		if !r.Answered() {
			continue
		}
		m.Answered++
		if r.Right() {
			m.Right++
		}
		abstain := r.Outcome() == "inconclusive" || r.Outcome() == "ask"
		switch {
		case r.Expected == ExpectFail:
			fpN++
			nB++
			brokenCase[r.Case] = true
			if r.Outcome() == ExpectPass {
				fp++
				brokenPassed[r.Case] = true
			}
			if abstain {
				abB++
			}
		case r.Expected == ExpectPass:
			ffN++
			nC++
			if r.Outcome() == ExpectFail {
				ff++
			}
			if abstain {
				abC++
			}
		case r.Kind == KindInfra:
			nI++
			if abstain {
				abI++
			}
		}
		if c, n, ok := coverage(r); ok {
			m.WithChecks++
			covered += c
			mustTotal += n
		}
	}
	m.FalsePass, m.FalseFail = rate(fp, fpN), rate(ff, ffN)
	m.FalsePassCases = rate(len(brokenPassed), len(brokenCase))
	m.AbstainCorrect, m.AbstainBroken, m.AbstainInfra = rate(abC, nC), rate(abB, nB), rate(abI, nI)
	m.Obtained = rate(obt, ran)
	m.Coverage = rate(covered, mustTotal)
	m.RefusedVerdicts = rate(refused, attempted)
	m.SecondsP50, m.SecondsP95 = Percentile(secs, 50), Percentile(secs, 95)
	m.TokensP50, m.TokensP95 = Percentile(toks, 50), Percentile(toks, 95)

	byCase := map[string][]Result{}
	for _, r := range results {
		byCase[r.Case] = append(byCase[r.Case], r)
	}
	var consistent, allRight, cases int
	m.MinTrials = math.MaxInt
	for _, rs := range byCase {
		m.MinTrials = min(m.MinTrials, len(rs))
		if len(rs) < 2 {
			continue
		}
		cases++
		same, right := true, true
		for _, r := range rs {
			same = same && r.Outcome() == rs[0].Outcome()
			right = right && r.Right()
		}
		if same {
			consistent++
		}
		if right {
			allRight++
		}
	}
	if len(byCase) == 0 {
		m.MinTrials = 0
	}
	m.Consistent, m.AllRight = rate(consistent, cases), rate(allRight, cases)
	return m
}

// coverage counts how many of r's must_check items its checks cover; ok is false when the
// trial carried no checks (before ADR 0024, or a turn that declared none).
func coverage(r Result) (covered, total int, ok bool) {
	text := checksText(r.DeclaredChecks) + "\n" + checksText(r.Checks)
	if strings.TrimSpace(text) == "" || len(r.MustCheck) == 0 {
		return 0, 0, false
	}
	have := tokenSet(text)
	for _, item := range r.MustCheck {
		if covers(have, item) {
			covered++
		}
	}
	return covered, len(r.MustCheck), true
}

// checksText is every string in a checks array (criterion, observed, id), lowercased.
func checksText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			b.WriteString(strings.ToLower(x))
			b.WriteByte('\n')
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return b.String()
}

var tokenRE = regexp.MustCompile(`\d+(?:\.\d+)?|[a-z]+`)

var stopwords = map[string]bool{"with": true, "after": true, "reads": true, "read": true, "still": true,
	"same": true, "each": true, "that": true, "then": true, "when": true, "from": true, "such": true,
	"another": true, "other": true, "every": true, "into": true, "shows": true, "show": true, "right": true}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokenRE.FindAllString(strings.ToLower(s), -1) {
		out[t] = true
	}
	return out
}

// covers is a deliberate, simple heuristic: a must_check item is covered when every number in
// it appears in the checks' text and at least half of its other words of 4 letters or more do.
// Numbers carry most criteria ("$48.00"); words catch the rest ("persisted", "Reset").
func covers(have map[string]bool, item string) bool {
	var nums, words []string
	for _, t := range tokenRE.FindAllString(strings.ToLower(item), -1) {
		switch {
		case t[0] >= '0' && t[0] <= '9':
			nums = append(nums, t)
		case len(t) >= 4 && !stopwords[t]:
			words = append(words, t)
		}
	}
	for _, n := range nums {
		if !have[n] {
			return false
		}
	}
	hit := 0
	for _, w := range words {
		if have[w] {
			hit++
		}
	}
	return hit*2 >= len(words) && (len(nums) > 0 || len(words) > 0)
}

// WithTiers returns results with each Tier taken from the current case files by id, so
// re-tagging a case applies to results recorded before. A result whose case is not in cases (a
// case since removed or renamed) keeps the tier it was recorded with; results written before
// tiers existed have none, which reads as not tiered. matched counts the results found in cases.
func WithTiers(results []Result, cases []Case) (out []Result, matched int) {
	tier := map[string]string{}
	for _, c := range cases {
		tier[c.ID] = c.Tier
	}
	out = slices.Clone(results)
	for i := range out {
		if t, ok := tier[out[i].Case]; ok {
			out[i].Tier = t
			matched++
		}
	}
	return out, matched
}

// ReportOptions shape a report.
type ReportOptions struct {
	Tier       string // score only results of this tier; "" scores all and adds the By tier section
	TierSource string // where the results' tiers came from, for the header; "" leaves it out
}

// Report renders the Markdown report for results read from source.
func Report(source string, all []Result, now time.Time, opts ReportOptions) string {
	results := Latest(all)
	if opts.Tier != "" {
		results = filterResults(results, func(r Result) bool { return r.Tier == opts.Tier })
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("# Verifier bench report\n\n")
	p("- Results: `%s` (%d lines, %d case and trial pairs, %d cases)\n", source, len(all), len(results), countCases(results))
	if opts.Tier != "" {
		p("- Tier: only `%s` cases; every number below is theirs alone.\n", opts.Tier)
	}
	if opts.TierSource != "" {
		p("- Tiers: %s.\n", opts.TierSource)
	}
	p("- Models: %s. Images: %s.\n", joinOrNone(distinct(results, func(r Result) string { return r.Model })),
		joinOrNone(distinct(results, func(r Result) string { return r.Image })))
	if first, last, ok := span(results); ok {
		p("- Trials started %s to %s. Scored %s.\n", first.Format(time.RFC3339), last.Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	p("- Bounds are exact one-sided %.0f%% Clopper-Pearson upper bounds (0 of 30 gives 9.5%%). Trials of one case are not independent; the per-case false pass rate is the conservative reading.\n\n", Confidence*100)

	bySplit := func(rs []Result, split string) Metrics {
		return Compute(filterResults(rs, func(r Result) bool { return split == "" || r.Split == split }))
	}
	all3 := bySplit(results, "")
	var simple []Result
	if opts.Tier == "" {
		simple = filterResults(results, func(r Result) bool { return r.Tier == TierSimple })
	}
	p("## Headline\n\n")
	p("False pass rate (pass on a broken build): **%s**, per case %s.\n\n", fmtRate(all3.FalsePass), fmtRate(all3.FalsePassCases))
	if len(simple) > 0 {
		s := bySplit(simple, "")
		p("Simple tier only: **%s**, per case %s.\n\n", fmtRate(s.FalsePass), fmtRate(s.FalsePassCases))
	}

	p("## By split\n\n")
	metricsTable(&b, []string{"dev", "holdout", "all"},
		[]Metrics{bySplit(results, SplitDev), bySplit(results, SplitHoldout), all3})

	if opts.Tier == "" {
		p("\n## By tier\n\n")
		if len(simple) == 0 {
			p("No simple cases in these results.\n")
		} else {
			p("Simple cases (one flow of one app, explicit steps and outcome; `bench/README.md`) against every case.\n\n")
			metricsTable(&b, []string{"simple dev", "simple holdout", "simple", "all"},
				[]Metrics{bySplit(simple, SplitDev), bySplit(simple, SplitHoldout), bySplit(simple, ""), all3})
		}
	}

	p("\n## By kind\n\n| Split | Kind | Trials | Right | False pass | False fail | Inconclusive or ask | Obtained |\n| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, s := range []string{SplitDev, SplitHoldout} {
		for _, k := range Kinds {
			rs := filterResults(results, func(r Result) bool { return r.Split == s && r.Kind == k })
			if len(rs) == 0 {
				continue
			}
			m := Compute(rs)
			abstain := m.AbstainCorrect
			switch {
			case k == KindInfra:
				abstain = m.AbstainInfra
			case k == KindMutant:
				abstain = m.AbstainBroken
			case k != KindCorrect:
				abstain = rate(abstain.K+m.AbstainBroken.K, abstain.N+m.AbstainBroken.N)
			}
			p("| %s | %s | %d | %s | %s | %s | %s | %s |\n", s, k, m.Trials, fmtShare(rate(m.Right, m.Answered)),
				fmtRate(m.FalsePass), fmtRate(m.FalseFail), fmtShare(abstain), fmtShare(m.Obtained))
		}
	}

	p("\n## Outcomes\n\n| Kind | pass | fail | inconclusive | ask | reply | limit | model_error | timeout | setup_error |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	outcomes := []string{"pass", "fail", "inconclusive", "ask", EndReply, EndLimit, EndModelError, EndTimeout, EndSetupError}
	for _, k := range Kinds {
		rs := filterResults(results, func(r Result) bool { return r.Kind == k })
		if len(rs) == 0 {
			continue
		}
		p("| %s |", k)
		for _, o := range outcomes {
			n := 0
			for _, r := range rs {
				if r.Outcome() == o {
					n++
				}
			}
			p(" %d |", n)
		}
		p("\n")
	}

	broken := filterResults(results, func(r Result) bool { return r.Expected == ExpectFail })
	if len(broken) > 0 {
		p("\n## Broken builds by family\n\n| Family | Trials | Caught (fail) | False pass | Inconclusive or ask | No answer |\n| --- | --- | --- | --- | --- | --- |\n")
		for _, f := range Families {
			rs := filterResults(broken, func(r Result) bool { return r.Family == f })
			if len(rs) == 0 {
				continue
			}
			var caught, passed, abst, none int
			for _, r := range rs {
				switch {
				case !r.Answered():
					none++
				case r.Outcome() == "fail":
					caught++
				case r.Outcome() == "pass":
					passed++
				case r.Outcome() == "inconclusive" || r.Outcome() == "ask":
					abst++
				}
			}
			p("| %s | %d | %d | %d | %d | %d |\n", f, len(rs), caught, passed, abst, none)
		}
	}

	wrong := filterResults(results, func(r Result) bool { return r.Answered() && !r.Right() })
	p("\n## Wrong results (%d)\n\n", len(wrong))
	if len(wrong) == 0 {
		p("None.\n")
	} else {
		p("| Case | Trial | Kind | Expected | Got | What the verifier said | Run directory |\n| --- | --- | --- | --- | --- | --- | --- |\n")
		for _, r := range sortedResults(wrong) {
			p("| %s | %d | %s | %s | %s | %s | `%s` |\n", r.Case, r.Trial, r.Kind, r.Expected, r.Outcome(), cell(r.Text, 160), r.RunDir)
		}
	}
	unanswered := filterResults(results, func(r Result) bool { return !r.Answered() })
	p("\n## No answer: model errors, timeouts and setup errors (%d)\n\n", len(unanswered))
	if len(unanswered) == 0 {
		p("None.\n")
	} else {
		p("| Case | Trial | Ending | Detail | Run directory |\n| --- | --- | --- | --- | --- |\n")
		for _, r := range sortedResults(unanswered) {
			p("| %s | %d | %s | %s | `%s` |\n", r.Case, r.Trial, r.Ending, cell(orElse(r.Error, r.Text), 200), orElse(r.RunDir, "none"))
		}
	}
	return b.String()
}

// metricsTable writes one Markdown table: a row per metric, a column per slice.
func metricsTable(b *strings.Builder, headers []string, cols []Metrics) {
	p := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	p("| Metric |")
	for _, h := range headers {
		p(" %s |", h)
	}
	p("\n| --- |%s\n", strings.Repeat(" --- |", len(headers)))
	row := func(name string, f func(Metrics) string) {
		p("| %s |", name)
		for _, m := range cols {
			p(" %s |", f(m))
		}
		p("\n")
	}
	row("Trials (answered, right)", func(m Metrics) string { return fmt.Sprintf("%d (%d, %d)", m.Trials, m.Answered, m.Right) })
	row("False pass rate, per trial", func(m Metrics) string { return fmtRate(m.FalsePass) })
	row("False pass rate, per case", func(m Metrics) string { return fmtRate(m.FalsePassCases) })
	row("False fail rate", func(m Metrics) string { return fmtRate(m.FalseFail) })
	row("Inconclusive or ask, correct builds", func(m Metrics) string { return fmtShare(m.AbstainCorrect) })
	row("Inconclusive or ask, broken builds", func(m Metrics) string { return fmtShare(m.AbstainBroken) })
	row("Inconclusive or ask, infra (right)", func(m Metrics) string { return fmtShare(m.AbstainInfra) })
	row("Verdict or question obtained", func(m Metrics) string { return fmtShare(m.Obtained) })
	row("pass^k: same outcome in every trial", func(m Metrics) string { return fmtPassK(m.Consistent, m.MinTrials) })
	row("pass^k: right in every trial", func(m Metrics) string { return fmtPassK(m.AllRight, m.MinTrials) })
	row("Checklist coverage of must_check", func(m Metrics) string {
		if m.WithChecks == 0 {
			return "n/a (no checks recorded)"
		}
		return fmt.Sprintf("%s over %d trials", fmtShare(m.Coverage), m.WithChecks)
	})
	row("Verdicts refused (ADR 0024)", func(m Metrics) string { return fmtShare(m.RefusedVerdicts) })
	row("Wall time per verdict, p50 / p95", func(m Metrics) string {
		if m.SecondsP50 == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%s / %s", fmtSeconds(m.SecondsP50), fmtSeconds(m.SecondsP95))
	})
	row("Tokens per verdict, p50 / p95", func(m Metrics) string {
		if m.SecondsP50 == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%.0f / %.0f", m.TokensP50, m.TokensP95)
	})
}

func filterResults(rs []Result, keep func(Result) bool) []Result {
	var out []Result
	for _, r := range rs {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func sortedResults(rs []Result) []Result {
	out := slices.Clone(rs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Case != out[j].Case {
			return out[i].Case < out[j].Case
		}
		return out[i].Trial < out[j].Trial
	})
	return out
}

func countCases(rs []Result) int {
	return len(distinct(rs, func(r Result) string { return r.Case }))
}

func distinct(rs []Result, f func(Result) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		if v := f(r); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func span(rs []Result) (first, last time.Time, ok bool) {
	for _, r := range rs {
		if r.StartedAt.IsZero() {
			continue
		}
		if !ok || r.StartedAt.Before(first) {
			first = r.StartedAt
		}
		if !ok || r.StartedAt.After(last) {
			last = r.StartedAt
		}
		ok = true
	}
	return first, last, ok
}

func joinOrNone(xs []string) string {
	if len(xs) == 0 {
		return "none recorded"
	}
	return "`" + strings.Join(xs, "`, `") + "`"
}

// fmtRate prints "2/30 (6.7%), upper 19.5%".
func fmtRate(r Rate) string {
	if r.N == 0 {
		return "n/a (0 trials)"
	}
	return fmt.Sprintf("%d/%d (%s), upper %s", r.K, r.N, pct(r.Value()), pct(r.Upper))
}

// fmtShare prints "2/30 (6.7%)" for rates whose bound nobody acts on.
func fmtShare(r Rate) string {
	if r.N == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d (%s)", r.K, r.N, pct(r.Value()))
}

func fmtPassK(r Rate, k int) string {
	if r.N == 0 {
		return "n/a (one trial per case)"
	}
	return fmt.Sprintf("%s of cases, k=%d", fmtShare(r), k)
}

func pct(v float64) string {
	if math.IsNaN(v) {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", v*100)
}

func fmtSeconds(s float64) string {
	if s == 0 {
		return "n/a"
	}
	return (time.Duration(s * float64(time.Second))).Round(time.Second).String()
}

// cell makes text safe for one Markdown table cell.
func cell(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "..."
	}
	return s
}
