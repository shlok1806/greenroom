package verifier

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// The verifier evidence contract (ADR 0024, issue #116). The model declares its acceptance checks
// before it acts, and a verdict answers each one with the steps that show it. The daemon checks
// those steps before it posts the verdict. The checklist comes from the transcript (declarations
// are progress messages carrying checks); each step's tool, seat, error and effect come from the
// run's step records (steps.jsonl, machine.Step), never from progress text, which is only for
// display. Both are files, so a daemon restart loses nothing.

// declareFirst refuses input on an open task with no checks declared for it.
const declareFirst = "error: declare_checks first: a task is open and you have not declared its checks. Call " +
	"declare_checks with the acceptance checks you derived from the task (1 to 12, each one you can observe), " +
	"then act. Looking (machine_ui, machine_screenshot, machine_exec) needs no checks."

// observationTools may be a check's evidence; isInputTool's may be its actions.
var observationTools = []string{"machine_ui", "machine_screenshot", "machine_exec"}

// taskStart is the index in msgs of the newest task from the coder or a human, or -1.
func taskStart(msgs []session.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Kind == session.Task && msgs[i].From != session.Verifier {
			return i
		}
	}
	return -1
}

// declaredChecks is the newest checklist declared since the newest task: the one a verdict
// answers. Declaring again replaces it. Nil when none was declared for that task.
func declaredChecks(msgs []session.Message) []session.Check {
	for i := len(msgs) - 1; i > taskStart(msgs); i-- {
		m := msgs[i]
		if m.From == session.Verifier && m.Kind == session.Progress && len(m.Checks) > 0 {
			return m.Checks
		}
	}
	return nil
}

// inputRefusal is why an input call may not run now, or "".
func inputRefusal(call nim.ToolCall, msgs []session.Message) string {
	if isInputTool(call.Name) && hasOpenTask(msgs) && declaredChecks(msgs) == nil {
		return declareFirst
	}
	return ""
}

