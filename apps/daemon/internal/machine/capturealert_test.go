package machine

import (
	"context"
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

// The script runs for real against a throwaway home. pgrep is stubbed to name a
// sleep process standing in for replayd, so the host's own replayd is left alone.
func TestCaptureApprovalsScriptWritesFiveKeysHoldsReplaydAndIsIdempotent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("needs PlistBuddy and plutil")
	}
	home := t.TempDir()
	bin := t.TempDir()
	cellar := filepath.Join(t.TempDir(), "Cellar", "tart-guest-agent", "0.14.1", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(cellar, "tart-guest-agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// replayd keys the record by the resolved path, not the Homebrew symlink.
	if err := os.Symlink(agent, filepath.Join(bin, "tart-guest-agent")); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(agent)
	if err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "Group Containers", "group.com.apple.replayd", "ScreenCaptureApprovals.plist")

	run := func() (pgrepCalls int, replaydKilled bool) {
		t.Helper()
		replayd := exec.Command("/bin/sleep", "60")
		if err := replayd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan error, 1)
		go func() { exited <- replayd.Wait() }()
		defer func() { _ = replayd.Process.Kill() }()
		pgrepLog := filepath.Join(home, "pgrep.log")
		_ = os.Remove(pgrepLog)
		stub := "#!/bin/sh\necho \"$@\" >> " + pgrepLog + "\necho " + strconv.Itoa(replayd.Process.Pid) + "\n"
		if err := os.WriteFile(filepath.Join(bin, "pgrep"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("/bin/sh", "-c", captureApprovalsScript)
		cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":/bin:/usr/bin:/usr/sbin")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("script: %v\n%s", err, out)
		}
		calls, _ := os.ReadFile(pgrepLog)
		if len(calls) > 0 && !strings.Contains(string(calls), "-x -u") {
			t.Errorf("pgrep %q would also match other users' replayd", calls)
		}
		select {
		case err := <-exited:
			replaydKilled = err != nil && strings.Contains(err.Error(), "killed")
		case <-time.After(2 * time.Second):
		}
		return strings.Count(string(calls), "\n"), replaydKilled
	}

	// A record in the old three-key form, which macOS 26 ignores, must be replaced.
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/libexec/PlistBuddy",
		"-c", "Add :"+resolved+" dict",
		"-c", "Add :"+resolved+":kScreenCapturePrivacyHintDate date Thu Jan 01 00:00:00 UTC 3024", plist).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v\n%s", err, out)
	}

	if calls, killed := run(); calls != 1 || !killed {
		t.Errorf("first run: pgrep calls %d, replayd killed %v; want replayd found and killed so it drops its cached copy", calls, killed)
	}
	out, err := exec.Command("plutil", "-p", plist).CombinedOutput()
	if err != nil {
		t.Fatalf("plutil: %v\n%s", err, out)
	}
	text := string(out)
	for _, client := range []string{resolved, "/usr/libexec/sshd-keygen-wrapper"} {
		if strings.Count(text, `"`+client+`" => {`) != 1 {
			t.Errorf("want one dictionary for %s\n%s", client, text)
		}
	}
	for key, want := range map[string]string{
		"kScreenCaptureAlertableUsageCount": `=> 1`,
		"kScreenCaptureApprovalLastAlerted": `=> `,
		"kScreenCaptureApprovalLastUsed":    `=> `,
		"kScreenCapturePrivacyHintDate":     `=> 3024-01-01`,
		"kScreenCapturePrivacyHintPolicy":   `=> 2592000`,
	} {
		if got := strings.Count(text, `"`+key+`" `+want); got != 2 {
			t.Errorf("%s %s appears for %d clients, want 2 (macOS 26 drops a record missing any of the five)\n%s", key, want, got, text)
		}
	}

	// Already approved: the second run reads and leaves replayd alone.
	if calls, killed := run(); calls != 0 || killed {
		t.Errorf("second run: pgrep calls %d, replayd killed %v; want an early exit that restarts nothing", calls, killed)
	}
}
