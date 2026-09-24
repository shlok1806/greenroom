package machine

import (
	"context"
	"fmt"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// quitTerminalScript quits a Terminal that the image starts at login and removes its saved state
// (issue #60). The Cirrus base image comes up with Terminal running (windowless on
// greenroom-base, with a restored window showing the image build's shell history on lean-a),
// so every run's first screenshot and every machine_ui "running" list showed an app nobody
// opened. Nothing a run starts is running yet when boot calls this. TERM quits Terminal without
// its "terminate running processes?" sheet; KILL follows if it stays. The saved state goes
// before and after, so a restored window cannot come back at the next login. What relaunches
// Terminal is not a login item System Events can see (see apps/daemon/CLAUDE.md), so boot does
// this on every machine, whatever image it came from.
const quitTerminalScript = `saved="$HOME/Library/Saved Application State/com.apple.Terminal.savedState"
rm -rf "$saved"
if pgrep -x Terminal >/dev/null; then
  pkill -x Terminal
  i=0
  while pgrep -x Terminal >/dev/null && [ $i -lt 50 ]; do sleep 0.1; i=$((i+1)); done
  pgrep -x Terminal >/dev/null && pkill -9 -x Terminal && sleep 0.5
fi
rm -rf "$saved"
if pgrep -x Terminal >/dev/null; then echo "Terminal is still running" >&2; exit 1; fi
exit 0
`

// quitTerminal runs quitTerminalScript in the guest. Boot runs it before ready; PrepareGuest
// bakes the cleared saved state into an image.
func quitTerminal(ctx context.Context, c *tart.Client, vmName string) error {
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", quitTerminalScript); err != nil {
		return fmt.Errorf("quit the Terminal the image starts at login: %w", err)
	}
	return nil
}
