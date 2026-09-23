package machine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

func bootStep(t *testing.T, dir string) map[string]any {
	t.Helper()
	for _, s := range readSteps(t, dir) {
		if s.Tool == "machine_boot" {
			out, _ := s.Output.(map[string]any)
			return out
		}
	}
	t.Fatal("no machine_boot step")
	return nil
}

// Issue #41: an image whose input helper is older than this daemon's is found
// at boot, named in the log with its fix, recorded in the boot step, and the
// helper is compiled before ready rather than inside the first UI call.
func TestBootFindsAStaleInputHelperAndCompilesItBeforeReady(t *testing.T) {
	mgr, _, control := newTestManager(t)
	var logs logBuffer
	mgr.Log = logs.logger()
	testsupport.Flag(t, control, "input-stale")
	mc := readyMachine(t, mgr)

	if !strings.Contains(testsupport.Calls(t, control), "swiftc") {
		t.Error("the stale helper was not compiled during boot")
	}
	out := bootStep(t, mc.Dir)
	if out["inputHelperStale"] != true || !strings.Contains(asString(out["inputHelperFound"]), "greenroom-input-2") {
		t.Errorf("the boot step does not record the stale helper: %v", out)
	}
	if out["inputHelperSeconds"] == nil {
		t.Errorf("the boot step does not time the helper phase: %v", out)
	}
	log := logs.String()
	for _, want := range []string{"greenroom-input-2", "scripts/build-image.sh", "-force", testImage} {
		if !strings.Contains(log, want) {
			t.Errorf("the daemon log does not say %q:\n%s", want, log)
		}
	}
}

// A current image pays only the check: no compile at boot, nothing stale recorded.
func TestBootLeavesACurrentInputHelperAlone(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if strings.Contains(testsupport.Calls(t, control), "swiftc") {
		t.Error("boot compiled a helper the image already has")
	}
	out := bootStep(t, mc.Dir)
	if _, ok := out["inputHelperStale"]; ok {
		t.Errorf("a current helper is recorded as stale: %v", out)
	}
}

// A compile that fails at boot is recorded, never fatal: the first UI call tries again.
func TestBootSurvivesAFailedHelperCompile(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "input-stale")
	testsupport.Flag(t, control, "fail-input-install")
	mc := readyMachine(t, mgr)
	if msg := asString(bootStep(t, mc.Dir)["inputHelperError"]); !strings.Contains(msg, "swiftc") {
		t.Errorf("the boot step does not say the compile failed: %q", msg)
	}
}

// The check itself, run by a real shell against a throwaway home.
func TestHelperCheckScriptTellsCurrentFromStale(t *testing.T) {
	home := t.TempDir()
	run := func() string {
		cmd := exec.Command("/bin/sh", "-c", helperCheckScript())
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(); got != "stale" {
		t.Errorf("an image with no helper: %q, want stale", got)
	}
	bin := filepath.Join(home, ".greenroom", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	answer := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(filepath.Join(bin, "greenroom-input-2"), answer, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := run(); got != "stale greenroom-input-2" {
		t.Errorf("an image with an old helper: %q, want stale greenroom-input-2", got)
	}
	if err := os.WriteFile(filepath.Join(home, helperName()), answer, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := run(); got != "current" {
		t.Errorf("an image with this daemon's helper: %q, want current", got)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
