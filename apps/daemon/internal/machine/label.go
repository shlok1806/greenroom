package machine

// RecordLabel writes the run's name and the client that created it into its manifest (root
// ADR 0036), through the live recorder if the machine is alive, else directly on disk, like
// RecordFinish. An empty value leaves that field as it was.
func (m *Manager) RecordLabel(runID, name, source string) error {
	set := func(man *Manifest) {
		if name != "" {
			man.Name = name
		}
		if source != "" {
			man.Source = source
		}
	}
	if mc, err := m.get(runID); err == nil {
		return mc.rec.update(set)
	}
	dir := m.RunDir(runID)
	man, err := ReadManifest(dir)
	if err != nil {
		return err
	}
	set(&man)
	return saveManifest(dir, man)
}

// MaxMachines is how many machines the host may run at once; 0 or less means no limit.
func (m *Manager) MaxMachines() int { return m.maxMachines }
