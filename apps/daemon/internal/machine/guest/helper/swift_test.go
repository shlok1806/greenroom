package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The helper is compiled only in the guest (Command Line Tools, ADR 0019), so a mistake in it
// used to show only when a VM booted. These tests compile it on the host instead: every source
// typechecks against the host's SDK, and the pure code in logic/ runs under tests/. Both skip
// on a host without swiftc. Each result is cached by the hash of what it compiles, in the temp
// directory, so the race soak's repeated runs pay the compile once.

func swiftc(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed on this host")
	}
	return p
}

// sources lists the .swift files in dirs, relative to this directory, sorted.
func sources(t *testing.T, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.swift"))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, matches...)
	}
	slices.Sort(out)
	if len(out) == 0 {
		t.Fatalf("no Swift sources in %v", dirs)
	}
	return out
}

// cacheKey hashes the files' names and contents with the command that uses them.
func cacheKey(t *testing.T, what string, files []string) string {
	t.Helper()
	h := sha256.New()
	h.Write([]byte(what))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func cacheDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "greenroom-helper-swift")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every source the guest compiles must typecheck. SourceHash.swift is the placeholder the
// install script replaces.
func TestTheHelperTypechecksOnTheHost(t *testing.T) {
	bin := swiftc(t)
	files := sources(t, ".", "logic")
	stamp := filepath.Join(cacheDir(t), "typecheck-"+cacheKey(t, "typecheck", files))
	if _, err := os.Stat(stamp); err == nil {
		return
	}
	cmd := exec.Command(bin, append([]string{"-typecheck", "-swift-version", "5"}, files...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the helper does not typecheck on this host: %v\n%s", err, out)
	}
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// logic/ with tests/ builds into one test binary, which prints each failure and exits non-zero
// if any test failed (tests/main.swift).
func TestTheHelperLogicPassesItsHostTests(t *testing.T) {
	bin := swiftc(t)
	files := sources(t, "logic", "tests")
	exe := filepath.Join(cacheDir(t), "logic-tests-"+cacheKey(t, "logic-tests", files))
	if _, err := os.Stat(exe); err != nil {
		tmp := exe + ".building"
		cmd := exec.Command(bin, append([]string{"-Onone", "-swift-version", "5", "-o", tmp}, files...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the helper's host tests do not compile: %v\n%s", err, out)
		}
		if err := os.Rename(tmp, exe); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("the helper's host tests failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "passed") {
		t.Errorf("the host tests printed no summary:\n%s", out)
	}
}
