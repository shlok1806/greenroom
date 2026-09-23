package machine

import (
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #78: the guest ran in UTC, so the recording's menu bar clock disagreed with every
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