// parseDeclaredChecks reads declare_checks arguments into a checklist, or says what is wrong. Each
// check gets its applied kind (ADR 0027); notes says where that differs from what was declared.
func parseDeclaredChecks(args string) (checks []session.Check, notes []string, problem string) {
	const want = `error: declare_checks needs checks: 1 to 12 of {"id": "a short id", "criterion": "what you will observe", ` +
		`"kinds": ["value"], or any of "visual" and "timing", "within": seconds, for timing only}`
	var in struct {
		Checks []struct {
			ID        looseString `json:"id"`
			Criterion string      `json:"criterion"`
			Kinds     []string    `json:"kinds"`
			Kind      string      `json:"kind"` // one kind, as a model may still send it
			Within    *float64    `json:"within"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return nil, nil, want + argsProblem([]byte(args), err)
	}
	if len(in.Checks) == 0 || len(in.Checks) > session.MaxChecks {
		return nil, nil, fmt.Sprintf("%s; this call sent %d", want, len(in.Checks))
	}
	out := make([]session.Check, 0, len(in.Checks))
	for i, c := range in.Checks {
		id, criterion := strings.TrimSpace(string(c.ID)), strings.TrimSpace(c.Criterion)
		switch {
		case id == "" || len(id) > session.MaxCheckID:
			return nil, nil, fmt.Sprintf("error: declare_checks: check %d needs an id of 1 to %d characters", i+1, session.MaxCheckID)
		case criterion == "":
			return nil, nil, fmt.Sprintf("error: declare_checks: check %q needs a criterion: what you will observe", id)
		case slices.ContainsFunc(out, func(c session.Check) bool { return c.ID == id }):
			return nil, nil, fmt.Sprintf("error: declare_checks: check id %q appears twice; ids must be unique", id)
		}
		var declared []string // visual and timing only; value is what a check with neither is
		for _, k := range append(slices.Clone(c.Kinds), c.Kind) {
			switch k = strings.ToLower(strings.TrimSpace(k)); k {
			case "", session.CheckValue:
			case session.CheckVisual, session.CheckTiming:
				declared = append(declared, k)
			default:
				return nil, nil, fmt.Sprintf("error: declare_checks: check %q kind %q is not value, visual or timing", id, k)
			}
		}
		within := 0.0
		if c.Within != nil {
			within = *c.Within
			switch {
			case within <= 0 || within > session.MaxWithin:
				return nil, nil, fmt.Sprintf("error: declare_checks: check %q within must be seconds, above 0 and at most %g", id, session.MaxWithin)
			case len(declared) > 0 && !slices.Contains(declared, session.CheckTiming):
				return nil, nil, fmt.Sprintf("error: declare_checks: check %q is not timing; within is for a timing check only", id)
			}
		}
		kinds, within, note := applyKinds(id, criterion, declared, within)
		if note != "" {
			notes = append(notes, note)
		}
		out = append(out, session.Check{ID: id, Criterion: criterion, Kinds: kinds, Within: within})
	}
	return out, notes, ""
}

// declaredResult is what the model reads after a checklist is declared: each check with its
// applied kind and what that kind needs, and why the daemon changed a kind or a window.
func declaredResult(checks []session.Check, notes []string) string {
	ids := make([]string, len(checks))
	for i, c := range checks {
		ids[i] = kindLabel(c)
	}
	out := fmt.Sprintf("Declared %d checks: %s. report_verdict answers each by id with its status, evidence "+
		"steps and action steps. Declaring again replaces the list.", len(checks), strings.Join(ids, ", "))
	if len(notes) > 0 {
		out += "\nKinds applied by greenroom: " + strings.Join(notes, "; ") + "."
	}
	return out
}

// stepFact is what the step records say about one step.
type stepFact struct {
	tool       string
	by         string // the seat that made it
	failed     bool   // it recorded an error
	effect     string // for an input: machine.EffectChanged, EffectNone or EffectUnknown
	effectRead int    // for an input: the machine_ui step of its effect check, or 0
	at, end    time.Time
	// undrawn is, for a verifier UI read, its elements the read marked not drawn, off the
	// screen or covered (machine.UIElement.Rendered, ADR 0027).
	undrawn []machine.UIElement
}

// ledger is every step of the run by number. An input's effect is on the verifier's UI read
// that followed it (machine.Step.Effect), which is an observation step of its own.
func ledger(steps []machine.Step) map[int]stepFact {
	out := make(map[int]stepFact, len(steps))
	for _, s := range steps {
		f := stepFact{tool: s.Tool, by: s.By, failed: s.Error != "", at: s.At,
			end: s.At.Add(time.Duration(s.DurationMS) * time.Millisecond)}
		if s.Tool == "machine_ui" && s.By == machine.HolderVerifier && s.Error == "" {
			f.undrawn = undrawn(s.Output)
		}
		out[s.Seq] = f
	}
	for _, s := range steps {
		if s.Effect == nil || s.By != machine.HolderVerifier {
			continue
		}
		if f, ok := out[s.Effect.Of]; ok {
			f.effect, f.effectRead = s.Effect.Kind, s.Seq
			out[s.Effect.Of] = f
		}
	}
	return out
}

// undrawn is the elements a recorded UI read marks as not rendered. The output is a
// machine.UITree when the step was built in memory and its JSON when it was read from the log.
func undrawn(output any) []machine.UIElement {
	b, err := json.Marshal(output)
	if err != nil {
		return nil
	}
	var tree struct {
		Elements []machine.UIElement `json:"elements"`
	}
	if json.Unmarshal(b, &tree) != nil {
		return nil
	}
	var out []machine.UIElement
	for _, e := range tree.Elements {
		if e.Rendered != "" {
			out = append(out, e)
		}
	}
	return out
}

// verdictCall is a parsed report_verdict call.
type verdictCall struct {
	verdict, summary string
	checks           []session.Check
	paths            []string            // artifact paths
	problems         map[string][]string // by check id: arguments that did not parse
	general          []string            // problems with no check
}

// stepRefRE matches evidence that is a step reference, not an artifact path.
var stepRefRE = regexp.MustCompile(`(?i)^\s*(step\s*)?#?\d+\s*$`)

// parseVerdict reads report_verdict's arguments. Step lists take numbers, numeric strings and
// "step 4"; anything else is a problem the verdict check reports.
func parseVerdict(args string) verdictCall {
	var in struct {
		Verdict  string   `json:"verdict"`
		Summary  string   `json:"summary"`
		Evidence []string `json:"evidence"`
		Checks   []struct {
			ID       looseString       `json:"id"`
			Status   string            `json:"status"`
			Evidence []json.RawMessage `json:"evidence"`
			Actions  []json.RawMessage `json:"actions"`
			Observed string            `json:"observed"`
		} `json:"checks"`
	}
	out := verdictCall{problems: map[string][]string{}}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		out.general = append(out.general, "arguments: "+clip(err.Error(), 200))
	}
	out.verdict = normalVerdict(in.Verdict)
	out.summary = orElse(strings.TrimSpace(in.Summary), "(no summary)")
	for _, p := range in.Evidence {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if stepRefRE.MatchString(p) {
			out.general = append(out.general, fmt.Sprintf("evidence: %q is a step; evidence is for artifact paths "+
				"only, so cite steps in a check's evidence or actions", p))
			continue
		}
		out.paths = append(out.paths, p)
	}
	for _, c := range in.Checks {
		id := strings.TrimSpace(string(c.ID))
		ev, bad := stepNumbers(c.Evidence)
		acts, badActs := stepNumbers(c.Actions)
		for _, b := range append(bad, badActs...) {
			out.problems[id] = append(out.problems[id], fmt.Sprintf("cited step): %s is not a step number", b))
		}
		out.checks = append(out.checks, session.Check{ID: id, Status: strings.ToLower(strings.TrimSpace(c.Status)),
			Evidence: ev, Actions: acts, Observed: strings.TrimSpace(c.Observed)})
	}
	return out
}

// stepNumbers reads a step list, returning what it could not read as written.
func stepNumbers(raw []json.RawMessage) (steps []int, bad []string) {
	for _, r := range raw {
		var n float64
		if json.Unmarshal(r, &n) == nil && n == float64(int(n)) && n > 0 {
			steps = append(steps, int(n))
			continue
		}
		var s string
		if json.Unmarshal(r, &s) == nil && stepRefRE.MatchString(s) {
			digits := strings.TrimLeft(strings.ToLower(strings.TrimSpace(s)), "step #")
			if n, err := strconv.Atoi(strings.TrimSpace(digits)); err == nil && n > 0 {
				steps = append(steps, n)
				continue
			}
		}
		bad = append(bad, clip(string(r), 40))
	}
	return steps, bad
}

// verdictReview is the result of checking a verdict against the transcript.
type verdictReview struct {
	msg      session.Message // what would be posted; checks carry their declared criteria
	problems []string        // each broken rule, naming its check; empty when the verdict holds
}

// reviewVerdict checks call against the checks declared in msgs and the run's step records,
// where the screen last changed hands at handoverStep (0 for never). A pass or fail that breaks a rule
// has problems and must not be posted as it is. An inconclusive is always allowed: its answers
// are made honest instead (settle).
func reviewVerdict(call verdictCall, msgs []session.Message, records []machine.Step, handoverStep int) verdictReview {
	declared := declaredChecks(msgs)
	steps := ledger(records)
	var problems []string
	bad := map[string][]string{} // by check id, the rules its answer broke
	add := func(id, rule string) {
		bad[id] = append(bad[id], rule)
		problems = append(problems, fmt.Sprintf("check %q (%s", id, rule))
	}
	problems = append(problems, call.general...)
	if len(declared) == 0 && call.verdict != "inconclusive" {
		problems = append(problems, "checks: no checks are declared for this task; call declare_checks, observe "+
			"each check, then report_verdict answering them")
	}

	answered := map[string]session.Check{}
	for _, c := range call.checks {
		switch {
		case c.ID == "":
			problems = append(problems, "checks: a check has no id; answer each declared check by its id")
			continue
		case !slices.ContainsFunc(declared, func(d session.Check) bool { return d.ID == c.ID }):
			problems = append(problems, fmt.Sprintf("check %q (unknown id): it was not declared; the declared ids are %s",
				c.ID, idList(declared)))
			continue
		case answered[c.ID].ID != "":
			problems = append(problems, fmt.Sprintf("check %q (answered twice): answer each check once", c.ID))
			continue
		}
		d := declared[slices.IndexFunc(declared, func(d session.Check) bool { return d.ID == c.ID })]
		c.Criterion, c.Kinds, c.Within = d.Criterion, d.Kinds, d.Within
		answered[c.ID] = c
		for _, p := range call.problems[c.ID] {
			add(c.ID, p)
		}
		switch c.Status {
		case session.CheckPass, session.CheckFail:
			for _, rule := range checkEvidence(c, steps, handoverStep) {
				add(c.ID, rule)
			}
		case session.CheckUnchecked:
		default:
			add(c.ID, fmt.Sprintf("status): %q is not pass, fail or unchecked", c.Status))
		}
	}

	var checks []session.Check
	for _, d := range declared {
		c, ok := answered[d.ID]
		if !ok {
			problems = append(problems, fmt.Sprintf("check %q (answered): not answered; give it a status "+
				"(pass, fail or unchecked)", d.ID))
			c = session.Check{ID: d.ID, Status: session.CheckUnchecked, Observed: "Not answered."}
		}
		c.Criterion, c.Kinds, c.Within = d.Criterion, d.Kinds, d.Within
		checks = append(checks, c)
	}

	switch call.verdict {
	case "pass":
		for _, c := range checks {
			if _, ok := answered[c.ID]; ok && c.Status != session.CheckPass && len(bad[c.ID]) == 0 {
				add(c.ID, fmt.Sprintf("pass): its status is %s; a pass needs every check pass, else report fail "+
					"or inconclusive", orElse(c.Status, "missing")))
			}
		}
	case "fail":
		if !slices.ContainsFunc(checks, func(c session.Check) bool {
			return c.Status == session.CheckFail && len(bad[c.ID]) == 0
		}) {
			problems = append(problems, "fail: no check is fail with valid evidence; a fail needs at least one")
		}
	}

	msg := session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: call.verdict,
		Text: call.summary, Evidence: call.paths, Checks: checks}
	if call.verdict == "inconclusive" {
		msg.Checks = settle(checks, bad)
		problems = nil
	}
	return verdictReview{msg: msg, problems: problems}
}

// checkEvidence applies the per-check rules to a pass or fail answer and returns each rule it
// breaks, worded for the model. Each rule string continues "check "id" (".
func checkEvidence(c session.Check, steps map[int]stepFact, handoverStep int) []string {
	var out []string
	var fresh []int // valid evidence steps
	for _, n := range c.Evidence {
		f, ok := steps[n]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("cited step): evidence step %d is not a step you recorded in this run", n))
		case f.by != machine.HolderVerifier:
			out = append(out, fmt.Sprintf("cited step): evidence step %d was recorded by %s, not by you", n, seat(f.by)))
		case !slices.Contains(observationTools, f.tool):
			out = append(out, fmt.Sprintf("kind): evidence step %d is a %s; evidence must be an observation "+
				"(machine_ui, machine_screenshot or machine_exec)", n, f.tool))
		case f.failed:
			out = append(out, fmt.Sprintf("kind): evidence step %d failed, so it observed nothing", n))
		default:
			fresh = append(fresh, n)
		}
	}
	lastAction := 0
	var noEffect []int // effect-check reads of actions that changed nothing
	for _, n := range c.Actions {
		f, ok := steps[n]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("cited step): action step %d is not a step you recorded in this run", n))
			continue
		case f.by != machine.HolderVerifier:
			out = append(out, fmt.Sprintf("cited step): action step %d was recorded by %s, not by you", n, seat(f.by)))
			continue
		case f.tool != "machine_input":
			out = append(out, fmt.Sprintf("kind): action step %d is a %s; actions must be inputs (machine_click, "+
				"machine_type, machine_key, machine_scroll or machine_input)", n, f.tool))
			continue
		case f.failed:
			out = append(out, fmt.Sprintf("kind): action step %d failed, so it did not act", n))
			continue
		}
		lastAction = max(lastAction, n)
		if f.effect == machine.EffectNone && c.Status == session.CheckPass {
			noEffect = append(noEffect, max(f.effectRead, n))
		}
	}
	if len(c.Evidence) == 0 {
		return append(out, "freshness): no evidence steps; cite the observation that shows the result")
	}
	if len(fresh) == 0 {
		return out
	}
	newest := slices.Max(fresh)
	if newest <= lastAction {
		out = append(out, fmt.Sprintf("freshness): no evidence step comes after action step %d; observe the "+
			"result after the last action and cite that step", lastAction))
	}
	if newest <= handoverStep {
		out = append(out, fmt.Sprintf("freshness): the screen changed hands after step %d, and no evidence step "+
			"comes after that; look again and cite the new step", handoverStep))
	}
	for _, read := range noEffect {
		if newest <= read {
			out = append(out, fmt.Sprintf("effect): an action in it found no change detected (effect check at step %d), "+
				"and no later observation is cited; look again and cite a step that shows the expected state", read))
		}
	}
	// Every kind's rules apply (ADR 0027): a check that is visual and timing needs both.
	if c.Is(session.CheckVisual) {
		out = append(out, visualRule(fresh, steps, lastAction)...)
	}
	if c.Is(session.CheckTiming) {
		out = append(out, timingRule(c, fresh, steps, lastAction, noEffect)...)
	}
	if c.Status == session.CheckPass {
		out = append(out, drawnRule(c, steps, newest)...)
	}
	return out
}

