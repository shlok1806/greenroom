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
// process's resolved executable path. A client with no record, or one whose hint
// date has passed, gets the alert on its next capture, and every capture while it
// is up can raise another. Every screencapture and the live screen helper run
// through `tart exec`, so tart-guest-agent is the responsible process for all of
// them; sshd-keygen-wrapper is the one for anything run over ssh.
//
// The fix is to write that record before anything captures, with a hint date in
// 3024. On macOS 26 replayd drops a record that lacks any of the five keys below
// and alerts anyway (a bare date, the 15.0 form, is ignored too). replayd caches
// the file and writes its copy back, so it is held with SIGSTOP across the write
// and then killed, on any exit so a failed write cannot leave it stopped. launchd
// starts it again on the next capture, which reads the new records. When every client already has a complete 3024 record the script
// exits before touching replayd, so a baked image or a reboot costs one read.
//
// TZ and LC_ALL are pinned because PlistBuddy prints dates in local time and
// locale, and the "already done" check matches on the year. The last line reads
// both records back, so a key macOS renamed fails loudly. Keep
// images/scripts/greenroom-tcc.sh in step.
const captureApprovalsScript = `export LC_ALL=C TZ=UTC
p="$HOME/Library/Group Containers/group.com.apple.replayd/ScreenCaptureApprovals.plist"
pb=/usr/libexec/PlistBuddy
approved() {
  case "$($pb -c "Print :$1:kScreenCapturePrivacyHintDate" "$p" 2>/dev/null)" in
    *" 3024") $pb -c "Print :$1:kScreenCapturePrivacyHintPolicy" "$p" >/dev/null 2>&1 ;;
    *) return 1 ;;
  esac
}
agent="$(realpath "$(command -v tart-guest-agent || echo /opt/homebrew/bin/tart-guest-agent)")" || exit 1
keygen="$(realpath /usr/libexec/sshd-keygen-wrapper)" || exit 1
approved "$agent" && approved "$keygen" && exit 0
mkdir -p "$(dirname "$p")" || exit 1
pid="$(pgrep -x -u "$(id -u)" replayd)"
if [ -n "$pid" ]; then trap 'kill -9 $pid 2>/dev/null' EXIT; kill -STOP $pid; fi
now="$(date -u '+%a %b %d %H:%M:%S UTC %Y')"
s=0
for c in "$agent" "$keygen"; do
  $pb -c "Delete :$c" "$p" >/dev/null 2>&1
  $pb -c "Add :$c dict" \
    -c "Add :$c:kScreenCaptureAlertableUsageCount integer 1" \
    -c "Add :$c:kScreenCaptureApprovalLastAlerted date $now" \
    -c "Add :$c:kScreenCaptureApprovalLastUsed date $now" \
    -c "Add :$c:kScreenCapturePrivacyHintDate date Thu Jan 01 00:00:00 UTC 3024" \
    -c "Add :$c:kScreenCapturePrivacyHintPolicy integer 2592000" "$p" >/dev/null || s=1
done
[ -n "$pid" ] && kill -9 $pid 2>/dev/null
[ "$s" = 0 ] && approved "$agent" && approved "$keygen"
`

// approveScreenCapture writes replayd's approval records in the guest. Boot runs it
// before anything captures (never fatal); PrepareGuest bakes it into an image (fatal).
// The paths are resolved each time, so a tart-guest-agent upgrade in the image cannot
// leave the record keyed to a path that no longer exists.
func approveScreenCapture(ctx context.Context, c *tart.Client, vmName string) error {
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", captureApprovalsScript); err != nil {
		return fmt.Errorf("pre-approve screen capture (replayd): %w", err)
	}
	return nil
}
