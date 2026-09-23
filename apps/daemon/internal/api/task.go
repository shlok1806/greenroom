package api

import (
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// taskTitleLimit bounds RunSummary.Task in runes, so a long brief does not bloat the run list.
const taskTitleLimit = 280

// runTask is the text of a run's first task message, whitespace collapsed and clipped to
// taskTitleLimit runes; empty when the run has none or its conversation cannot be read.
func (a *api) runTask(runID string) string {
	store, err := a.reg.Get(runID)
	if err != nil {
		return ""
	}
	for _, m := range store.After(0) {
		if m.Kind != session.Task {
			continue
		}
		text := []rune(strings.Join(strings.Fields(m.Text), " "))
		if len(text) > taskTitleLimit {
			text = append(text[:taskTitleLimit-1], '…')
		}
		return string(text)
	}
	return ""
}
