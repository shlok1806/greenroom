package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const fileName = "conversation.jsonl"

// DefaultMaxDisputes is how many times the coder may dispute before a
// verdict becomes contested and only a human can close it.
const DefaultMaxDisputes = 2

// ErrContested is returned when the coder disputes or accepts a verdict that
// only a human may now close.
var ErrContested = errors.New("the verdict is contested; only a human can accept or dispute it now")

// ErrClosed is returned by Append on a store the Registry has evicted.
var ErrClosed = errors.New("the conversation store is closed")

// Store is one run's conversation and the only source of its sequence
// numbers, so two writers never choose the same one.
type Store struct {
	mu          sync.Mutex
	path        string
	msgs        []Message
	changed     chan struct{} // closed and replaced on every append
	closed      bool
	subs        map[int]func(Message)
	nextSub     int
	maxDisputes int
}

// Open loads dir/conversation.jsonl, creating dir if needed. A final line
// torn by a crash mid-append is cut off the file; a bad line anywhere else is
// an error.
func Open(dir string, maxDisputes int) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if maxDisputes <= 0 {
		maxDisputes = DefaultMaxDisputes
	}
	s := &Store{path: filepath.Join(dir, fileName), changed: make(chan struct{}), subs: map[int]func(Message){}, maxDisputes: maxDisputes}
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	var offset, badAt int64
	var badErr error
	for line := 1; ; line++ {
		raw, err := r.ReadBytes('\n')
		if len(raw) > 0 {
			if badErr != nil {
				return nil, badErr
			}
			var m Message
			if jerr := json.Unmarshal(raw, &m); jerr != nil {
				badAt, badErr = offset, fmt.Errorf("parse %s line %d: %w", s.path, line, jerr)
			} else {
				s.msgs = append(s.msgs, m)
			}
			offset += int64(len(raw))
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if badErr != nil {
		if err := os.Truncate(s.path, badAt); err != nil {
			return nil, fmt.Errorf("%w; truncating it: %w", badErr, err)
		}
		slog.Warn("dropped a torn final line from a conversation", "path", s.path, "err", badErr)
	}
	return s, nil
}

// Append validates m, numbers it, writes it and wakes every waiter. The
// returned message carries the Seq and At the store chose.
func (s *Store) Append(m Message) (Message, error) {
	if err := validate(m); err != nil {
		return Message{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Message{}, ErrClosed
	}
	if err := s.checkReplyLocked(m); err != nil {
		return Message{}, err
	}
	if m.Finish != nil {
		if err := s.finishRefusalLocked(m); err != nil {
			return Message{}, err
		}
	}
	m.Seq = len(s.msgs) + 1
	m.At = time.Now().UTC()
	if m.Finish != nil {
		f := *m.Finish // the caller's copy stays as it was
		f.At = m.At
		m.Finish = &f
	}
	line, err := json.Marshal(m)
	if err != nil {
		return Message{}, err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Message{}, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return Message{}, err
	}
	if err := f.Close(); err != nil {
		return Message{}, err
	}
	s.msgs = append(s.msgs, m)
	close(s.changed)
	s.changed = make(chan struct{})
	for _, fn := range s.subs {
		fn(m)
	}
	return m, nil
}

// checkReplyLocked enforces ADR 0006's agreement rules: replies target the
// right kind, and only a human may close a contested verdict.
func (s *Store) checkReplyLocked(m Message) error {
	if m.ReplyTo == 0 {
		return nil
	}
	if m.ReplyTo > len(s.msgs) {
		return fmt.Errorf("no message %d to reply to", m.ReplyTo)
	}
	target := s.msgs[m.ReplyTo-1]
	switch m.Kind {
	case Answer:
		if target.Kind != Question {
			return fmt.Errorf("message %d is a %s, not a question", m.ReplyTo, target.Kind)
		}
	case Accept, Dispute:
		if target.Kind != Verdict {
			return fmt.Errorf("message %d is a %s, not a verdict", m.ReplyTo, target.Kind)
		}
		v := s.verdictLocked()
		if v.Seq != m.ReplyTo {
			return fmt.Errorf("message %d is not the latest verdict (that is %d)", m.ReplyTo, v.Seq)
		}
		if v.Status == Accepted {
			return fmt.Errorf("verdict %d is already accepted", v.Seq)
		}
		// The coder may close only a proposed verdict: a contested one waits for a human (ADR 0006
		// rule 4, issue #34), and a human's rejection is not the coder's to override.
		if m.From == Coder && v.Status != Proposed {
			return ErrContested
		}
	}
	return nil
}

// After returns every message with a sequence number greater than seq.
func (s *Store) After(seq int) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq < 0 {
		seq = 0
	}
	if seq >= len(s.msgs) {
		return []Message{}
	}
	out := make([]Message, len(s.msgs)-seq)
	copy(out, s.msgs[seq:])
	return out
}

// LastAt is when the newest message was appended; zero for an empty conversation.
func (s *Store) LastAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.msgs) == 0 {
		return time.Time{}
	}
	return s.msgs[len(s.msgs)-1].At
}

