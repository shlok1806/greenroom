package machine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// The approvals must be on disk before anything captures: the frame recorder
// starts at ready, and one capture without them raises the alert.
func TestBootApprovesScreenCaptureBeforeReady(t *testing.T) {
	mgr, _, control := newTestManager(t, WithFrameInterval(50*time.Millisecond))
	mc := readyMachine(t, mgr)

	log := testsupport.Calls(t, control)
	approve := strings.Index(log, "ScreenCaptureApprovals")
	if approve < 0 {
		t.Fatalf("boot never wrote replayd's screen-capture approvals\ncalls:\n%s", log)
	}
	if shot := strings.Index(log, "screencapture"); shot >= 0 && shot < approve {
		t.Errorf("a screen capture ran before the approvals were written, so the guest shows the alert\ncalls:\n%s", log)
	}
	for _, s := range readSteps(t, mc.Dir) {
		if s.Tool == "machine_boot" {
			if out, _ := s.Output.(map[string]any); out["captureAlertSeconds"] == nil {
				t.Errorf("the boot step does not time the approval phase: %v", s.Output)
			}
		}
	}
}

// The alert covers the screen but the machine works, so a failed write is
// recorded and the machine still becomes ready.
func TestBootSurvivesAFailedScreenCaptureApproval(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-capture-approval")
	mc := readyMachine(t, mgr)

	var boot *Step
	for _, s := range readSteps(t, mc.Dir) {
		if s.Tool == "machine_boot" {
			boot = &s
		}
	}
	if boot == nil {
		t.Fatal("no machine_boot step")
	}
	out, _ := boot.Output.(map[string]any)
	if msg, _ := out["captureAlertError"].(string); !strings.Contains(msg, "pre-approve screen capture") {
		t.Errorf("the boot step does not say the approval failed: %v", boot.Output)
	}
}

func TestPrepareGuestApprovesScreenCapture(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	if err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil); err != nil {
		t.Fatalf("PrepareGuest: %v", err)
	}
	if log := testsupport.Calls(t, control); !strings.Contains(log, "ScreenCaptureApprovals") {
		t.Errorf("PrepareGuest never wrote replayd's approvals, so the image alerts on first capture\ncalls:\n%s", log)
	}

	testsupport.Flag(t, control, "fail-capture-approval")
	err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil)
	if err == nil || !strings.Contains(err.Error(), "pre-approve screen capture") {
		t.Errorf("error = %v, want an image build that cannot write the approvals to fail and say so", err)
	}
}

// ageApprovalCheck makes the next capture check the approvals again.
func ageApprovalCheck(t *testing.T, mgr *Manager, runID string) {
	t.Helper()
	live, err := mgr.get(runID)
	if err != nil {
		t.Fatal(err)
	}
	live.input.approval.mu.Lock()
	live.input.approval.checked = time.Now().Add(-2 * captureApprovalCheck)
	live.input.approval.mu.Unlock()
}

// Boot has just written the approvals, so the first captures do not check again;
// once the check interval has passed (by the wall clock, so a host sleep counts),
// the next capture checks first. A current record is only read, never rewritten.
func TestCapturesCheckTheApprovalsAtMostOnceAMinute(t *testing.T) {
	mgr, _, control := newTestManager(t)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, mgr)
	count := func(s string) int { return strings.Count(testsupport.Calls(t, control), s) }
	writes := count("sh write")

	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if n := count("sh check"); n != 0 {
		t.Errorf("a capture right after boot checked the approvals %d times, want 0", n)
	}

	ageApprovalCheck(t, mgr, mc.RunID)
	for i := 0; i < 3; i++ {
		if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
			t.Fatal(err)
		}
	}
	if n := count("sh check"); n != 1 {
		t.Errorf("three captures after the interval checked %d times, want 1", n)
	}
	if n := count("sh write"); n != writes {
		t.Errorf("a current record was rewritten (%d writes, want %d): that kills replayd for nothing", n, writes)
	}
	log := testsupport.Calls(t, control)
	if c, shot := strings.LastIndex(log, "sh check"), strings.LastIndex(log, "screencapture"); c > shot {
		t.Error("the check ran after the capture it should protect")
	}
}

