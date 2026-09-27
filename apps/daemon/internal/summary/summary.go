// Package summary is a run in the few plain words a person reads first (root ADR 0036): a
// short name, one status word from a fixed vocabulary, the checks tally, what is happening
// now, and the one action that moves the run on. Every UI renders it as it comes, so the
// Companion and a web UI cannot word one run two ways.
//
// Derive is a pure function of what the daemon already records (the manifest, the live
// machine, the conversation, the steps and frames). It reads no file and calls no model.
package summary

import (
	"slices"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// State is a status word's stable id, for code that switches on it.
type State string

// The status vocabulary (root ADR 0036). Every run is in exactly one.
const (
	Starting     State = "starting"      // the Mac is booting
	Checking     State = "checking"      // the run is live and has no outcome yet
	Paused       State = "paused"        // the verifier waits for a person: its limit, or a question
	NotAnswering State = "not-answering" // the Mac's screen stopped answering
	Restarting   State = "restarting"    // the Mac is rebooting on the same disk
	Passed       State = "passed"        // the current verdict is a pass
	Failed       State = "failed"        // the current verdict is a fail
	Inconclusive State = "inconclusive"  // the current verdict could not decide
	Stopped      State = "stopped"       // the run is over with no outcome
)

// Word is the status word a UI shows for s.
func (s State) Word() string {
	switch s {
	case Starting:
		return "Starting"
	case Checking:
		return "Checking"
	case Paused:
		return "Paused"
	case NotAnswering:
		return "Not answering"
	case Restarting:
		return "Restarting"
	case Passed:
		return "Passed"
	case Failed:
		return "Failed"
	case Inconclusive:
		return "Inconclusive"
	case Stopped:
		return "Stopped"
	}
	return string(s)
}

// States is the whole vocabulary, in the order a run usually meets it.
var States = []State{Starting, Checking, Paused, NotAnswering, Restarting, Passed, Failed, Inconclusive, Stopped}

// Group is where a run sits in a list (root ADR 0036).
type Group string

// Groups, in the order a list shows them.
const (
	NeedsYou Group = "needs-you" // the open run cannot go on without a person
	Running  Group = "running"   // the open run goes on by itself
	Done     Group = "done"      // the run is over: its machine is gone or the coding agent finished it
)

// Title is the group's heading.
func (g Group) Title() string {
	switch g {
	case NeedsYou:
		return "Needs you"
	case Running:
		return "Running"
	case Done:
		return "Done"
	}
	return string(g)
}

// Groups is every group, in list order.
var Groups = []Group{NeedsYou, Running, Done}

// Tone is the one colour a status may carry. Colour is only for a real state.
type Tone string

// Tones.
const (
	TonePass  Tone = "pass"  // a pass waiting on review, accepted by a person, or a verified finish
	ToneFail  Tone = "fail"  // a fail waiting on review or accepted by a person
	ToneLive  Tone = "live"  // the Mac is working
	ToneWait  Tone = "wait"  // it waits for a person
	ToneQuiet Tone = "quiet" // nothing to signal: over, unsure, or unreviewed
)

// Action ids. A UI maps each to its own control; the label is what the control says.
const (
	ActAccept      = "accept"       // human accept of the current verdict
	ActReject      = "reject"       // human dispute of the current verdict
	ActContinue    = "continue"     // the note "Continue." to a verifier stopped at its limit
	ActAnswer      = "answer"       // answer the verifier's question
	ActRestart     = "restart"      // reboot the machine, keeping its disk
	ActKeepWaiting = "keep-waiting" // dismiss "not answering" for now
	ActTakeControl = "take-control" // take the screen's control lease
	ActGiveBack    = "give-back"    // give the lease back
	ActRecheck     = "recheck"      // a human task asking the verifier to look again
)

// Summary is one run as a person reads it first. Every string in it is plain words: no
// tool names, image names, timings in milliseconds or internal terms (root ADR 0036).
type Summary struct {
	RunID string `json:"runId"`
	// Name is five words or fewer: the coding agent's name for the run, else one made from
	// its task.
	Name string `json:"name"`
	// Source is who started the run, in words ("Claude Code"); empty when unknown.
	Source string `json:"source,omitempty"`
	// State and Status are the status: its id and its word.
	State  State  `json:"state"`
	Status string `json:"status"`
	Tone   Tone   `json:"tone"`
	Group  Group  `json:"group"`
	// Detail is the one sentence under the status, if the state has one ("Proposed by the
	// verifier after 3:26").
	Detail string `json:"detail,omitempty"`
	// Now is what the run is doing, in plain words ("clicking 25% in TipSplit"), while it is
	// open.
	Now string `json:"now,omitempty"`
	// Since is when the run entered its status.
	Since time.Time `json:"since"`
	// StartedAt and EndedAt bound the run; EndedAt is nil while it is open.
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	// ElapsedSeconds is start to end, or to the time the summary was made while open.
	ElapsedSeconds int64  `json:"elapsedSeconds"`
	Checks         Checks `json:"checks"`
	// Failing is the first failing check with its proof, or nil.
	Failing          *Failing `json:"failing"`
	PrimaryAction    *Action  `json:"primaryAction"`
	SecondaryActions []Action `json:"secondaryActions"`
	Machine          Machine  `json:"machine"`
	// Outcome is how the coding agent finished the run ("Verified", "Unverified",
	// "Abandoned"); empty while it has not.
	Outcome string `json:"outcome,omitempty"`
	// LastFrame is the run's newest frame, for a thumbnail or the picture of an open run.
	LastFrame *Picture `json:"lastFrame"`
	// UpdatedAt is the newest record the summary was made from.
	UpdatedAt time.Time `json:"updatedAt"`
}

// Checks is the tally of the checks that apply now: the current verdict's, else the
// verifier's newest plan.
type Checks struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"` // planned and not answered yet, or answered "not checked"
	// Text is the tally in words ("2 of 4 checks failed"); empty with no checks.
	Text string `json:"text,omitempty"`
	// Current is the check a person looks at first: the first failed, else the first not
	// checked. Nil when there is none.
	Current *CheckRef `json:"current"`
}

