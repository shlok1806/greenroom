package machine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #187, daemon ADR 0003. The fake tart stands in for a guest whose WindowServer is wedged
// (shot-hang, ui-hang): the capture never answers, and only a deadline ends it.

// quickLooks are look limits short enough for a test, in the production proportions.
func quickLooks(capture time.Duration) lookTimes {
	return lookTimes{capture: capture, ui: capture, grace: 300 * time.Millisecond, cap: 3*capture + time.Second}
}

func hang(t *testing.T, control, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, file), []byte("30"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captures(t *testing.T, control string) int {
	t.Helper()
	return strings.Count(testsupport.Calls(t, control), "screencapture -x")
}

// A screenshot of a hung screen answers within its limits with ErrScreenNotAnswering, whose text
// says what still works and how to recover, and the step records it.
func TestAHungScreenshotAnswersWithinItsCap(t *testing.T) {
	mgr, _, control := newTestManager(t, withLookTimes(quickLooks(time.Second)))
	mc := readyMachine(t, mgr)
	hang(t, control, "shot-hang")

	started := time.Now()
	_, shot, err := mgr.Screenshot(context.Background(), mc.RunID)
	took := time.Since(started)
	if !errors.Is(err, ErrScreenNotAnswering) {
		t.Fatalf("a hung screenshot gave %v, want ErrScreenNotAnswering", err)
	}
	if took > 3*time.Second {
		t.Errorf("a hung screenshot took %s, want about its 1.3 s capture limit", took)
	}
	for _, want := range []string{"the guest screen is not answering", "machine_exec may still work", "machine_reboot"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not say %q", err, want)
		}
	}
	steps := readSteps(t, mgr.RunDir(mc.RunID))
	last := steps[len(steps)-1]
	if last.Tool != "machine_screenshot" || last.Seq != shot.Step || !strings.Contains(last.Error, "not answering") {
		t.Errorf("the step is %+v, want the screenshot's with the error", last)
	}
}

// Single flight: while one guest capture is outstanding, a look never starts another. A caller
// that gives up returns at once but leaves the slot held until the guest command is over, and a
// look that waited behind a capture that then timed out fails at once without capturing.
func TestALookWhileACaptureIsOutstandingStartsNoSecondCapture(t *testing.T) {
	mgr, _, control := newTestManager(t, withLookTimes(quickLooks(2*time.Second)))
	mc := readyMachine(t, mgr)
	hang(t, control, "shot-hang")
	before := captures(t, control)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, _, err := mgr.Screenshot(ctx, mc.RunID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a screenshot whose caller gave up gave %v, want its ctx's error", err)
	}
	if took := time.Since(started); took > time.Second {
		t.Errorf("a caller that gave up waited %s", took)
	}

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = mgr.Screenshot(context.Background(), mc.RunID)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if !errors.Is(err, ErrScreenNotAnswering) || !strings.Contains(err.Error(), "already in flight") {
			t.Errorf("a look behind a hung capture gave %v, want ErrScreenNotAnswering naming the capture in flight", err)
		}
	}
	if took := time.Since(started); took > 4*time.Second {
		t.Errorf("the looks behind the hung capture took %s, want them to end when it timed out (2.3 s)", took)
	}
	if n := captures(t, control) - before; n != 1 {
		t.Errorf("%d guest captures started, want 1: a look while one is outstanding must not start another", n)
	}

	// The frame recorder does not wait at all.
	live, _ := mgr.get(mc.RunID)
	if err := live.input.capture.acquire(context.Background(), false, time.Second); err != nil {
		t.Fatalf("the slot is still held after the capture ended: %v", err)
	}
	if err := live.input.capture.acquire(context.Background(), false, time.Second); !errors.Is(err, errCaptureBusy) {
		t.Errorf("a recorder capture while one is outstanding gave %v, want errCaptureBusy", err)
	}
	live.input.capture.release(false, true)
}

