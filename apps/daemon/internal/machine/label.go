package machine

// Label is the run's name and the client that created it (root ADR 0036). CreateLabeled
// writes it into the run's first manifest, so a create that fails keeps it too (root ADR
// 0049). An empty field is left out of the manifest.
type Label struct {
	Name   string
	Source string
}

// MaxMachines is how many machines the host may run at once; 0 or less means no limit.
func (m *Manager) MaxMachines() int { return m.maxMachines }
