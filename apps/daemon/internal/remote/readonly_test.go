package remote

import "testing"

// ADR 0034: run_report only reads, so a dropped connection retries it; run_finish records the
// finish and destroys the machine, so it is never sent twice. Its links need no rewriting here:
// through the public host the daemon already points them at its artifact route, which this
// token opens.
func TestRunReportIsRetriedAndRunFinishIsNot(t *testing.T) {
	if !readOnlyTools["run_report"] {
		t.Error("run_report is not in readOnlyTools")
	}
	if readOnlyTools["run_finish"] {
		t.Error("run_finish is in readOnlyTools; a retry could finish twice")
	}
}

// The desktop toolkit's looks are retried; an action posts input, and a retry could post it twice
// (daemon ADR 0005: an input is never posted twice).
func TestToolkitLooksAreRetriedAndActionsAreNot(t *testing.T) {
	for _, look := range toolkitLooks {
		if !readOnlyTools[look] {
			t.Errorf("%s is not in readOnlyTools", look)
		}
	}
	for _, action := range []string{"machine_press", "machine_type", "machine_set_value", "machine_key", "machine_scroll"} {
		if readOnlyTools[action] {
			t.Errorf("%s is in readOnlyTools; a retry could post its input twice", action)
		}
	}
}

// toolkitLooks are the toolkit's tools that only read.
var toolkitLooks = []string{"machine_snapshot", "machine_find", "machine_wait_for", "machine_expect"}
