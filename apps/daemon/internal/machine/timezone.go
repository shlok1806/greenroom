package machine

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// tzName is a tz database name ("America/Chicago", "UTC", "Etc/GMT+5"). Anything else is
// refused before it reaches a guest shell.
var tzName = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`)

// HostTimeZone is the host's tz database name, from the /etc/localtime link macOS keeps,
// or "" when it cannot be told.
func HostTimeZone() string {
	if tz := os.Getenv("TZ"); tzName.MatchString(tz) {
		return tz
	}
	link, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	return zoneFromLink(link)
}

// zoneFromLink is the zone a /etc/localtime link points at: the path after "zoneinfo/".
func zoneFromLink(link string) string {
	_, zone, ok := strings.Cut(link, "zoneinfo/")
	if !ok || !tzName.MatchString(zone) {
		return ""
	}
	return zone
}

// setGuestTimeZone puts the guest on zone, so the recording's menu bar clock reads the
// same times as everything the host prints (issue #78). The Cirrus image runs in UTC. The
// guest agent's user has passwordless sudo; the link is read back so a systemsetup that
// silently did nothing fails loudly.
func setGuestTimeZone(ctx context.Context, c *tart.Client, vmName, zone string) error {
	if !tzName.MatchString(zone) {
		return fmt.Errorf("set the time zone: %q is not a time zone name", zone)
	}
	// systemsetup can report an error early in a boot and still set the zone, or fail and
	// set nothing, so its word is not taken: the link decides, with one retry, then a plain
	// link as the fallback (seen on macOS 26: exit 1 at boot, zone set).
	script := fmt.Sprintf(`set -e
[ -e /usr/share/zoneinfo/%[1]s ]
ok() { case "$(readlink /etc/localtime)" in */zoneinfo/%[1]s) return 0 ;; esac; return 1; }
sudo -n systemsetup -settimezone %[1]s >/dev/null 2>&1 || true
ok || { sleep 2; sudo -n systemsetup -settimezone %[1]s >/dev/null 2>&1 || true; }
ok || sudo -n ln -sf /usr/share/zoneinfo/%[1]s /etc/localtime
ok || { echo "the guest's /etc/localtime is $(readlink /etc/localtime)" >&2; exit 1; }
`, zone)
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("set the time zone to %s: %w", zone, err)
	}
	return nil
}
