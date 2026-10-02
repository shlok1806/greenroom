package verifier

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

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

// promptStopText says why greenroom stopped a command (ADR 0047, issue #283) and what to do
// next. Measured on a real guest: the prompt stays on screen until it is answered or times
// out, about 2 minutes after it appeared, whatever happens to the command, and its timeout
// records a denial over any approval written while it was up. So the approval comes after the
// prompt is gone, and a command run meanwhile is not stopped for the same prompt again.
func promptStopText(res machine.ExecResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "stopped: greenroom ended this command after %.0f s because a system prompt is on screen "+
		"that nothing will answer, so the command would have waited until its timeout.", res.Seconds)
	if res.Desktop != nil {
		for _, p := range res.Desktop.Prompts {
			if p.Text != "" {
				fmt.Fprintf(&b, "\nThe prompt, drawn by %s, says: %q", p.Owner, p.Text)
			} else {
				fmt.Fprintf(&b, "\nThe prompt is a window drawn by %s, %.0fx%.0f points at %.0f,%.0f; its text could not be read.",
					p.Owner, p.Width, p.Height, p.X, p.Y)
			}
		}
	}
	fmt.Fprintf(&b, "\nIt stays on screen until it times out, about 2 minutes after it appeared, and an approval "+
		"made while it is up is undone when it times out. If it names an app you built in this run: run "+
		"\"sleep %d\" with machine_exec, check with machine_screenshot that the prompt is gone (wait again if "+
		"not), call machine_approve_control with the app's .app path, then run the command again. Otherwise "+
		"call ask and quote the prompt.", promptWait(res.Seconds))
	return b.String()
}

// promptWait is how long to wait for a prompt raised during a command that ran for seconds to
// time out: 2 minutes from the command's start, which is no earlier than the prompt's, plus a
// margin, never under 10 s.
func promptWait(seconds float64) int {
	return max(10, int(math.Ceil(promptLifetime.Seconds()+10-seconds)))
}

// promptLifetime is how long a TCC prompt nobody answers stays on screen, measured on a real
// guest for the Apple Events prompt (issue #283).
const promptLifetime = 2 * time.Minute

// touchesTCCDatabase reports whether command names a TCC database file.
func touchesTCCDatabase(command string) bool {
	return strings.Contains(strings.ToLower(command), "tcc.db")
}
