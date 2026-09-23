package machine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #77: the guest ran in UTC, so the recording's menu bar clock disagreed with every
// time the Companion printed. Boot sets the guest's zone to the host's and times it.
func TestBootSetsTheGuestTimeZoneToTheHosts(t *testing.T) {
	mgr, _, control := newTestManager(t, WithHostTimeZone(func() string { return "America/Chicago" }))
	mc := readyMachine(t, mgr)
	if log := testsupport.Calls(t, control); !strings.Contains(log, "settimezone America/Chicago") {
		t.Fatalf("boot never set the guest time zone\ncalls:\n%s", log)
	}
	if out := bootStep(t, mc.Dir); out["timeZoneSeconds"] == nil || out["timeZone"] != "America/Chicago" {
		t.Errorf("the boot step does not record the time zone: %v", out)
	}
}

// Never fatal, like the other boot preferences: a machine on UTC still works.
func TestBootSurvivesAFailedTimeZone(t *testing.T) {
	mgr, _, control := newTestManager(t, WithHostTimeZone(func() string { return "America/Chicago" }))
	testsupport.Flag(t, control, "fail-timezone")
	mc := readyMachine(t, mgr)
	if msg, _ := bootStep(t, mc.Dir)["timeZoneError"].(string); !strings.Contains(msg, "time zone") {
		t.Errorf("the boot step does not say the time zone failed: %v", bootStep(t, mc.Dir))
	}
}

// A zone name that is not a plain tz database name is never put into a shell command.
func TestAHostZoneThatIsNotATzNameIsSkipped(t *testing.T) {
	mgr, _, control := newTestManager(t, WithHostTimeZone(func() string { return "America/Chicago; rm -rf /" }))
	readyMachine(t, mgr)
	if log := testsupport.Calls(t, control); strings.Contains(log, "settimezone") {
		t.Fatalf("an unsafe zone reached the guest\ncalls:\n%s", log)
	}
}

func TestZoneFromLocaltimeLink(t *testing.T) {
	for link, want := range map[string]string{
		"/var/db/timezone/zoneinfo/America/Chicago": "America/Chicago",
		"/usr/share/zoneinfo/Europe/Berlin":         "Europe/Berlin",
		"/usr/share/zoneinfo/UTC":                   "UTC",
		"/etc/somewhere":                            "",
	} {
		if got := zoneFromLink(link); got != want {
			t.Errorf("zoneFromLink(%q) = %q, want %q", link, got, want)
		}
	}
}

// tzGuest stubs the commands timeZoneScript drives (sudo, systemsetup, ln, readlink,
// sleep) around a fake /etc/localtime link kept in a file, and a temp zoneinfo directory
// holding America/Chicago, so the real script runs in a real /bin/sh. TZ_SYSTEMSETUP picks
// how systemsetup behaves: "fail-but-set" exits 1 after setting the link, as macOS 26 does
// at boot, and "noop" changes nothing. TZ_LN=noop makes ln change nothing too.
type tzGuest struct {
	dir, zoneinfo, link string
}

func newTZGuest(t *testing.T) *tzGuest {
	t.Helper()
	g := &tzGuest{dir: t.TempDir()}
	g.zoneinfo = filepath.Join(g.dir, "zoneinfo")
	g.link = filepath.Join(g.dir, "localtime")
	bin := filepath.Join(g.dir, "bin")
	for _, d := range []string{filepath.Join(g.zoneinfo, "America"), bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(g.zoneinfo, "America", "Chicago"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.link, []byte("/var/db/timezone/zoneinfo/UTC"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		"sudo": `[ "$1" = -n ] && shift
exec "$@"`,
		// systemsetup -settimezone <zone>
		"systemsetup": `case "$TZ_SYSTEMSETUP" in
  fail-but-set) printf '%s' "/var/db/timezone/zoneinfo/$2" > "` + g.link + `"; exit 1 ;;
  noop) exit 0 ;;
esac`,
		// ln -sf <target> /etc/localtime
		"ln": `[ "$TZ_LN" = noop ] && exit 0
printf '%s' "$2" > "` + g.link + `"`,
		"readlink": `cat "` + g.link + `"`,
		"sleep":    `exit 0`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func (g *tzGuest) run(t *testing.T, zone string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", timeZoneScript(zone, g.zoneinfo))
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(g.dir, "bin")+":/usr/bin:/bin")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *tzGuest) localtime(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(g.link)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// macOS 26's systemsetup exits 1 at boot yet sets the zone: the link decides.
func TestTimeZoneScriptTrustsTheLinkOverSystemsetupsExitStatus(t *testing.T) {
	g := newTZGuest(t)
	if out, err := g.run(t, "America/Chicago", "TZ_SYSTEMSETUP=fail-but-set"); err != nil {
		t.Fatalf("the script failed though the zone was set: %v\n%s", err, out)
	}
	if got := g.localtime(t); got != "/var/db/timezone/zoneinfo/America/Chicago" {
		t.Errorf("/etc/localtime = %q, want systemsetup's link left alone", got)
	}
}

func TestTimeZoneScriptFallsBackToALinkWhenSystemsetupDoesNothing(t *testing.T) {
	g := newTZGuest(t)
	if out, err := g.run(t, "America/Chicago", "TZ_SYSTEMSETUP=noop"); err != nil {
		t.Fatalf("the ln fallback did not rescue the zone: %v\n%s", err, out)
	}
	if got, want := g.localtime(t), filepath.Join(g.zoneinfo, "America/Chicago"); got != want {
		t.Errorf("/etc/localtime = %q, want %q", got, want)
	}
}

func TestTimeZoneScriptFailsNamingTheLinkWhenNothingSetsIt(t *testing.T) {
	g := newTZGuest(t)
	out, err := g.run(t, "America/Chicago", "TZ_SYSTEMSETUP=noop", "TZ_LN=noop")
	if err == nil {
		t.Fatalf("the script succeeded with the zone unset\n%s", out)
	}
	if !strings.Contains(out, "/etc/localtime is /var/db/timezone/zoneinfo/UTC") {
		t.Errorf("the failure does not name the guest's link: %q", out)
	}
}

func TestTimeZoneScriptSaysWhenTheGuestHasNoSuchZone(t *testing.T) {
	g := newTZGuest(t)
	out, err := g.run(t, "UTC0", "TZ_SYSTEMSETUP=fail-but-set")
	if err == nil {
		t.Fatalf("the script succeeded for a zone the guest lacks\n%s", out)
	}
	if !strings.Contains(out, "no zoneinfo for UTC0 in the guest") {
		t.Errorf("the failure does not say the zone is missing: %q", out)
	}
	if got := g.localtime(t); got != "/var/db/timezone/zoneinfo/UTC" {
		t.Errorf("/etc/localtime changed to %q for a missing zone", got)
	}
}
