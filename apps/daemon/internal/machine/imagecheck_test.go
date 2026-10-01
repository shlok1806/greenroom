package machine

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// What a clean lean desktop reported on 26.6.2, and what greenroom-base and a prompt add.
const (
	cleanDesktop = `{"windows":[
 {"owner":"Control Center","name":"Clock","layer":25,"alpha":1,"x":870,"y":0,"width":154,"height":30},
 {"owner":"Window Server","name":"Menubar","layer":24,"alpha":1,"x":0,"y":0,"width":1024,"height":30},
 {"owner":"Dock","name":"Dock","layer":20,"alpha":1,"x":0,"y":0,"width":1024,"height":768}],
 "apps":[{"name":"Finder","bundleId":"com.apple.finder","pid":391}]}`
	baseWidgets = `{"windows":[
 {"owner":"Window Server","name":"Menubar","layer":24,"alpha":1,"x":0,"y":0,"width":1024,"height":30},
 {"owner":"Notification Center","name":"Forecast","layer":-2147483601,"alpha":1,"x":188,"y":38,"width":180,"height":180}],
 "apps":[{"name":"Finder","bundleId":"com.apple.finder","pid":391}]}`
	promptAndTerminal = `{"windows":[
 {"owner":"Window Server","name":"Menubar","layer":24,"alpha":1,"x":0,"y":0,"width":1024,"height":30},
 {"owner":"UserNotificationCenter","name":"","layer":8,"alpha":1,"x":382,"y":210,"width":260,"height":348},
 {"owner":"Terminal","name":"admin - zsh","layer":0,"alpha":1,"x":40,"y":219,"width":877,"height":499},
 {"owner":"Notification Center","name":"banner","layer":23,"alpha":1,"x":660,"y":38,"width":350,"height":80}],
 "apps":[{"name":"Finder","bundleId":"com.apple.finder","pid":391},{"name":"Terminal","bundleId":"com.apple.Terminal","pid":379}]}`
)