// CheckRef is one check in a few words.
type CheckRef struct {
	Text  string `json:"text"`
	State string `json:"state"` // pass, fail, pending
}

// Failing is the first failing check and what shows it.
type Failing struct {
	Text string `json:"text"`
	// Expected and Saw are the values that disagree, when the check's words name them
	// ("$50.00", "$10.00"). Either may be empty; Observed then says it.
	Expected string `json:"expected,omitempty"`
	Saw      string `json:"saw,omitempty"`
	Observed string `json:"observed,omitempty"`
	// Step is the evidence step the picture shows, 0 when the check cites none.
	Step    int      `json:"step,omitempty"`
	Picture *Picture `json:"picture,omitempty"`
	// Mark is where the wrong value sits on the picture, when a UI read found it.
	Mark *Box `json:"mark,omitempty"`
}

// Picture is an image of the screen and where to fetch it.
type Picture struct {
	// Kind is "screenshot" (a PNG artifact) or "frame" (a recorded JPEG).
	Kind string    `json:"kind"`
	File string    `json:"file"`
	URL  string    `json:"url"`
	At   time.Time `json:"at,omitempty"`
	Step int       `json:"step,omitempty"`
}

// Box is a rectangle as fractions of the screen: left, top, width, height.
type Box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Action is one thing a person can do, as a control would say it.
type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Machine is the run's Mac in words.
type Machine struct {
	// Status is "starting", "on", "restarting", "not running" or "off".
	Status string `json:"status"`
	// Warning is a problem a person should act on, in words; empty when there is none.
	Warning string `json:"warning,omitempty"`
}

