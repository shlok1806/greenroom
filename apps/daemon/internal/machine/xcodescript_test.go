package machine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// xcodeGuest runs the real xcode.sh in /bin/sh against stubs: a fake Xcode.app (its own
// xcodebuild, a real Info.plist read by the real plutil), and stubs for sudo, xcode-select,
// xcodebuild, DevToolsSecurity, dseditgroup, defaults, codesign, spctl, xattr, lsregister,
// sfltool and sleep. The sfltool stub plays Background Task Management: it lists nothing of
// Xcode until lsregister has registered it, then Xcode and its two extensions "not notified"
// for BTM_POSTS_AFTER dumps, then notified, as BTM does when it posts its alert. An unrelated
// item that is never notified (the Cirrus base's tart-guest-agent daemon) is always listed.
// Knobs:
//
//	BTM_POSTS_AFTER=<n>  dumps before BTM posts (default 3)
//	BTM_NEVER=1          BTM registers Xcode but never posts
//	BTM_IGNORES=1        BTM never lists anything of Xcode (an Xcode with no extensions)
type xcodeGuest struct {
	dir, app string
}

func newXcodeGuest(t *testing.T) *xcodeGuest {
	t.Helper()
	g := &xcodeGuest{dir: t.TempDir()}
	g.app = filepath.Join(g.dir, "Applications", "Xcode.app")
	bin := filepath.Join(g.dir, "bin")
	devBin := filepath.Join(g.app, "Contents", "Developer", "usr", "bin")
	for _, d := range []string{bin, devBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.apple.dt.Xcode</string>
<key>CFBundleShortVersionString</key><string>27.0</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(g.app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(g.dir, "calls")
	xcodebuild := `echo "xcodebuild $*" >> "` + log + `"
case "$1" in -version) printf 'Xcode 27.0\nBuild version 27A266a\n' ;; esac
exit 0`
	if err := os.WriteFile(filepath.Join(devBin, "xcodebuild"), []byte("#!/bin/sh\n"+xcodebuild+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	item := func(n int, name, disposition, extra string) string {
		return ` #` + string(rune('0'+n)) + `:
                 UUID: 00000000-0000-0000-0000-00000000000` + string(rune('0'+n)) + `
                 Name: ` + name + `
          Disposition: ` + disposition + `
` + extra + `
`
	}
	unrelated := item(1, "tart-guest-agent", "[enabled, allowed, not notified] (0x3)",
		`                  URL: file:///Library/LaunchDaemons/org.cirruslabs.tart-guest-daemon.plist`)
	xcodeItems := func(d string) string {
		return item(2, "Xcode", d, `           Identifier: 2.com.apple.dt.Xcode
                  URL: file://`+g.app+`/
    Bundle Identifier: com.apple.dt.Xcode`) +
			item(3, "ProvisoningProfileQuicklookExtension.appex", d, `                  URL: Contents/PlugIns/ProvisoningProfileQuicklookExtension.appex
    Parent Identifier: 2.com.apple.dt.Xcode`) +
			item(4, "uuid.mdimporter", d, `                  URL: Contents/Library/Spotlight/uuid.mdimporter
    Parent Identifier: 2.com.apple.dt.Xcode`)
	}
	writeFile := func(name, body string) {
		if err := os.WriteFile(filepath.Join(g.dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("btm-unrelated", unrelated)
	writeFile("btm-waiting", xcodeItems("[enabled, allowed, not notified] (0x3)"))
	writeFile("btm-notified", xcodeItems("[enabled, allowed, notified] (0xb)"))
	stubs := map[string]string{
		"sudo": `[ "$1" = -n ] && shift; exec "$@"`,
		"xcode-select": `case "$1" in
  -s) echo "$2" > "` + g.dir + `/selected" ;;
  -p) cat "` + g.dir + `/selected" ;;
esac`,
		"xcodebuild":       `exec "$(cat "` + g.dir + `/selected")/usr/bin/xcodebuild" "$@"`,
		"DevToolsSecurity": `case "$1" in -status) echo "Developer mode is currently enabled." ;; esac`,
		"dseditgroup":      `exit 0`,
		"defaults":         `echo 27.0`,
		"codesign":         `exit 0`,
		"spctl":            `exit 0`,
		"xattr":            `exit 1`,
		"sw_vers":          `echo 26.6.2`,
		"sleep":            `exit 0`,
		"lsregister":       `echo "lsregister $*" >> "` + log + `"; : > "` + g.dir + `/registered"`,
		"sfltool": `[ "$1" = dumpbtm ] || exit 1
echo "========================"
echo " Records for UID 501 : 4C4C4413-5555-3144-A1F2-8E1F7D1F2D1B"
echo "========================"
echo
echo " Items:"
echo
cat "` + g.dir + `/btm-unrelated"
[ -f "` + g.dir + `/registered" ] && [ -z "${BTM_IGNORES:-}" ] || exit 0
n=$(cat "` + g.dir + `/dumps" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "` + g.dir + `/dumps"
if [ -z "${BTM_NEVER:-}" ] && [ $n -gt "${BTM_POSTS_AFTER:-3}" ]; then cat "` + g.dir + `/btm-notified"; else cat "` + g.dir + `/btm-waiting"; fi`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func (g *xcodeGuest) run(t *testing.T, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", xcodeScript, "sh", g.app, "26.6")
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(g.dir, "bin")+":/usr/bin:/bin")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *xcodeGuest) dumps(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(g.dir, "dumps"))
	return strings.TrimSpace(string(b))
}

// Installing Xcode makes Background Task Management post "Multiple Extensions Added" as an
// alert that every clone showed at every login. xcode.sh registers Xcode itself and waits
// until BTM has posted, so the alert is on screen for base.sh to close, never after it.
func TestXcodeScriptWaitsForLoginItemsToPostAboutXcode(t *testing.T) {
	g := newXcodeGuest(t)
	out, err := g.run(t)
	if err != nil || !strings.Contains(out, "xcode: ok") {
		t.Fatalf("xcode script failed: %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(filepath.Join(g.dir, "calls"))
	if !strings.Contains(string(calls), "lsregister -f "+g.app+"\n") {
		t.Errorf("Xcode was not registered with LaunchServices, so BTM may post after base.sh:\n%s", calls)
	}
	if i, j := strings.Index(string(calls), "lsregister"), strings.Index(string(calls), "xcodebuild -runFirstLaunch"); i < 0 || i > j {
		t.Errorf("Xcode was registered after its first launch, not before, so the wait does not overlap setup:\n%s", calls)
	}
	// 3 dumps while BTM coalesces, the one that saw it post, and the read-back's.
	if got := g.dumps(t); got != "5" {
		t.Errorf("BTM was read %s times, want 5: the wait did not last until BTM posted", got)
	}
}

func TestXcodeScriptFailsWhenLoginItemsNeverPosts(t *testing.T) {
	g := newXcodeGuest(t)
	out, err := g.run(t, "BTM_NEVER=1")
	if err == nil || !strings.Contains(out, "xcode: check failed: btm-notified\n") {
		t.Fatalf("a BTM that never posted passed: %v\n%s", err, out)
	}
	if got := g.dumps(t); got != "91" {
		t.Errorf("BTM was read %s times, want 91: the wait is bounded at 90 s plus the read-back", got)
	}
}

// An Xcode BTM lists nothing of (no extensions) has no alert to wait for.
func TestXcodeScriptPassesWhenLoginItemsTracksNothingOfXcode(t *testing.T) {
	g := newXcodeGuest(t)
	out, err := g.run(t, "BTM_IGNORES=1")
	if err != nil || !strings.Contains(out, "xcode: ok") {
		t.Fatalf("an Xcode BTM does not track failed the build: %v\n%s", err, out)
	}
}
