package machine

import (
	"context"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Before ready, so no click of an agent's can reach the wallpaper first.
func TestBootTurnsOffClickWallpaperToShowDesktop(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)

	log := testsupport.Calls(t, control)
	if !strings.Contains(log, "EnableStandardClickToShowDesktop -bool false") {
		t.Fatalf("boot never turned off click wallpaper to show desktop\ncalls:\n%s", log)
	}
	for _, key := range []string{"NSQuitAlwaysKeepsWindows -bool false", "TALLogoutSavesState -bool false"} {
		if !strings.Contains(log, key) {
			t.Errorf("boot never set %s, so window restore stays on", key)
		}
	}
	// A sleeping guest display turns every frame black with no error.
	for _, key := range []string{"pmset -a displaysleep 0 sleep 0", "com.apple.screensaver idleTime -int 0", "screenLock off"} {
		if !strings.Contains(log, key) {
			t.Errorf("boot never ran %q, so an idle guest can blank or lock its screen", key)
		}
	}
	for _, s := range readSteps(t, mc.Dir) {
		if s.Tool == "machine_boot" {
			if out, _ := s.Output.(map[string]any); out["desktopPrefsSeconds"] == nil {
				t.Errorf("the boot step does not time the desktop phase: %v", s.Output)
			}
		}
	}
}

func TestBootSurvivesFailedDesktopPrefs(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-desktop-prefs")
	mc := readyMachine(t, mgr)
	for _, s := range readSteps(t, mc.Dir) {
		if s.Tool == "machine_boot" {
			out, _ := s.Output.(map[string]any)
			if msg, _ := out["desktopPrefsError"].(string); !strings.Contains(msg, "desktop preferences") {
				t.Errorf("the boot step does not say the preferences failed: %v", s.Output)
			}
		}
	}
}

func TestPrepareGuestBakesTheDesktopPrefs(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	if err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil); err != nil {
		t.Fatalf("PrepareGuest: %v", err)
	}
	if !strings.Contains(testsupport.Calls(t, control), "EnableStandardClickToShowDesktop") {
		t.Error("PrepareGuest never set the desktop preferences, so the image restores windows at login")
	}
	testsupport.Flag(t, control, "fail-desktop-prefs")
	if err := PrepareGuest(context.Background(), bin, "greenroom-base-test", testPubKey, nil); err == nil {
		t.Error("an image build that cannot set the preferences succeeded")
	}
}