// visualRule: a visual check needs a machine_screenshot among its evidence, after its actions
// (ADR 0027). The tree says what the app claims is there, not what is drawn.
func visualRule(fresh []int, steps map[int]stepFact, lastAction int) []string {
	for _, n := range fresh {
		if steps[n].tool == "machine_screenshot" && n > lastAction {
			return nil
		}
	}
	after := ""
	if lastAction > 0 {
		after = fmt.Sprintf(" after action step %d", lastAction)
	}
	return []string{fmt.Sprintf("visual): it is a visual check, and no machine_screenshot%s is among its evidence; "+
		"the UI tree does not show what is drawn. Take a machine_screenshot and cite it", after)}
}

// timingRule: a timing check needs an observation that started within its window after its last
// action ended (ADR 0027). A pass may cite no observation after its action that started later: a
// later one cannot show it happened in time. A check that is also visual may cite a later
// screenshot, which shows how it looks, not when; the in-time observation still has to be there.
// After an action whose effect read found no change, a pass needs an in-time observation after
// that read.
func timingRule(c session.Check, fresh []int, steps map[int]stepFact, lastAction int, noEffect []int) []string {
	if lastAction == 0 {
		return []string{fmt.Sprintf("timing): it is a timing check (within %g s) and cites no action; cite the input "+
			"step it times in actions and an observation that started within %g s after it", c.Within, c.Within)}
	}
	act := steps[lastAction]
	if act.end.IsZero() {
		return nil
	}
	deadline := act.end.Add(time.Duration(c.Within * float64(time.Second)))
	after := lastAction
	if c.Status == session.CheckPass && len(noEffect) > 0 {
		after = max(after, slices.Max(noEffect))
	}
	var inTime, late []int
	for _, n := range fresh {
		switch f := steps[n]; {
		case n <= lastAction:
		case f.at.After(deadline) && f.tool == "machine_screenshot" && c.Is(session.CheckVisual):
		case f.at.After(deadline):
			late = append(late, n)
		case n > after:
			inTime = append(inTime, n)
		}
	}
	var out []string
	if c.Status == session.CheckPass {
		for _, n := range late {
			out = append(out, fmt.Sprintf("timing): evidence step %d started %.1f s after action step %d ended, "+
				"later than its %g s; a later observation cannot pass a timing check. Cite only observations "+
				"that started in time, or answer fail if they do not show the state", n,
				steps[n].at.Sub(act.end).Seconds(), lastAction, c.Within))
		}
	}
	if len(inTime) == 0 {
		hint := ""
		if act.effectRead > lastAction {
			hint = fmt.Sprintf(" (the UI read right after it is step %d)", act.effectRead)
		}
		if after > lastAction {
			hint = fmt.Sprintf(" after its effect read at step %d found no change", after)
		}
		out = append(out, fmt.Sprintf("timing): no evidence step started within %g s after action step %d ended%s; "+
			"cite the observation that did, and answer fail if it does not show the state", c.Within, lastAction, hint))
	}
	return out
}

