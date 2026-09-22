package machine

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
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

// Screenshot and the frame recorder run at once, so each capture must use its
// own guest file and remove it, or one could read the other's picture.
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

	re := regexp.MustCompile(`/tmp/greenroom-shot-[0-9a-f]+\.png`)
	seen := map[string]bool{}
	captures := 0
	for _, line := range strings.Split(testsupport.Calls(t, control), "\n") {
		if !strings.Contains(line, "screencapture") {
			continue
		}
		captures++
		path := re.FindString(line)
		if path == "" || !strings.Contains(line, "rm -f '"+path+"'") {
			t.Fatalf("a capture does not use and remove a private file: %s", line)
		}
		if seen[path] {
			t.Fatalf("two captures shared the guest file %s", path)
		}
		seen[path] = true
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