// The recorder backs off while the screen does not answer, logs once when that starts and once
// when it ends, and is back to its interval after a capture works.
func TestTheRecorderBacksOffAndRecovers(t *testing.T) {
	mgr, _, control := newTestManager(t, withLookTimes(quickLooks(300*time.Millisecond)), WithFrameInterval(20*time.Millisecond))
	var logs logBuffer
	mgr.Log = logs.logger()
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	hang(t, control, "shot-hang")
	mc := readyMachine(t, mgr)

	waitFor(t, 5*time.Second, func() bool { return strings.Contains(logs.String(), "the guest screen is not answering") })
	hung := captures(t, control)
	time.Sleep(time.Second) // at a 20 ms interval, without the backoff, this is dozens of attempts
	if n := captures(t, control); n > hung+1 {
		t.Errorf("the recorder started %d captures in the second after a timeout, want it backing off", n-hung)
	}

	if err := os.Remove(filepath.Join(control, "shot-hang")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return strings.Contains(logs.String(), "the guest screen answers again") })
	frames, _ := ReadFrames(mgr.RunDir(mc.RunID))
	waitFor(t, 5*time.Second, func() bool {
		now, _ := ReadFrames(mgr.RunDir(mc.RunID))
		return len(now) >= len(frames)+5
	})
	if n := strings.Count(logs.String(), "the guest screen is not answering"); n != 1 {
		t.Errorf("the outage was logged %d times, want once", n)
	}
	if strings.Contains(logs.String(), "frame capture failed") {
		t.Error("a timeout was also logged as a frame failure")
	}
}

// The backoff doubles from 2 s and stops at a minute.
func TestFrameBackoff(t *testing.T) {
	for streak, want := range map[int]time.Duration{
		0: 0, 1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 5: 32 * time.Second,
		6: time.Minute, 7: time.Minute, 60: time.Minute,
	} {
		if got := frameBackoff(streak); got != want {
			t.Errorf("frameBackoff(%d) = %s, want %s", streak, got, want)
		}
	}
}

// machine_ui on a hung screen answers within its limits, and records the step.
func TestAHungUIReadAnswersWithinItsCap(t *testing.T) {
	mgr, _, control := newTestManager(t, withLookTimes(quickLooks(time.Second)))
	mc := readyMachine(t, mgr)
	if _, err := mgr.ScreenOf(context.Background(), mc.RunID); err != nil {
		t.Fatalf("ScreenOf: %v", err)
	}
	hang(t, control, "ui-hang")

	started := time.Now()
	tree, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0)
	if !errors.Is(err, ErrScreenNotAnswering) || !strings.Contains(err.Error(), "UI tree read") {
		t.Fatalf("a hung UI read gave %v, want ErrScreenNotAnswering", err)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Errorf("a hung UI read took %s, want about its 1.3 s limit", took)
	}
	steps := readSteps(t, mgr.RunDir(mc.RunID))
	if last := steps[len(steps)-1]; last.Tool != "machine_ui" || last.Seq != tree.Step || last.Error == "" {
		t.Errorf("the step is %+v, want the UI read's with its error", last)
	}
}

// A UI read whose second capture (the render check) hangs still returns its tree, in time, and
// says why nothing was marked.
func TestAHungRenderCaptureLeavesTheTreeUnmarked(t *testing.T) {
	mgr, _, control := newTestManager(t, withLookTimes(quickLooks(time.Second)))
	mc := readyMachine(t, mgr)
	writeUI(t, control, `{"app":{"name":"TipSplit","pid":7},"apps":["TipSplit"],"screen":{"width":400,"height":300},
"truncated":false,"elements":[{"role":"AXStaticText","value":"Each pays: $49.56","depth":0,"frame":{"x":20,"y":140,"w":200,"h":30}}]}`)
	hang(t, control, "shot-hang")

	started := time.Now()
	tree, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0)
	if err != nil {
		t.Fatalf("UI: %v", err)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Errorf("the read took %s, want its render capture bounded", took)
	}
	if len(tree.Elements) != 1 || !strings.Contains(tree.Unrendered, "not answering") {
		t.Errorf("tree = %+v, want the element and unrendered naming the hung capture", tree)
	}
}

// ensureInput runs one install, detached from its callers: a caller whose ctx ends returns at
// once while the compile goes on, and the next caller gets the same install's result.
func TestInstallWaitsHonourTheCallersContext(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "input-stale")
	if err := os.WriteFile(filepath.Join(control, "input-install-sleep"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	started := time.Now()
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			if _, err := mgr.ScreenOf(ctx, mc.RunID); err == nil || !strings.Contains(err.Error(), "still being installed") {
				t.Errorf("a caller that gave up during the install got %v", err)
			}
		}()
	}
	wg.Wait()
	if took := time.Since(started); took > time.Second {
		t.Errorf("callers whose ctx ended waited %s for a 2 s install", took)
	}
	screen, err := mgr.ScreenOf(context.Background(), mc.RunID)
	if err != nil || screen.Width != 1024 {
		t.Fatalf("ScreenOf after the install = %+v, %v", screen, err)
	}
	if n := strings.Count(testsupport.Calls(t, control), "swiftc -O"); n != 1 {
		t.Errorf("the helper was compiled %d times, want once for every caller", n)
	}
}