// drawnRule: a check cannot pass on an element the latest UI read marks not drawn, off the
// screen or covered (ADR 0027). The latest read is the verifier's newest machine_ui up to the
// check's newest evidence step, cited or not; its claim is its criterion and observed text.
func drawnRule(c session.Check, steps map[int]stepFact, newest int) []string {
	read := 0
	for n, f := range steps {
		if n <= newest && n > read && f.tool == "machine_ui" && f.by == machine.HolderVerifier && !f.failed {
			read = n
		}
	}
	if read == 0 {
		return nil
	}
	claim := c.Criterion + "\n" + c.Observed
	var out []string
	for _, e := range steps[read].undrawn {
		if !mentions(claim, e) {
			continue
		}
		out = append(out, fmt.Sprintf("rendered): machine_ui step %d marks [%d] %s %s, and the check rests on it; "+
			"the UI tree's text is not what a person sees there. Take a machine_screenshot, and answer fail if it "+
			"does not show it", read, e.ID, e.Name(), renderedWords(e.Rendered)))
	}
	return out
}

// renderedWords says what a machine.UIElement.Rendered mark means.
func renderedWords(mark string) string {
	switch mark {
	case machine.RenderedBlank:
		return "not drawn (its frame on screen holds no text)"
	case machine.RenderedOffscreen:
		return "off the screen"
	case machine.RenderedCovered:
		return "covered by another window"
	}
	return mark
}

