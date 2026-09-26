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

// fakeXcode writes a host Xcode.app with just what HostXcode reads: xcodebuild and an
// Info.plist with the version and minimum macOS.
func fakeXcode(t *testing.T) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Xcode-27.0.app")
	bin := filepath.Join(app, "Contents", "Developer", "usr", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "xcodebuild"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleShortVersionString</key><string>27.0</string>
<key>LSMinimumSystemVersion</key><string>26.6</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	return app
}

// fakeSSH puts an ssh first on PATH that logs its arguments to <dir>/ssh-args and what it
// was sent to <dir>/ssh-stdin, as the guest's ditto -x would read it. With <dir>/fail-ssh it
// fails the way a full guest disk does.
func fakeSSH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/ssh-args"
cat > "` + dir + `/ssh-stdin"
[ -f "` + dir + `/fail-ssh" ] && { echo "ditto: /Applications/Xcode.app: No space left on device" >&2; exit 1; }
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestHostXcodeReadsTheAppItIsGiven(t *testing.T) {
	app := fakeXcode(t)
	h, err := HostXcode(app + "/")
	if err != nil {
		t.Fatalf("HostXcode: %v", err)
	}
	if h.Path != app || h.Version != "27.0" || h.MinimumOS != "26.6" {
		t.Errorf("HostXcode = %+v, want %s, 27.0, 26.6", h, app)
	}
}

