package machine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// The screen-capture alert (macOS 15 and later): `"tart-guest-agent" is requesting
// to bypass the system private window picker and directly access your screen and
// audio`. It is not TCC. The guest's TCC rows already grant screen capture to
// tart-guest-agent, and the capture works while the alert is up; what raises it is
// replayd, which keeps a per-user record keyed by the capturing client: the
// responsible process's resolved executable path, or an app's bundle URL
// (file:///.../App.app/). Every screencapture and the live screen helper run
// through `tart exec`, so tart-guest-agent is the client for all of them;
// sshd-keygen-wrapper is the one for anything run over ssh.
//
// Measured on 26.6.2, not guessed:
//   - The alert is decided by kScreenCaptureApprovalLastUsed alone: missing, or more
//     than 30 days old, and the next capture alerts. kScreenCaptureApprovalLastAlerted
//     is not consulted.
//   - kScreenCapturePrivacyHintDate separately schedules the monthly "... Is
//     Accessing Your Screen" banner; a past date shows it.
//   - replayd sets LastUsed to now on every capture, and resets the record after 30
//     days without one (or a guest clock jump), which brings the alert back.
//   - A record with just these three dates is enough.
//   - replayd caches the file and writes its copy back, and every restart while the
//     record is stale raises one more alert (they stack). So it is held with SIGSTOP
//     across the write and then killed, on any exit so a failed write cannot leave it
//     stopped. Until launchd has it back, every capture fails with "could not create
//     image from display" (2 to 4 s measured), so the script kickstarts it and waits
//     for it, up to 10 s: launchd throttles a job killed again within seconds.
//   - Killing replayd also stops every ScreenCaptureKit session, so the live helper
//     (--serve) exits. A write therefore ends a running live stream first, with a
//     reason; viewers reconnect to a fresh helper (the companion does on its own).
//
// Modes (the script's first argument):
//   - write: both clients, unconditionally. Boot and prepare-image. A baked image's
//     LastUsed is as old as the image, so a record that looks done is not skipped.
//   - check: read only. Exit 0 if both records are current, 3 if replayd reset one
//     or a clock jump aged LastUsed past a week. Never touches replayd, so it is
//     safe under a running live stream. ensureCaptureApproval writes only after a 3.
//   - app <path>: an application under test that captures the screen itself
//     (machine_approve_capture). A relative path is taken from the guest home.
//
// Each write reads LastUsed back while replayd is stopped. defaults(1), not
// PlistBuddy: a bundle URL key contains ':', PlistBuddy's path separator. This one
// script is what boot and prepare-image both write (ADR 0018), so they cannot disagree.
const captureApprovalsScript = `export LC_ALL=C TZ=UTC
p="$HOME/Library/Group Containers/group.com.apple.replayd/ScreenCaptureApprovals.plist"
far="3024-01-01 00:00:00 +0000"
field() { defaults read "$p" "$1" 2>/dev/null | sed -n "s/^ *$2 = \"\(.*\)\";$/\1/p"; }
fresh() {
  [ "$(field "$1" kScreenCapturePrivacyHintDate)" = "$far" ] || return 1
  u="$(field "$1" kScreenCaptureApprovalLastUsed)"
  [ "$u" = "$far" ] && return 0
  used="$(date -j -f '%Y-%m-%d %H:%M:%S %z' "$u" +%s 2>/dev/null)" || return 1
  [ $(( $(date +%s) - used )) -lt 604800 ]
}
mode="${1:-write}"
[ $# -gt 0 ] && shift
agent="$(realpath "$(command -v tart-guest-agent || echo /opt/homebrew/bin/tart-guest-agent)")" || exit 1
keygen="$(realpath /usr/libexec/sshd-keygen-wrapper)" || exit 1
case "$mode" in
  write) set -- "$agent" "$keygen" ;;
  check) fresh "$agent" && fresh "$keygen" && exit 0; exit 3 ;;
  app)
    case "$1" in /*) app="$1" ;; *) app="$HOME/$1" ;; esac
    [ -d "$app" ] || { echo "no application bundle at $app" >&2; exit 1; }
    app="$(realpath "$app")" || exit 1
    url="$(osascript -l JavaScript -e 'function run(a) { ObjC.import("Foundation"); return $.NSURL.fileURLWithPathIsDirectory(a[0], true).absoluteString.js }' "$app")" || exit 1
    set -- "$url" ;;
  *) echo "unknown mode $mode" >&2; exit 2 ;;
esac
mkdir -p "$(dirname "$p")" || exit 1
pid="$(pgrep -x -u "$(id -u)" replayd)"
if [ -n "$pid" ]; then trap 'kill -9 $pid 2>/dev/null' EXIT; kill -STOP $pid; fi
s=0
for c; do
  defaults write "$p" "$c" -dict \
    kScreenCaptureApprovalLastAlerted -date "$far" \
    kScreenCaptureApprovalLastUsed -date "$far" \
    kScreenCapturePrivacyHintDate -date "$far" || s=1
  [ "$(field "$c" kScreenCaptureApprovalLastUsed)" = "$far" ] || { echo "the record for $c did not take" >&2; s=1; }
  [ "$mode" = app ] && echo "$c"
done
if [ -n "$pid" ]; then
  kill -9 $pid 2>/dev/null
  launchctl kickstart "gui/$(id -u)/com.apple.replayd" >/dev/null 2>&1
  n=0
  until pgrep -x -u "$(id -u)" replayd >/dev/null || [ $n -ge 100 ]; do sleep 0.1; n=$((n+1)); done
fi
exit $s
`