// Input is everything one run's summary is made from. Nil and empty fields mean the daemon
// does not know or has not recorded them; Derive words what it has.
type Input struct {
	RunID     string
	CreatedAt time.Time
	// EndedAt is the manifest's destroyedAt.
	EndedAt *time.Time
	// Name is the coding agent's name for the run (machine_create's name), if it gave one.
	Name string
	// Source is the MCP client that created the run, as it named itself ("claude-code").
	Source string
	// Machine is the live machine, nil when the manager holds none for the run.
	Machine  *LiveMachine
	Messages []session.Message
	Verdict  session.VerdictState
	Finish   *session.Finish
	// Steps and Frames may be nil: a summary without them has no "now", no not-answering
	// screen and no picture for its failing check.
	Steps     []machine.Step
	Frames    []machine.Frame
	LastFrame *machine.Frame
	Now       time.Time
}

// LiveMachine is what the summary needs of a live machine.
type LiveMachine struct {
	Status machine.Status // booting, ready, failed, or rebooting
	Error  string
	// Boot is the boot phases so far (machine.Machine.BootPhases).
	Boot []machine.BootPhase
	// Controller is the seat holding the screen's lease, "" when nobody does.
	Controller string
	// LowOnFiles is set when the machine's tart run is near its open file limit (issue
	// #186's files warning, machine.FileUse.Warning). The machine may die, so the person
	// saves what they need.
	LowOnFiles bool
}

// statusRebooting is machine_reboot's status (daemon ADR 0004).
const statusRebooting = machine.Rebooting

// open reports whether the run can still go on: its machine is up or coming up, and the coding
// agent has not finished it.
func (in Input) open() bool {
	if in.Machine == nil || in.Finish != nil {
		return false
	}
	switch in.Machine.Status {
	case machine.Booting, machine.Ready, statusRebooting:
		return true
	}
	return false
}

func (in Input) ready() bool { return in.Machine != nil && in.Machine.Status == machine.Ready }

// Derive makes the run's summary.
func Derive(in Input) Summary {
	f := facts(in)
	s := Summary{
		RunID:            in.RunID,
		Name:             Name(in.Name, firstTask(in.Messages)),
		Source:           SourceWords(in.Source),
		StartedAt:        in.CreatedAt,
		SecondaryActions: []Action{},
		Machine:          machineWords(in),
		LastFrame:        framePicture(in.RunID, in.LastFrame),
		UpdatedAt:        updatedAt(in),
	}
	if in.Finish != nil {
		s.Outcome = outcomeWord(in.Finish.Outcome)
	}
	s.EndedAt = endedAt(in)
	end := in.Now
	if s.EndedAt != nil {
		end = *s.EndedAt
	}
	if d := end.Sub(in.CreatedAt); d > 0 {
		s.ElapsedSeconds = int64(d / time.Second)
	}

	s.State, s.Since = state(in, f)
	s.Status = s.State.Word()
	s.Group = group(in, s.State)
	s.Tone = tone(in, s.State)
	s.Detail = detail(in, f, s.State)
	s.Now = now(in, f, s.State)
	s.Checks = tally(in, f, s.State)
	if s.State == Failed || s.State == Inconclusive {
		s.Failing = failing(in, f)
	}
	s.PrimaryAction, s.SecondaryActions = actions(in, f, s.State)
	return s
}

// derived is what several rules read from the conversation, worked out once.
type derived struct {
	verdict   *session.Message // the current verdict's message
	accept    *session.Message // the accept that closed it, if one did
	owed      *session.Message // a turn-starting message the verifier has not answered
	stop      *session.Message // a verifier reply stopped at its limit, with nothing after it
	question  *session.Message // a verifier question nobody answered
	plan      *session.Message // the verifier's newest declared plan after the current verdict
	readyAt   time.Time        // the newest "machine is ready", else the run's start
	lookSince time.Time        // the first look of the current not-answering streak; zero when none
}

