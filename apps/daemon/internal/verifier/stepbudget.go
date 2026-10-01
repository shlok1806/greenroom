package verifier

import "github.com/shlok1806/greenroom/apps/daemon/internal/session"

const (
	checkSetupRounds  = 12
	checkRounds       = 8
	maxChecklistSteps = checkSetupRounds + checkRounds*session.MaxChecks
)

// stepLimit sizes only the default cap, using accepted declarations from the
// current open task. A turn retains its greatest cap, so redeclarations never
// replenish steps. The explicit upper bound also covers malformed old transcripts.
func (v *Verifier) stepLimit(msgs []session.Message) int {
	if !v.checklistSteps || !hasOpenTask(msgs) {
		return v.cfg.MaxSteps
	}
	checks := len(declaredChecks(msgs))
	return max(DefaultMaxSteps, checkSetupRounds+checkRounds*min(checks, session.MaxChecks))
}
