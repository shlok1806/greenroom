package machine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
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

// CheckImage is the dialog gate (ADR 0018): a clone of a clone of the image, booted
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

	proc, err := c.Start(vm, filepath.Join(o.OutDir, "tart-run.log"))
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
// exerciseWatchdog for its seconds (default 30), so a call blocked on a prompt fails
// instead of hanging, and its prompt is still on screen for the window check.
var exercises = []struct {
	name, script string
	seconds      int
}{
	{"appleevent-system-events", `osascript -e 'tell application "System Events" to get name of first process'`, 0},
	// The Safari repro of issue #25, plus do JavaScript (Safari's "Allow JavaScript from
	// Apple Events"). Safari is the check's own app, so it quits it when done.
	{"appleevent-safari", `mkdir -p /tmp/greenroom-check && echo "<p>greenroom</p>" > /tmp/greenroom-check/index.html &&
open -a Safari file:///tmp/greenroom-check/index.html && sleep 4 &&
osascript -e 'tell application "Safari" to get bounds of front window' &&
osascript -e 'tell application "Safari" to do JavaScript "1+1" in document 1'`, 0},
	{"quit-safari", `osascript -e 'tell application "Safari" to quit'; n=0; while pgrep -x Safari >/dev/null && [ $n -lt 100 ]; do sleep 0.1; n=$((n+1)); done; ! pgrep -x Safari >/dev/null`, 0},
	// Xcode (ADR 0026): first launch and the license are done, and an xcodebuild of a
	// fresh package succeeds in a new clone, with nothing it raises (a license, component,
	// privacy or developer tools prompt) left for the window check. A cold build in a fresh
	// clone takes about a minute.
	{"xcodebuild", xcodebuildExercise, 240},
	// The issue #252 repro exactly: an app outside base.sh's old fixed nine-target list.
	// ADR 0038's build-time enumeration must cover it with no machine_approve_control call.
	{"appleevent-calculator", `osascript -e 'tell application "Calculator" to get name of every window'`, 30},
	{"quit-calculator", quitAppScript("Calculator"), 0},
	// A freshly built app, never in any built-time list, approved by machine_approve_control's
	// guest script (tccgrant.sh) and then scripting itself (ADR 0038, issue #252 point 3).
	{"appleevent-freshbuild", selfScriptingAppExercise(), 60},
}

// quitAppScript asks name to quit over Apple Events and waits up to 10 s for it to leave, as
// the xcodebuild list's own "quit-safari" step does.
func quitAppScript(name string) string {
	return fmt.Sprintf(`osascript -e 'tell application %q to quit'; n=0
while pgrep -x %q >/dev/null && [ $n -lt 100 ]; do sleep 0.1; n=$((n+1)); done
! pgrep -x %q >/dev/null`, name, name, name)
}

// selfScriptingAppExercise builds a minimal .app in the guest's /tmp with swiftc (no Xcode
// project needed), approves it with tccgrant.sh (machine_approve_control's guest half), then
// has it run an AppleScript that targets its own bundle id: `tell application id "<its id>"`.
// A prompt here would mean either the approval script or the grants it writes are wrong.
func selfScriptingAppExercise() string {
	return `set -e
d=/tmp/greenroom-check-selfbuild && rm -rf "$d" && mkdir -p "$d/TestApp.app/Contents/MacOS"
bid=com.greenroom.check.testapp
cat > "$d/TestApp.app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>$bid</string>
	<key>CFBundleExecutable</key><string>TestApp</string>
	<key>CFBundleName</key><string>TestApp</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>LSUIElement</key><true/>
</dict>
</plist>
PLIST
result=/tmp/greenroom-check-selfbuild-result
rm -f "$result"
cat > "$d/main.swift" <<'SWIFT'
import Foundation
// activate alone (not "get name of every window"): this app has no AppKit run loop or
// windows, and a property Apple Events cannot answer would raise its own error unrelated
// to what this exercise checks, which is only that scripting itself never prompts.
let bid = Bundle.main.bundleIdentifier ?? ""
let src = "tell application id \"\(bid)\" to activate"
var err: NSDictionary?
let script = NSAppleScript(source: src)
let out = script?.executeAndReturnError(&err)
let text: String
if let err { text = "error: \(err)" } else { text = "ok: \(out?.stringValue ?? "")" }
try? text.write(toFile: "/tmp/greenroom-check-selfbuild-result", atomically: true, encoding: .utf8)
SWIFT
swiftc -o "$d/TestApp.app/Contents/MacOS/TestApp" "$d/main.swift"
cat > "$d/grant.sh" <<'GRANTEOF'
` + tccGrantScript + `
GRANTEOF
grant_out="$(sh "$d/grant.sh" "$d/TestApp.app" 2>&1)" || { echo "machine_approve_control's script failed: $grant_out" >&2; exit 1; }
open "$d/TestApp.app"
n=0
while [ ! -f "$result" ] && [ $n -lt 100 ]; do sleep 0.1; n=$((n+1)); done
[ -f "$result" ] || { echo "the freshly built app never finished scripting itself" >&2; exit 1; }
res="$(cat "$result")"
rm -f "$result"
osascript -e "tell application id \"$bid\" to quit" >/dev/null 2>&1 || true
n=0; while pgrep -x TestApp >/dev/null && [ $n -lt 50 ]; do sleep 0.1; n=$((n+1)); done
case "$res" in ok:*) echo "$res" ;; *) echo "self-scripting failed: $res" >&2; exit 1 ;; esac`
}

