package machine

import (
	"fmt"
	"strings"
	"time"
)

// Presence is who is at a run's machine besides the agents that call tools on it (daemon ADR
// 0008): people watching its live screen and a person holding its screen-control lease. Idle
// time counts only steps and messages, so a run a person is looking at can read as idle.
type Presence struct {
	Watchers int    `json:"watchers" jsonschema:"How many viewers watch the machine's live screen now, such as a person in the greenroom companion app"`
	Driver   string `json:"driver,omitempty" jsonschema:"The person who holds the machine's screen control now, if a person does"`
}

// Presence reports runID's live-screen viewers and the person driving it, if any.
func (m *Manager) Presence(runID string) Presence {
	mc, err := m.get(runID)
	if err != nil {
		return Presence{}
	}
	var p Presence
	m.mu.Lock()
	s := mc.screen
	if c := mc.Control; c != nil && time.Now().UTC().Before(c.Expires) && c.Holder != HolderCoder && c.Holder != HolderVerifier {
		p.Driver = c.Holder
	}
	m.mu.Unlock()
	if s != nil {
		p.Watchers = s.viewers()
	}
	return p
}

// Words says who is at the machine in a few words, "" when nobody is.
func (p Presence) Words() string {
	var parts []string
	if p.Driver != "" {
		parts = append(parts, "a person is driving it")
	}
	switch {
	case p.Watchers == 1:
		parts = append(parts, "a person is watching its screen")
	case p.Watchers > 1:
		parts = append(parts, fmt.Sprintf("%d viewers are watching its screen", p.Watchers))
	}
	return strings.Join(parts, ", ")
}

// viewers is how many watch the stream now.
func (s *screenStream) viewers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.watches)
}

// label is the run's name and the client that created it, as the manifest records them.
func (r *recorder) label() (name, source string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifest.Name, r.manifest.Source
}

// shortRunID is the last 8 characters of a runId: enough for an agent to recognise a runId its
// own machine_create returned, never one to pass to machine_destroy (daemon ADR 0008).
func shortRunID(runID string) string {
	if len(runID) <= 8 {
		return runID
	}
	return "..." + runID[len(runID)-8:]
}

// describeRun is one run in the host-limit message: who made it, how long ago, how idle, and
// who is at it.
func (m *Manager) describeRun(mc *Machine) string {
	name, source := "", ""
	if mc.rec != nil {
		name, source = mc.rec.label()
	}
	var b strings.Builder
	b.WriteString("run " + shortRunID(mc.RunID))
	if name != "" {
		fmt.Fprintf(&b, " %q", name)
	}
	facts := []string{}
	if source != "" {
		facts = append(facts, "created by "+source)
	}
	facts = append(facts, "started "+describeAge(time.Since(mc.CreatedAt)), describeIdle(m.IdleFor(mc.RunID)))
	if w := m.Presence(mc.RunID).Words(); w != "" {
		facts = append(facts, w)
	}
	fmt.Fprintf(&b, " (%s)", strings.Join(facts, ", "))
	return b.String()
}

// describeAge is how long ago something started, in minutes or hours.
func describeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm ago", int(d.Hours()), int(d.Minutes())%60)
	}
}