func facts(in Input) derived {
	f := derived{readyAt: in.CreatedAt}
	for i := range in.Messages {
		m := &in.Messages[i]
		switch {
		case m.Kind == session.Verdict && m.Seq == in.Verdict.Seq:
			f.verdict = m
		case m.Kind == session.Accept && in.Verdict.Seq > 0 && m.ReplyTo == in.Verdict.Seq:
			f.accept = m
		case m.From == session.System && m.Kind == session.Event && readyText(m.Text):
			f.readyAt = m.At
		}
	}
	for i := len(in.Messages) - 1; i >= 0; i-- {
		m := &in.Messages[i]
		if m.From == session.Verifier && m.Kind == session.Progress && len(m.Checks) > 0 {
			if f.verdict == nil || m.Seq > f.verdict.Seq {
				f.plan = m
			}
			break
		}
	}
	f.owed = owedTurn(in.Messages)
	f.stop = waitingStop(in.Messages)
	f.question = openQuestion(in.Messages)
	if in.ready() {
		f.lookSince = notAnsweringSince(in.Steps, f.readyAt)
	}
	return f
}

// readyText reports whether an event is the lifecycle bridge's (main.go) for a machine that
// became ready, after its boot or after machine_reboot.
func readyText(text string) bool {
	return strings.HasPrefix(text, "machine is ready") || strings.HasPrefix(text, "machine rebooted and is ready")
}

// owedTurn is the newest human or coder message that starts a verifier turn with no verifier
// word after it, as the Companion's awaitingVerifier reads it: a system event saying nobody or
// nothing will answer closes it.
func owedTurn(msgs []session.Message) *session.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := &msgs[i]
		switch m.From {
		case session.Verifier:
			if m.Kind == session.Progress {
				continue
			}
			return nil
		case session.Coder, session.Human:
			if m.StartsTurn() {
				return m
			}
			return nil
		case session.System:
			if strings.Contains(m.Text, "nobody will answer") || strings.Contains(m.Text, "nothing will answer") {
				return nil
			}
		}
	}
	return nil
}

// waitingStop is the verifier's reply stopped at its limit (issue #127) when nothing that
// starts a turn, and no verifier work, came after it (the Companion's LimitStop.waiting).
func waitingStop(msgs []session.Message) *session.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := &msgs[i]
		switch m.From {
		case session.Coder, session.Human:
			if m.StartsTurn() {
				return nil
			}
		case session.Verifier:
			if m.Kind == session.Reply && m.Stop != "" {
				return m
			}
			return nil
		}
	}
	return nil
}

// openQuestion is the verifier's newest question when it is its last word and nobody answered.
func openQuestion(msgs []session.Message) *session.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := &msgs[i]
		switch m.From {
		case session.Coder, session.Human:
			if m.StartsTurn() {
				return nil
			}
		case session.Verifier:
			if m.Kind == session.Progress {
				continue
			}
			if m.Kind != session.Question {
				return nil
			}
			answered := slices.ContainsFunc(msgs[i+1:], func(a session.Message) bool {
				return a.Kind == session.Answer && a.ReplyTo == m.Seq
			})
			if answered {
				return nil
			}
			return m
		}
	}
	return nil
}

// notAnsweringMark is in the error of every look that timed out on a wedged guest screen
// (machine.ScreenNotAnsweringError, daemon ADR 0003): "the guest screen is not answering".
const notAnsweringMark = "screen is not answering"

// looks are the steps that need the guest's screen to answer.
var looks = []string{"machine_screenshot", "machine_ui"}

// notAnsweringSince is when the current run of timed-out looks began: the newest look since
// the machine was last ready timed out, as did every look back to the returned one. Zero when
// the newest look answered, or there is none.
func notAnsweringSince(steps []machine.Step, readyAt time.Time) time.Time {
	var since time.Time
	for i := len(steps) - 1; i >= 0; i-- {
		st := steps[i]
		if !slices.Contains(looks, st.Tool) {
			continue
		}
		if st.At.Before(readyAt) {
			break
		}
		if !strings.Contains(st.Error, notAnsweringMark) {
			break
		}
		since = st.At
	}
	return since
}

