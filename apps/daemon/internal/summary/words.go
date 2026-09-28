package summary

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Word limits (docs/20 section 6): a check row reads in eight words, a failing check's line in
// twelve.
const (
	checkWords    = 8
	failingWords  = 12
	observedWords = 25
	targetWords   = 3
)

// idleAfter is how long an open run may go without a step or message before "now" says it is
// idle rather than repeating its last step (the Companion's RunFacts.idleAfter).
const idleAfter = 5 * time.Minute

func detail(in Input, f derived, st State) string {
	switch st {
	case Passed, Failed, Inconclusive:
		return reviewSentence(in, f)
	case Paused:
		if f.question != nil {
			// The question itself is the verifier's prose; the composer shows it with Answer.
			return "The verifier has a question for you."
		}
		return stopSentence(f.stop.Stop) + " Continue lets it go on."
	case NotAnswering:
		return "The Mac's screen stopped answering. Restart it? Your files are kept."
	case Restarting:
		return "Restarting the Mac. Your files are kept."
	case Ready:
		if in.Verdict.Status == session.Rejected {
			return rejectedSentence(in)
		}
	case Stopped:
		return stoppedSentence(in)
	}
	return ""
}

// reviewSentence says who stands behind the current verdict.
func reviewSentence(in Input, f derived) string {
	v := in.Verdict
	switch {
	case v.Status == session.Proposed:
		return "Proposed by the verifier after " + clock(verdictTook(in, f)) + "."
	case v.Status == session.Contested:
		return "Proposed by the verifier. Only you can accept or reject it now."
	case v.Status == session.Accepted && v.AcceptedBy == session.Human:
		return "You accepted it."
	case v.Status == session.Accepted:
		return "The coding agent accepted it; you have not reviewed it."
	}
	return ""
}

// verdictTook is how long the verifier took: from the message its verdict answers to the verdict.
func verdictTook(in Input, f derived) time.Duration {
	if f.verdict == nil {
		return 0
	}
	from := in.CreatedAt
	for _, m := range in.Messages {
		if m.Seq >= f.verdict.Seq {
			break
		}
		if (m.From == session.Coder || m.From == session.Human) && m.StartsTurn() {
			from = m.At
		}
	}
	return f.verdict.At.Sub(from)
}

func rejectedSentence(in Input) string {
	outcome := in.Verdict.Verdict
	if outcome == "" {
		outcome = "verdict"
	}
	return "You rejected the verifier's " + outcome + "."
}

func stopSentence(stop string) string {
	switch stop {
	case session.StopTime:
		return "The verifier ran out of time."
	case session.StopSteps:
		return "The verifier used up its actions for this turn."
	}
	return "The verifier stopped at its limit."
}

// stoppedSentence says why a run with no outcome is over.
func stoppedSentence(in Input) string {
	if in.Finish != nil {
		switch in.Finish.Outcome {
		case session.OutcomeAbandoned:
			return "The coding agent gave it up."
		case session.OutcomeUnverified:
			return "The coding agent finished it without a verdict."
		}
		return "The coding agent finished it."
	}
	if in.Verdict.Status == session.Rejected {
		return rejectedSentence(in)
	}
	if stop := waitingStop(in.Messages); stop != nil {
		return strings.TrimSuffix(stopSentence(stop.Stop), ".") + " before a verdict."
	}
	for i := len(in.Messages) - 1; i >= 0; i-- {
		m := in.Messages[i]
		if m.From != session.System {
			continue
		}
		switch {
		case strings.HasPrefix(m.Text, "human destroyed"):
			return "You shut down the Mac."
		case strings.HasPrefix(m.Text, "machine failed to reboot"):
			return "The Mac did not restart. Its files are kept."
		case strings.HasPrefix(m.Text, "machine failed"):
			return "The Mac did not start."
		case strings.HasPrefix(m.Text, "machine stopped"):
			return "The Mac stopped on its own."
		case strings.HasPrefix(m.Text, "machine destroyed"):
			if hasPrefixed(in.Messages, "human destroyed") {
				return "You shut down the Mac."
			}
			return "The coding agent shut down the Mac."
		}
	}
	if in.Machine != nil && in.Machine.Status == machine.Failed {
		return "The Mac did not start."
	}
	return ""
}

func hasPrefixed(msgs []session.Message, prefix string) bool {
	for _, m := range msgs {
		if m.From == session.System && strings.HasPrefix(m.Text, prefix) {
			return true
		}
	}
	return false
}

