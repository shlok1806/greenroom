package machine

import "time"

// BootPhase is one stage of a machine coming up, published as it starts and again as it
// ends, so a watcher sees the boot while it happens instead of only the machine_boot step
// written at the end. The phases, in order: clone, start (Create), then agent, ip, key,
// settings, helper, checks, ssh (finishBoot). A failed boot's last phase carries Error.
type BootPhase struct {
	Phase string    `json:"phase"`
	At    time.Time `json:"at"`
	// Seconds is how long the phase took, rounded to 0.1 s; nil while it runs.
	Seconds *float64 `json:"seconds,omitempty"`
	// Detail is what the phase produced or worked on: the image for clone, the VM for
	// start, the address for ip.
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Boot phases, the values of BootPhase.Phase.
const (
	PhaseClone    = "clone"    // tart clone of the image
	PhaseStart    = "start"    // tart run spawned
	PhaseAgent    = "agent"    // the guest agent answers
	PhaseIP       = "ip"       // tart reports the address
	PhaseKey      = "key"      // the ssh key is installed
	PhaseSettings = "settings" // capture approvals, desktop preferences, time zone
	PhaseHelper   = "helper"   // the input helper is checked (and compiled when stale); the guest agent starts
	PhaseChecks   = "checks"   // the toolchain manifest and the desktop are read
	PhaseSSH      = "ssh"      // guest sshd accepts
)

// BootPhases returns the machine's boot phases so far, oldest first. They are not
// persisted: a reattached machine has none.
func (mc *Machine) BootPhases() []BootPhase { return mc.boot }

// beginPhase records that phase started and publishes it. The returned func ends it with
// what it produced and its error, and publishes that too. Before mc is shared (Create's
// clone and start) nothing is published; Create does that once the machine is known.
func (m *Manager) beginPhase(mc *Machine, phase string) func(detail string, err error) {
	at := time.Now()
	i := m.putPhase(mc, -1, BootPhase{Phase: phase, At: at.UTC()})
	return func(detail string, err error) {
		s := round1(time.Since(at))
		p := BootPhase{Phase: phase, At: at.UTC(), Seconds: &s, Detail: detail}
		if err != nil {
			p.Error = err.Error()
		}
		m.putPhase(mc, i, p)
	}
}

// putPhase appends p (i < 0) or replaces phase i, and publishes it while mc is live.
func (m *Manager) putPhase(mc *Machine, i int, p BootPhase) int {
	m.mu.Lock()
	if i < 0 {
		mc.boot = append(mc.boot, p)
		i = len(mc.boot) - 1
	} else {
		mc.boot[i] = p
	}
	live := m.liveLocked(mc)
	m.mu.Unlock()
	if live {
		m.emitPhase(mc.RunID, p)
	}
	return i
}

func (m *Manager) emitPhase(runID string, p BootPhase) {
	m.emit(LifecycleEvent{Kind: "boot", RunID: runID, Boot: &p})
}