// numberRE finds numbers in text: "$49.56" gives 49.56, "1,024" gives 1,024.
var numberRE = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// mentions reports whether claim names element e: one of its texts (3 characters or more) as
// whole words, or one of their numbers with two digits or a decimal point.
func mentions(claim string, e machine.UIElement) bool {
	low := strings.ToLower(claim)
	nums := map[string]bool{}
	for _, n := range numberRE.FindAllString(low, -1) {
		nums[n] = true
	}
	for _, text := range []string{e.Title, e.Label, e.Value} {
		text = strings.ToLower(strings.Join(strings.Fields(text), " "))
		if len(text) >= 3 && wordsRE([]string{text}).MatchString(low) {
			return true
		}
		for _, n := range numberRE.FindAllString(text, -1) {
			if (len(n) >= 2 || strings.ContainsAny(n, ".,")) && nums[n] {
				return true
			}
		}
	}
	return false
}

// settle makes an inconclusive verdict's answers honest: a pass or fail answer that broke a rule,
// and a check not answered, become unchecked, saying why.
func settle(checks []session.Check, bad map[string][]string) []session.Check {
	out := make([]session.Check, len(checks))
	for i, c := range checks {
		if rules := bad[c.ID]; len(rules) > 0 {
			c.Status = session.CheckUnchecked
			words := make([]string, len(rules))
			for j, r := range rules {
				words[j] = strings.Replace(r, "): ", ": ", 1)
			}
			c.Observed = "Not verified: " + strings.Join(words, "; ")
		}
		out[i] = c
	}
	return out
}

