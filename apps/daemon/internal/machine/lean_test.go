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

// leanGuest stubs the macOS commands leanScript drives (defaults, launchctl, sudo,
// mdutil, sw_vers and the rest) with a tiny preferences store and a launchd disabled
// list under a temp dir, so the real script runs in a real /bin/sh and its read-back
// is tested against what it wrote. Env knobs break one piece:
//
//	LEAN_DROP=<label>        launchd forgets that disable (print-disabled omits it)
//	LEAN_IGNORE=<domain> <key>  defaults drops writes to that key, as if macOS renamed it
type leanGuest struct {
	dir, store string
}

func newLeanGuest(t *testing.T) *leanGuest {
	t.Helper()
	g := &leanGuest{dir: t.TempDir()}
	g.store = filepath.Join(g.dir, "store")
	bin := filepath.Join(g.dir, "bin")
	for _, d := range []string{g.store, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stubs := map[string]string{
		// defaults [-currentHost] write|read <domain> <key> [-bool|-string|-int|-array|-array-add value]
		"defaults": `S="` + g.store + `"
[ "$1" = -currentHost ] && shift
op="$1"; domain="$(printf '%s' "$2" | tr '/' '_')"; key="$3"; f="$S/$domain.$(printf '%s' "$key" | tr ' ' '_')"
case "$op" in
  write)
    [ "$LEAN_IGNORE" = "$2 $3" ] && exit 0
    case "$4" in
      -bool) case "$5" in true|yes|1) echo 1 ;; *) echo 0 ;; esac > "$f" ;;
      -array) : > "$f" ;;
      -array-add) printf '%s\n' "$5" | sed -n 's|.*<string>\(.*\)</string>.*|        "_CFURLString" = "\1";|p' >> "$f" ;;
      *) printf '%s\n' "$5" > "$f" ;;
    esac ;;
  read)
    [ -f "$f" ] || { echo "The domain/default pair of ($2, $3) does not exist" >&2; exit 1; }
    cat "$f" ;;
esac`,
		// launchctl disable|bootout|print-disabled <target>
		"launchctl": `S="` + g.store + `"
case "$1" in
  disable) printf '%s\n' "${2#gui/*/}" >> "$S/disabled" ;;
  bootout) exit 3 ;;
  print-disabled)
    printf '\tdisabled services = {\n'
    [ -f "$S/disabled" ] && grep -vxF "${LEAN_DROP:-none}" "$S/disabled" | sed 's/.*/\t\t"&" => disabled/'
    printf '\t}\n' ;;
esac`,
		// sudo -n <command...>: run it, except the two that touch the real system.
		"sudo": `[ "$1" = -n ] && shift
case "$1" in
  touch) exit 0 ;;
  test) exit 0 ;;
esac
exec "$@"`,
		"mdutil":         `case "$1" in -s) printf '%s:\n\tIndexing disabled.\n' "$2" ;; esac`,
		"sw_vers":        `case "$1" in -productVersion) echo 26.6.2 ;; -buildVersion) echo 25G83 ;; esac`,
		"killall":        `exit 0`,
		"softwareupdate": `exit 0`,
		"tmutil":         `exit 0`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func (g *leanGuest) run(t *testing.T, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", leanScript)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(g.dir, "bin")+":/usr/bin:/bin")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *leanGuest) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.store, name))
	if err != nil {
		t.Fatalf("the script never wrote %s: %v", name, err)
	}
	return string(b)
}

func TestLeanScriptKeepsOnlyTheCoreAppsAndDisablesTheRest(t *testing.T) {
	g := newLeanGuest(t)
	out, err := g.run(t)
	if err != nil || !strings.Contains(out, "lean: ok") {
		t.Fatalf("lean script failed: %v\n%s", err, out)
	}

	dock := g.read(t, "com.apple.dock.persistent-apps")
	if n := strings.Count(dock, "_CFURLString"); n != 7 {
		t.Errorf("the Dock has %d apps, want the 7 core apps:\n%s", n, dock)
	}
	for _, app := range []string{"Terminal", "System%20Settings", "Safari", "TextEdit", "Preview", "Activity%20Monitor", "Console"} {
		if !strings.Contains(dock, "/"+app+".app/") {
			t.Errorf("the Dock lacks %s:\n%s", app, dock)
		}
	}
	for _, gone := range []string{"Music", "Photos", "Messages", "Apps.app", "App%20Store"} {
		if strings.Contains(dock, gone) {
			t.Errorf("the Dock still has %s:\n%s", gone, dock)
		}
	}
	// Safari's tile points into its cryptex, not through the /Applications link, which
	// would give the tile an alias arrow.
	if safari, _ := filepath.EvalSymlinks("/Applications/Safari.app"); safari != "/Applications/Safari.app" && safari != "" &&
		!strings.Contains(dock, strings.ReplaceAll(safari, " ", "%20")) {
		t.Errorf("Safari's tile does not use its real path %s:\n%s", safari, dock)
	}

	disabled := g.read(t, "disabled")
	// One agent per app or feature the profile names; the script lists more.
	for _, label := range []string{
		"com.apple.AMPLibraryAgent", "com.apple.itunescloudd", "com.apple.bookdatastored", "com.apple.newsd",
		"com.apple.weatherd", "com.apple.mobiletimerd", "com.apple.calaccessd", "com.apple.remindd",
		"com.apple.Maps.mapssyncd", "com.apple.photoanalysisd", "com.apple.mediaanalysisd", "com.apple.imagent",
		"com.apple.email.maild", "com.apple.homed", "com.apple.gamed", "com.apple.tipsd", "com.apple.voicememod",
		"com.apple.Siri.agent", "com.apple.assistantd", "com.apple.notificationcenterui.agent", "com.apple.chronod",
		"com.apple.followupd", "com.apple.SoftwareUpdateNotificationManager",
	} {
		if !strings.Contains(disabled, label+"\n") {
			t.Errorf("%s is not disabled", label)
		}
	}
	// Nothing greenroom depends on: capture, TCC, the window server's helpers, input.
	for _, keep := range []string{"com.apple.replayd", "com.apple.tccd", "com.apple.Dock.agent", "com.apple.Finder", "com.apple.WindowManager"} {
		if strings.Contains(disabled, keep+"\n") {
			t.Errorf("%s is disabled, and greenroom needs it", keep)
		}
	}

	for name, want := range map[string]string{
		"com.apple.WindowManager.StandardHideWidgets":                                    "1",
		"com.apple.assistant.support.Assistant_Enabled":                                  "0",
		"com.apple.gamed.Disabled":                                                       "1",
		"_Library_Preferences_com.apple.SoftwareUpdate.AutomaticDownload":                "0",
		"_Library_Preferences_com.apple.commerce.AutoUpdate":                             "0",
		"_Library_Preferences_com.apple.TimeMachine.DoNotOfferNewDisksForBackup":         "1",
		"com.apple.SetupAssistant.DidSeeCloudSetup":                                      "1",
		"com.apple.SetupAssistant.LastSeenBuddyBuildVersion":                             "25G83",
		"com.apple.dock.show-recents":                                                    "0",
		"_Library_Preferences_com.apple.SoftwareUpdate.AutomaticallyInstallMacOSUpdates": "0",
	} {
		if got := strings.TrimSpace(g.read(t, name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// launchd can refuse or forget a disable; the build must fail, naming the label.
func TestLeanScriptFailsWhenADisableDidNotStick(t *testing.T) {
	g := newLeanGuest(t)
	out, err := g.run(t, "LEAN_DROP=com.apple.photoanalysisd")
	if err == nil {
		t.Fatalf("the script passed although launchd lost a disable:\n%s", out)
	}
	if !strings.Contains(out, "com.apple.photoanalysisd") {
		t.Errorf("the failure does not name the label launchd lost:\n%s", out)
	}
}

// A key macOS renamed takes the write and does nothing; only the read-back sees it.
func TestLeanScriptFailsWhenAKeyDoesNotReadBack(t *testing.T) {
	g := newLeanGuest(t)
	out, err := g.run(t, "LEAN_IGNORE=com.apple.gamed Disabled")
	if err == nil {
		t.Fatalf("the script passed although Game Center's key never took:\n%s", out)
	}
	if !strings.Contains(out, "check failed: gamecenter") {
		t.Errorf("the failure does not name the check:\n%s", out)
	}
	if strings.Contains(out, "lean: ok") {
		t.Errorf("a failed read-back still printed lean: ok:\n%s", out)
	}
}

// SIP, the authenticated root and the sealed system volume are out of bounds.
func TestLeanScriptNeverTouchesTheSealedSystem(t *testing.T) {
	for _, banned := range []string{"csrutil", "bless", "authenticated-root", "mount -uw", "/System/Library/LaunchAgents/", "rm -rf /System"} {
		if strings.Contains(leanScript, banned) {
			t.Errorf("leanScript contains %q", banned)
		}
	}
}

func TestApplyLeanProfileRunsTheScriptAndSyncs(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	if err := ApplyLeanProfile(context.Background(), bin, "greenroom-lean-test", nil); err != nil {
		t.Fatalf("ApplyLeanProfile: %v", err)
	}
	log := testsupport.Calls(t, control)
	if !strings.Contains(log, "StandardHideWidgets") {
		t.Errorf("ApplyLeanProfile never ran the lean script\ncalls:\n%s", log)
	}
	if !strings.Contains(log, "exec greenroom-lean-test /bin/sh -c sync") {
		t.Errorf("ApplyLeanProfile never synced, so a stop right after can lose the profile\ncalls:\n%s", log)
	}
}

func TestApplyLeanProfileFailsTheBuildOnAFailedCheck(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	testsupport.Flag(t, control, "fail-lean")
	err := ApplyLeanProfile(context.Background(), bin, "greenroom-lean-test", nil)
	if err == nil || !strings.Contains(err.Error(), "gamecenter") {
		t.Fatalf("a failed read-back did not fail the build with the check's name: %v", err)
	}
}
