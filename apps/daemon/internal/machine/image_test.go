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

// runShWithHome runs script against a real /bin/sh -c, with HOME pointed at
// home rather than the test process's own home directory. installHelperScript
// and appendAuthorizedKeyScript are idempotency contracts about the shell
// text itself, not about anything Go decides, so the only faithful way to
// test that contract is to hand the text to a real shell and look at what it
// left in a throwaway home, the same way PrepareGuest and installInputHelper
// hand it to the guest's shell.
func runShWithHome(t *testing.T, home, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestPrepareGuestInstallsTheHelper proves PrepareGuest's call shape: it must
// run the compile script (installHelperScript, which always mentions
// swiftc), naming the exact path the current inputHelperVersion installs at,
// so a base image and a machine's own first control request can never
// install two different things.
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

// TestPrepareGuestSyncsBeforeReturning pins a bug found by running
// scripts/build-image.sh end to end against a real VM: a `tart stop` issued
// right after PrepareGuest returns does not by itself flush the guest's
// dirty filesystem pages, so a helper that was only ever verified while the
// VM stayed running was gone again on the very next boot -- greenroom-base
// still paid the swiftc compile it was built to avoid. PrepareGuest now runs
// `sync` in the guest as its last step so what verifyHelper just proved
// works is actually still there once the VM is stopped and cloned.
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

// TestInstallHelperScriptSkipsTheCompileWhenTheBinaryAlreadyAnswers pins the
// script's own short-circuit: `if [ -x "$bin" ] && "$bin" --version`. A
// FakeTart run cannot observe this, because it matches on the script's TEXT
// and installHelperScript's text always contains "swiftc" whether or not the
// guest's own shell ever reaches the compile line -- there is no real guest
// to actually skip anything in. So this runs the exact script text PrepareGuest
// and installInputHelper both send, against a real local shell and a
// pre-seeded fake binary, and checks that the source directory the compile
// step would have created never appears.
func TestInstallHelperScriptSkipsTheCompileWhenTheBinaryAlreadyAnswers(t *testing.T) {
	home := t.TempDir()
	binPath := filepath.Join(home, helperName())
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stand-in for the already-baked helper: it only has to answer
	// --version with exit 0, the same as the real binary does.
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

// TestInstallHelperScriptCompilesWhenNoBinaryIsThere is the other half of the
// short-circuit: with no pre-seeded binary, the script must actually reach
// the compile line rather than silently doing nothing. It does not require a
// real swiftc: reaching "command not found" (exit 127) is proof enough that
// the script walked past the short-circuit and tried.
func TestInstallHelperScriptCompilesWhenNoBinaryIsThere(t *testing.T) {
	home := t.TempDir()
	// A PATH built from symlinks to only the plain utilities the script
	// needs (mkdir, dirname, printf, base64, rm), with no swiftc on it, so
	// the script's own "command -v swiftc" check is what ends it,
	// deterministically, rather than however long a real compile takes on
	// whatever toolchain this machine happens to have. This is never run
	// against FakeTart precisely so it can prove the real script reaches
	// this line without a real guest to ask.
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

// toolFarm builds a directory of symlinks to the named real executables,
// found via the test process's own PATH, and returns it as a PATH value. It
// lets a test hand a shell a PATH that has exactly the ordinary utilities a
// script needs and deliberately nothing else, rather than either the test
// machine's whole PATH (which may have swiftc on it) or an empty one (which
// breaks the script before it reaches the line the test cares about).
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

// TestAppendAuthorizedKeyScriptDoesNotDuplicateTheKey pins the same kind of
// idempotency, for the ssh key install: a clone of the base image already
// has the key PrepareGuest put there, and a machine's own boot
// (installSSHKey) runs the same shape of script again. It must never grow
// authorized_keys by one line per boot.
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

// TestPrepareGuestSurfacesReadableGuestFailures pins the three ways a real
// guest can fail PrepareGuest, and that each becomes a message naming what
// broke rather than a bare exit code: a build script or a person reading its
// output has to be able to tell "no Swift toolchain" from "cannot write the
// ssh key" from "the helper does not work" without reading Go source.
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

// TestPrepareGuestRejectsMissingArguments pins the argument checks that let a
// caller with a typo fail before ever touching a VM.
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