// Killing replayd stops ScreenCaptureKit, so a rewrite ends a running live
// stream first with a reason, and a fresh helper serves the next viewer. A check
// that finds the record current leaves the stream alone.
func TestAStaleApprovalEndsTheLiveStreamWithAReasonBeforeTheWrite(t *testing.T) {
	mgr, _, control := newTestManager(t)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, mgr)
	w := watchScreen(t, mgr, mc.RunID)
	expectOpening(t, w)

	ageApprovalCheck(t, mgr, mc.RunID)
	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if !streamOf(mgr, mc.RunID).running() {
		t.Fatal("a check that found the record current ended the live stream")
	}

	testsupport.Flag(t, control, "capture-approval-stale")
	writes := strings.Count(testsupport.Calls(t, control), "sh write")
	ageApprovalCheck(t, mgr, mc.RunID)
	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, w)
	if err := w.Err(); err == nil || !strings.Contains(err.Error(), "re-approved") || !strings.Contains(err.Error(), "reconnect") {
		t.Errorf("the viewer ended with %v, want the re-approval named and a reconnect", err)
	}
	if n := strings.Count(testsupport.Calls(t, control), "sh write"); n != writes+1 {
		t.Errorf("a stale record was not rewritten (%d writes, want %d)", n, writes+1)
	}
	expectOpening(t, watchScreen(t, mgr, mc.RunID))
}

// A failed refresh never fails the capture.
func TestAFailedApprovalRefreshDoesNotFailTheCapture(t *testing.T) {
	mgr, _, control := newTestManager(t)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, mgr)
	live, _ := mgr.get(mc.RunID)
	live.input.approval.mu.Lock()
	live.input.approval.checked = time.Time{}
	live.input.approval.mu.Unlock()
	testsupport.Flag(t, control, "fail-capture-approval")
	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
		t.Errorf("screenshot failed with the refresh: %v", err)
	}
}

func TestApproveCaptureRecordsAStepAndReturnsTheClient(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	client, step, err := mgr.ApproveCapture(context.Background(), mc.RunID, "~/work/Shot.app")
	if err != nil {
		t.Fatal(err)
	}
	if client != "file:///Users/admin/work/Shot.app/" || step == 0 {
		t.Errorf("client %q step %d, want the bundle URL and a step", client, step)
	}
	if !strings.Contains(testsupport.Calls(t, control), "sh app work/Shot.app") {
		t.Errorf("the path did not reach the script as an argument\n%s", testsupport.Calls(t, control))
	}
	if _, _, err := mgr.ApproveCapture(context.Background(), mc.RunID, "/Applications/Shot"); err == nil {
		t.Error("a path that is not an .app bundle was accepted")
	}

	// Approving kills replayd, so a running live stream ends with a reason first.
	w := watchScreen(t, mgr, mc.RunID)
	expectOpening(t, w)
	if _, _, err := mgr.ApproveCapture(context.Background(), mc.RunID, "work/Shot.app"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, w)
	if err := w.Err(); err == nil || !strings.Contains(err.Error(), "reconnect") {
		t.Errorf("the viewer ended with %v, want a reconnect", err)
	}
}

// scriptHome runs captureApprovalsScript for real against a throwaway home, with a
// fake tart-guest-agent behind a Homebrew-style symlink and pgrep stubbed to name
// a sleep process standing in for replayd, so the host's replayd is left alone.
type scriptHome struct {
	t     *testing.T
	home  string
	bin   string
	agent string // resolved path, the record's key
	plist string
}

