package machine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// ImageCheck configures CheckImage. Zero durations take the defaults, which are what a
// real guest needs; tests shorten them.
type ImageCheck struct {
	TartBin string
	Image   string // the image to check; never booted or changed itself
	OutDir  string // screenshots land here
	Log     *slog.Logger

	Settle  time.Duration // after login, for loginwindow's relaunches and login items (default 20 s)
	Linger  time.Duration // after the exercise, for a dialog it raised (default 5 s)
	Timeout time.Duration // each boot and each reboot (default 3 min); an exercised call has 30 s
	Poll    time.Duration // between readiness probes (default 1 s)
}

// ImageCheckPass is one look at a machine's screen: after the first boot, and again after
// an in-guest reboot.
type ImageCheckPass struct {
	Label      string             `json:"label"`
	Desktop    DesktopReport      `json:"desktop"`
	Findings   []string           `json:"findings,omitempty"` // every reason this pass failed, desktop included
	Screenshot string             `json:"screenshot,omitempty"`
	Seconds    map[string]float64 `json:"seconds"`
}

// ImageCheckResult is the verdict. Passed is false if any pass has a finding.
type ImageCheckResult struct {
	Image  string           `json:"image"`
	Clone  string           `json:"clone"`
	Passed bool             `json:"passed"`
	Passes []ImageCheckPass `json:"passes"`
}

// CheckImage is the dialog gate (ADR 0016): a clone of a clone of the image, booted
// headless, used the way clients use a machine (a screencapture through tart exec, an
// event posted by the input helper, Apple Events to System Events and to Safari, including
// `do JavaScript`), then 5 s later its on-screen windows and running apps are compared with
// the allowlist in desktopcheck.go, and softwareupdated must be disabled and not running.
// Then it reboots from inside the guest and does it all again. A screenshot of each pass is
// saved either way. It never writes the screen-capture approvals or desktop preferences
// that a daemon's boot writes: the image alone must pass. An error means the check could
// not run; a failed check is a result with Passed false.
func CheckImage(ctx context.Context, o ImageCheck) (ImageCheckResult, error) {
	if strings.TrimSpace(o.TartBin) == "" || strings.TrimSpace(o.Image) == "" || strings.TrimSpace(o.OutDir) == "" {
		return ImageCheckResult{}, errors.New("TartBin, Image and OutDir are required")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	o.Settle = orDefault(o.Settle, 20*time.Second)
	o.Linger = orDefault(o.Linger, 5*time.Second)
	o.Timeout = orDefault(o.Timeout, 3*time.Minute)
	o.Poll = orDefault(o.Poll, time.Second)
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return ImageCheckResult{}, err
	}
	c := &tart.Client{Bin: o.TartBin}
	var tag [4]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return ImageCheckResult{}, err
	}
	first := o.Image + "-check-" + hex.EncodeToString(tag[:])
	vm := first + "-clone"
	res := ImageCheckResult{Image: o.Image, Clone: vm}

	// A clone of a clone: what a derived image's machines are, and what catches state that
	// only survives one clone.
	o.Log.Info("cloning", "image", o.Image, "clone", first, "cloneOfClone", vm)
	if err := c.Clone(ctx, o.Image, first); err != nil {
		return res, err
	}
	defer deleteVM(c, first, o.Log) // only its clone boots; both go once the VM has stopped
	if err := c.Clone(ctx, first, vm); err != nil {
		return res, err
	}
	defer deleteVM(c, vm, o.Log)

	proc, err := c.Start(vm, filepath.Join(o.OutDir, "tart-run.log"), false)
	if err != nil {
		return res, err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		_ = c.Stop(stopCtx, vm)
		proc.Wait()
	}()

	for _, label := range []string{"first-boot", "after-reboot"} {
		if label == "after-reboot" {
			if err := rebootGuest(ctx, c, vm, o); err != nil {
				return res, err
			}
		}
		if err := awaitLogin(ctx, c, vm, proc, o); err != nil {
			return res, fmt.Errorf("%s: %w", label, err)
		}
		pass, err := checkPass(ctx, c, vm, label, o)
		if err != nil {
			return res, fmt.Errorf("%s: %w", label, err)
		}
		res.Passes = append(res.Passes, pass)
	}
	res.Passed = true
	for _, p := range res.Passes {
		if len(p.Findings) > 0 {
			res.Passed = false
		}
	}
	return res, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func deleteVM(c *tart.Client, name string, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := c.Delete(ctx, name); err != nil {
		log.Warn("cannot delete the check's clone; delete it by hand", "vm", name, "err", err)
	}
}

