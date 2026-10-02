package machine

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// shotBase64 gives the fake tart a PNG of an exact size to hand back.
func shotBase64(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// A shot carries its pixel size and the measured pixels-per-point scale.
func TestAScreenshotReportsItsOwnSizeAndScale(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "screen"), []byte("400x300"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(shotBase64(t, 800, 600)), 0o644); err != nil {
		t.Fatal(err)
	}
	// The scale needs the point size, which ScreenOf reads.
	if _, err := mgr.ScreenOf(context.Background(), mc.RunID); err != nil {
		t.Fatalf("ScreenOf: %v", err)
	}

	_, shot, err := mgr.Screenshot(context.Background(), mc.RunID)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if shot.Width != 800 || shot.Height != 600 {
		t.Errorf("the shot is %dx%d, want 800x600", shot.Width, shot.Height)
	}
	if shot.Scale != 2 {
		t.Errorf("scale is %v, want 2 for an 800 pixel image of a 400 point desktop", shot.Scale)
	}
}

// A guest's pixel density follows the host's main display (ADR 0046): the same 1024x768 point
// desktop is 1024 or 2048 pixels wide. Clicks aimed from a screenshot, or from the UI tree, land
// on the same guest point either way, because fractions become points and never pixels.
func TestClicksLandOnTheSamePointAt1xAnd2x(t *testing.T) {
	for _, scale := range []int{1, 2} {
		t.Run(fmt.Sprintf("%dx", scale), func(t *testing.T) {
			mgr, _, control := newTestManager(t)
			mc := readyMachine(t, mgr)
			if err := os.WriteFile(filepath.Join(control, "screen"), []byte("1024x768"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(shotBase64(t, 1024*scale, 768*scale)), 0o644); err != nil {
				t.Fatal(err)
			}
			writeUI(t, control, tipSplitUI)
			if _, err := mgr.ScreenOf(context.Background(), mc.RunID); err != nil {
				t.Fatalf("ScreenOf: %v", err)
			}
			_, shot, err := mgr.Screenshot(context.Background(), mc.RunID)
			if err != nil {
				t.Fatalf("Screenshot: %v", err)
			}
			if shot.Scale != float64(scale) {
				t.Fatalf("scale is %v, want %d", shot.Scale, scale)
			}
			tree, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "TipSplit", 0)
			if err != nil {
				t.Fatalf("UI: %v", err)
			}
			seg := tree.Elements[4] // the 25% segment, frame (586, 347) 48x24 points

			// A pixel the agent picked on the picture, at (300, 200) points.
			px, py := float64(300*scale), float64(200*scale)
			if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
				t.Fatalf("TakeControl: %v", err)
			}
			if _, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{
				{Type: "click", X: frac(px / float64(shot.Width)), Y: frac(py / float64(shot.Height))},
				{Type: "click", X: frac(seg.X), Y: frac(seg.Y)},
			}); err != nil {
				t.Fatalf("Input: %v", err)
			}
			posted := postedActions(t, control)
			if len(posted) != 2 {
				t.Fatalf("posted %d actions, want 2", len(posted))
			}
			if *posted[0].X != 300 || *posted[0].Y != 200 {
				t.Errorf("a click at picture pixel (%v, %v) went out at %v,%v points, want 300,200", px, py, *posted[0].X, *posted[0].Y)
			}
			if *posted[1].X != 610 || *posted[1].Y != 359 {
				t.Errorf("the segment's center went out at %v,%v points, want 610,359", *posted[1].X, *posted[1].Y)
			}
		})
	}
}

// A non-Retina display reports 1, so the scale is never hardcoded.
func TestScreenshotScaleIsMeasuredNotAssumed(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "screen"), []byte("800x600"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(shotBase64(t, 800, 600)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ScreenOf(context.Background(), mc.RunID); err != nil {
		t.Fatalf("ScreenOf: %v", err)
	}
	_, shot, err := mgr.Screenshot(context.Background(), mc.RunID)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if shot.Scale != 1 {
		t.Errorf("scale is %v, want 1 when the image matches the desktop", shot.Scale)
	}
}

// A screenshot never compiles the input helper; scale stays absent until the
// point size is known.
func TestAScreenshotDoesNotInstallTheInputHelperToLearnItsScale(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(shotBase64(t, 800, 600)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, shot, err := mgr.Screenshot(context.Background(), mc.RunID)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if shot.Width != 800 || shot.Height != 600 {
		t.Errorf("the shot is %dx%d, want 800x600 even with no scale", shot.Width, shot.Height)
	}
	if shot.Scale != 0 {
		t.Errorf("scale is %v, want it absent until the point size is known", shot.Scale)
	}
	if strings.Contains(testsupport.Calls(t, control), "swiftc") {
		t.Error("taking a screenshot compiled the guest input helper")
	}
}

// Screenshot and the frame recorder run at once. Each capture runs under the guest
// watchdog and writes its picture into the watchdog's private temp dir, which the
// watchdog removes, so no capture can read another's picture or leave a file behind
// (daemon ADR 0003). The screenshots all succeed: they wait their turn.
func TestConcurrentScreenshotsAndFramesUseTheirOwnGuestFile(t *testing.T) {
	mgr, _, control := newTestManager(t, WithFrameInterval(10*time.Millisecond))
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, mgr)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
				t.Errorf("Screenshot: %v", err)
			}
		}()
	}
	wg.Wait()
	waitFor(t, 5*time.Second, func() bool {
		frames, _ := ReadFrames(mgr.RunDir(mc.RunID))
		return len(frames) >= 2
	})

	captures := 0
	for _, line := range strings.Split(testsupport.Calls(t, control), "\n") {
		if !strings.Contains(line, "screencapture") {
			continue
		}
		captures++
		if !strings.Contains(line, `screencapture -x "$f"`) || !strings.Contains(line, `f="$GREENROOM_LOOK_DIR/shot.png"`) {
			t.Fatalf("a capture does not write into the watchdog's private dir: %s", line)
		}
	}
	if !strings.Contains(testsupport.Calls(t, control), "greenroom-watchdog") {
		t.Error("the captures do not run under the guest watchdog")
	}
	if captures < 10 {
		t.Errorf("saw %d captures, want the 8 screenshots and at least 2 frames", captures)
	}
}

// A screenshot must not wait behind an input helper install, which holds the
// install lock for up to a Swift compile.
func TestAScreenshotDoesNotWaitForTheInputHelperInstall(t *testing.T) {
	mgr, _, control := newTestManager(t)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	ready := readyMachine(t, mgr)
	mc, err := mgr.get(ready.RunID)
	if err != nil {
		t.Fatal(err)
	}
	mc.input.mu.Lock() // an install in progress
	defer mc.input.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := mgr.Screenshot(ctx, ready.RunID); err != nil {
		t.Fatalf("Screenshot during an install: %v", err)
	}
}
