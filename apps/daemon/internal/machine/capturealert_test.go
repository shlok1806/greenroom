package machine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// The script runs for real against a throwaway home, with a fake agent and a
// killall stub so the host's own replayd is left alone.
func TestCaptureApprovalsScriptWritesTheNestedFormAndIsIdempotent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("needs defaults(1) and plutil(1)")
	}
	home := t.TempDir()
	bin := t.TempDir()
	cellar := filepath.Join(t.TempDir(), "Cellar", "tart-guest-agent", "0.14.1", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(cellar, "tart-guest-agent")
	for path, body := range map[string]string{agent: "#!/bin/sh\n", filepath.Join(bin, "killall"): "#!/bin/sh\necho \"$@\" >> \"$HOME/killall.log\"\n"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// replayd keys the record by the resolved path, not the Homebrew symlink.
	if err := os.Symlink(agent, filepath.Join(bin, "tart-guest-agent")); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(agent)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		cmd := exec.Command("/bin/sh", "-c", captureApprovalsScript)
		cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":/bin:/usr/bin:/usr/sbin")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run %d: %v\n%s", i+1, err, out)
		}
	}

	plist := filepath.Join(home, "Library", "Group Containers", "group.com.apple.replayd", "ScreenCaptureApprovals.plist")
	out, err := exec.Command("plutil", "-p", plist).CombinedOutput()
	if err != nil {
		t.Fatalf("plutil: %v\n%s", err, out)
	}
	text := string(out)
	for _, client := range []string{resolved, "/usr/libexec/sshd-keygen-wrapper"} {
		if strings.Count(text, `"`+client+`" => {`) != 1 {
			t.Errorf("want one dictionary for %s (macOS 15.1+ ignores a bare date)\n%s", client, text)
		}
	}
	for _, key := range []string{"kScreenCaptureApprovalLastAlerted", "kScreenCaptureApprovalLastUsed", "kScreenCapturePrivacyHintDate"} {
		if got := strings.Count(text, `"`+key+`" => 3024-01-01`); got != 2 {
			t.Errorf("%s is dated 3024 for %d clients, want 2\n%s", key, got, text)
		}
	}
	killed, _ := os.ReadFile(filepath.Join(home, "killall.log"))
	if !strings.Contains(string(killed), "replayd") {
		t.Errorf("the script never restarted replayd, which would write its cached copy back over the approvals")
	}
}