// Every image carries Xcode (ADR 0026): a host without one fails by name, with the fix.
func TestHostXcodeFailsByNameWithoutAnXcode(t *testing.T) {
	notXcode := t.TempDir()
	if _, err := HostXcode(notXcode); err == nil || !strings.Contains(err.Error(), "is not an Xcode") {
		t.Errorf("HostXcode(%s) = %v, want it to say this is not an Xcode", notXcode, err)
	}

	// xcode-select pointing at the Command Line Tools, as on a host with no Xcode.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "xcode-select"), []byte("#!/bin/sh\necho /Library/Developer/CommandLineTools\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := HostXcode("")
	if err == nil {
		t.Fatal("HostXcode found an Xcode on a host whose xcode-select points at the Command Line Tools")
	}
	for _, want := range []string{"no Xcode selected", "CommandLineTools", "ADR 0026", "-xcode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

// InstallXcode waits for the grown disk, installs the key, streams the app over ssh into
// the guest's ditto with compression, and runs the setup script with the app and the
// minimum macOS, then syncs.
func TestInstallXcodeCopiesTheHostAppAndSetsItUp(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	ssh := fakeSSH(t)
	app := fakeXcode(t)
	err := InstallXcode(context.Background(), XcodeInstall{
		TartBin: bin, VM: "greenroom-base-test", App: app, SSHKey: "/keys/id_ed25519", PubKey: testPubKey,
	})
	if err != nil {
		t.Fatalf("InstallXcode: %v", err)
	}
	calls := testsupport.Calls(t, control)
	for _, want := range []string{"greenroom-grow-disk", "authorized_keys", "ip greenroom-base-test", "greenroom-xcode-setup"} {
		if !strings.Contains(calls, want) {
			t.Errorf("InstallXcode never ran %q\ncalls:\n%s", want, calls)
		}
	}
	if i, j := strings.Index(calls, "greenroom-grow-disk"), strings.Index(calls, "greenroom-xcode-setup"); i > j {
		t.Errorf("Xcode was set up before the disk was checked\ncalls:\n%s", calls)
	}
	if !strings.Contains(calls, "sh "+GuestXcodePath+" 26.6") {
		t.Errorf("the setup script was not given the guest's Xcode path and the minimum macOS\ncalls:\n%s", calls)
	}
	lines := strings.Split(strings.TrimSpace(calls), "\n")
	if last := lines[len(lines)-1]; !strings.HasSuffix(last, "sync") {
		t.Errorf("InstallXcode did not end with sync, so a stop right after can lose the copy; last call %q", last)
	}

	args, err := os.ReadFile(filepath.Join(ssh, "ssh-args"))
	if err != nil {
		t.Fatalf("ssh never ran: %v", err)
	}
	for _, want := range []string{"/keys/id_ed25519", "admin@192.168.64.9", "BatchMode=yes",
		"sudo -n rm -rf /Applications/Xcode.app && sudo -n ditto -x --hfsCompression - /Applications/Xcode.app"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("ssh arguments do not hold %q:\n%s", want, args)
		}
	}
	// What went over ssh is ditto's archive of the app: it unpacks to the same files.
	out := t.TempDir()
	unpack := exec.Command("ditto", "-x", filepath.Join(ssh, "ssh-stdin"), out)
	if b, err := unpack.CombinedOutput(); err != nil {
		t.Fatalf("the stream is not a ditto archive: %v: %s", err, b)
	}
	if _, err := os.Stat(filepath.Join(out, "Contents", "Developer", "usr", "bin", "xcodebuild")); err != nil {
		t.Errorf("the archive does not hold the app's contents: %v", err)
	}
}

// Each failure names its phase, and a host without Xcode stops before the VM is touched.
func TestInstallXcodeFailsByPhase(t *testing.T) {
	tests := []struct {
		name, flag, sshFlag, want string
		noApp                     bool
	}{
		{"the disk did not grow", "fail-disk", "", "grow greenroom-base-test's disk", false},
		{"the copy fails", "", "fail-ssh", "No space left on device", false},
		{"a read-back fails", "fail-xcode", "", "set up Xcode in greenroom-base-test: exit 1: xcode: failed: first-launch", false},
		{"the host has no Xcode", "", "", "is not an Xcode", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, control := testsupport.FakeTart(t)
			ssh := fakeSSH(t)
			if tt.flag != "" {
				testsupport.Flag(t, control, tt.flag)
			}
			if tt.sshFlag != "" {
				testsupport.Flag(t, ssh, tt.sshFlag)
			}
			app := fakeXcode(t)
			if tt.noApp {
				app = t.TempDir()
			}
			err := InstallXcode(context.Background(), XcodeInstall{
				TartBin: bin, VM: "greenroom-base-test", App: app, SSHKey: "/keys/k", PubKey: testPubKey,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("InstallXcode = %v, want an error containing %q", err, tt.want)
			}
			if tt.noApp && strings.TrimSpace(testsupport.Calls(t, control)) != "" {
				t.Errorf("InstallXcode touched the VM although the host has no Xcode:\n%s", testsupport.Calls(t, control))
			}
		})
	}
}

// ADR 0026 point 4: an image with Xcode never claims a toolchain it does not have.
func TestToolchainProblemsNamesEveryFailedXcodeProbe(t *testing.T) {
	good := map[string]any{"known": true, "xcode": true, "xcodeFirstLaunch": true, "xctest": true, "swiftTesting": true, "xcodebuild": true}
	if p := toolchainProblems(good); len(p) != 0 {
		t.Errorf("a passing manifest has problems: %v", p)
	}
	noXcode := map[string]any{"known": true, "xcode": false, "xctest": false, "swiftTesting": false}
	if p := toolchainProblems(noXcode); len(p) != 0 {
		t.Errorf("a manifest without Xcode is judged on Xcode's probes: %v", p)
	}
	bad := map[string]any{"known": true, "xcode": true, "xcodeFirstLaunch": true, "xctest": false,
		"xctestError": "no such module 'XCTest'", "swiftTesting": true}
	got := strings.Join(toolchainProblems(bad), "; ")
	for _, want := range []string{"the XCTest probe failed: no such module 'XCTest'", "the xcodebuild probe failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems %q do not name %q", got, want)
		}
	}
	if strings.Contains(got, "swift-testing") || strings.Contains(got, "first launch") {
		t.Errorf("problems %q name a probe that passed", got)
	}
}

