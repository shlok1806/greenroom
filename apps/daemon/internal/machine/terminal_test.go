package machine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #60: every fresh machine had Terminal running at login, on lean-a with the image build's
// shell history on screen. Boot quits it before ready and records the phase.
func TestBootQuitsTheTerminalTheImageStarts(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if log := testsupport.Calls(t, control); !strings.Contains(log, "pkill -x Terminal") {
		t.Fatalf("boot never quit Terminal\ncalls:\n%s", log)
	}
	if out := bootStep(t, mc.Dir); out["terminalSeconds"] == nil {
		t.Errorf("the boot step does not time the Terminal phase: %v", out)
	}
}

// Never fatal, like the other boot provisioning.
func TestBootSurvivesATerminalThatWillNotQuit(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-terminal")
	mc := readyMachine(t, mgr)
	if msg, _ := bootStep(t, mc.Dir)["terminalError"].(string); !strings.Contains(msg, "Terminal") {
		t.Errorf("the boot step does not say Terminal stayed: %v", bootStep(t, mc.Dir))
	}
}

// prepare-image bakes a clean image: no Terminal saved state to restore at the next login.
func TestPrepareGuestQuitsTerminalAndClearsItsSavedState(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	if err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil); err != nil {
		t.Fatalf("PrepareGuest: %v", err)
	}
	if !strings.Contains(testsupport.Calls(t, control), "com.apple.Terminal.savedState") {
		t.Error("PrepareGuest never cleared Terminal's saved state")
	}
}

// The script itself, under stub pgrep and pkill: a Terminal that quits passes, one that stays fails
// naming it, and its saved state is removed either way.
func TestQuitTerminalScript(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stays bool
	}{{"quits", false}, {"stays", true}} {
		t.Run(tc.name, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			saved := filepath.Join(home, "Library", "Saved Application State", "com.apple.Terminal.savedState")
			if err := os.MkdirAll(saved, 0o755); err != nil {
				t.Fatal(err)
			}
			flag := filepath.Join(bin, "running")
			_ = os.WriteFile(flag, nil, 0o644)
			pgrep := "#!/bin/sh\n[ -f " + flag + " ]\n"
			pkill := "#!/bin/sh\n"
			if !tc.stays {
				pkill += "rm -f " + flag + "\n"
			}
			for name, body := range map[string]string{"pgrep": pgrep, "pkill": pkill, "sleep": "#!/bin/sh\n"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/bin/sh", "-c", quitTerminalScript)
			cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if tc.stays != (err != nil) {
				t.Fatalf("stays=%v: err = %v, output %s", tc.stays, err, out)
			}
			if tc.stays && !strings.Contains(string(out), "Terminal") {
				t.Errorf("the failure does not name Terminal: %s", out)
			}
			if _, err := os.Stat(saved); !os.IsNotExist(err) {
				t.Errorf("Terminal's saved state is still there (%v)", err)
			}
		})
	}
}
