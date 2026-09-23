package machine

import (
	"context"
	"fmt"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// The screen-capture alert (macOS 15 and later): `"tart-guest-agent" is requesting
// to bypass the system private window picker and directly access your screen and
// audio`. It is not TCC. The guest's TCC rows already grant screen capture to
// tart-guest-agent, and the capture works while the alert is up; what raises the
// alert is replayd, which keeps a per-user record keyed by the responsible
// process's resolved executable path. A client with no record, or one alerted too
// long ago, gets the alert on its next capture, and every capture while it is up
// can raise another. Every screencapture and the live screen helper run through
// `tart exec`, so tart-guest-agent is the responsible process for all of them.
//
// The fix is to write that record before anything captures, dated far in the
// future. macOS 15.1 and later (the guest is 26.x) want a dictionary per client;
// the 15.0 form, a bare date, is ignored. replayd caches the file and writes its
// own copy back, so it is killed afterwards; launchd starts it again on demand.
const captureApprovalsScript = `set -e
approvals="$HOME/Library/Group Containers/group.com.apple.replayd/ScreenCaptureApprovals.plist"
far="3024-01-01 00:00:00 +0000"
agent="$(command -v tart-guest-agent || echo /opt/homebrew/bin/tart-guest-agent)"
for client in "$agent" /usr/libexec/sshd-keygen-wrapper; do
  client="$(realpath "$client")"
  defaults write "$approvals" "$client" -dict \
    kScreenCaptureApprovalLastAlerted -date "$far" \
    kScreenCaptureApprovalLastUsed -date "$far" \
    kScreenCapturePrivacyHintDate -date "$far"
  defaults read "$approvals" "$client" >/dev/null
done
killall -9 replayd 2>/dev/null || true
`

// approveScreenCapture writes replayd's approval records in the guest. Boot runs it
// before anything captures; PrepareGuest bakes it into an image. The path is
// resolved each time, so a tart-guest-agent upgrade in the image cannot leave it
// keyed to a path that no longer exists.
func approveScreenCapture(ctx context.Context, c *tart.Client, vmName string) error {
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", captureApprovalsScript); err != nil {
		return fmt.Errorf("pre-approve screen capture (replayd): %w", err)
	}
	return nil
}
