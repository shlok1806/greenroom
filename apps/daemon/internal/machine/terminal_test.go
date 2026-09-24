package machine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #60: every fresh machine had Terminal running at login, on lean-a with the image build's
// shell history on screen. Boot quits it before ready and records the phase.
func TestBootQuitsTheTerminalTheImageStarts(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	out := bootStep(t, mc.Dir)
	if out["terminalSeconds"] == nil {
		t.Errorf("the boot step does not time the Terminal phase: %v", out)
	}
	if out["terminalQuit"] != true {
		t.Errorf("the boot step does not record the Terminal it quit: %v", out)
	}
	if _, ok := out["terminalError"]; ok {
		t.Errorf("the boot step records a Terminal error: %v", out)
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

// prepare-image bakes a clean image: it runs the Terminal script, and a Terminal that stays does
// not fail the image.
func TestPrepareGuestQuitsTerminal(t *testing.T) {
	for _, stays := range []bool{false, true} {
		bin, control := testsupport.FakeTart(t)
		if stays {
			testsupport.Flag(t, control, "fail-terminal")
		}
		if err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil); err != nil {
			t.Fatalf("stays=%v: PrepareGuest: %v", stays, err)
		}
		if _, err := os.Stat(filepath.Join(control, "terminal-quit-ran")); err != nil {
			t.Errorf("stays=%v: PrepareGuest never ran the Terminal script (%v)", stays, err)
		}
	}
}

// The script itself, under stub pgrep, pkill and sleep. Each process appears after a number of
// pgrep polls (never if absent), so a Terminal launchd starts late is still found and quit, a
// session with no Terminal reports none, one that stays fails naming it, and the saved state is
// removed every time.
func TestQuitTerminalScript(t *testing.T) {
	for _, tc := range []struct {
		name   string
		after  map[string]int
		stays  bool
		report string
	}{
		{"quits", map[string]int{"Dock": 0, "Finder": 0, "Terminal": 0}, false, "quit"},
		{"appears late", map[string]int{"Dock": 3, "Finder": 0, "Terminal": 5}, false, "quit"},
		{"none", map[string]int{"Dock": 0, "Finder": 0}, false, "none"},
		{"stays", map[string]int{"Dock": 0, "Finder": 0, "Terminal": 0}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, bin, state := t.TempDir(), t.TempDir(), t.TempDir()
			saved := filepath.Join(home, "Library", "Saved Application State", "com.apple.Terminal.savedState")
			if err := os.MkdirAll(saved, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, n := range tc.after {
				if err := os.WriteFile(filepath.Join(state, "after-"+name), []byte(strconv.Itoa(n)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			pgrep := `#!/bin/sh
S=` + state + `
name=$2
[ -f "$S/gone-$name" ] && exit 1
n=$(( $(cat "$S/polls-$name" 2>/dev/null || echo 0) + 1 ))
echo $n > "$S/polls-$name"
[ -f "$S/after-$name" ] && [ $n -gt $(cat "$S/after-$name") ]
`
			pkill := "#!/bin/sh\n"
			if !tc.stays {
				pkill += "for last; do :; done\n: > " + state + "/gone-$last\n"
			}
			for name, body := range map[string]string{"pgrep": pgrep, "pkill": pkill, "sleep": "#!/bin/sh\n"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/bin/sh", "-c", quitTerminalScript)
			cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin"}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if tc.stays != (err != nil) {
				t.Fatalf("stays=%v: err = %v, stdout %q, stderr %q", tc.stays, err, out, stderr.String())
			}
			if tc.stays && !strings.Contains(stderr.String(), "Terminal") {
				t.Errorf("the failure does not name Terminal: %q", stderr.String())
			}
			if !tc.stays && strings.TrimSpace(string(out)) != tc.report {
				t.Errorf("the script reports %q, want %q", out, tc.report)
			}
			if _, err := os.Stat(saved); !os.IsNotExist(err) {
				t.Errorf("Terminal's saved state is still there (%v)", err)
			}
		})
	}
}