func newScriptHome(t *testing.T) *scriptHome {
	if runtime.GOOS != "darwin" {
		t.Skip("needs defaults(1) and osascript")
	}
	h := &scriptHome{t: t, home: t.TempDir(), bin: t.TempDir()}
	cellar := filepath.Join(t.TempDir(), "Cellar", "tart-guest-agent", "0.14.1", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(cellar, "tart-guest-agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// replayd keys the record by the resolved path, not the Homebrew symlink.
	if err := os.Symlink(agent, filepath.Join(h.bin, "tart-guest-agent")); err != nil {
		t.Fatal(err)
	}
	var err error
	if h.agent, err = filepath.EvalSymlinks(agent); err != nil {
		t.Fatal(err)
	}
	h.plist = filepath.Join(h.home, "Library", "Group Containers", "group.com.apple.replayd", "ScreenCaptureApprovals.plist")
	return h
}

// run runs the script with args and reports whether it looked for replayd and
// whether the stand-in was killed, and what it printed.
func (h *scriptHome) run(args ...string) (looked, killed bool, out string) {
	t := h.t
	t.Helper()
	replayd := exec.Command("/bin/sleep", "60")
	if err := replayd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- replayd.Wait() }()
	defer func() { _ = replayd.Process.Kill() }()
	pgrepLog := filepath.Join(h.home, "pgrep.log")
	_ = os.Remove(pgrepLog)
	stub := "#!/bin/sh\necho \"$@\" >> " + pgrepLog + "\necho " + strconv.Itoa(replayd.Process.Pid) + "\n"
	if err := os.WriteFile(filepath.Join(h.bin, "pgrep"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// Never the host's launchd: the script restarts replayd with launchctl kickstart.
	launchLog := filepath.Join(h.home, "launchctl.log")
	_ = os.Remove(launchLog)
	if err := os.WriteFile(filepath.Join(h.bin, "launchctl"), []byte("#!/bin/sh\necho \"$@\" >> "+launchLog+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", append([]string{"-c", captureApprovalsScript, "sh"}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+h.home, "PATH="+h.bin+":/bin:/usr/bin:/usr/sbin")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script %v: %v\n%s", args, err, b)
	}
	calls, _ := os.ReadFile(pgrepLog)
	if len(calls) > 0 && !strings.Contains(string(calls), "-x -u") {
		t.Errorf("pgrep %q would also match other users' replayd", calls)
	}
	select {
	case err := <-exited:
		killed = err != nil && strings.Contains(err.Error(), "killed")
	case <-time.After(time.Second):
	}
	// Until replayd is back every capture fails, so a kill is always followed by a kickstart.
	started, _ := os.ReadFile(launchLog)
	if kicked := strings.Contains(string(started), "kickstart gui/") && strings.Contains(string(started), "/com.apple.replayd"); kicked != killed {
		t.Errorf("replayd killed %v but kickstarted %v (%q)", killed, kicked, started)
	}
	return len(calls) > 0, killed, string(b)
}

// record writes a client's record as replayd would have left it.
func (h *scriptHome) record(client, lastUsed, hint string) {
	h.t.Helper()
	if out, err := exec.Command("defaults", "write", h.plist, client, "-dict",
		"kScreenCaptureApprovalLastUsed", "-date", lastUsed,
		"kScreenCapturePrivacyHintDate", "-date", hint).CombinedOutput(); err != nil {
		h.t.Fatalf("seed %s: %v\n%s", client, err, out)
	}
}

func (h *scriptHome) field(client, key string) string {
	h.t.Helper()
	out, _ := exec.Command("defaults", "read", h.plist, client).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), " = "); ok && k == key {
			return strings.Trim(v, `";`)
		}
	}
	return ""
}

const far = "3024-01-01 00:00:00 +0000"

