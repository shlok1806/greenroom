package verifier

import (
	"context"
	"fmt"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// The verifier approves an app its run built (ADR 0044, issue #269). Without its own call it
// hand-wrote TCC rows through machine_exec, or stalled on the "wants access to control" prompt
// until the coder approved the app for it.

// approveControl approves app for the verifier: the same guest script as the coder's
// machine_approve_control, scoped to the guest home, recorded as the verifier's step. It is
// both brains' call.
func approveControl(ctx context.Context, mgr *machine.Manager, runID, app string) (string, int) {
	grant, step, err := mgr.ApproveControlInHome(ctx, runID, machine.HolderVerifier, app)
	if err != nil {
		return "error: " + err.Error(), step
	}
	return fmt.Sprintf("step %d\napproved %s (%s): %s", step, grant.BundleID, grant.Executable,
		strings.Join(grant.Granted, ", ")), step
}

// tccDatabaseRefusal answers a verifier command that names a TCC database. It is guidance, not a
// boundary: the guest is the run's own and machine_exec can do anything in it. A row written by
// hand is easy to get wrong (client path, client type, the user and the system database, tccd's
// cache), and a wrong one leaves the command that needed it blocked on a prompt until it times
// out, which is what issue #269 saw.
const tccDatabaseRefusal = "error: not run: this command names a TCC database. To let an app you built be " +
	"scripted or controlled, call machine_approve_control with its .app path; it writes and checks the rows. " +
	"For any other permission change, ask the coder or the human."

// touchesTCCDatabase reports whether command names a TCC database file.
func touchesTCCDatabase(command string) bool {
	return strings.Contains(strings.ToLower(command), "tcc.db")
}