// Len is the sequence number of the last message, or 0.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

// Wait blocks until a message newer than seq exists, ctx ends or the store is
// closed, then returns whatever is newer, which is empty on a timeout.
func (s *Store) Wait(ctx context.Context, seq int) []Message {
	for {
		s.mu.Lock()
		if len(s.msgs) > seq || s.closed {
			s.mu.Unlock()
			return s.After(seq)
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return s.After(seq)
		}
	}
}

// close refuses further appends and wakes every waiter.
func (s *Store) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.changed)
}

// Subscribe calls fn, under the store lock, for every message appended from
// now on. The returned function removes the subscription.
func (s *Store) Subscribe(fn func(Message)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}

// Status of the latest verdict.
type Status string

const (
	None      Status = "none"      // no verdict yet
	Proposed  Status = "proposed"  // open to accept or dispute
	Accepted  Status = "accepted"  // final
	Contested Status = "contested" // the coder has used its disputes; a human closes it
	Rejected  Status = "rejected"  // a human disputed it; final unless a human accepts a later one
)

// VerdictState summarises the latest verdict for the run manifest.
type VerdictState struct {
	Seq        int      `json:"seq,omitempty"`
	Verdict    string   `json:"verdict,omitempty"` // pass, fail, inconclusive
	Summary    string   `json:"summary,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	Checks     []Check  `json:"checks,omitempty"` // ADR 0024
	Status     Status   `json:"status"`
	AcceptedBy From     `json:"acceptedBy,omitempty"`
	Disputes   int      `json:"disputes"` // by the coder, across the run
}

// Verdict derives the current state from the transcript.
func (s *Store) Verdict() VerdictState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdictLocked()
}

func (s *Store) verdictLocked() VerdictState {
	v := VerdictState{Status: None}
	humanDisputed := false
	for _, m := range s.msgs {
		switch m.Kind {
		case Verdict:
			v = VerdictState{Seq: m.Seq, Verdict: m.Verdict, Summary: m.Text, Evidence: m.Evidence, Checks: m.Checks, Status: Proposed, Disputes: v.Disputes}
			if v.Disputes >= s.maxDisputes || humanDisputed {
				v.Status = Contested
			}
		case Dispute:
			if m.From == Human {
				humanDisputed = true
				if m.ReplyTo == v.Seq {
					v.Status = Rejected
				}
			} else {
				v.Disputes++
				if v.Disputes >= s.maxDisputes && m.ReplyTo == v.Seq {
					v.Status = Contested
				}
			}
		case Accept:
			if m.ReplyTo == v.Seq {
				v.Status = Accepted
				v.AcceptedBy = m.From
			}
		}
	}
	return v
}