// A helper that is current costs one bounded check and no compile.
func TestACurrentHelperIsNotCompiled(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, err := mgr.ScreenOf(context.Background(), mc.RunID); err != nil {
		t.Fatalf("ScreenOf: %v", err)
	}
	calls := testsupport.Calls(t, control)
	if strings.Contains(calls, "swiftc -O") {
		t.Error("a current helper was compiled")
	}
	if !strings.Contains(calls, ": greenroom-watchdog") || !strings.Contains(calls, "greenroom-helper-check") {
		t.Error("the helper check did not run under the guest watchdog")
	}
}

// runWatchdog runs lookWatchdogScript on the host's /bin/sh, as the guest's would.
func runWatchdog(t *testing.T, limit string, args ...string) (stdout, stderr string, code int, took time.Duration) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-c", lookWatchdogScript, "greenroom-watchdog", limit}, args...)...)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	started := time.Now()
	err := cmd.Run()
	took = time.Since(started)
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code, took
}

// running reports whether a process whose command line contains pattern is running.
func running(t *testing.T, pattern string) bool {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	return strings.TrimSpace(string(out)) != ""
}

// The watchdog passes a command's output and exit status through untouched, and a quick
// command leaves no watchdog behind.
func TestTheWatchdogKeepsTheCommandsOutputAndStatus(t *testing.T) {
	stdout, stderr, code, took := runWatchdog(t, "37", "/bin/sh", "-c", `echo out; echo "$GREENROOM_LOOK_DIR" >&2; exit 3`)
	if stdout != "out\n" || code != 3 {
		t.Errorf("stdout %q, exit %d; want out and 3", stdout, code)
	}
	dir := strings.TrimSpace(stderr)
	if !strings.HasPrefix(dir, "/tmp/greenroom-look.") {
		t.Fatalf("stderr %q, want only the command's own (its private dir)", stderr)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the private dir %s is still there", dir)
	}
	if took > 3*time.Second {
		t.Errorf("a quick command took %s under a 37 s watchdog", took)
	}
	if running(t, "sleep 37") {
		t.Error("the watchdog's sleep outlived the command")
	}
}

// At its limit the watchdog ends the command and everything in its process group, exits 124
// with a note, keeps the output so far and removes its dir. A command that ignores TERM is
// killed 2 s later.
func TestTheWatchdogEndsAHungCommand(t *testing.T) {
	for name, command := range map[string]string{
		"hung":          `echo started; echo "$GREENROOM_LOOK_DIR" >&2; sleep 41.25; echo never`,
		"ignoring TERM": `trap "" TERM; echo started; echo "$GREENROOM_LOOK_DIR" >&2; sleep 41.5; echo never`,
	} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, took := runWatchdog(t, "1", "/bin/sh", "-c", command)
			if code != lookTimedOutExit || stdout != "started\n" {
				t.Errorf("exit %d, stdout %q; want 124 and the output so far", code, stdout)
			}
			if !strings.Contains(stderr, "greenroom: the guest stopped it after 1 s") {
				t.Errorf("stderr %q lacks the note", stderr)
			}
			if took > 6*time.Second {
				t.Errorf("a 1 s watchdog took %s", took)
			}
			if dir := strings.SplitN(stderr, "\n", 2)[0]; dir == "" || !strings.HasPrefix(dir, "/tmp/greenroom-look.") {
				t.Errorf("no private dir in stderr %q", stderr)
			} else if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("the private dir %s is still there", dir)
			}
			pattern := "sleep 41.25"
			if name != "hung" {
				pattern = "sleep 41.5"
			}
			waitFor(t, 2*time.Second, func() bool { return !running(t, pattern) })
		})
	}
}

// The tart exec command line a capture runs.
func TestWatchdogArgs(t *testing.T) {
	args := watchdogArgs(1500*time.Millisecond, "/bin/sh", "-c", captureShellScript)
	want := []string{"/bin/sh", "-c", lookWatchdogScript, "greenroom-watchdog", "2", "/bin/sh", "-c", captureShellScript}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("watchdogArgs = %q, want %q", args, want)
	}
	if got := watchdogArgs(10 * time.Millisecond)[4]; got != "1" {
		t.Errorf("a limit under a second is %s s in the guest, want 1", got)
	}
}