// now is what an open run is doing, in plain words.
func now(in Input, f derived, st State) string {
	switch st {
	case Starting, Restarting:
		return bootWords(in.Machine.Boot)
	case Ready, Checking:
	default:
		return ""
	}
	if in.Machine.Controller == humanSeat {
		return "You have control"
	}
	last := laterOf(f.readyAt, lastAt(in.Messages))
	var step *machine.Step
	if n := len(in.Steps); n > 0 && !in.Steps[n-1].At.Before(f.readyAt) {
		step = &in.Steps[n-1]
		last = laterOf(last, step.At)
	}
	if idle := in.Now.Sub(last); idle >= idleAfter {
		return "Idle for " + span(idle)
	}
	if f.owed != nil && (step == nil || step.At.Before(f.owed.At)) {
		return "Reading the task"
	}
	if step == nil {
		if f.owed == nil {
			return "Waiting for the coding agent"
		}
		return "Reading the task"
	}
	return capitalise(stepWords(actionOf(*step, in.Steps), in.Steps))
}

// actionOf is the input a verifier's effect read followed (machine.StepEffect.Of), else st: a
// person watching sees the click, not the read that checked what it changed.
func actionOf(st machine.Step, steps []machine.Step) machine.Step {
	if st.Effect == nil || st.Effect.Of == 0 {
		return st
	}
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Seq == st.Effect.Of {
			return steps[i]
		}
	}
	return st
}

// bootWords names the boot phase in progress (machine.Phase*; PhaseStop is machine_reboot's).
func bootWords(phases []machine.BootPhase) string {
	if len(phases) == 0 {
		return "Starting the Mac"
	}
	p := phases[len(phases)-1]
	switch p.Phase {
	case machine.PhaseStop:
		return "Shutting down the Mac"
	case machine.PhaseClone:
		return "Copying the Mac"
	case machine.PhaseStart:
		return "Starting the Mac"
	case machine.PhaseSettings, machine.PhaseHelper, machine.PhaseChecks:
		return "Getting the Mac ready"
	}
	return "Waiting for the Mac"
}

// stepWords is one step as what it does, never the tool's name.
func stepWords(st machine.Step, steps []machine.Step) string {
	switch st.Tool {
	case "machine_input":
		return inputWords(st, steps)
	case "machine_screenshot":
		return "looking at the screen"
	case "machine_ui":
		if tree, ok := uiTree(st); ok && tree.App != "" {
			return "reading " + tree.App
		}
		return "reading the screen"
	case "machine_exec", "machine_session_start":
		return commandWords(commandOf(st))
	case "machine_sync":
		return "copying files to the Mac"
	case "machine_pull":
		return "copying files from the Mac"
	case "machine_create", "machine_boot":
		return "starting the Mac"
	case "machine_destroy":
		return "shutting down the Mac"
	}
	return "working"
}

func commandOf(st machine.Step) string {
	if in, ok := st.Input.(map[string]any); ok {
		if c, ok := in["command"].(string); ok {
			return c
		}
	}
	return ""
}

