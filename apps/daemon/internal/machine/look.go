package machine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// Bounded looks (daemon ADR 0003, issue #187). A look is anything that needs the guest's
// WindowServer to answer: a screenshot, a frame, a UI tree read, a desktop read. When
// WindowServer wedges, every one of those blocks in the guest, and killing the host's
// `tart exec` does not reach the guest process (ADR 0014). So each look runs under a guest
// watchdog (lookWatchdogScript) and a host deadline a little longer, every screenshot and UI
// read has an overall cap under the MCP client's 60 s first-byte timer, and one machine never
// has two guest captures at once (captureGate).

// ErrScreenNotAnswering matches the error a look returns when the guest screen did not answer
// in time. errors.Is works on every *ScreenNotAnsweringError.
var ErrScreenNotAnswering = errors.New("the guest screen is not answering")

// ScreenNotAnsweringError is a look that got nothing back in time. Its text is what the agent
// reads, so it says what still works and how to recover.
type ScreenNotAnsweringError struct {
	What  string        // what did not answer, e.g. "the screen capture"
	After time.Duration // how long it was given
}

func (e *ScreenNotAnsweringError) Error() string {
	return fmt.Sprintf("the guest screen is not answering: %s got nothing back within %s. "+
		"machine_exec may still work (the guest's shell does not need the screen), so logs and files can still be read; "+
		"to recover the screen, call machine_reboot, which restarts the machine and keeps the run",
		e.What, e.After.Round(time.Second))
}

// Is makes errors.Is(err, ErrScreenNotAnswering) true.
func (e *ScreenNotAnsweringError) Is(target error) bool { return target == ErrScreenNotAnswering }

// lookTimes are a look's limits. Production uses defaultLookTimes; tests shorten them.
type lookTimes struct {
	capture time.Duration // the guest watchdog on one screencapture or helper read
	ui      time.Duration // the guest watchdog on one UI tree read (a large tree walks for a while)
	grace   time.Duration // how much longer the host waits than the guest watchdog
	cap     time.Duration // a whole screenshot or machine_ui call, approvals and waits included
}

// defaultLookTimes keep every look under the MCP client's 60 s first-byte timer, with the same
// headroom as maxWait (50 s) in mcpserver. A capture takes about a second on a healthy guest.
var defaultLookTimes = lookTimes{
	capture: 15 * time.Second,
	ui:      25 * time.Second,
	grace:   5 * time.Second,
	cap:     45 * time.Second,
}

// looks returns the manager's look limits.
func (m *Manager) looks() lookTimes {
	if m.lookTimes.cap <= 0 {
		return defaultLookTimes
	}
	return m.lookTimes
}

// withLookTimes replaces the look limits (tests only).
func withLookTimes(t lookTimes) Option {
	return func(m *Manager) { m.lookTimes = t }
}

// lookLimit is one guest look's watchdog and the host's grace beyond it.
type lookLimit struct {
	guest, grace time.Duration
}

func (t lookTimes) captureLimit() lookLimit { return lookLimit{t.capture, t.grace} }
func (t lookTimes) uiLimit() lookLimit      { return lookLimit{t.ui, t.grace} }

// host is how long the host waits for the look's tart exec.
func (l lookLimit) host() time.Duration { return l.guest + l.grace }

// lookTimedOutExit is what lookWatchdogScript exits with when its watchdog ended the command.
const lookTimedOutExit = 124

// lookWatchdogScript runs "$@" (the arguments after $1) and ends it after $1 seconds, in the
// guest, because only the guest can (ADR 0014). It is the machine_exec wrapper's watchdog cut
// down to one command:
//
//   - set -m gives the command its own process group, and the watchdog another. At the limit
//     the watchdog sends the command's group TERM, then KILL 2 s later, and the script exits
//     124 with a note on stderr. When the command ends first, the watchdog's group (its
//     subshell and its sleep) is killed at once, so nothing outlives the script either way.
//   - The command's output goes to files in a private temp dir, printed once it has ended, and
//     the dir is removed on every path. A child that outlives the command holds a file, never
//     the tart exec pipes, so it cannot hold the call open. The dir is GREENROOM_LOOK_DIR in
//     the command's environment, for a file it writes (the screenshot).
//   - The script's own stderr goes to /dev/null, so the shell's job notices never reach the
//     caller; the command's stderr comes back through fd 3, which neither child inherits.
//   - The command's exit status is the script's, except 124 when the watchdog fired.
//
// The first line names it for the fake tart and for a person reading the guest's ps.
const lookWatchdogScript = `: greenroom-watchdog
exec 3>&2 2>/dev/null
t=$1
shift
d=$(mktemp -d /tmp/greenroom-look.XXXXXX) || exit 125
set -m
GREENROOM_LOOK_DIR="$d" "$@" >"$d/out" 2>"$d/err" </dev/null 3>&- &
c=$!
(sleep "$t"; : >"$d/timedout"; kill -TERM -"$c"; sleep 2; kill -KILL -"$c") >/dev/null 2>&1 </dev/null 3>&- &
w=$!
wait "$c"
s=$?
kill -KILL -"$w"
if [ -f "$d/timedout" ]; then
  kill -KILL -"$c"
  s=124
fi
cat "$d/out"
cat "$d/err" >&3
[ -f "$d/timedout" ] && echo "greenroom: the guest stopped it after $t s" >&3
rm -rf "$d"
exit $s
`