// approveScreenCapture writes replayd's approval records in the guest. Boot runs it
// before anything captures (never fatal); PrepareGuest bakes it into an image (fatal).
// The paths are resolved each time, so a tart-guest-agent upgrade in the image cannot
// leave the record keyed to a path that no longer exists.
func approveScreenCapture(ctx context.Context, c *tart.Client, vmName string) error {
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", captureApprovalsScript, "sh", "write"); err != nil {
		return fmt.Errorf("pre-approve screen capture (replayd): %w", err)
	}
	return nil
}

// captureApprovalCheck is how often a capture may first check that replayd has not
// reset the approvals. The check is one exec that reads two records.
const captureApprovalCheck = time.Minute

// Bounds on the approval execs, so no capture waits behind one for long (issue #187). The
// write waits up to 10 s for replayd to come back.
const (
	captureApprovalCheckTimeout = 15 * time.Second
	captureApprovalWriteTimeout = 30 * time.Second
)

// captureApproval is when a machine's approvals were last written or checked.
type captureApproval struct {
	mu      sync.Mutex // guards checked and warned; never held across a guest call
	run     ctxLock    // held across a check or write, so they never overlap; waiters honour their ctx
	checked time.Time  // wall clock
	warned  bool
}

// captureStale is the check mode's exit for a record that must be rewritten.
const captureStale = 3