// commandWords names what a shell command is for, without the command.
func commandWords(command string) string {
	c := " " + strings.ToLower(strings.Join(strings.Fields(command), " ")) + " "
	has := func(parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(c, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has(" test ", "xcodebuild test", " test;", " test&"):
		return "running tests"
	case has("xcodebuild", "swift build", "build.sh", "go build", " make ", "pnpm build", "npm run build", "cargo build", "swiftc "):
		return "building the app"
	case has(" open ", "launchctl"):
		return "opening the app"
	}
	return "running a command"
}

// inputWords says what a batch of input did, naming what was clicked when a UI read before it
// shows an element there.
func inputWords(st machine.Step, steps []machine.Step) string {
	actions := inputActions(st)
	var act *machine.InputAction
	for i := len(actions) - 1; i >= 0; i-- {
		switch actions[i].Type {
		case "click", "type", "key", "scroll":
			act = &actions[i]
		}
		if act != nil {
			break
		}
	}
	if act == nil {
		if len(actions) > 0 && actions[len(actions)-1].Type == "sleep" {
			return "waiting"
		}
		return "moving the pointer"
	}
	tree, hasTree := lastUIRead(steps, st.Seq)
	in := ""
	if hasTree && tree.App != "" {
		in = " in " + tree.App
	}
	switch act.Type {
	case "click":
		verb := "clicking"
		if act.Clicks >= 2 {
			verb = "double-clicking"
		}
		if hasTree && act.X != nil && act.Y != nil {
			if target := elementAt(tree, *act.X, *act.Y); target != "" {
				return verb + " " + target + in
			}
		}
		return verb + in
	case "type":
		text := clipWords(strings.Join(strings.Fields(act.Text), " "), targetWords)
		if text == "" {
			return "typing" + in
		}
		return "typing " + text + in
	case "key":
		return "pressing " + keyName(act.Key, act.Mods)
	}
	return "scrolling" + in
}

func inputActions(st machine.Step) []machine.InputAction {
	in, ok := st.Input.(map[string]any)
	if !ok {
		return nil
	}
	var out []machine.InputAction
	if !remarshal(in["actions"], &out) {
		return nil
	}
	return out
}

// lastUIRead is the newest successful UI read before step seq.
func lastUIRead(steps []machine.Step, seq int) (machine.UITree, bool) {
	for i := len(steps) - 1; i >= 0; i-- {
		st := steps[i]
		if st.Seq >= seq || st.Tool != "machine_ui" || st.Error != "" {
			continue
		}
		if tree, ok := uiTree(st); ok {
			return tree, true
		}
	}
	return machine.UITree{}, false
}

func uiTree(st machine.Step) (machine.UITree, bool) {
	var tree machine.UITree
	if st.Tool != "machine_ui" || st.Output == nil || !remarshal(st.Output, &tree) {
		return tree, false
	}
	return tree, true
}

// elementAt names the smallest element with short words that contains the point: what a
// person would say they clicked.
func elementAt(tree machine.UITree, x, y float64) string {
	best, area := "", 2.0
	for _, e := range tree.Elements {
		if e.Role == "Window" || !inside(e, x, y) {
			continue
		}
		name := elementName(e)
		if name == "" || len(strings.Fields(name)) > targetWords+1 {
			continue
		}
		if a := e.W * e.H; a < area {
			best, area = name, a
		}
	}
	return best
}

func inside(e machine.UIElement, x, y float64) bool {
	return x >= e.X-e.W/2 && x <= e.X+e.W/2 && y >= e.Y-e.H/2 && y <= e.Y+e.H/2
}

func elementName(e machine.UIElement) string {
	for _, s := range []string{e.Label, e.Title, e.Value} {
		if s = strings.Join(strings.Fields(s), " "); s != "" {
			return s
		}
	}
	return ""
}

var modWords = map[string]string{"cmd": "Command", "shift": "Shift", "alt": "Option", "ctrl": "Control", "fn": "Fn"}

func keyName(key string, mods []string) string {
	parts := make([]string, 0, len(mods)+1)
	for _, m := range mods {
		if w, ok := modWords[strings.ToLower(m)]; ok {
			parts = append(parts, w)
		}
	}
	k := strings.TrimSpace(key)
	if len([]rune(k)) == 1 {
		k = strings.ToUpper(k)
	} else {
		k = capitalise(strings.ToLower(k))
	}
	if k == "" {
		k = "a key"
	}
	return strings.Join(append(parts, k), "-")
}

// tally counts the checks that apply to the status: the verdict's for an outcome, else the
// newest plan's, all pending.
func tally(in Input, f derived, st State) Checks {
	out := Checks{Items: []CheckItem{}}
	switch st {
	case Passed, Failed, Inconclusive:
		for _, c := range in.Verdict.Checks {
			out.Total++
			switch c.Status {
			case session.CheckPass:
				out.Passed++
			case session.CheckFail:
				out.Failed++
			default:
				out.Pending++
			}
		}
		if out.Total == 0 {
			return out
		}
		if st == Failed {
			out.Text = fmt.Sprintf("%d of %d %s failed", out.Failed, out.Total, plural(out.Total, "check"))
		} else {
			out.Text = fmt.Sprintf("%d of %d %s passed", out.Passed, out.Total, plural(out.Total, "check"))
		}
		out.Current = currentCheck(in.Verdict.Checks)
		out.Items = verdictItems(in)
		return out
	}
	if f.plan == nil {
		return out
	}
	out.Total, out.Pending = len(f.plan.Checks), len(f.plan.Checks)
	out.Text = fmt.Sprintf("%d %s planned", out.Total, plural(out.Total, "check"))
	for _, c := range f.plan.Checks {
		out.Items = append(out.Items, CheckItem{ID: c.ID, Text: checkText(c, checkWords), State: "pending"})
	}
	return out
}

// verdictItems is every check of the current verdict with its values and proof: failed
// first, then not checked, then passed, each in the verdict's order.
func verdictItems(in Input) []CheckItem {
	out := []CheckItem{}
	for _, want := range []string{session.CheckFail, session.CheckUnchecked, session.CheckPass} {
		for _, c := range in.Verdict.Checks {
			status := c.Status
			if status != session.CheckPass && status != session.CheckFail {
				status = session.CheckUnchecked
			}
			if status != want {
				continue
			}
			observed := plain(c.Observed)
			item := CheckItem{ID: c.ID, Text: checkText(c, checkWords), Observed: clipWords(observed, observedWords)}
			switch status {
			case session.CheckFail:
				item.State = "fail"
				item.Expected, item.Saw = Disagreement(c.Criterion, observed)
			case session.CheckPass:
				item.State = "pass"
				item.Saw = Agreement(c.Criterion, observed)
			default:
				item.State = "pending"
			}
			item.Step, item.Picture = evidencePicture(in, c.Evidence)
			if item.Picture != nil && item.Saw != "" {
				item.Mark = markFor(in.Steps, c.Evidence, item.Step, item.Saw)
			}
			out = append(out, item)
		}
	}
	return out
}

// currentCheck is the first failed check, else the first not checked.
func currentCheck(checks []session.Check) *CheckRef {
	for _, want := range []string{session.CheckFail, session.CheckUnchecked} {
		for _, c := range checks {
			status := c.Status
			if status == "" {
				status = session.CheckUnchecked
			}
			if status != want {
				continue
			}
			state := "fail"
			if want == session.CheckUnchecked {
				state = "pending"
			}
			return &CheckRef{Text: checkText(c, checkWords), State: state}
		}
	}
	return nil
}

func checkText(c session.Check, words int) string {
	text := plain(c.Criterion)
	if text == "" {
		text = plain(strings.ReplaceAll(c.ID, "-", " "))
	}
	return clipWords(text, words)
}

// failing is the first failed check with its values and the picture that shows it.
func failing(in Input, f derived) *Failing {
	var check *session.Check
	for i := range in.Verdict.Checks {
		if in.Verdict.Checks[i].Status == session.CheckFail {
			check = &in.Verdict.Checks[i]
			break
		}
	}
	if check == nil {
		return nil
	}
	observed := plain(check.Observed)
	out := &Failing{Text: checkText(*check, failingWords), Observed: clipWords(observed, observedWords)}
	out.Expected, out.Saw = Disagreement(check.Criterion, observed)
	out.Step, out.Picture = evidencePicture(in, check.Evidence)
	if out.Picture != nil && out.Saw != "" {
		out.Mark = markFor(in.Steps, check.Evidence, out.Step, out.Saw)
	}
	return out
}

// evidencePicture is the picture of a check's evidence: its first screenshot, else the frame
// recorded at its first evidence step.
func evidencePicture(in Input, evidence []int) (int, *Picture) {
	bySeq := make(map[int]machine.Step, len(in.Steps))
	for _, st := range in.Steps {
		bySeq[st.Seq] = st
	}
	for _, n := range evidence {
		st, ok := bySeq[n]
		if !ok || st.Tool != "machine_screenshot" || st.Error != "" {
			continue
		}
		if file := screenshotFile(st); file != "" {
			return n, &Picture{Kind: "screenshot", File: file, URL: artifactURL(in.RunID, file), At: st.At, Step: n}
		}
	}
	if len(evidence) == 0 {
		return 0, nil
	}
	n := evidence[0]
	for _, fr := range in.Frames {
		if fr.Step >= n {
			return n, framePicture(in.RunID, &fr)
		}
	}
	return n, nil
}

func screenshotFile(st machine.Step) string {
	out, ok := st.Output.(map[string]any)
	if !ok {
		return ""
	}
	path, _ := out["path"].(string)
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	return path
}

// markFor finds where saw sits on the picture: an element of a UI read the check cites that
// shows saw, when no input came between that read and the picture (companion ADR 0014).
func markFor(steps []machine.Step, evidence []int, pictureStep int, saw string) *Box {
	bySeq := make(map[int]machine.Step, len(steps))
	for _, st := range steps {
		bySeq[st.Seq] = st
	}
	for _, n := range evidence {
		tree, ok := uiTree(bySeq[n])
		if !ok || bySeq[n].Error != "" || inputBetween(steps, n, pictureStep) {
			continue
		}
		for _, e := range tree.Elements {
			if e.Role == "Window" || !strings.Contains(elementText(e), saw) {
				continue
			}
			return &Box{X: round3(e.X - e.W/2), Y: round3(e.Y - e.H/2), W: round3(e.W), H: round3(e.H)}
		}
	}
	return nil
}

func elementText(e machine.UIElement) string {
	return e.Label + " " + e.Title + " " + e.Value
}

// inputBetween reports whether an input step lies after the earlier of a and b and at or
// before the later.
func inputBetween(steps []machine.Step, a, b int) bool {
	lo, hi := min(a, b), max(a, b)
	for _, st := range steps {
		if st.Seq > lo && st.Seq <= hi && st.Tool == "machine_input" {
			return true
		}
	}
	return false
}

func round3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

func framePicture(runID string, fr *machine.Frame) *Picture {
	if fr == nil {
		return nil
	}
	return &Picture{Kind: "frame", File: fr.File, URL: "/api/runs/" + runID + "/frames/" + fr.File, At: fr.At, Step: fr.Step}
}

func artifactURL(runID, file string) string { return "/api/runs/" + runID + "/artifacts/" + file }

// clock is a duration as m:ss, or h:mm:ss from an hour.
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// span is a duration in words, rounded down: "12 minutes", "3 hours".
func span(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	case d >= time.Hour:
		return "an hour"
	}
	n := int(d / time.Minute)
	return fmt.Sprintf("%d %s", n, plural(n, "minute"))
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func capitalise(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