// watchdogArgs is the tart exec command line that runs args under lookWatchdogScript with a
// guest limit of limit, rounded up to whole seconds.
func watchdogArgs(limit time.Duration, args ...string) []string {
	secs := max(1, int(math.Ceil(limit.Seconds())))
	return append([]string{"/bin/sh", "-c", lookWatchdogScript, "greenroom-watchdog", strconv.Itoa(secs)}, args...)
}

// guestLook runs args in the guest under the look watchdog and a host deadline lim.grace
// later. timedOut says one of them fired: the guest's (exit 124) or the host's while the
// caller's ctx was still live. A caller that gave up gets its ctx's error, not a timeout.
func guestLook(ctx context.Context, c *tart.Client, vm string, lim lookLimit, args ...string) (res tart.ExecResult, timedOut bool, err error) {
	hctx, cancel := context.WithTimeout(ctx, lim.host())
	defer cancel()
	res, err = c.Exec(hctx, vm, watchdogArgs(lim.guest, args...)...)
	if err != nil {
		return res, ctx.Err() == nil && errors.Is(hctx.Err(), context.DeadlineExceeded), err
	}
	return res, res.ExitCode == lookTimedOutExit, nil
}

// readHelper runs the installed input helper with args (shell-safe: base64 or fixed flags)
// under the look watchdog. A timeout is a *ScreenNotAnsweringError naming what.
func readHelper(ctx context.Context, c *tart.Client, vm string, lim lookLimit, what string, args ...string) (tart.ExecResult, error) {
	res, timedOut, err := guestLook(ctx, c, vm, lim,
		append([]string{"/bin/sh", "-c", `exec "$HOME/` + helperName() + `" "$@"`, "greenroom-input"}, args...)...)
	switch {
	case timedOut:
		return res, &ScreenNotAnsweringError{What: what, After: lim.guest}
	case err != nil:
		return res, err
	case res.ExitCode != 0:
		return res, fmt.Errorf("exit %d: %s", res.ExitCode, helperError(res.Stderr))
	}
	return res, nil
}

// lookError turns a look's error into what its caller returns. The look ran on look, a
// context capped from parent: an error that is the cap firing, while the caller had not given
// up, is a screen that did not answer in time.
func lookError(parent, look context.Context, err error, what string, limit time.Duration) error {
	if err == nil || errors.Is(err, ErrScreenNotAnswering) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil && errors.Is(look.Err(), context.DeadlineExceeded) {
		return &ScreenNotAnsweringError{What: what, After: limit}
	}
	return err
}

// errCaptureBusy is a capture that was not started because another is outstanding and the
// caller (the frame recorder) does not wait.
var errCaptureBusy = errors.New("a screen capture is already outstanding")

// captureGate lets one guest screencapture run per machine at a time (single flight). A look
// that finds one outstanding waits for it, within its own cap, and then captures itself; it
// never shares the other's picture, which may predate the input the caller just made. When
// the capture it waited for timed out, it fails at once with ErrScreenNotAnswering instead of
// starting another capture on a screen that just proved hung. The frame recorder does not
// wait: it skips the frame. The slot is held until the guest command is over (its tart exec
// returned), not until the caller gives up, so an abandoned capture still blocks the next.
type captureGate struct {
	mu       sync.Mutex
	busy     bool
	freed    chan struct{} // closed when the outstanding capture ends
	timeouts uint64        // captures that timed out, ever
	streak   int           // consecutive timeouts; 0 after a capture that got a picture
}

// acquire claims the slot. With wait false a busy slot is errCaptureBusy; with wait true it
// waits until the slot is free or ctx is done. limit is a capture's own limit, for the error
// when the capture it waited for timed out.
func (g *captureGate) acquire(ctx context.Context, wait bool, limit time.Duration) error {
	for {
		g.mu.Lock()
		if !g.busy {
			g.busy = true
			g.freed = make(chan struct{})
			g.mu.Unlock()
			return nil
		}
		if !wait {
			g.mu.Unlock()
			return errCaptureBusy
		}
		freed, seen := g.freed, g.timeouts
		g.mu.Unlock()
		select {
		case <-freed:
		case <-ctx.Done():
			return ctx.Err()
		}
		g.mu.Lock()
		stuck := g.timeouts != seen
		g.mu.Unlock()
		if stuck {
			return &ScreenNotAnsweringError{What: "the screen capture already in flight", After: limit}
		}
	}
}

// release frees the slot and records how the capture ended: timedOut, or got a picture (ok).
// A capture that failed any other way leaves the streak as it was.
func (g *captureGate) release(timedOut, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case timedOut:
		g.timeouts++
		g.streak++
	case ok:
		g.streak = 0
	}
	g.busy = false
	close(g.freed)
}

// timeoutStreak is how many captures in a row have timed out.
func (g *captureGate) timeoutStreak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.streak
}

// frameBackoff is how long the recorder waits after streak consecutive capture timeouts: 2 s,
// doubling with each, capped at a minute. Zero means no backoff.
func frameBackoff(streak int) time.Duration {
	if streak <= 0 {
		return 0
	}
	const base, ceiling = 2 * time.Second, time.Minute
	if streak > 6 {
		return ceiling
	}
	return min(base<<(streak-1), ceiling)
}

// ctxLock is a mutex whose lock gives up when ctx does, for a lock held across a guest exec.
// The zero value is unlocked.
type ctxLock struct {
	once sync.Once
	ch   chan struct{}
}

func (l *ctxLock) lock(ctx context.Context) error {
	l.once.Do(func() { l.ch = make(chan struct{}, 1) })
	select {
	case l.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *ctxLock) unlock() { <-l.ch }
