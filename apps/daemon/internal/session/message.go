// Package session is a run's append-only conversation.jsonl (ADR 0006).
package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// From names a participant.
type From string

const (
	Coder    From = "coder"    // the coding agent, over MCP
	Human    From = "human"    // the companion app, over the HTTP API
	Verifier From = "verifier" // greenroom's own agent
	System   From = "system"   // the daemon, for machine events
)

// Kind says what a message is for. The table in ADR 0006 is the reference.
type Kind string

const (
	Task     Kind = "task"     // work for the verifier; starts a turn
	Note     Kind = "note"     // context; from a human it starts a turn
	Reply    Kind = "reply"    // a plain answer with no verdict; ends a turn
	Question Kind = "question" // the verifier is blocked; ends a turn
	Answer   Kind = "answer"   // reply to a question; starts a turn
	Progress Kind = "progress" // one per verifier tool call
	Verdict  Kind = "verdict"  // a proposal; ends a turn
	Accept   Kind = "accept"   // the verdict in ReplyTo is final
	Dispute  Kind = "dispute"  // why the verdict in ReplyTo is wrong; starts a turn
	Event    Kind = "event"    // machine ready, destroyed, turn failed, human action
)

// Message is one line of conversation.jsonl.
type Message struct {
	Seq      int       `json:"seq"`
	At       time.Time `json:"at"`
	From     From      `json:"from"`
	Kind     Kind      `json:"kind"`
	Text     string    `json:"text"`
	ReplyTo  int       `json:"replyTo,omitempty"`  // answer, accept, dispute
	Step     int       `json:"step,omitempty"`     // progress: the step in steps.jsonl
	Verdict  string    `json:"verdict,omitempty"`  // verdict: pass, fail, inconclusive
	Evidence []string  `json:"evidence,omitempty"` // verdict: artifact paths and step refs
	Stop     string    `json:"stop,omitempty"`     // verifier reply: the limit that ended its turn (issue #127)
	Control  string    `json:"control,omitempty"`  // system event: the screen was taken or came back (issue #124)
	Checks   []Check   `json:"checks,omitempty"`   // verifier progress (declared) or verdict (answered), ADR 0024
}

// Check is one acceptance check of the verifier's (ADR 0024). A progress message from
// declare_checks carries the list with ID, Criterion and the applied Kinds (and Within) only. A
// verdict answers each one with a Status, the observation steps that show it (Evidence), the input
// steps it depends on (Actions) and what was seen (Observed), and repeats its Kinds (ADR 0027).
type Check struct {
	ID        string `json:"id"`
	Criterion string `json:"criterion,omitempty"`
	// Kinds is what evidence the check needs (ADR 0027): [CheckValue], or CheckVisual,
	// CheckTiming or both, in that order; each kind's rules apply. Empty on transcripts from
	// before ADR 0027, where it means [CheckValue].
	Kinds []string `json:"kinds,omitempty"`
	// Within is a timing check's window in seconds, from the end of its last action.
	Within   float64 `json:"within,omitempty"`
	Status   string  `json:"status,omitempty"`   // verdict: pass, fail or unchecked
	Evidence []int   `json:"evidence,omitempty"` // verdict: machine_ui, machine_screenshot or machine_exec steps
	Actions  []int   `json:"actions,omitempty"`  // verdict: input steps the check depends on
	Observed string  `json:"observed,omitempty"` // verdict: what the evidence showed, one sentence
}

// Is reports whether the check has kind k. A check with no kinds is a value check.
func (c Check) Is(k string) bool {
	if len(c.Kinds) == 0 {
		return k == CheckValue
	}
	return slices.Contains(c.Kinds, k)
}

