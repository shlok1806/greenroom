package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// scripts/install.sh keeps how the daemon was installed (root ADR 0033): an update runs it with
// none of the variables a person set by hand, so it reads them back from the launchd job it
// replaces. These tests run the real install.sh with HOME in a temp dir (so the plist it reads
// and writes is a temp one, never ~/Library/LaunchAgents) and fakes for everything that would
// touch the host: launchctl, lsof, curl, go and tart. installRun refuses to start unless the
// fakes are the ones the script will find.

const (
	fakeLaunchctl = "#!/bin/sh\necho \"launchctl $*\" >>\"$HOME/calls\"\n[ \"$1\" = print ] && exit 1\nexit 0\n"
	fakeLsof      = "#!/bin/sh\nexit 0\n"
	fakeCurl      = "#!/bin/sh\necho 'ok 0 machines'\n"
	fakeTart      = "#!/bin/sh\nexit 0\n"
	// go build ... -o <file> .: the file appears, nothing is compiled.
	fakeGo = "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -o ] && { shift; echo fake >\"$1\"; }; shift; done\n"
)

type installFixture struct {
	t    *testing.T
	home string
	bin  string
}

func newInstallFixture(t *testing.T) *installFixture {
	t.Helper()
	f := &installFixture{t: t, home: t.TempDir(), bin: t.TempDir()}
	for name, body := range map[string]string{"launchctl": fakeLaunchctl, "lsof": fakeLsof, "curl": fakeCurl, "go": fakeGo, "tart": fakeTart} {
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *installFixture) plist() string {
	return filepath.Join(f.home, "Library", "LaunchAgents", "com.greenroom.daemon.plist")
}

// env is the host's environment without any GREENROOM_ variable, HOME and PATH replaced.
func (f *installFixture) env(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GREENROOM_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, append([]string{"HOME=" + f.home, "PATH=" + f.bin + ":/usr/bin:/bin:/usr/sbin:/sbin"}, extra...)...)
}

func (f *installFixture) run(extra ...string) string {
	f.t.Helper()
	env := f.env(extra...)
	// The guard: every host-touching command must resolve to its fake.
	for _, name := range []string{"launchctl", "lsof", "curl", "go"} {
		cmd := exec.Command("/bin/sh", "-c", "command -v "+name)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) != filepath.Join(f.bin, name) {
			f.t.Fatalf("%s resolves to %q, not the fake: refusing to run install.sh", name, out)
		}
	}
	cmd := exec.Command(filepath.Join("scripts", "install.sh"))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if !strings.HasPrefix(f.plist(), f.home) {
		f.t.Fatal("plist outside the temp home")
	}
	return string(out)
}

// read returns a plist entry, or "" when it is missing.
func (f *installFixture) read(entry string) string {
	f.t.Helper()
	out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :"+entry, f.plist()).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// arg is the value after flag in ProgramArguments.
func (f *installFixture) arg(flag string) string {
	f.t.Helper()
	for i := 0; ; i++ {
		v := f.read("ProgramArguments:" + strconv.Itoa(i))
		if v == "" {
			return ""
		}
		if v == flag {
			return f.read("ProgramArguments:" + strconv.Itoa(i+1))
		}
	}
}

func (f *installFixture) buddy(command string) {
	f.t.Helper()
	if out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", command, f.plist()).CombinedOutput(); err != nil {
		f.t.Fatalf("PlistBuddy %s: %v\n%s", command, err, out)
	}
}

