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

const testPubKey = "ssh-ed25519 AAAAfaketestkey test@greenroom"

// runShWithHome runs script in a real /bin/sh with HOME set to home, to test
// the guest scripts' idempotency against a throwaway home.
func runShWithHome(t *testing.T, home, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// PrepareGuest must install at the same versioned path a machine's first
// control request does.
func TestPrepareGuestInstallsTheHelper(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	if err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil); err != nil {
		t.Fatalf("PrepareGuest: %v", err)
	}
	log := testsupport.Calls(t, control)
	if !strings.Contains(log, "swiftc") {
		t.Errorf("PrepareGuest never ran the compile script\ncalls:\n%s", log)
	}
	if !strings.Contains(log, helperName()) {
		t.Errorf("PrepareGuest never named %q, the path the current inputHelperVersion (%d) installs at\ncalls:\n%s",
			helperName(), inputHelperVersion, log)
	}
}

// `tart stop` does not flush the guest's page cache, so PrepareGuest must
// sync or the baked helper is lost on the next boot.
func TestPrepareGuestSyncsBeforeReturning(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	vmName := "greenroom-base-test"
	if err := PrepareGuest(context.Background(), bin, vmName, testPubKey, nil); err != nil {
		t.Fatalf("PrepareGuest: %v", err)
	}
	log := testsupport.Calls(t, control)
	if !strings.Contains(log, "exec "+vmName+" /bin/sh -c sync") {
		t.Errorf("PrepareGuest never ran sync in the guest before returning; a caller that stops the VM right "+
			"after (scripts/build-image.sh) may ship an image whose just-baked helper never reached disk\ncalls:\n%s", log)
	}
}

// The script skips the compile when the binary already answers --version.
// FakeTart only sees the script text, so this runs it in a real shell.
func TestInstallHelperScriptSkipsTheCompileWhenTheBinaryAlreadyAnswers(t *testing.T) {
	home := t.TempDir()
	binPath := filepath.Join(home, helperName())
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runShWithHome(t, home, installHelperScript())
	if err != nil {
		t.Fatalf("installHelperScript: %v\n%s", err, out)
	}

	srcDir := filepath.Join(home, helperSourceDir())
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Errorf("installHelperScript created %s although the helper already answered --version; the compile was not skipped", srcDir)
	}
}

// With no binary, the script must reach the compile. A PATH without swiftc
// makes it stop deterministically at the swiftc check.
func TestInstallHelperScriptCompilesWhenNoBinaryIsThere(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command("/bin/sh", "-c", installHelperScript())
	cmd.Env = []string{"HOME=" + home, "PATH=" + toolFarm(t, "mkdir", "dirname", "printf", "base64", "rm")}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("installHelperScript succeeded with no swiftc on PATH\n%s", out)
	}
	if !strings.Contains(string(out), "swiftc is not installed") {
		t.Errorf("the script's own message is missing; got:\n%s", out)
	}
	srcDir := filepath.Join(home, helperSourceDir())
	if _, err := os.Stat(filepath.Join(srcDir, "main.swift")); err != nil {
		t.Errorf("the script never wrote main.swift before failing to find swiftc: %v", err)
	}
}

// toolFarm returns a PATH holding symlinks to only the named executables.
func toolFarm(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("toolFarm: %s is not on this machine's PATH: %v", name, err)
		}
		if err := os.Symlink(p, filepath.Join(dir, name)); err != nil {
			t.Fatalf("toolFarm: symlink %s: %v", name, err)
		}
	}
	return dir
}

// Every boot reruns the key script; authorized_keys must not grow.
func TestAppendAuthorizedKeyScriptDoesNotDuplicateTheKey(t *testing.T) {
	home := t.TempDir()
	script := appendAuthorizedKeyScript(testPubKey)
	for i := 0; i < 2; i++ {
		if out, err := runShWithHome(t, home, script); err != nil {
			t.Fatalf("run %d: %v\n%s", i+1, err, out)
		}
	}

	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatalf("authorized_keys was not written: %v", err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == testPubKey {
			count++
		}
	}
	if count != 1 {
		t.Errorf("authorized_keys holds the key %d times after running the script twice, want 1\ncontents:\n%s", count, data)
	}
}

// Each PrepareGuest failure names the phase that broke.
func TestPrepareGuestSurfacesReadableGuestFailures(t *testing.T) {
	tests := []struct {
		name string
		flag string
		want string
	}{
		{"the compile fails", "fail-input-install", "install the input helper"},
		{"the ssh key cannot be written", "fail-keyinstall", "install the ssh key"},
		{"the helper does not answer", "input-down", "verify the input helper"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, control := testsupport.FakeTart(t)
			testsupport.Flag(t, control, tt.flag)

			err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil)
			if err == nil {
				t.Fatal("PrepareGuest returned no error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestPrepareGuestRejectsMissingArguments(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	tests := []struct {
		name, tartBin, vmName, pubKey string
	}{
		{"no tart binary", "", "vm", testPubKey},
		{"no vm name", bin, "", testPubKey},
		{"no pub key", bin, "vm", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := PrepareGuest(context.Background(), tt.tartBin, tt.vmName, tt.pubKey, nil); err == nil {
				t.Error("PrepareGuest accepted a missing argument")
			}
		})
	}
}
