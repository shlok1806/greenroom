package machine

import (
	"context"
	"fmt"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// desktopPrefsScript sets the guest's desktop up for an agent that clicks
// by coordinates:
//
//   - "Click wallpaper to reveal desktop" (Sonoma and later, on by default)
//     hides every window when a click lands on the wallpaper, so one missed
//     click makes the app under test vanish, and the next click on the same
//     spot brings it back. A verifier cannot tell that from a crash. Off. It
//     takes effect at once, without restarting WindowManager (verified on
//     26.6.2).
//   - Saved-state restore reopens whatever windows were open when the image
//     was shut down, such as a Terminal window over the app under test. Off
//     for every app and for the next login. It cannot undo this boot's
//     restore, which happened at login; baked by prepare-image, it prevents it.
//   - The display never sleeps, the screensaver never starts, and the screen
//     never locks. A guest display that sleeps turns every frame and
//     screenshot black with no error (measured: displaysleep 1, 60 s idle,
//     screencapture exits 0 with a black image). The Cirrus base ships these
//     off; this keeps a different base from turning them on silently. The
//     lock is changed only if it is on, since sysadminctl wants the admin
//     password (admin in the base image).
//
// Every key is read back, so a key macOS renamed fails loudly rather than
// silently doing nothing. Keep images/scripts/greenroom-tcc.sh in step.
const desktopPrefsScript = `set -e
defaults write com.apple.WindowManager EnableStandardClickToShowDesktop -bool false
defaults write NSGlobalDomain NSQuitAlwaysKeepsWindows -bool false
defaults write com.apple.loginwindow TALLogoutSavesState -bool false
sudo -n pmset -a displaysleep 0 sleep 0
defaults -currentHost write com.apple.screensaver idleTime -int 0
sysadminctl -screenLock status 2>&1 | grep -q "screenLock is off" || sysadminctl -screenLock off -password admin
[ "$(defaults read com.apple.WindowManager EnableStandardClickToShowDesktop)" = 0 ]
[ "$(defaults read NSGlobalDomain NSQuitAlwaysKeepsWindows)" = 0 ]
[ "$(defaults read com.apple.loginwindow TALLogoutSavesState)" = 0 ]
[ "$(pmset -g | awk '$1=="displaysleep"{print $2}')" = 0 ]
[ "$(pmset -g | awk '$1=="sleep"{print $2}')" = 0 ]
[ "$(defaults -currentHost read com.apple.screensaver idleTime)" = 0 ]
sysadminctl -screenLock status 2>&1 | grep -q "screenLock is off"
`

// applyDesktopPrefs writes desktopPrefsScript's settings in the guest. Boot
// runs it before ready; PrepareGuest bakes it into an image.
func applyDesktopPrefs(ctx context.Context, c *tart.Client, vmName string) error {
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", desktopPrefsScript); err != nil {
		return fmt.Errorf("set the desktop preferences (click wallpaper to show desktop, window restore, display sleep, screensaver, screen lock): %w", err)
	}
	return nil
}
