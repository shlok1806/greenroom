package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// The dialog check (ADR 0018). A fresh machine should show the desktop and nothing else:
// no permission prompt, crash dialog, update nag or app nobody opened. The image build
// fails on anything else (CheckImage), and boot reports it in the machine's desktop field.
// The runtime surfaces, never sweeps: nothing here closes a window or quits an app.

// DesktopWindow is one on-screen window as CGWindowListCopyWindowInfo reports it.
// Layer is the window level: 0 for app windows, 8 and up for modal panels and alerts,
// 20 the Dock, 24 and 25 the menu bar and its status items, hugely negative for windows
// under the desktop icons (widgets).
type DesktopWindow struct {
	Owner  string  `json:"owner"`
	PID    int     `json:"pid"`
	Name   string  `json:"name,omitempty"`
	Layer  int     `json:"layer"`
	Alpha  float64 `json:"alpha"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// DesktopApp is one regular (Dock) application that is running.
type DesktopApp struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
	PID      int    `json:"pid"`
}

// Desktop is what the input helper's --desktop reads.
type Desktop struct {
	Windows []DesktopWindow `json:"windows"`
	Apps    []DesktopApp    `json:"apps"`
}

// DesktopReport is what a machine's screen showed when boot checked it. Clean means no
// unexpected window and no app but Finder. It is a snapshot, not a watch.
type DesktopReport struct {
	Clean             bool            `json:"clean"`
	UnexpectedWindows []DesktopWindow `json:"unexpectedWindows,omitempty"`
	UnexpectedApps    []DesktopApp    `json:"unexpectedApps,omitempty"`
	// Prompts are the unexpected windows that look like a system prompt waiting for an answer
	// (ADR 0047): at a modal panel or alert level, drawn by a process that is not a regular app.
	// Each is also in UnexpectedWindows. Text is filled only by a look during a command
	// (execprompt.go); boot's check leaves it empty.
	Prompts []DesktopPrompt `json:"prompts,omitempty"`
	Error   string          `json:"error,omitempty"` // the check could not run; nothing is known
}

// DesktopPrompt is a window that looks like a system prompt, with what it says when that could
// be read. The window's own name is empty for a TCC prompt; the text names the app that asked
// and the app it wants, e.g. "“tart-guest-agent” wants access to control “PromptCheck”. ...".
type DesktopPrompt struct {
	DesktopWindow
	Text string `json:"text,omitempty"`
}

// promptLayers are the window levels a prompt is drawn at: NSModalPanelWindowLevel (8), where
// macOS draws a TCC prompt (measured: UserNotificationCenter at layer 8, ADR 0044), up to below
// the Dock (20). A menu (101) or a floating panel (3) is not a prompt.
const (
	promptLayerMin = 8
	promptLayerMax = 19
)

// isPrompt reports whether w, an unexpected window, looks like a prompt: at a prompt's level and
// drawn by a process that is not one of apps (a regular app's own alert is that app's business,
// and only a system process draws a TCC, Gatekeeper or authorization prompt). Notification
// Center's banners are not prompts: they block nothing.
func isPrompt(w DesktopWindow, apps []DesktopApp) bool {
	if w.Layer < promptLayerMin || w.Layer > promptLayerMax || w.Owner == "Notification Center" {
		return false
	}
	for _, a := range apps {
		if a.PID == w.PID && w.PID != 0 {
			return false
		}
	}
	return true
}

// allowedOwners are the system processes that draw on every clean desktop, at any layer.
var allowedOwners = map[string]bool{
	"Window Server":      true, // the menu bar and its backdrop
	"Dock":               true, // the Dock, the wallpaper
	"Control Center":     true, // menu bar status items
	"SystemUIServer":     true, // menu bar extras
	"TextInputMenuAgent": true, // the input source menu
	"Spotlight":          true, // the menu bar icon
}

// desktopOwners draw on the desktop itself, under every window, and nowhere else is
// allowed: Notification Center's widgets there (the base image keeps them; lean hides
// them), but a Notification Center banner above the windows is a finding.
var desktopOwners = map[string]bool{
	"Notification Center": true,
	"Finder":              true,
	"WindowManager":       true,
}

// allowedApps are the regular apps a fresh machine may have running.
var allowedApps = map[string]bool{"com.apple.finder": true}

// Expected reports whether w belongs on a clean desktop.
func (w DesktopWindow) Expected() bool {
	if w.Alpha == 0 || w.Width <= 1 || w.Height <= 1 {
		return true // invisible: nothing a person or a screenshot sees
	}
	if allowedOwners[w.Owner] {
		return true
	}
	return desktopOwners[w.Owner] && w.Layer < 0
}

// Report compares d with the allowlist.
func (d Desktop) Report() DesktopReport {
	r := DesktopReport{}
	for _, w := range d.Windows {
		if !w.Expected() {
			r.UnexpectedWindows = append(r.UnexpectedWindows, w)
			if isPrompt(w, d.Apps) {
				r.Prompts = append(r.Prompts, DesktopPrompt{DesktopWindow: w})
			}
		}
	}
	for _, a := range d.Apps {
		if !allowedApps[a.BundleID] {
			r.UnexpectedApps = append(r.UnexpectedApps, a)
		}
	}
	r.Clean = len(r.UnexpectedWindows) == 0 && len(r.UnexpectedApps) == 0
	return r
}

// Findings is one line per unexpected window or app, for logs and build output.
func (r DesktopReport) Findings() []string {
	var out []string
	if r.Error != "" {
		out = append(out, "the desktop could not be read: "+r.Error)
	}
	for _, w := range r.UnexpectedWindows {
		out = append(out, fmt.Sprintf("window owned by %q named %q at layer %d, %.0fx%.0f at %.0f,%.0f",
			w.Owner, w.Name, w.Layer, w.Width, w.Height, w.X, w.Y))
	}
	for _, a := range r.UnexpectedApps {
		out = append(out, fmt.Sprintf("app %s (%s) is running", a.Name, a.BundleID))
	}
	for _, p := range r.Prompts {
		if p.Text != "" {
			out = append(out, fmt.Sprintf("the prompt from %q says %q", p.Owner, p.Text))
		}
	}
	return out
}

// readDesktop asks the installed input helper what is on the screen, under the look watchdog.
func readDesktop(ctx context.Context, c *tart.Client, vm string) (Desktop, error) {
	res, err := readHelper(ctx, c, vm, defaultLookTimes.captureLimit(), "the desktop read", "--desktop")
	if err != nil {
		return Desktop{}, fmt.Errorf("read the desktop: %w", err)
	}
	var d Desktop
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &d); err != nil {
		return Desktop{}, fmt.Errorf("read the desktop: %w: %s", err, strings.TrimSpace(res.Stdout))
	}
	return d, nil
}

// awaitLoginScript waits up to 30 s for the login session (Dock and Finder), then until
// Finder has run for 12 s: loginwindow relaunches its persistent apps and login items raise
// their prompts about a dozen seconds after login, which can be after the guest agent
// answers. A machine that logged in long ago does not wait. It only waits and reads.
const awaitLoginScript = `: greenroom-desktop-login
i=0
while ! { pgrep -x Dock >/dev/null && pgrep -x Finder >/dev/null; } && [ $i -lt 120 ]; do sleep 0.25; i=$((i+1)); done
pid="$(pgrep -x Finder | head -n 1)"
[ -n "$pid" ] || { echo "no login session after 30 s" >&2; exit 1; }
age="$(ps -o etime= -p "$pid" | awk -F'[-:]' '{s=0; for (i=1; i<=NF; i++) s=s*60+$i; print s}')"
[ "${age:-0}" -lt 12 ] && sleep $((12 - ${age:-0}))
exit 0
`

// awaitDesktopLogin runs awaitLoginScript in the guest.
func awaitDesktopLogin(ctx context.Context, c *tart.Client, vm string) error {
	if _, err := execChecked(ctx, c, vm, "/bin/sh", "-c", awaitLoginScript); err != nil {
		return fmt.Errorf("wait for the login session: %w", err)
	}
	return nil
}

// checkDesktop is boot's desktop phase: once the login has settled, one read, reported,
// never acted on. A login that does not settle is logged and the desktop is read anyway.
func (m *Manager) checkDesktop(ctx context.Context, mc *Machine) *DesktopReport {
	if err := awaitDesktopLogin(ctx, m.tart, mc.Name); err != nil {
		m.Log.Warn("reading this machine's desktop before its login settled", "runId", mc.RunID, "err", err)
	}
	d, err := readDesktop(ctx, m.tart, mc.Name)
	if err != nil {
		m.Log.Warn("cannot check this machine's desktop for dialogs", "runId", mc.RunID, "err", err)
		return &DesktopReport{Error: err.Error()}
	}
	r := d.Report()
	if !r.Clean {
		m.Log.Warn("this machine started with something on its screen besides the desktop; the image should be rebuilt "+
			"(scripts/build-image.sh fails on it)", "runId", mc.RunID, "image", mc.Image, "found", strings.Join(r.Findings(), "; "))
	}
	return &r
}
