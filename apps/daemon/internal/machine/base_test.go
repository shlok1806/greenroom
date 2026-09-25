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

// baseGuest runs the real base.sh in /bin/sh against stubs: a home directory under a temp
// dir, TCC databases that are real SQLite files with the access table's columns, the real
// plutil on the relaunch list, and stubs for defaults, launchctl, sudo, pkill and the rest.
// Knobs:
//
//	BASE_RELAUNCH=<bundle id>  loginwindow writes this app back into its list during the
//	                           settle wait, as it does for an app still running
//	BASE_TCC_READONLY=1        the TCC writes do nothing (SIP on, or a moved database)
//	BASE_LAUNCHD_FORGET=1      launchd forgets the DiagnosticsReporter disable
type baseGuest struct {
	dir, home, sys, user string
}

func newBaseGuest(t *testing.T) *baseGuest {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	g := &baseGuest{dir: t.TempDir()}
	g.home = filepath.Join(g.dir, "home")
	g.sys = filepath.Join(g.dir, "system-TCC.db")
	g.user = filepath.Join(g.dir, "containers", "com.apple.TCC", "TCC.db") // where tccd has it open
	bin := filepath.Join(g.dir, "bin")
	cellar := filepath.Join(g.dir, "Cellar", "tart-guest-agent", "0.99.0", "bin")
	for _, d := range []string{bin, cellar, filepath.Dir(g.user), filepath.Join(g.home, "Library", "Saved Application State", "com.apple.Terminal.savedState"),
		filepath.Join(g.home, "Library", "Group Containers", "group.com.apple.loginwindow.persistent-apps")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	schema := `CREATE TABLE access (service TEXT NOT NULL, client TEXT NOT NULL, client_type INTEGER NOT NULL,
  auth_value INTEGER NOT NULL, auth_reason INTEGER NOT NULL, auth_version INTEGER NOT NULL, csreq BLOB,
  policy_id INTEGER, indirect_object_identifier_type INTEGER, indirect_object_identifier TEXT NOT NULL DEFAULT 'UNUSED',
  indirect_object_code_identity BLOB, flags INTEGER, last_modified INTEGER NOT NULL DEFAULT 0, pid INTEGER,
  pid_version INTEGER, boot_uuid TEXT NOT NULL DEFAULT 'UNUSED', last_reminded INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (service, client, client_type, indirect_object_identifier));`
	for _, db := range []string{g.sys, g.user} {
		if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
			t.Fatalf("create %s: %v: %s", db, err, out)
		}
	}
	// The Cirrus base's list: Finder and Terminal.
	list := filepath.Join(g.home, "Library", "Group Containers", "group.com.apple.loginwindow.persistent-apps", "persistantApps")
	if out, err := exec.Command("plutil", "-create", "xml1", list).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if out, err := exec.Command("plutil", "-insert", "PersistentApps", "-json",
		`[{"BackgroundState":2,"BundleID":"com.apple.finder","Hide":false,"Path":"/System/Library/CoreServices/Finder.app"},`+
			`{"BackgroundState":3,"BundleID":"com.apple.terminal","Hide":false,"Path":"/System/Applications/Utilities/Terminal.app"}]`, list).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	agent := filepath.Join(cellar, "tart-guest-agent")
	stubs := map[string]string{
		// sudo -n <cmd>: remaps the system paths the script touches into the temp dir.
		"sudo": `[ "$1" = -n ] && shift
a=""; for x in "$@"; do
  case "$x" in
    "/Library/Application Support/com.apple.TCC/TCC.db") x="` + g.sys + `" ;;
    /Library/Logs/*|/private/var/db/*) x="` + g.dir + `/sysdirs$x" ;;
  esac
  a="$a$(printf '%s' "$x" | sed "s/'/'\\\\''/g; s/^/'/; s/$/'/") "
done
eval "set -- $a"; exec "$@"`,
		"sqlite3": `case "$2" in *INSERT*) [ -n "${BASE_TCC_READONLY:-}" ] && exit 0 ;; esac; exec /usr/bin/sqlite3 "$@"`,
		"lsof":    `printf 'p123\nn` + g.user + `\n'`,
		"killall": `exit 0`,
		"automationmodetool": `[ -f "` + g.dir + `/automation" ] && { echo "This device DOES NOT REQUIRE user authentication to enable Automation Mode."; exit 0; }
echo "This device REQUIRES user authentication to enable Automation Mode."`,
		"expect": `: > "` + g.dir + `/automation"`,
		// defaults write|read <domain> <key> [-bool|-string v]
		"defaults": `f="` + g.dir + `/defaults.$(printf '%s' "$2" | tr '/' '_').$3"
case "$1" in
  write) case "$4" in -bool) case "$5" in true|yes|1) echo 1 ;; *) echo 0 ;; esac > "$f" ;; *) printf '%s\n' "$5" > "$f" ;; esac ;;
  read) cat "$f" 2>/dev/null || exit 1 ;;
esac`,
		"launchctl": `f="` + g.dir + `/disabled"
case "$1" in
  disable) [ -n "${BASE_LAUNCHD_FORGET:-}" ] || echo "${2##*/}" >> "$f" ;;
  bootout) exit 3 ;;
  print-disabled) printf '\tdisabled services = {\n'; [ -f "$f" ] && sed 's/.*/\t\t"&" => disabled/' "$f"; printf '\t}\n' ;;
esac`,
		"pkill": `echo "pkill $*" >> "` + g.dir + `/killed"`,
		"pgrep": `exit 1`,
		"find":  `[ -d "$1" ] || exit 1; exec /usr/bin/find "$@"`,
		"sleep": `[ -n "${BASE_RELAUNCH:-}" ] || exit 0
exec plutil -insert PersistentApps.1 -json "{\"BundleID\":\"$BASE_RELAUNCH\",\"Path\":\"/x.app\"}" "` + list + `"`,
		"tart-guest-agent": `exit 0`,
	}
	for name, body := range stubs {
		path := filepath.Join(bin, name)
		if name == "tart-guest-agent" {
			path = agent
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// On PATH through a link, as Homebrew installs it; the script must key rows by the real path.
	if err := os.Symlink(agent, filepath.Join(bin, "tart-guest-agent")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"/Library/Logs/DiagnosticReports", "/private/var/db/PanicReporter", "/Library/Logs/DiagnosticReports/Retired"} {
		p := filepath.Join(g.dir, "sysdirs", d)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "crash.ips"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func (g *baseGuest) run(t *testing.T, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", baseScript)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(g.dir, "bin")+":/usr/bin:/bin", "HOME="+g.home)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *baseGuest) sql(t *testing.T, db, q string) string {
	t.Helper()
	out, err := exec.Command("sqlite3", db, q).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestBaseScriptGrantsAppleEventsAndLeavesOnlyFinderToRelaunch(t *testing.T) {
	g := newBaseGuest(t)
	out, err := g.run(t)
	if err != nil || !strings.Contains(out, "base: ok") {
		t.Fatalf("base script failed: %v\n%s", err, out)
	}
	agent, _ := filepath.EvalSymlinks(filepath.Join(g.dir, "Cellar", "tart-guest-agent", "0.99.0", "bin", "tart-guest-agent"))
	keygen, _ := filepath.EvalSymlinks("/usr/libexec/sshd-keygen-wrapper")
	if keygen == "" {
		keygen = "/usr/libexec/sshd-keygen-wrapper"
	}
	for _, db := range []string{g.sys, g.user} {
		for _, sender := range []string{agent, keygen} {
			for _, target := range []string{"com.apple.Safari", "com.apple.systemevents", "com.apple.finder", "com.apple.Terminal",
				"com.apple.systempreferences", "com.apple.TextEdit", "com.apple.Preview", "com.apple.ActivityMonitor", "com.apple.Console"} {
				if got := g.sql(t, db, "SELECT auth_value FROM access WHERE service='kTCCServiceAppleEvents' AND client='"+sender+"' AND indirect_object_identifier='"+target+"'"); got != "2" {
					t.Errorf("%s: no allowed Apple Events row from %s to %s (got %q)", filepath.Base(db), sender, target, got)
				}
			}
		}
	}
	if n := g.sql(t, g.user, "SELECT count(*) FROM access WHERE client LIKE '%0.14.1%'"); n != "0" {
		t.Errorf("rows keyed to a pinned tart-guest-agent version: %s", n)
	}
	list := filepath.Join(g.home, "Library", "Group Containers", "group.com.apple.loginwindow.persistent-apps", "persistantApps")
	if b, _ := exec.Command("plutil", "-extract", "PersistentApps", "json", "-o", "-", list).Output(); strings.Contains(string(b), "terminal") || !strings.Contains(string(b), "com.apple.finder") {
		t.Errorf("the relaunch list is not Finder alone: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(g.dir, "killed")); !strings.Contains(string(b), "Terminal.app/Contents/MacOS/") {
		t.Errorf("Terminal, in the old list, was not stopped before the list was cut, so loginwindow would write it back: %q", b)
	}
	if left, _ := os.ReadDir(filepath.Join(g.home, "Library", "Saved Application State")); len(left) != 0 {
		t.Errorf("saved application state was left: %v", left)
	}
	if b, _ := os.ReadFile(filepath.Join(g.dir, "defaults.com.apple.Safari.AllowJavaScriptFromAppleEvents")); strings.TrimSpace(string(b)) != "1" {
		t.Error("Safari's AllowJavaScriptFromAppleEvents is not on")
	}
	if b, _ := os.ReadFile(filepath.Join(g.dir, "defaults.com.apple.CrashReporter.DialogType")); strings.TrimSpace(string(b)) != "none" {
		t.Error("CrashReporter DialogType is not none")
	}
	if b, _ := os.ReadFile(filepath.Join(g.dir, "disabled")); !strings.Contains(string(b), "com.apple.DiagnosticsReporter") {
		t.Error("DiagnosticsReporter was not disabled")
	}
	if _, err := os.Stat(filepath.Join(g.dir, "sysdirs", "Library", "Logs", "DiagnosticReports", "Retired", "crash.ips")); !os.IsNotExist(err) {
		t.Error("a queued crash report was left to show at the next login")
	}
	if _, err := os.Stat(filepath.Join(g.dir, "automation")); err != nil {
		t.Error("automation mode without authentication was not enabled")
	}
}

// Every fix is read back, and a failed read-back names its check and fails the build.
func TestBaseScriptFailsTheBuildByName(t *testing.T) {
	for env, check := range map[string]string{
		"BASE_RELAUNCH=com.apple.ical": "relaunch-list-finder-only",
		"BASE_TCC_READONLY=1":          "appleevents-system-tart-guest-agent-com.apple.Safari",
		"BASE_LAUNCHD_FORGET=1":        "diagnostics-reporter",
	} {
		t.Run(check, func(t *testing.T) {
			g := newBaseGuest(t)
			out, err := g.run(t, env)
			if err == nil || !strings.Contains(out, "base: check failed: "+check+"\n") {
				t.Fatalf("%s did not fail check %s: %v\n%s", env, check, err, out)
			}
		})
	}
}

func TestSoftwareUpdateOffDisablesBothJobsAndFailsIfLaunchdForgets(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		"sudo": `[ "$1" = -n ] && shift; exec "$@"`,
		"launchctl": `f="` + dir + `/disabled"
case "$1" in
  disable) [ "${2#system/}" = "${FORGET:-}" ] || echo "${2#system/}" >> "$f" ;;
  bootout) exit 0 ;;
  print-disabled) [ -f "$f" ] && sed 's/.*/\t\t"&" => disabled/' "$f" ;;
esac`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := func(env ...string) (string, error) {
		_ = os.Remove(filepath.Join(dir, "disabled"))
		cmd := exec.Command("/bin/sh", "-c", softwareUpdateScript())
		cmd.Env = append(append(os.Environ(), "PATH="+bin+":/usr/bin:/bin"), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(); err != nil || !strings.Contains(out, "softwareupdate: off") {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "disabled"))
	for _, l := range []string{"com.apple.softwareupdated", "com.apple.mobile.softwareupdated"} {
		if !strings.Contains(string(b), l) {
			t.Errorf("%s was not disabled; it restarts softwareupdated after a reboot", l)
		}
	}
	if out, err := run("FORGET=com.apple.mobile.softwareupdated"); err == nil || !strings.Contains(out, "com.apple.mobile.softwareupdated") {
		t.Errorf("a disable launchd did not keep passed: %v\n%s", err, out)
	}
}

func TestDisableSoftwareUpdateSyncsAndNamesAFailure(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	if err := DisableSoftwareUpdate(context.Background(), bin, "vm", nil); err != nil {
		t.Fatal(err)
	}
	if calls := testsupport.Calls(t, control); !strings.Contains(calls, "greenroom-softwareupdate-off") || !strings.HasSuffix(strings.TrimSpace(calls), "exec vm /bin/sh -c sync") {
		t.Errorf("Software Update was not disabled and then synced:\n%s", calls)
	}
	testsupport.Flag(t, control, "fail-softwareupdate")
	if err := DisableSoftwareUpdate(context.Background(), bin, "vm", nil); err == nil || !strings.Contains(err.Error(), "com.apple.mobile.softwareupdated") {
		t.Errorf("a failed disable did not fail the build by name: %v", err)
	}
}
