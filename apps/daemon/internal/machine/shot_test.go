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
	"testing"
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

// A guest draws on a Retina display, so screencapture hands back an image
// twice the size of the desktop it shows. A screenshot whose resolution
// cannot be reconstructed is not evidence, so the shot carries its own
// pixels and the factor that relates them to the coordinate space clicks
// land in. The factor is measured, never assumed to be 2.
func TestAScreenshotReportsItsOwnSizeAndScale(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "screen"), []byte("400x300"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(shotBase64(t, 800, 600)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Something has to have asked the guest its point size before the scale
	// can be known; taking control is what does that in a real run.
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

// The factor is not always 2: a machine pinned to a plain display reports 1,
// which is why it is computed rather than written down.
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

// A screenshot must not pay for the guest input helper's compile, which costs
// tens of seconds on an image that was not prepared. Until something else has
// asked the guest its point size the scale is simply not known, and saying so
// is better than guessing or going slow: the image's own size, which is what
// a caller needs to aim a click, is always there.
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
	if calls := readCalls(t, control); bytes.Contains(calls, []byte("swiftc")) {
		t.Error("taking a screenshot compiled the guest input helper")
	}
}

func readCalls(t *testing.T, control string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(control, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
