// Package session is a run's append-only conversation.jsonl (ADR 0006).
package session

import (
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
}

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
	if m.Text == "" && m.Kind != Accept && m.Kind != Progress {
		return fmt.Errorf("a %s needs text", m.Kind)
	}
	return nil
}
