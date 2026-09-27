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