// awaitLogin waits for the guest agent, then for Finder (the login finished), then Settle
// more for what loginwindow and login items start.
func awaitLogin(ctx context.Context, c *tart.Client, vm string, proc *tart.Process, o ImageCheck) error {
	deadline := time.Now().Add(o.Timeout)
	for {
		if proc.Exited() {
			return proc.Err()
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := c.Exec(probe, vm, "/bin/sh", "-c", ": greenroom-check-login\npgrep -x Finder >/dev/null")
		cancel()
		if err == nil && res.ExitCode == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not log in within %s", vm, o.Timeout)
		}
		if err := sleepCtx(ctx, o.Poll); err != nil {
			return err
		}
	}
	return sleepCtx(ctx, o.Settle)
}

// rebootGuest reboots from inside the guest, as `sudo reboot` in machine_exec would, and
// waits until the guest reports a new boot time.
func rebootGuest(ctx context.Context, c *tart.Client, vm string, o ImageCheck) error {
	bootTime := func() string {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		res, err := c.Exec(probe, vm, "/bin/sh", "-c", ": greenroom-check-boottime\nsysctl -n kern.boottime")
		if err != nil || res.ExitCode != 0 {
			return ""
		}
		return strings.TrimSpace(res.Stdout)
	}
	before := bootTime()
	if before == "" {
		return errors.New("cannot read the guest's boot time before rebooting it")
	}
	o.Log.Info("rebooting from inside the guest", "vm", vm)
	if _, err := execChecked(ctx, c, vm, "/bin/sh", "-c", ": greenroom-check-reboot\n(sleep 1; sudo -n /sbin/reboot) >/dev/null 2>&1 &"); err != nil {
		return fmt.Errorf("reboot %s: %w", vm, err)
	}
	deadline := time.Now().Add(o.Timeout)
	for {
		if now := bootTime(); now != "" && now != before {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not come back from its reboot within %s", vm, o.Timeout)
		}
		if err := sleepCtx(ctx, o.Poll); err != nil {
			return err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// exercises are the calls real clients make, as guest scripts. Each runs under
// exerciseWatchdog, so a call blocked on a prompt fails instead of hanging, and its prompt
// is still on screen for the window check.
var exercises = []struct{ name, script string }{
	{"appleevent-system-events", `osascript -e 'tell application "System Events" to get name of first process'`},
	// The Safari repro of issue #25, plus do JavaScript (Safari's "Allow JavaScript from
	// Apple Events"). Safari is the check's own app, so it quits it when done.
	{"appleevent-safari", `mkdir -p /tmp/greenroom-check && echo "<p>greenroom</p>" > /tmp/greenroom-check/index.html &&
open -a Safari file:///tmp/greenroom-check/index.html && sleep 4 &&
osascript -e 'tell application "Safari" to get bounds of front window' &&
osascript -e 'tell application "Safari" to do JavaScript "1+1" in document 1'`},
	{"quit-safari", `osascript -e 'tell application "Safari" to quit'; n=0; while pgrep -x Safari >/dev/null && [ $n -lt 100 ]; do sleep 0.1; n=$((n+1)); done; ! pgrep -x Safari >/dev/null`},
}

// exerciseWatchdog runs $1 in its own process group and kills the whole group after 30 s
// (exit 124), as machine_exec's wrapper does: tart exec returns only when every holder of
// the guest's pipes has exited, so killing the shell alone left a blocked osascript holding
// the call open. The shell's job notices go to /dev/null and the call's stderr through fd 3,
// as in execWrapper. macOS has no timeout(1).
const exerciseWatchdog = `exec 3>&2 2>/dev/null
set -m
/bin/sh -c "$1" 2>&3 3>&- &
p=$!
(sleep 30; : > /tmp/greenroom-check-timedout; kill -KILL -"$p") >/dev/null 2>&1 </dev/null 3>&- &
w=$!
wait "$p"; s=$?
kill -KILL "$w" 2>/dev/null
[ -f /tmp/greenroom-check-timedout ] && { rm -f /tmp/greenroom-check-timedout; echo "timed out after 30 s" >&3; s=124; }
exit $s`

// checkPass exercises the machine, waits Linger, and looks.
func checkPass(ctx context.Context, c *tart.Client, vm, label string, o ImageCheck) (ImageCheckPass, error) {
	p := ImageCheckPass{Label: label, Seconds: map[string]float64{}}
	timed := func(name string, fn func() error) error {
		at := time.Now()
		err := fn()
		p.Seconds[name] = round1(time.Since(at))
		return err
	}

	// The helper the check reads the desktop with. An image built by this daemon has it;
	// an older one compiles it here (not an image change: the clone is thrown away).
	if err := timed("inputHelper", func() error {
		_, err := execChecked(ctx, c, vm, "/bin/sh", "-c", installHelperScript())
		return err
	}); err != nil {
		return p, fmt.Errorf("install the input helper: %w", err)
	}

	// A screencapture through tart exec, as machine_screenshot and every frame take one.
	if err := timed("screencapture", func() error {
		data, err := guestScreenshot(ctx, c, vm)
		if err != nil {
			return err
		}
		if uniform(data) {
			p.Findings = append(p.Findings, "the screenshot is one flat colour (a missing screen-capture grant, or a sleeping display)")
		}
		return nil
	}); err != nil {
		p.Findings = append(p.Findings, "screencapture failed: "+err.Error())
	}

	// A posted event through the input helper, as machine_input does.
	if err := timed("input", func() error {
		_, err := runHelper(ctx, c, vm, "--json-base64", base64.StdEncoding.EncodeToString([]byte(`{"actions":[{"type":"move","x":500,"y":400}]}`)))
		return err
	}); err != nil {
		p.Findings = append(p.Findings, "the input helper could not post an event: "+err.Error())
	}

	for _, ex := range exercises {
		call, cancel := context.WithTimeout(ctx, 60*time.Second)
		var res tart.ExecResult
		err := timed(ex.name, func() (err error) {
			res, err = c.Exec(call, vm, "/bin/sh", "-c", ": greenroom-check-"+ex.name+"\n"+exerciseWatchdog, "sh", ex.script)
			return err
		})
		cancel()
		switch {
		case err != nil:
			p.Findings = append(p.Findings, fmt.Sprintf("%s: %v", ex.name, err))
		case res.ExitCode != 0:
			p.Findings = append(p.Findings, fmt.Sprintf("%s: exit %d after %.1f s: %s", ex.name, res.ExitCode, p.Seconds[ex.name], strings.TrimSpace(res.Stderr+" "+res.Stdout)))
		}
	}

	if err := sleepCtx(ctx, o.Linger); err != nil {
		return p, err
	}

	d, err := readDesktop(ctx, c, vm)
	if err != nil {
		return p, err
	}
	p.Desktop = d.Report()
	p.Findings = append(p.Findings, p.Desktop.Findings()...)

	// Software Update stays off across reboots and clones.
	res, err := c.Exec(ctx, vm, "/bin/sh", "-c", ": greenroom-check-softwareupdate\n"+softwareUpdateCheckScript())
	if err != nil {
		return p, err
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line != "" {
			p.Findings = append(p.Findings, line)
		}
	}

	// The artifact: what the screen looked like when the check looked.
	if data, err := guestScreenshot(ctx, c, vm); err == nil {
		p.Screenshot = filepath.Join(o.OutDir, label+".png")
		if err := os.WriteFile(p.Screenshot, data, 0o644); err != nil {
			return p, err
		}
	} else {
		p.Findings = append(p.Findings, "the final screenshot failed: "+err.Error())
	}
	return p, nil
}

// softwareUpdateCheckScript prints one line per way Software Update is not off.
func softwareUpdateCheckScript() string {
	var b strings.Builder
	for _, l := range softwareUpdateLabels {
		fmt.Fprintf(&b, "launchctl print-disabled system | grep -qF '\"%[1]s\" => disabled' || echo 'launchd job %[1]s is not disabled'\n", l)
	}
	b.WriteString("pgrep -x softwareupdated >/dev/null && echo 'softwareupdated is running'\ntrue\n")
	return b.String()
}

// guestScreenshot is captureScreen without a Manager: a screencapture through tart exec.
func guestScreenshot(ctx context.Context, c *tart.Client, vm string) ([]byte, error) {
	f := "/tmp/greenroom-check-shot.png"
	res, err := execChecked(ctx, c, vm, "/bin/sh", "-c", ": greenroom-check-shot\nscreencapture -x "+f+" && base64 -i "+f+"; s=$?; rm -f "+f+"; exit $s")
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(res.Stdout))
}

// uniform reports whether a PNG has at most a few distinct colours on a coarse grid: what a
// capture without the grant, or of a sleeping display, looks like. An unreadable image
// counts as uniform.
func uniform(data []byte) bool {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return true
	}
	b := img.Bounds()
	seen := map[[4]uint32]bool{}
	for y := b.Min.Y; y < b.Max.Y; y += max(1, b.Dy()/32) {
		for x := b.Min.X; x < b.Max.X; x += max(1, b.Dx()/32) {
			r, g, bl, a := img.At(x, y).RGBA()
			seen[[4]uint32{r >> 8, g >> 8, bl >> 8, a >> 8}] = true
		}
	}
	return len(seen) <= 4
}