// xcodebuildExercise builds a one-file package with xcodebuild in the guest's /tmp.
const xcodebuildExercise = `xcodebuild -checkFirstLaunchStatus || { echo "xcodebuild -checkFirstLaunchStatus: first launch is not done" >&2; exit 1; }
d=/tmp/greenroom-check-xcodebuild && rm -rf "$d" && mkdir -p "$d/Sources/CheckTool" && cd "$d" &&
printf '// swift-tools-version:5.9\nimport PackageDescription\nlet package = Package(name: "CheckTool", targets: [.executableTarget(name: "CheckTool")])\n' > Package.swift &&
echo 'print("check")' > Sources/CheckTool/main.swift &&
xcodebuild -scheme CheckTool -destination platform=macOS -derivedDataPath "$d/dd" build > "$d/log" 2>&1 &&
grep -q -F '** BUILD SUCCEEDED **' "$d/log" || { grep -m 3 'error' "$d/log" >&2; tail -n 3 "$d/log" >&2; exit 1; }`

// exerciseWatchdog runs $1 in its own process group and kills the whole group after $2 s
// (exit 124), as machine_exec's wrapper does: tart exec returns only when every holder of
// the guest's pipes has exited, so killing the shell alone left a blocked osascript holding
// the call open. The shell's job notices go to /dev/null and the call's stderr through fd 3,
// as in execWrapperTail. macOS has no timeout(1).
const exerciseWatchdog = `exec 3>&2 2>/dev/null
set -m
/bin/sh -c "$1" 2>&3 3>&- &
p=$!
(sleep "$2"; : > /tmp/greenroom-check-timedout; kill -KILL -"$p") >/dev/null 2>&1 </dev/null 3>&- &
w=$!
wait "$p"; s=$?
kill -KILL "$w" 2>/dev/null
[ -f /tmp/greenroom-check-timedout ] && { rm -f /tmp/greenroom-check-timedout; echo "timed out after $2 s" >&3; s=124; }
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

	// The guest agent (daemon ADR 0005), as a daemon with -desktop-toolkit drives the machine.
	_ = timed("agent", func() error {
		p.Findings = append(p.Findings, agentSmoke(ctx, c, vm)...)
		return nil
	})

	for _, ex := range exercises {
		secs := ex.seconds
		if secs <= 0 {
			secs = 30
		}
		call, cancel := context.WithTimeout(ctx, time.Duration(secs+30)*time.Second)
		var res tart.ExecResult
		err := timed(ex.name, func() (err error) {
			res, err = c.Exec(call, vm, "/bin/sh", "-c", ": greenroom-check-"+ex.name+"\n"+exerciseWatchdog, "sh", ex.script, strconv.Itoa(secs))
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

// agentSmokeLimit bounds the whole agent check: the start, HELLO and four requests.
const agentSmokeLimit = 60 * time.Second

// agentSmoke checks the guest agent the way the daemon uses it (daemon ADR 0005): it starts on
// one tart exec pipe and says HELLO with all three permissions it inherits from
// tart-guest-agent (Accessibility, screen capture, posting events), a snapshot of Finder lists
// something, a capture over the channel is not one flat colour, and the frontmost app is found
// idle. It returns a finding per failure; it never fails the check run itself.
func agentSmoke(ctx context.Context, c *tart.Client, vm string) (findings []string) {
	ctx, cancel := context.WithTimeout(ctx, agentSmokeLimit)
	defer cancel()
	pipe, err := c.StartPipe(vm, "/bin/sh", "-c", agentScript())
	if err != nil {
		return []string{"the guest agent did not start: " + err.Error()}
	}
	conn, err := guestagent.Open(ctx, pipe, guestagent.Options{})
	if err != nil {
		_ = pipe.Close()
		return []string{"the guest agent did not say hello: " + err.Error()}
	}
	defer func() { _ = conn.Close() }()
	h := conn.Hello()
	for _, grant := range []struct {
		ok   bool
		name string
	}{{h.Trusted.Accessibility, "Accessibility"}, {h.Trusted.Screen, "screen capture"}, {h.Trusted.PostEvent, "posting events"}} {
		if !grant.ok {
			findings = append(findings, "the guest agent is not trusted for "+grant.name+": tart-guest-agent's grant is missing from the image")
		}
	}
	call := func(op string, args any) (guestagent.Response, error) {
		return conn.Call(ctx, guestagent.Request{Op: op, Args: args, Reader: HolderVerifier, Deadline: 15 * time.Second})
	}
	if resp, err := call("snapshot", map[string]any{"app": "Finder", "mode": "all", "limit": 50}); err != nil {
		findings = append(findings, "the guest agent could not snapshot Finder: "+err.Error())
	} else {
		var snap struct {
			Nodes []json.RawMessage `json:"nodes"`
		}
		if err := json.Unmarshal(resp.Result, &snap); err != nil || len(snap.Nodes) == 0 {
			findings = append(findings, "the guest agent's snapshot of Finder lists nothing")
		}
	}
	if resp, err := call("capture", map[string]any{"format": "png"}); err != nil {
		findings = append(findings, "the guest agent could not capture the screen: "+err.Error())
	} else if uniform(resp.Blob) {
		findings = append(findings, "the guest agent's capture is one flat colour (a missing screen-capture grant, or a sleeping display)")
	}
	if resp, err := call("waitFor", map[string]any{"target": map[string]any{"idle": true}, "timeoutMs": 10000}); err != nil {
		findings = append(findings, "the guest agent's wait for idle failed: "+err.Error())
	} else {
		var w struct {
			Satisfied bool `json:"satisfied"`
		}
		if err := json.Unmarshal(resp.Result, &w); err != nil || !w.Satisfied {
			findings = append(findings, "the frontmost app never went idle within 10 s")
		}
	}
	return findings
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
