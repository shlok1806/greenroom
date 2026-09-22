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
	"strings"
	"testing"

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
