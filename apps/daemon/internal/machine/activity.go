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

// LastActivity is when anyone last did something with runID: the end of its
// newest step or its newest message, and never earlier than its creation. It
// reads the run directory, so it answers for finished runs too. Zero when the
// run has no manifest, no steps and no messages.
func (m *Manager) LastActivity(runID string) time.Time {
	var last time.Time
	later := func(t time.Time) {
		if t.After(last) {
			last = t
		}
	}
	dir := m.RunDir(runID)
	if man, err := ReadManifest(dir); err == nil {
		later(man.CreatedAt)
	}
	if steps, err := ReadStepLog(dir); err == nil {
		later(steps.Last)
	}
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
