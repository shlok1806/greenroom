package session

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Outcomes of a finished run (ADR 0031).
const (
	// OutcomeVerified: the run's current verdict is a pass that was accepted. Only then.
	OutcomeVerified = "verified"
	// OutcomeUnverified: the work shipped without an accepted pass.
	OutcomeUnverified = "unverified"
	// OutcomeAbandoned: the work was given up.
	OutcomeAbandoned = "abandoned"
)

// Limits on what a finish carries, so one call cannot grow the transcript and the manifest
// without bound.
const (
	MaxFinishSummary = 2000 // characters
	MaxRefField      = 500  // characters, each of branch, commit and pr
)

// Ref is what the finished work became. Each field is free text: a branch name, a commit sha,
// a PR URL or number.
type Ref struct {
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	PR     string `json:"pr,omitempty"`
}

// IsZero reports whether r names nothing.
func (r Ref) IsZero() bool { return r.Branch == "" && r.Commit == "" && r.PR == "" }

// Finish is how a run ended, as the coding agent said with run_finish (ADR 0031). It rides on
// the system event that records it and is mirrored in the run manifest.
type Finish struct {
	Outcome string    `json:"outcome"`
	Summary string    `json:"summary"`
	Ref     *Ref      `json:"ref,omitempty"`
	At      time.Time `json:"at"`
}

// FinishText is the text of the event that records f.
func FinishText(f Finish) string {
	return "run finished: " + f.Outcome + ". " + f.Summary
}

// validateFinish checks a finish's own fields; the rules that need the transcript (once, an
// accepted pass for verified, no turn owed) are Append's (finishRefusalLocked).
func validateFinish(m Message) error {
	if m.Kind != Event || m.From != System {
		return fmt.Errorf("only a system event may carry finish, not a %s from %s", m.Kind, m.From)
	}
	f := m.Finish
	switch f.Outcome {
	case OutcomeVerified, OutcomeUnverified, OutcomeAbandoned:
	default:
		return fmt.Errorf("outcome must be %s, %s or %s, not %q", OutcomeVerified, OutcomeUnverified, OutcomeAbandoned, f.Outcome)
	}
	if strings.TrimSpace(f.Summary) == "" {
		return fmt.Errorf("summary is required: one or two sentences of what changed")
	}
	if n := utf8.RuneCountInString(f.Summary); n > MaxFinishSummary {
		return fmt.Errorf("summary is %d characters; keep it to one or two sentences (at most %d)", n, MaxFinishSummary)
	}
	if f.Ref != nil {
		for name, v := range map[string]string{"branch": f.Ref.Branch, "commit": f.Ref.Commit, "pr": f.Ref.PR} {
			if n := utf8.RuneCountInString(v); n > MaxRefField {
				return fmt.Errorf("ref.%s is %d characters, at most %d", name, n, MaxRefField)
			}
		}
	}
	return nil
}

// finishRefusalLocked says why the run cannot finish as m says, or nil. A run finishes once;
// verified needs the current verdict to be an accepted pass; and a turn the verifier owes an
// answer is not cut short.
func (s *Store) finishRefusalLocked(m Message) error {
	for _, old := range s.msgs {
		if old.Finish != nil {
			return fmt.Errorf("this run already finished as %s at %s (message %d); a run finishes once",
				old.Finish.Outcome, old.Finish.At.Format(time.RFC3339), old.Seq)
		}
	}
	if pending := s.owedTurnLocked(); pending != nil {
		return fmt.Errorf("the verifier has not answered message %d (a %s from %s) yet; call agent_wait until it replies, then finish",
			pending.Seq, pending.Kind, pending.From)
	}
	if m.Finish.Outcome != OutcomeVerified {
		return nil
	}
	v := s.verdictLocked()
	switch {
	case v.Status == None:
		return fmt.Errorf("cannot finish as verified: this run has no verdict. Send the verifier a task (agent_send kind task), " +
			"accept its pass, then finish; or finish as unverified")
	case v.Verdict != "pass":
		return fmt.Errorf("cannot finish as verified: the latest verdict (message %d) is %s, not pass. Fix the work and ask again, "+
			"or finish as unverified", v.Seq, v.Verdict)
	case v.Status == Proposed:
		return fmt.Errorf("cannot finish as verified: the pass in message %d is proposed, not accepted. Accept it first "+
			"(agent_send kind accept, replyTo %d), or finish as unverified", v.Seq, v.Seq)
	case v.Status != Accepted:
		return fmt.Errorf("cannot finish as verified: the pass in message %d is %s, not accepted, and only a human can "+
			"accept it now; finish as unverified, or wait for a human to accept it", v.Seq, v.Status)
	}
	return nil
}

// owedTurnLocked returns the last turn-starting message that nothing has answered yet: no
// verifier reply, question or verdict after it, and no system event either (a daemon with no
// verifier, a destroyed machine or a turn the verifier gave up on each say so with an event).
// While one exists the verifier has a turn open or about to open.
func (s *Store) owedTurnLocked() *Message {
	var pending *Message
	for i := range s.msgs {
		m := &s.msgs[i]
		switch {
		case m.StartsTurn():
			pending = m
		case m.EndsTurn(), m.From == System && m.Kind == Event:
			pending = nil
		}
	}
	return pending
}

// Finished returns how the run finished, or nil while it has not.
func (s *Store) Finished() *Finish {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.msgs {
		if m.Finish != nil {
			f := *m.Finish
			return &f
		}
	}
	return nil
}