// A 4x4 PNG with 16 colours, so the check's flat-screenshot test passes on it.
func busyPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < 16; i++ {
		img.Set(i%4, i/4, color.RGBA{uint8(i * 16), uint8(255 - i*16), uint8(i * 7), 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func writeControl(t *testing.T, control, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCheck(t *testing.T, bin, out string) ImageCheckResult {
	t.Helper()
	control := filepath.Join(filepath.Dir(bin), "control")
	// A healthy guest agent (agentSmoke), unless the test canned another answer.
	for name, body := range map[string]string{
		"agent-snapshot.json": `{"result": {"nodes": [{"ref": "e1", "role": "Window", "name": "Desktop"}]}}`,
		"agent-waitFor.json":  `{"result": {"satisfied": true, "elapsedMs": 300}}`,
	} {
		if _, err := os.Stat(filepath.Join(control, name)); os.IsNotExist(err) {
			writeControl(t, control, name, body)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := CheckImage(ctx, ImageCheck{TartBin: bin, Image: "greenroom-new", OutDir: out,
		Settle: time.Millisecond, Linger: time.Millisecond, Poll: 10 * time.Millisecond, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("CheckImage could not run: %v", err)
	}
	return res
}

func TestCheckImagePassesACleanImageOnACloneOfACloneBeforeAndAfterAReboot(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	writeControl(t, control, "shot.b64", busyPNG(t))
	writeControl(t, control, "desktop.json", cleanDesktop)
	out := t.TempDir()

	res := runCheck(t, bin, out)
	if !res.Passed {
		t.Fatalf("a clean image failed: %+v", res.Passes)
	}
	if len(res.Passes) != 2 || res.Passes[0].Label != "first-boot" || res.Passes[1].Label != "after-reboot" {
		t.Fatalf("want a pass before and after the reboot, got %+v", res.Passes)
	}
	calls := testsupport.Calls(t, control)
	clones := regexp.MustCompile(`(?m)^clone (\S+) (\S+)$`).FindAllStringSubmatch(calls, -1)
	if len(clones) != 2 || clones[0][1] != "greenroom-new" || clones[1][1] != clones[0][2] || res.Clone != clones[1][2] {
		t.Fatalf("want a clone of a clone of the image, got %v", clones)
	}
	if !strings.Contains(calls, "run "+res.Clone+" --no-graphics") || strings.Contains(calls, "run greenroom-new ") || strings.Contains(calls, "run "+clones[0][2]+" ") {
		t.Errorf("only the clone of the clone may boot, headless:\n%s", calls)
	}
	if !strings.Contains(calls, "greenroom-check-reboot") {
		t.Error("the check never rebooted the guest")
	}
	for _, name := range []string{clones[0][2], res.Clone} {
		if !strings.Contains(calls, "delete "+name+"\n") {
			t.Errorf("the check left its clone %s behind", name)
		}
	}
	for _, p := range res.Passes {
		if _, err := os.Stat(p.Screenshot); err != nil {
			t.Errorf("pass %s kept no screenshot: %v", p.Label, err)
		}
	}
	// The image must pass on its own: nothing the daemon's boot writes is written here.
	if strings.Contains(calls, "ScreenCaptureApprovals") || strings.Contains(calls, "EnableStandardClickToShowDesktop") {
		t.Errorf("the check wrote boot-time settings, so it would pass an image that needs them:\n%s", calls)
	}
}

func TestCheckImageFailsOnAPromptAnUnaskedAppAndABanner(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	writeControl(t, control, "shot.b64", busyPNG(t))
	writeControl(t, control, "desktop.json", promptAndTerminal)

	res := runCheck(t, bin, t.TempDir())
	if res.Passed {
		t.Fatal("an image with a prompt on screen passed")
	}
	findings := strings.Join(res.Passes[0].Findings, "\n")
	for _, want := range []string{`"UserNotificationCenter"`, `"Terminal"`, "app Terminal (com.apple.Terminal) is running", `"banner" at layer 23`} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings do not name %s:\n%s", want, findings)
		}
	}
	if strings.Contains(findings, "Menubar") {
		t.Errorf("the menu bar is on every desktop and must not be a finding:\n%s", findings)
	}
	// Surface, never sweep: the check reports the window and leaves it. The guest agent's start
	// pkills an orphaned agent of its own (agentScript), and the appleevent-freshbuild exercise
	// flushes tccd's cache after granting its test app (tccgrant.sh, ADR 0038) the same way
	// base.sh does; neither is the check closing something it found.
	calls := regexp.MustCompile(`pkill -f '\[g\]reenroom-input-\d+ --agent'`).ReplaceAllString(testsupport.Calls(t, control), "")
	calls = regexp.MustCompile(`(sudo -n )?killall tccd[^\n]*`).ReplaceAllString(calls, "")
	if regexp.MustCompile(`pkill|killall|quit app "Terminal"|to quit\b.*Terminal`).MatchString(calls) {
		t.Errorf("the check closed something it found:\n%s", calls)
	}
}

func TestCheckImageFailsWhenAnExerciseBlocksOrSoftwareUpdateRuns(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	writeControl(t, control, "shot.b64", busyPNG(t))
	testsupport.Flag(t, control, "fail-check-appleevent-safari")
	writeControl(t, control, "softwareupdate", "softwareupdated is running\n")

	res := runCheck(t, bin, t.TempDir())
	findings := strings.Join(res.Passes[1].Findings, "\n")
	if res.Passed || !strings.Contains(findings, "appleevent-safari: exit 1") || !strings.Contains(findings, "softwareupdated is running") {
		t.Fatalf("a blocked Safari Apple Event and a running softwareupdated did not both fail the check after the reboot:\n%s", findings)
	}
}

func TestCheckImageFailsOnAFlatScreenshot(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	img := image.NewRGBA(image.Rect(0, 0, 8, 8)) // all black: no grant, or a sleeping display
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	writeControl(t, control, "shot.b64", base64.StdEncoding.EncodeToString(b.Bytes()))

	res := runCheck(t, bin, t.TempDir())
	if res.Passed || !strings.Contains(strings.Join(res.Passes[0].Findings, "\n"), "one flat colour") {
		t.Fatalf("a black screenshot passed: %+v", res.Passes[0].Findings)
	}
}

// The gate checks the guest agent too (daemon ADR 0005): an agent that cannot read the
// screen fails the image.
func TestCheckImageFailsWhenTheGuestAgentCannotWork(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	writeControl(t, control, "shot.b64", busyPNG(t))
	writeControl(t, control, "desktop.json", cleanDesktop)
	writeControl(t, control, "agent-snapshot.json", `{"error": {"code": "not_trusted", "message": "this machine has not granted Accessibility"}}`)
	writeControl(t, control, "agent-waitFor.json", `{"result": {"satisfied": false, "elapsedMs": 10000}}`)
	res := runCheck(t, bin, t.TempDir())
	if res.Passed {
		t.Fatal("an image whose guest agent cannot read the screen passed")
	}
	got := strings.Join(res.Passes[0].Findings, "\n")
	for _, want := range []string{"could not snapshot Finder", "never went idle"} {
		if !strings.Contains(got, want) {
			t.Errorf("findings lack %q:\n%s", want, got)
		}
	}
	if !strings.Contains(testsupport.Calls(t, control), "--agent") {
		t.Error("the gate never started the guest agent")
	}
}

func TestDesktopReportAllowsWidgetsUnderTheDesktopButNothingAbove(t *testing.T) {
	for name, tc := range map[string]struct {
		json  string
		clean bool
	}{
		"clean lean desktop":            {cleanDesktop, true},
		"base desktop with widgets":     {baseWidgets, true},
		"prompt, Terminal and a banner": {promptAndTerminal, false},
	} {
		t.Run(name, func(t *testing.T) {
			bin, control := testsupport.FakeTart(t)
			writeControl(t, control, "desktop.json", tc.json)
			d, err := readDesktop(context.Background(), &tart.Client{Bin: bin}, "vm")
			if err != nil {
				t.Fatal(err)
			}
			if r := d.Report(); r.Clean != tc.clean {
				t.Errorf("clean = %v, want %v; findings %v", r.Clean, tc.clean, r.Findings())
			}
		})
	}
}

// ADR 0026: every pass builds with xcodebuild under its own, longer watchdog, and a build
// that fails (a license, first launch left undone, a prompt it waited on) fails the image.
func TestCheckImageBuildsWithXcodebuildAndFailsWhenItCannot(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	writeControl(t, control, "shot.b64", busyPNG(t))
	writeControl(t, control, "desktop.json", cleanDesktop)
	res := runCheck(t, bin, t.TempDir())
	if !res.Passed {
		t.Fatalf("a clean image failed: %+v", res.Passes)
	}
	calls := testsupport.Calls(t, control)
	if n := strings.Count(calls, ": greenroom-check-xcodebuild\n"); n != 2 {
		t.Errorf("want one xcodebuild per pass, got %d", n)
	}
	if !regexp.MustCompile(`(?s)greenroom-check-xcodebuild.*xcodebuild -checkFirstLaunchStatus.*\*\* BUILD SUCCEEDED \*\*.* 240\n`).MatchString(calls) {
		t.Errorf("the xcodebuild exercise did not run its build under a 240 s watchdog:\n%s", calls)
	}

	bin, control = testsupport.FakeTart(t)
	writeControl(t, control, "shot.b64", busyPNG(t))
	writeControl(t, control, "desktop.json", cleanDesktop)
	testsupport.Flag(t, control, "fail-check-xcodebuild")
	res = runCheck(t, bin, t.TempDir())
	if res.Passed {
		t.Fatal("an image whose xcodebuild fails passed")
	}
	if got := strings.Join(res.Passes[0].Findings, "\n"); !strings.Contains(got, "xcodebuild: exit 1") {
		t.Errorf("the finding does not name the xcodebuild exercise:\n%s", got)
	}
}

// Issue #282: the Calculator exercise counts only an answer from the running app, past TCC.
// Measured live: without the grant `count windows` blocks on the prompt; with it Calculator,
// which has no scripting dictionary, answers -1708. A fake osascript plays each answer here,
// run through the exercise's own watchdog.
func TestCalculatorExerciseCountsOnlyAnAnswerFromTheApp(t *testing.T) {
	for name, tc := range map[string]struct {
		osascript string
		secs      string
		exit      int
	}{
		"a scriptable answer":      {`echo 1`, "5", 0},
		"the app's own -1708":      {`echo "33:46: execution error: Calculator got an error: every window doesn’t understand the “count” message. (-1708)" >&2; exit 1`, "5", 0},
		"a -1708 not from the app": {`echo "33:46: execution error: Can’t make some data into the expected type. (-1708)" >&2; exit 1`, "5", 1},
		"-1708 then more output":   {`echo "Calculator got an error: (-1708)"; echo "execution error: Not authorized to send Apple events to Calculator. (-1743)" >&2; exit 1`, "5", 1},
		"denied, -1743":            {`echo "execution error: Not authorized to send Apple events to Calculator. (-1743)" >&2; exit 1`, "5", 1},
		"no window, -1728":         {`echo "execution error: Can't get window 1. (-1728)" >&2; exit 1`, "5", 1},
		"blocked on the prompt":    {`sleep 30`, "1", 124},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte("#!/bin/sh\n"+tc.osascript+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/sh", "-c", exerciseWatchdog, "sh", calculatorExercise, tc.secs)
			cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin")
			out, _ := cmd.CombinedOutput()
			if got := cmd.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("exit %d, want %d: %s", got, tc.exit, out)
			}
		})
	}
}

// AppleScript answers an application's name and version from its bundle, and TCC exempts
// activate and quit (measured, issue #282 and ADR 0044 "Measured"): none of them proves a
// grant. An exercise that proves one asks the app, or an element of it, for something else.
func TestNoAppleEventExerciseProvesAGrantWithAnEventThatNeedsNone(t *testing.T) {
	ungated := regexp.MustCompile(`tell application (id )?"[^"]+" to (get name|get version|get frontmost|activate|quit)'`)
	for _, ex := range exercises {
		if strings.HasPrefix(ex.name, "appleevent-") && ungated.MatchString(ex.script) {
			t.Errorf("%s proves nothing about its grant: %s", ex.name, ungated.FindString(ex.script))
		}
	}
}