// A baked image carries a record with the hint date in 3024 but LastUsed from the
// day it was built. That alerts on first capture a month later, so boot must
// rewrite it, not skip it: the dialog is decided by LastUsed alone.
func TestCaptureApprovalsWriteReplacesAStaleRecordAndRestartsReplayd(t *testing.T) {
	h := newScriptHome(t)
	h.record(h.agent, "2026-08-01 00:00:00 +0000", far)

	looked, killed, _ := h.run("write")
	if !looked || !killed {
		t.Errorf("pgrep called %v, replayd killed %v; want replayd stopped and killed so it drops its cached copy", looked, killed)
	}
	for _, client := range []string{h.agent, "/usr/libexec/sshd-keygen-wrapper"} {
		for _, key := range []string{"kScreenCaptureApprovalLastUsed", "kScreenCapturePrivacyHintDate", "kScreenCaptureApprovalLastAlerted"} {
			if got := h.field(client, key); got != far {
				t.Errorf("%s %s = %q, want %q", client, key, got, far)
			}
		}
	}

	// write never skips, even when everything is already in 3024.
	if looked, killed, _ := h.run("write"); !looked || !killed {
		t.Errorf("a second write skipped (pgrep %v, killed %v); boot must write unconditionally", looked, killed)
	}
}

// check only reads: it exits 0 for a record replayd keeps current and 3 for one
// that was reset or aged past a week by a clock jump or a long host sleep.
func TestCaptureApprovalsCheckOnlyReads(t *testing.T) {
	h := newScriptHome(t)
	h.run("write")
	_ = os.Remove(filepath.Join(h.home, "pgrep.log"))
	check := func() int {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", captureApprovalsScript, "sh", "check")
		cmd.Env = append(os.Environ(), "HOME="+h.home, "PATH="+h.bin+":/bin:/usr/bin:/usr/sbin")
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return 0
	}
	if code := check(); code != 0 {
		t.Errorf("check of a fresh write = %d, want 0", code)
	}
	// replayd sets LastUsed to now on every capture.
	now := time.Now().UTC()
	h.record(h.agent, now.Add(-time.Hour).Format("2006-01-02 15:04:05 +0000"), far)
	if code := check(); code != 0 {
		t.Errorf("check of a record used an hour ago = %d, want 0", code)
	}
	old := now.Add(-40 * 24 * time.Hour).Format("2006-01-02 15:04:05 +0000")
	h.record(h.agent, old, far)
	if code := check(); code != captureStale {
		t.Errorf("check of a record last used 40 days ago = %d, want %d", code, captureStale)
	}
	if got := h.field(h.agent, "kScreenCaptureApprovalLastUsed"); got != old {
		t.Errorf("check wrote LastUsed = %q; it must only read", got)
	}
	if out, err := exec.Command("defaults", "delete", h.plist, h.agent).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if code := check(); code != captureStale {
		t.Errorf("check of a missing record = %d, want %d", code, captureStale)
	}
	if calls, _ := os.ReadFile(filepath.Join(h.home, "pgrep.log")); len(calls) > 0 {
		t.Errorf("check looked for replayd: %q", calls)
	}
}

// An app under test that captures the screen itself is keyed by its bundle URL.
func TestCaptureApprovalsAppModeKeysTheBundleURL(t *testing.T) {
	h := newScriptHome(t)
	if err := os.MkdirAll(filepath.Join(h.home, "work", "Shot Tool.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, killed, out := h.run("app", "work/Shot Tool.app")
	realHome, err := filepath.EvalSymlinks(h.home)
	if err != nil {
		t.Fatal(err)
	}
	want := "file://" + strings.ReplaceAll(realHome, " ", "%20") + "/work/Shot%20Tool.app/"
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("app mode printed %q, want the bundle URL %q", got, want)
	}
	if got := h.field(want, "kScreenCaptureApprovalLastUsed"); got != far {
		t.Errorf("the app's LastUsed = %q, want %q", got, far)
	}
	if !killed {
		t.Error("app mode did not restart replayd")
	}
}