// downgrade turns a pass or fail whose evidence broke the rules into the inconclusive the closing
// call posts at a limit (issue #127): never a pass the evidence does not hold, with the reasons.
func downgrade(call verdictCall, msgs []session.Message, records []machine.Step, handoverStep int) session.Message {
	r := reviewVerdict(call, msgs, records, handoverStep)
	if len(r.problems) == 0 {
		return r.msg
	}
	was := call.verdict
	call.verdict = "inconclusive"
	msg := reviewVerdict(call, msgs, records, handoverStep).msg
	msg.Text = fmt.Sprintf("%s\n\n[greenroom] Reported as %s; posted as inconclusive because its evidence does not "+
		"hold: %s.", call.summary, was, strings.Join(r.problems, "; "))
	return msg
}

// seat names who made a step for the model.
func seat(by string) string {
	if by == "" {
		return "no named seat"
	}
	return "the " + by
}

// records is runID's step records and where the screen last changed hands, for a review. A log
// that cannot be read is a problem the review reports: no step can be shown to exist.
func (v *Verifier) records(runID string, call *verdictCall) ([]machine.Step, int) {
	steps, err := v.mgr.Steps(runID)
	if err != nil {
		v.log.Warn("verifier cannot read the step records", "runId", runID, "err", err)
		call.general = append(call.general, "steps: the run's step records could not be read: "+clip(err.Error(), 200))
	}
	return steps, v.mgr.HandoverStep(runID)
}

// refusal is the tool result for a verdict that was not posted.
func refusal(problems []string) string {
	var b strings.Builder
	b.WriteString("error: report_verdict refused; nothing was posted. Fix each problem and call it again, or " +
		"report inconclusive and mark what you could not show unchecked:")
	for _, p := range problems {
		b.WriteString("\n- " + p)
	}
	return b.String()
}

func idList(checks []session.Check) string {
	if len(checks) == 0 {
		return "none (call declare_checks)"
	}
	ids := make([]string, len(checks))
	for i, c := range checks {
		ids[i] = strconv.Quote(c.ID)
	}
	return strings.Join(ids, ", ")
}