func TestPrepareGuestFailsWhenXcodeIsThereButAProbeFails(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	writeControl(t, control, "toolchain-measured", `{"known":true,"imageRecipe":`+strconv.Itoa(imageRecipeVersion)+
		`,"xcode":true,"xcodePath":"/Applications/Xcode.app","xcodeFirstLaunch":true,"xctest":true,"swiftTesting":false,`+
		`"swiftTestingError":"no such module 'Testing'","xcodebuild":true}`)
	err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil)
	if err == nil {
		t.Fatal("PrepareGuest passed an image whose Xcode cannot run swift-testing")
	}
	for _, want := range []string{"/Applications/Xcode.app", "the swift-testing probe failed: no such module 'Testing'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

// The manifest carries this recipe's version (ADR 0026 point 6), which the script is given.
func TestPrepareGuestBakesTheRecipeVersion(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	if err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil); err != nil {
		t.Fatalf("PrepareGuest: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(control, "toolchain.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `"imageRecipe":` + strconv.Itoa(imageRecipeVersion); !strings.Contains(string(b), want) {
		t.Errorf("the manifest %s does not record %s", b, want)
	}

	// A manifest that does not read back with this recipe fails the build.
	bin, control = testsupport.FakeTart(t)
	writeControl(t, control, "toolchain-measured", `{"known":true,"xcode":false}`)
	err = PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil)
	if err == nil || !strings.Contains(err.Error(), "records image recipe <nil>, want "+strconv.Itoa(imageRecipeVersion)) {
		t.Errorf("PrepareGuest = %v, want it to refuse a manifest without the recipe", err)
	}
}

func TestStaleRecipeJudgesOnlyAKnownManifest(t *testing.T) {
	tests := []struct {
		name      string
		manifest  map[string]any
		wantFound int
		wantStale bool
	}{
		{"current", map[string]any{"known": true, "imageRecipe": float64(imageRecipeVersion)}, imageRecipeVersion, false},
		{"older", map[string]any{"known": true, "imageRecipe": float64(imageRecipeVersion - 1)}, imageRecipeVersion - 1, true},
		{"newer", map[string]any{"known": true, "imageRecipe": float64(imageRecipeVersion + 1)}, imageRecipeVersion + 1, true},
		{"before recipe versions", map[string]any{"known": true, "xcode": false}, 0, true},
		{"not a number", map[string]any{"known": true, "imageRecipe": "1"}, 0, true},
		{"no manifest", unknownToolchain(), 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, stale := staleRecipe(tt.manifest)
			if found != tt.wantFound || stale != tt.wantStale {
				t.Errorf("staleRecipe(%v) = %d, %v, want %d, %v", tt.manifest, found, stale, tt.wantFound, tt.wantStale)
			}
		})
	}
}

// An image built by another recipe boots, says so with the fix, and records it in the step.
func TestBootWarnsAboutAnImageFromAnotherRecipe(t *testing.T) {
	mgr, _, control := newTestManager(t)
	var logs logBuffer
	mgr.Log = logs.logger()
	writeControl(t, control, "toolchain.json", `{"known":true,"xcode":false,"xctest":false,"swiftTesting":true}`)
	mc := readyMachine(t, mgr)

	out := bootStep(t, mc.Dir)
	if out["imageRecipeStale"] != true || out["imageRecipeFound"] != float64(0) {
		t.Errorf("the boot step does not record the stale recipe: %v", out)
	}
	log := logs.String()
	for _, want := range []string{"another image recipe", "Xcode", "scripts/build-image.sh", "-force", testImage} {
		if !strings.Contains(log, want) {
			t.Errorf("the daemon log does not say %q:\n%s", want, log)
		}
	}
}

func TestBootIsQuietAboutACurrentRecipeAndAnImageWithoutAManifest(t *testing.T) {
	for name, manifest := range map[string]string{
		"current": `{"known":true,"imageRecipe":` + strconv.Itoa(imageRecipeVersion) + `}`,
		"none":    "",
	} {
		t.Run(name, func(t *testing.T) {
			mgr, _, control := newTestManager(t)
			if manifest != "" {
				writeControl(t, control, "toolchain.json", manifest)
			}
			mc := readyMachine(t, mgr)
			if out := bootStep(t, mc.Dir); out["imageRecipeStale"] != nil {
				t.Errorf("the boot step calls the recipe stale: %v", out)
			}
		})
	}
}