// UnmarshalJSON also reads the single "kind" an early ADR 0027 build wrote, as Kinds.
func (c *Check) UnmarshalJSON(b []byte) error {
	type plain Check
	var in struct {
		plain
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	*c = Check(in.plain)
	if len(c.Kinds) == 0 && in.Kind != "" {
		c.Kinds = []string{in.Kind}
	}
	return nil
}

// Check statuses on a verdict.
const (
	CheckPass      = "pass"
	CheckFail      = "fail"
	CheckUnchecked = "unchecked"
)

// Check kinds (ADR 0027).
const (
	CheckValue  = "value"  // a text, number or state; any observation can show it
	CheckVisual = "visual" // appearance on screen; needs a screenshot after its actions
	CheckTiming = "timing" // something happens within Within seconds of its last action
)

// Limits on a declared checklist (ADR 0024, 0027). MinWithin is the shortest timing window the
// effect read after an input can meet; a shorter one is raised to it.
const (
	MaxChecks  = 12
	MaxCheckID = 40
	MinWithin  = 2.0
	MaxWithin  = 600.0
)

// Control values: a system event saying a person took the screen, or that it came back to nobody
// (given back, or the lease lapsed). The verifier is told to look before acting again, and the
// actor resumes an interrupted task on ControlReturned (issue #124).
const (
	ControlTaken    = "taken"
	ControlReturned = "returned"
)

// Stop values: the verifier's turn ended at its step cap or its time budget with no verdict
// (issue #127). Only a verifier reply carries one, so a client can tell it from a plain answer.
const (
	StopSteps = "steps"
	StopTime  = "time"
)

// StartsTurn reports whether the verifier should act on this message.
func (m Message) StartsTurn() bool {
	switch m.Kind {
	case Task, Answer, Dispute:
		return m.From != Verifier
	case Note:
		// A human is always answered; a coder's note is context, since its
		// reply channel is its next agent_wait.
		return m.From == Human
	}
	return false
}

// EndsTurn reports whether this message closes a verifier turn.
func (m Message) EndsTurn() bool {
	return m.From == Verifier && (m.Kind == Reply || m.Kind == Question || m.Kind == Verdict)
}

// allowed says who may send each kind.
var allowed = map[Kind][]From{
	Task:     {Coder, Human},
	Note:     {Coder, Human},
	Reply:    {Verifier},
	Question: {Verifier},
	Answer:   {Coder, Human},
	Progress: {Verifier},
	Verdict:  {Verifier},
	Accept:   {Coder, Human},
	Dispute:  {Coder, Human},
	Event:    {System},
}

func validate(m Message) error {
	froms, ok := allowed[m.Kind]
	if !ok {
		return fmt.Errorf("no message kind %q", m.Kind)
	}
	if !slices.Contains(froms, m.From) {
		return fmt.Errorf("%s may not send a %s", m.From, m.Kind)
	}
	if m.ReplyTo < 0 {
		return fmt.Errorf("replyTo must be the seq of an earlier message (1 or more), or left out; got %d", m.ReplyTo)
	}
	switch m.Kind {
	case Answer, Accept, Dispute:
		if m.ReplyTo <= 0 {
			return fmt.Errorf("%s needs replyTo: the seq of the message it replies to", m.Kind)
		}
	case Verdict:
		switch m.Verdict {
		case "pass", "fail", "inconclusive":
		default:
			return fmt.Errorf("a verdict must be pass, fail or inconclusive, not %q", m.Verdict)
		}
	}
	if m.Stop != "" {
		if m.Kind != Reply || m.From != Verifier {
			return fmt.Errorf("only a verifier reply may carry stop, not a %s from %s", m.Kind, m.From)
		}
		if m.Stop != StopSteps && m.Stop != StopTime {
			return fmt.Errorf("stop must be %s or %s, not %q", StopSteps, StopTime, m.Stop)
		}
	}
	if m.Control != "" {
		if m.Kind != Event || m.From != System {
			return fmt.Errorf("only a system event may carry control, not a %s from %s", m.Kind, m.From)
		}
		if m.Control != ControlTaken && m.Control != ControlReturned {
			return fmt.Errorf("control must be %s or %s, not %q", ControlTaken, ControlReturned, m.Control)
		}
	}
	if len(m.Checks) > 0 {
		if err := validateChecks(m); err != nil {
			return err
		}
	}
	if m.Text == "" && m.Kind != Accept && m.Kind != Progress {
		return fmt.Errorf("a %s needs text", m.Kind)
	}
	return nil
}

// validateChecks allows checks only on a verifier progress (a declaration: id and criterion) or a
// verdict (an answer: id and status, and a pass verdict only of passing checks).
func validateChecks(m Message) error {
	if m.From != Verifier || (m.Kind != Progress && m.Kind != Verdict) {
		return fmt.Errorf("only a verifier progress or verdict may carry checks, not a %s from %s", m.Kind, m.From)
	}
	if len(m.Checks) > MaxChecks {
		return fmt.Errorf("at most %d checks, not %d", MaxChecks, len(m.Checks))
	}
	seen := map[string]bool{}
	for i, c := range m.Checks {
		if c.ID == "" || len(c.ID) > MaxCheckID {
			return fmt.Errorf("check %d needs an id of 1 to %d characters", i+1, MaxCheckID)
		}
		if seen[c.ID] {
			return fmt.Errorf("check id %q appears twice", c.ID)
		}
		seen[c.ID] = true
		if err := validKinds(c.Kinds); err != nil {
			return fmt.Errorf("check %q %w", c.ID, err)
		}
		if c.Within != 0 && (!c.Is(CheckTiming) || c.Within < MinWithin || c.Within > MaxWithin) {
			return fmt.Errorf("check %q: within (%g) is for a timing check, %g to %g seconds", c.ID, c.Within, MinWithin, MaxWithin)
		}
		for _, n := range append(slices.Clone(c.Evidence), c.Actions...) {
			if n <= 0 {
				return fmt.Errorf("check %q cites step %d; steps start at 1", c.ID, n)
			}
		}
		if m.Kind == Progress {
			if c.Criterion == "" {
				return fmt.Errorf("declared check %q needs a criterion", c.ID)
			}
			if c.Status != "" || len(c.Evidence) > 0 || len(c.Actions) > 0 || c.Observed != "" {
				return fmt.Errorf("declared check %q carries a result; only a verdict answers a check", c.ID)
			}
			continue
		}
		switch c.Status {
		case CheckPass, CheckFail, CheckUnchecked:
		default:
			return fmt.Errorf("check %q status must be pass, fail or unchecked, not %q", c.ID, c.Status)
		}
		if m.Verdict == "pass" && c.Status != CheckPass {
			return fmt.Errorf("a pass verdict needs every check pass; %q is %s", c.ID, c.Status)
		}
	}
	return nil
}

// validKinds allows no kinds, [value], or visual and timing once each, in that order.
func validKinds(kinds []string) error {
	switch {
	case len(kinds) == 0, len(kinds) == 1 && kinds[0] == CheckValue:
		return nil
	case slices.Equal(kinds, []string{CheckVisual}), slices.Equal(kinds, []string{CheckTiming}),
		slices.Equal(kinds, []string{CheckVisual, CheckTiming}):
		return nil
	}
	return fmt.Errorf("kinds must be [value], [visual], [timing] or [visual, timing], not %q", kinds)
}
