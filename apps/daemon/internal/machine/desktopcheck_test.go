package machine

import (
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// loginwindow relaunches its apps a dozen seconds after login, which can be after the guest
// agent answers, so a desktop read before the login settles misses them.
func TestBootReadsTheDesktopOnlyAfterTheLoginSettles(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)

	log := testsupport.Calls(t, control)
	login, read := strings.Index(log, "greenroom-desktop-login"), strings.Index(log, "--desktop")
	if login < 0 || read < 0 || login > read {
		t.Fatalf("boot read the desktop without first waiting for the login (login at %d, read at %d)\ncalls:\n%s", login, read, log)
	}
	if mc.Desktop == nil || !mc.Desktop.Clean {
		t.Errorf("a clean desktop was reported as %+v", mc.Desktop)
	}
}

func TestBootStillReportsTheDesktopWhenTheLoginNeverSettles(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-desktop-login")
	mc := readyMachine(t, mgr)

	if mc.Status != Ready {
		t.Fatalf("a login that never settled failed the boot: %s %s", mc.Status, mc.Error)
	}
	if mc.Desktop == nil || mc.Desktop.Error != "" || !mc.Desktop.Clean {
		t.Errorf("the desktop was not read after the login wait failed: %+v", mc.Desktop)
	}
}
