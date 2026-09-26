package verifier

import (
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// Scripted replies for the evidence contract (ADR 0024): a task's checks are declared before any
// input, and a verdict answers each with the steps that show it.

// declared is a declare_checks reply with one check per id.
func declared(ids ...string) string {
	checks := make([]map[string]any, len(ids))
	for i, id := range ids {
		checks[i] = map[string]any{"id": id, "criterion": "The " + id + " is what the task asks for."}
	}
	return toolCall("declare_checks", map[string]any{"checks": checks})
}

// answer is one check's answer in a report_verdict.
func answer(id, status string, evidence []int, actions ...int) map[string]any {
	if evidence == nil {
		evidence = []int{}
	}
	if actions == nil {
		actions = []int{}
	}
	return map[string]any{"id": id, "status": status, "evidence": evidence, "actions": actions,
		"observed": "The " + id + " looked as described."}
}

// verdictOf is a report_verdict reply answering checks.
func verdictOf(verdict, summary string, checks ...map[string]any) string {
	if checks == nil {
		checks = []map[string]any{}
	}
	return toolCall("report_verdict", map[string]any{"verdict": verdict, "summary": summary, "checks": checks})
}

// lastStep is the run's highest step so far: the next tool call records lastStep+1.
func lastStep(t *testing.T, mgr *machine.Manager, runID string) int {
	t.Helper()
	log, err := machine.ReadStepLog(mgr.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	return log.Highest
}