// ensureCaptureApproval re-approves screen capture if replayd reset its record, at
// most once per captureApprovalCheck per machine. It runs before a screenshot, a
// frame, and when a live stream starts (never during one), so a record that aged
// out while the host slept is back before the capture that would alert. The check
// is read only; only a stale record is rewritten, which ends a running live stream
// first (see captureApprovalsScript). A failure is logged once per machine and
// never fails the capture. The check and the write are bounded, and a capture that
// cannot get the lock before its ctx ends captures without checking.
func (m *Manager) ensureCaptureApproval(ctx context.Context, mc *Machine) {
	a := &mc.input.approval
	if !a.due() {
		return
	}
	if a.run.lock(ctx) != nil {
		return
	}
	defer a.run.unlock()
	// Wall clock (Round(0) drops the monotonic reading, which stops while the host
	// sleeps): after a long host sleep the next capture must check.
	a.mu.Lock()
	now := time.Now().Round(0)
	if !a.checked.IsZero() && now.Sub(a.checked) < captureApprovalCheck {
		a.mu.Unlock()
		return // another capture checked while this one waited
	}
	a.checked = now
	a.mu.Unlock()
	checkCtx, cancel := context.WithTimeout(ctx, captureApprovalCheckTimeout)
	res, err := m.tart.Exec(checkCtx, mc.Name, "/bin/sh", "-c", captureApprovalsScript, "sh", "check")
	cancel()
	switch {
	case err == nil && res.ExitCode == 0:
		return
	case err == nil && res.ExitCode == captureStale:
		m.endLiveScreenFor(mc, "screen capture is being re-approved (replayd reset its record)")
		writeCtx, cancel := context.WithTimeout(ctx, captureApprovalWriteTimeout)
		err = approveScreenCapture(writeCtx, m.tart, mc.Name)
		cancel()
	case err == nil:
		err = fmt.Errorf("check the approvals: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	a.mu.Lock()
	warn := err != nil && !a.warned
	a.warned = a.warned || warn
	a.mu.Unlock()
	if warn {
		m.Log.Warn("cannot refresh the screen-capture approvals; the guest may show a capture alert", "runId", mc.RunID, "err", err)
	}
}

// due reports whether the approvals were last checked long enough ago to check again.
func (a *captureApproval) due() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checked.IsZero() || time.Now().Round(0).Sub(a.checked) >= captureApprovalCheck
}

// endLiveScreenFor ends a running live stream before replayd is killed, which would
// stop its capture anyway, so viewers get a reason rather than a helper crash.
func (m *Manager) endLiveScreenFor(mc *Machine, reason string) {
	m.mu.Lock()
	s := mc.screen
	m.mu.Unlock()
	if s != nil && s.running() {
		m.Log.Info("ending the live screen; viewers reconnect", "runId", mc.RunID, "reason", reason)
		s.end(errors.New("the live screen restarted: " + reason + "; reconnect to resume"))
	}
}

// markCaptureApproved records that boot has just written the approvals, so the
// first captures do not check again.
func (mc *Machine) markCaptureApproved() {
	a := &mc.input.approval
	a.mu.Lock()
	a.checked = time.Now().Round(0)
	a.mu.Unlock()
}

// ApproveCapture pre-approves an application under test that captures the screen
// itself (ScreenCaptureKit, CGWindowList), so replayd does not alert on it. app is
// the guest path of its .app bundle, absolute or relative to the guest home (~/
// accepted). It records a machine_approve_capture step and returns the bundle URL
// replayd keys the record by.
func (m *Manager) ApproveCapture(ctx context.Context, runID, app string) (client string, step int, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return "", 0, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return "", 0, err
	}
	started := time.Now()
	client, err = m.approveApp(ctx, mc, app)
	step = mc.rec.step("machine_approve_capture", map[string]any{"app": app}, map[string]any{"client": client}, err, started)
	m.emitStep(mc.RunID, step)
	return client, step, err
}

func (m *Manager) approveApp(ctx context.Context, mc *Machine, app string) (string, error) {
	if rest, tilde := homeRelative(app); tilde {
		app = rest
	}
	if !strings.HasSuffix(strings.TrimRight(app, "/"), ".app") {
		return "", fmt.Errorf("app %q must be the path of an .app bundle in the guest", app)
	}
	// One writer at a time: a concurrent check-and-write would kill replayd while this
	// writes, and replayd's cached write-back could drop the record despite the read-back.
	a := &mc.input.approval
	if err := a.run.lock(ctx); err != nil {
		return "", err
	}
	defer a.run.unlock()
	m.endLiveScreenFor(mc, "an app under test is being approved for screen capture")
	// The path is an argument, never part of the script text.
	ctx, cancel := context.WithTimeout(ctx, captureApprovalWriteTimeout)
	defer cancel()
	res, err := execChecked(ctx, m.tart, mc.Name, "/bin/sh", "-c", captureApprovalsScript, "sh", "app", app)
	if err != nil {
		return "", fmt.Errorf("pre-approve %s for screen capture: %w", app, err)
	}
	return strings.TrimSpace(res.Stdout), nil
}