// state is the run's status word and when it entered it. The first rule that holds wins.
func state(in Input, f derived) (State, time.Time) {
	if in.Finish == nil && in.Machine != nil {
		switch in.Machine.Status {
		case statusRebooting:
			return Restarting, rebootSince(in)
		case machine.Booting:
			return Starting, in.CreatedAt
		}
	}
	open := in.open()
	if open && !f.lookSince.IsZero() {
		return NotAnswering, f.lookSince
	}
	if open && f.question != nil {
		return Paused, f.question.At
	}
	if open && f.stop != nil {
		return Paused, f.stop.At
	}
	if open && f.owed != nil {
		return Checking, laterOf(f.readyAt, f.owed.At)
	}
	if st, ok := verdictState(in); ok {
		at := in.CreatedAt
		if f.verdict != nil {
			at = f.verdict.At
		}
		if f.accept != nil {
			at = f.accept.At
		}
		return st, at
	}
	if open {
		since := f.readyAt
		if f.verdict != nil { // a rejected verdict: the run is back to having no outcome
			since = laterOf(since, lastAt(in.Messages))
		}
		return Checking, since
	}
	return Stopped, stoppedSince(in)
}

// verdictState is the status the current verdict gives, if it gives one. A verdict a person
// rejected gives none: the person's word outranks it.
func verdictState(in Input) (State, bool) {
	v := in.Verdict
	if v.Status == session.None || v.Status == session.Rejected {
		return "", false
	}
	switch v.Verdict {
	case "pass":
		return Passed, true
	case "fail":
		return Failed, true
	}
	return Inconclusive, true
}

// reviewWaits reports whether the current verdict is open to a person's accept or reject.
func reviewWaits(in Input) bool {
	return in.Verdict.Status == session.Proposed || in.Verdict.Status == session.Contested
}

func group(in Input, st State) Group {
	if !in.open() {
		return Done
	}
	switch st {
	case Paused, NotAnswering:
		return NeedsYou
	case Passed, Failed, Inconclusive:
		if reviewWaits(in) {
			return NeedsYou
		}
	}
	if in.Machine.LowOnFiles {
		return NeedsYou
	}
	return Running
}

func tone(in Input, st State) Tone {
	switch st {
	case Starting, Checking, Restarting:
		return ToneLive
	case Paused, NotAnswering:
		return ToneWait
	case Passed:
		if reviewWaits(in) || acceptedByHuman(in) || verified(in) {
			return TonePass
		}
	case Failed:
		if reviewWaits(in) || acceptedByHuman(in) {
			return ToneFail
		}
	}
	return ToneQuiet
}

func acceptedByHuman(in Input) bool {
	return in.Verdict.Status == session.Accepted && in.Verdict.AcceptedBy == session.Human
}

func verified(in Input) bool {
	return in.Finish != nil && in.Finish.Outcome == session.OutcomeVerified
}

func actions(in Input, f derived, st State) (*Action, []Action) {
	secondary := []Action{}
	switch st {
	case Passed, Failed, Inconclusive:
		if reviewWaits(in) {
			label := "Accept"
			switch st {
			case Passed:
				label = "Accept pass"
			case Failed:
				label = "Accept fail"
			}
			return &Action{ActAccept, label}, append(secondary, Action{ActReject, "Reject"})
		}
		if in.open() && in.Verdict.Status == session.Accepted && in.Verdict.AcceptedBy == session.Coder {
			return nil, append(secondary, Action{ActRecheck, "Ask for a re-check"})
		}
		return nil, secondary
	case Paused:
		if f.question != nil {
			return &Action{ActAnswer, "Answer"}, secondary
		}
		return &Action{ActContinue, "Continue"}, secondary
	case NotAnswering:
		return &Action{ActRestart, "Restart the Mac"}, append(secondary, Action{ActKeepWaiting, "Keep waiting"})
	case Checking:
		if in.Machine.LowOnFiles {
			secondary = append(secondary, Action{ActRestart, "Restart the Mac"})
		}
		if !in.ready() {
			return nil, secondary
		}
		if in.Machine.Controller == humanSeat {
			return &Action{ActGiveBack, "Give control back"}, secondary
		}
		return &Action{ActTakeControl, "Take control"}, secondary
	}
	return nil, secondary
}