func (f *installFixture) writePlist(body string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.plist()), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.plist(), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func TestInstallKeepsTheSettingsItWasInstalledWith(t *testing.T) {
	f := newInstallFixture(t)
	tart := filepath.Join(f.bin, "tart")
	f.run("GREENROOM_VERIFIER=manual", "GREENROOM_IMAGE=my-image", "GREENROOM_TART="+tart, "GREENROOM_ENV=/custom/.env")
	if f.arg("-verifier") != "manual" || f.arg("-image") != "my-image" || f.arg("-env-file") != "/custom/.env" {
		t.Fatalf("first install: verifier %q image %q env %q", f.arg("-verifier"), f.arg("-image"), f.arg("-env-file"))
	}
	if f.read("EnvironmentVariables:GREENROOM_TART") != tart || f.read("EnvironmentVariables:GREENROOM_IMAGE") != "my-image" {
		t.Fatal("the chosen settings are not recorded in the job's environment")
	}
	// A variable added to the job by hand is kept too.
	f.buddy("Add :EnvironmentVariables:NVIDIA_BASE_URL string https://example.test/v1?a=1&b=<2>")

	// An update: nothing set.
	out := f.run()
	if f.arg("-verifier") != "manual" || f.arg("-image") != "my-image" || f.arg("-env-file") != "/custom/.env" ||
		f.read("EnvironmentVariables:GREENROOM_TART") != tart {
		t.Fatalf("update lost a setting: verifier %q image %q env %q tart %q\n%s", f.arg("-verifier"), f.arg("-image"),
			f.arg("-env-file"), f.read("EnvironmentVariables:GREENROOM_TART"), out)
	}
	if got := f.read("EnvironmentVariables:NVIDIA_BASE_URL"); got != "https://example.test/v1?a=1&b=<2>" {
		t.Fatalf("hand-added variable %q", got)
	}
	if !strings.Contains(out, "verifier: manual (previous install)") || !strings.Contains(out, "keeping NVIDIA_BASE_URL") {
		t.Fatalf("output does not say where the settings came from:\n%s", out)
	}
	if f.read("EnvironmentVariables:GREENROOM_CHECKOUT") == "" || f.read("EnvironmentVariables:PATH") == "" {
		t.Fatal("install.sh's own variables are missing")
	}

	// The environment still wins.
	f.run("GREENROOM_VERIFIER=nim", "GREENROOM_IMAGE=other-image")
	if f.arg("-verifier") != "nim" || f.arg("-image") != "other-image" || f.arg("-env-file") != "/custom/.env" {
		t.Fatalf("explicit environment: verifier %q image %q env %q", f.arg("-verifier"), f.arg("-image"), f.arg("-env-file"))
	}
}

// A job written before install.sh recorded its choices: the verifier carries over, an image or
// env file install.sh would pick by itself stays its own pick, and one it would not is kept.
func TestInstallReadsAJobFromBeforeItRecordedChoices(t *testing.T) {
	legacy := func(image, envFile string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.greenroom.daemon</string>
  <key>ProgramArguments</key><array>
    <string>/x/greenroom</string><string>serve</string><string>-env-file</string><string>` + envFile + `</string>
    <string>-verifier</string><string>manual</string><string>-image</string><string>` + image + `</string>
  </array>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin</string></dict>
</dict></plist>
`
	}

	f := newInstallFixture(t)
	f.writePlist(legacy("greenroom-base", "/elsewhere/.env"))
	f.run()
	if f.arg("-verifier") != "manual" {
		t.Fatalf("verifier %q", f.arg("-verifier"))
	}
	// The fake tart lists no local image, so install.sh's own pick is upstream.
	if f.arg("-image") != "ghcr.io/cirruslabs/macos-tahoe-base:latest" || f.read("EnvironmentVariables:GREENROOM_IMAGE") != "" {
		t.Fatalf("an image install.sh picked by itself was frozen: %q", f.arg("-image"))
	}
	if f.arg("-env-file") != "/elsewhere/.env" || f.read("EnvironmentVariables:GREENROOM_ENV") != "/elsewhere/.env" {
		t.Fatalf("a chosen env file was lost: %q", f.arg("-env-file"))
	}

	f = newInstallFixture(t)
	f.writePlist(legacy("my-image", "/elsewhere/.env"))
	f.run()
	if f.arg("-image") != "my-image" || f.read("EnvironmentVariables:GREENROOM_IMAGE") != "my-image" {
		t.Fatalf("a chosen image was lost: %q", f.arg("-image"))
	}
}

func TestInstallWithAnUnreadableJobUsesTheDefaults(t *testing.T) {
	f := newInstallFixture(t)
	f.writePlist("not a plist")
	out := f.run()
	if f.arg("-verifier") != "nim" || !strings.Contains(out, "verifier: nim (default)") {
		t.Fatalf("verifier %q\n%s", f.arg("-verifier"), out)
	}
}
