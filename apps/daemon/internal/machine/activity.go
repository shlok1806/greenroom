package machine

import (
	"fmt"
	"time"
)

// SetMessageActivity tells the manager when a run's conversation last had a
// message. The conversation lives above this package (internal/session), so
// main wires it in; unset, only steps count. Every message a person or an
// agent sends is activity, as is every step, which includes each input batch
// under a control lease. Frames are not: the recorder captures an idle machine
// every few seconds, and counting it made a run abandoned hours ago look active.
func (m *Manager) SetMessageActivity(fn func(runID string) time.Time) {
	m.mu.Lock()
	m.messageActivity = fn
	m.mu.Unlock()
}

// SetModels names who verifies the runs created from now on (issue #154): each one's manifest
// and machine record it. serve and bench call it once, before any run is created.
func (m *Manager) SetModels(models Models) {
	m.mu.Lock()
	m.models = &models
	m.mu.Unlock()
}

// LastActivity is when anyone last did something with runID: the end of its
// newest step or its newest message, and never earlier than its creation. It
// reads the run directory, so it answers for finished runs too. Zero when the
// run has no manifest, no steps and no messages.
func (m *Manager) LastActivity(runID string) time.Time {
	var created time.Time
	dir := m.RunDir(runID)
	if man, err := ReadManifest(dir); err == nil {
		created = man.CreatedAt
	}
	steps, _ := ReadStepLog(dir)
	return m.ActivityFrom(runID, created, steps)
}

// ActivityFrom is LastActivity for a caller that has already read the run's
// creation time and step log, so a run list parses steps.jsonl once per run.
func (m *Manager) ActivityFrom(runID string, created time.Time, steps StepLog) time.Time {
	last := created
	later := func(t time.Time) {
		if t.After(last) {
			last = t
		}
	}
	later(steps.Last)
	m.mu.Lock()
	fn := m.messageActivity
	if mc, ok := m.machines[runID]; ok {
		later(mc.CreatedAt)
	}
	m.mu.Unlock()
	if fn != nil {
		later(fn(runID))
	}
	return last
}

// IdleFor is how long runID has had no activity, rounded to the second.
func (m *Manager) IdleFor(runID string) time.Duration {
	last := m.LastActivity(runID)
	if last.IsZero() {
		return 0
	}
	return max(0, time.Since(last)).Round(time.Second)
}

// describeIdle says how long a machine has been idle in words a person or an
// agent can act on when choosing which machine to destroy.
func describeIdle(idle time.Duration) string {
	switch {
	case idle < time.Minute:
		return fmt.Sprintf("active %ds ago", int(idle.Seconds()))
	case idle < time.Hour:
		return fmt.Sprintf("idle %dm", int(idle.Minutes()))
	default:
		return fmt.Sprintf("idle %dh%02dm", int(idle.Hours()), int(idle.Minutes())%60)
	}
}