// humanSeat is the Companion's seat on the control lease (api.humanSeat).
const humanSeat = "human"

func machineWords(in Input) Machine {
	if in.Machine == nil {
		return Machine{Status: "off"}
	}
	out := Machine{}
	switch in.Machine.Status {
	case machine.Booting:
		out.Status = "starting"
	case machine.Ready:
		out.Status = "on"
	case statusRebooting:
		out.Status = "restarting"
	default:
		out.Status = "not running"
	}
	if in.Machine.LowOnFiles && in.Machine.Status != machine.Failed {
		out.Warning = LowOnFilesWarning
	}
	return out
}

// LowOnFilesWarning is the machine warning for issue #186's files warning, in words.
const LowOnFilesWarning = "The Mac is running low on resources; save what you need."

// endedAt is when the run ended, nil while it is open: the machine's recorded end, else the
// finish, else the event that says the machine failed or stopped, else the run's last record.
func endedAt(in Input) *time.Time {
	if in.open() {
		return nil
	}
	if in.EndedAt != nil {
		t := *in.EndedAt
		return &t
	}
	if in.Finish != nil {
		t := in.Finish.At
		return &t
	}
	for i := len(in.Messages) - 1; i >= 0; i-- {
		m := in.Messages[i]
		if m.From == session.System && (strings.HasPrefix(m.Text, "machine failed") || strings.HasPrefix(m.Text, "machine stopped")) {
			t := m.At
			return &t
		}
	}
	// No machine and no recorded end: the run's machine went while the daemon was down and no
	// daemon stamped it yet, or a run from before destroyedAt. Its last record is its end.
	t := laterOf(in.CreatedAt, lastAt(in.Messages))
	if n := len(in.Steps); n > 0 {
		t = laterOf(t, in.Steps[n-1].At)
	}
	return &t
}

func stoppedSince(in Input) time.Time {
	if e := endedAt(in); e != nil {
		return *e
	}
	return in.CreatedAt // unreachable: a stopped run is not open
}

// rebootSince is when the reboot began: its first boot phase, else the newest system event
// (the reboot's own), else the run's start.
func rebootSince(in Input) time.Time {
	if in.Machine != nil && len(in.Machine.Boot) > 0 {
		return in.Machine.Boot[0].At
	}
	for i := len(in.Messages) - 1; i >= 0; i-- {
		if in.Messages[i].From == session.System {
			return in.Messages[i].At
		}
	}
	return in.CreatedAt
}

func updatedAt(in Input) time.Time {
	t := laterOf(in.CreatedAt, lastAt(in.Messages))
	if n := len(in.Steps); n > 0 {
		t = laterOf(t, in.Steps[n-1].At)
	}
	if in.EndedAt != nil {
		t = laterOf(t, *in.EndedAt)
	}
	if in.Machine != nil {
		for _, p := range in.Machine.Boot {
			t = laterOf(t, p.At)
		}
	}
	return t
}

func lastAt(msgs []session.Message) time.Time {
	if n := len(msgs); n > 0 {
		return msgs[n-1].At
	}
	return time.Time{}
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func firstTask(msgs []session.Message) string {
	for _, m := range msgs {
		if m.Kind == session.Task {
			return m.Text
		}
	}
	return ""
}

func outcomeWord(outcome string) string {
	switch outcome {
	case session.OutcomeVerified:
		return "Verified"
	case session.OutcomeUnverified:
		return "Unverified"
	case session.OutcomeAbandoned:
		return "Abandoned"
	}
	if outcome == "" {
		return "Finished"
	}
	return strings.ToUpper(outcome[:1]) + outcome[1:]
}
