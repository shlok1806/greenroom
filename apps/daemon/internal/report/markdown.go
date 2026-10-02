package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Markdown renders the report to paste into a PR body or comment: a heading with the outcome,
// the summary, the facts, every check with what was observed and its evidence, then the task.
func (r Report) Markdown() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	// A run whose Mac never started says so, unless the coding agent finished it: its word on
	// the run outranks (root ADR 0049).
	notStarted := r.StartError != "" && r.Finish == nil
	if notStarted {
		w("## Greenroom: Did not start\n\n")
		w("- **Outcome:** did not start: the machine never became ready: %s\n", code(r.StartError))
	} else {
		w("## Greenroom: %s\n\n", outcomeTitle(r.Finish))
		if r.Finish != nil {
			w("%s\n\n", oneLine(r.Finish.Summary))
		}
		w("- **Outcome:** %s\n", outcomeLine(r.Finish))
	}
	if r.StartError != "" && !notStarted {
		w("- **Machine:** never became ready: %s\n", code(r.StartError))
	}
	if r.Finish != nil && r.Finish.Ref != nil && !r.Finish.Ref.IsZero() {
		w("- **Ref:** %s\n", refLine(*r.Finish.Ref))
	}
	w("- **Verdict:** %s\n", verdictLine(r.Verdict))
	w("- **Models:** %s\n", modelsLine(r.Models))
	w("- **Run:** %s\n", r.runLine())

	w("\n### Checks\n\n%s\n\n", r.Scope)
	if r.Verdict != nil {
		if r.Verdict.Summary != "" {
			w("Verifier: %s\n\n", oneLine(r.Verdict.Summary))
		}
		for _, c := range r.Verdict.Checks {
			writeCheck(&b, c)
		}
		if len(r.Verdict.Artifacts) > 0 {
			links := make([]string, 0, len(r.Verdict.Artifacts))
			for _, a := range r.Verdict.Artifacts {
				links = append(links, artifactLink(a, a.File))
			}
			w("\nFiles the verdict cites: %s\n", strings.Join(links, ", "))
		}
	}

	if r.Task != "" {
		w("\n<details><summary>Task (message %d)</summary>\n\n%s\n\n</details>\n", r.TaskSeq, quote(r.Task))
	}
	w("\n<sub>Made by greenroom from run %s. A check passes only on evidence the verifier observed; the coding agent's claims are not evidence.</sub>\n", code(r.RunID))
	return b.String()
}

func writeCheck(b *strings.Builder, c Check) {
	fmt.Fprintf(b, "- %s **%s** (%s): %s\n", statusWord(c.Status), oneLine(c.ID), kindsText(c), oneLine(c.Criterion))
	if c.Observed != "" {
		fmt.Fprintf(b, "  - Observed: %s\n", oneLine(c.Observed))
	}
	if len(c.Evidence) > 0 {
		fmt.Fprintf(b, "  - Evidence: %s\n", stepsText(c.Evidence))
	}
	if len(c.Actions) > 0 {
		fmt.Fprintf(b, "  - Actions: %s\n", stepsText(c.Actions))
	}
}

func statusWord(status string) string {
	switch status {
	case session.CheckPass:
		return "PASS"
	case session.CheckFail:
		return "FAIL"
	}
	return "NOT CHECKED"
}

func kindsText(c Check) string {
	s := strings.Join(c.Kinds, ", ")
	if c.Within > 0 {
		s += fmt.Sprintf(" within %g s", c.Within)
	}
	return s
}

func stepsText(steps []Step) string {
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		label := fmt.Sprintf("step %d", s.Step)
		if s.Tool != "" {
			label += " (" + strings.TrimPrefix(s.Tool, "machine_") + ")"
		}
		if s.Screenshot != nil {
			parts = append(parts, artifactLink(*s.Screenshot, label))
			continue
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

// artifactLink is a Markdown link to a, shown as an image when it is embedded.
func artifactLink(a Artifact, label string) string {
	if a.Link == "" {
		return label
	}
	if strings.HasPrefix(a.Link, "data:") {
		return fmt.Sprintf("![%s](%s)", label, a.Link)
	}
	return fmt.Sprintf("[%s](<%s>)", label, a.Link)
}

func outcomeTitle(f *session.Finish) string {
	if f == nil {
		return "Not finished"
	}
	switch f.Outcome {
	case session.OutcomeVerified:
		return "Verified"
	case session.OutcomeUnverified:
		return "Unverified"
	case session.OutcomeAbandoned:
		return "Abandoned"
	}
	return f.Outcome
}

func outcomeLine(f *session.Finish) string {
	if f == nil {
		return "not finished: the coding agent has not called run_finish"
	}
	at := stamp(f.At)
	switch f.Outcome {
	case session.OutcomeVerified:
		return "verified, an accepted pass on this run, finished " + at
	case session.OutcomeUnverified:
		return "unverified, finished without an accepted pass " + at
	case session.OutcomeAbandoned:
		return "abandoned, the work was given up " + at
	}
	return f.Outcome + ", finished " + at
}

func refLine(ref session.Ref) string {
	var parts []string
	if ref.Branch != "" {
		parts = append(parts, "branch "+code(ref.Branch))
	}
	if ref.Commit != "" {
		parts = append(parts, "commit "+code(ref.Commit))
	}
	if ref.PR != "" {
		pr := oneLine(ref.PR)
		if !strings.HasPrefix(pr, "http://") && !strings.HasPrefix(pr, "https://") {
			pr = code(pr)
		}
		parts = append(parts, "PR "+pr)
	}
	return strings.Join(parts, ", ")
}

func verdictLine(v *Verdict) string {
	if v == nil {
		return "none"
	}
	s := fmt.Sprintf("%s (message %d), %s", v.Verdict, v.Seq, v.Status)
	if v.Status == session.Accepted && v.AcceptedBy != "" {
		s += " by " + string(v.AcceptedBy)
	}
	if v.Disputes > 0 {
		s += ", after " + count(v.Disputes, "dispute")
	}
	return s
}

func modelsLine(m Models) string {
	if m.Brain == "" && m.Vision == "" {
		return "no verifier configured"
	}
	var parts []string
	if m.Brain != "" {
		parts = append(parts, "brain "+code(m.Brain))
	}
	if m.Vision != "" {
		parts = append(parts, "describer "+code(m.Vision))
	} else {
		parts = append(parts, "no describer")
	}
	s := strings.Join(parts, ", ")
	if m.Source != "" {
		s += " (" + m.Source + ")"
	}
	return s
}

func (r Report) runLine() string {
	s := fmt.Sprintf("%s, %s, created %s", code(r.RunID), count(r.Steps, "step"), stamp(r.CreatedAt))
	if r.DestroyedAt != nil {
		s += ", machine destroyed " + stamp(*r.DestroyedAt)
	}
	return s
}

// count is n and noun, plural unless n is one: "1 step", "0 steps", "3 steps" (issue #297).
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "at an unknown time"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// oneLine keeps a field on its list line: Markdown would end the item at a blank line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// code wraps s in a code span that its own backticks cannot close.
func code(s string) string {
	s = oneLine(s)
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// quote renders text as a block quote, keeping its lines. A closing </details> in it is
// defused so the task cannot end the fold early.
func quote(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "</details>", "&lt;/details>")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}
