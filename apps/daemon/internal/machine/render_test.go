package machine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	imagepng "image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// A synthetic 400x300-point window for the ink test (ADR 0027): text in several colours on the
// window background, text drawn in that background, an empty frame and a noisy picture.
var (
	windowBG = color.NRGBA{R: 236, G: 236, B: 236, A: 255}
	inkScene = []struct {
		name  string
		frame pointRect
		ink   color.Color // nil draws no text
		busy  bool        // fill the frame with noise first
		blank bool        // what the ink test must say
	}{
		{"black text", pointRect{20, 20, 160, 16}, color.Black, false, false},
		{"secondary grey text", pointRect{20, 50, 160, 16}, color.NRGBA{R: 138, G: 138, B: 138, A: 255}, false, false},
		{"placeholder grey text", pointRect{20, 80, 160, 16}, color.NRGBA{R: 190, G: 190, B: 190, A: 255}, false, false},
		{"red text of the same lightness", pointRect{20, 110, 160, 16}, color.NRGBA{R: 255, G: 60, B: 60, A: 255}, false, false},
		{"text in the background colour", pointRect{20, 140, 200, 30}, windowBG, false, true},
		{"text a few levels off the background", pointRect{20, 180, 200, 30}, color.NRGBA{R: 240, G: 240, B: 240, A: 255}, false, true},
		{"an empty frame", pointRect{220, 20, 160, 16}, nil, false, true},
		{"text on a noisy picture", pointRect{220, 50, 160, 60}, windowBG, true, false},
		{"a single character", pointRect{220, 140, 10, 16}, color.Black, false, false},
	}
)

// drawScene draws inkScene at 1 pixel a point, then scales it by scale (nearest neighbour, as a
// Retina capture doubles every point).
func drawScene(t *testing.T, scale int) image.Image {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	draw.Draw(img, img.Bounds(), image.NewUniform(windowBG), image.Point{}, draw.Src)
	rng := rand.New(rand.NewPCG(1, 2))
	for _, s := range inkScene {
		r := image.Rect(int(s.frame.X), int(s.frame.Y), int(s.frame.X+s.frame.W), int(s.frame.Y+s.frame.H))
		if s.busy {
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					img.Set(x, y, color.NRGBA{R: uint8(rng.IntN(256)), G: uint8(rng.IntN(256)), B: uint8(rng.IntN(256)), A: 255})
				}
			}
		}
		if s.ink == nil {
			continue
		}
		text := "Each pays: $49.56"
		if s.frame.W < 20 {
			text = "0"
		}
		d := font.Drawer{Dst: img, Src: image.NewUniform(s.ink), Face: basicfont.Face7x13,
			Dot: fixed.P(r.Min.X+2, r.Min.Y+12)}
		d.DrawString(text)
	}
	if scale == 1 {
		return img
	}
	big := image.NewNRGBA(image.Rect(0, 0, 400*scale, 300*scale))
	xdraw.NearestNeighbor.Scale(big, big.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	return big
}

// The ink test tells text from a frame with none, at 1x and at Retina 2x, with frames in points.
func TestTheInkTestFindsTextThatIsNotDrawn(t *testing.T) {
	for _, scale := range []int{1, 2} {
		img := drawScene(t, scale)
		frames := make([]pointRect, len(inkScene))
		texts := make([]bool, len(inkScene))
		for i, s := range inkScene {
			frames[i], texts[i] = s.frame, true
		}
		marks, problem := renderStates(img, Screen{Width: 400, Height: 300}, frames, texts, nil, 7)
		if problem != "" {
			t.Fatalf("%dx: %s", scale, problem)
		}
		for i, s := range inkScene {
			if got := marks[i] == RenderedBlank; got != s.blank {
				t.Errorf("%dx: %s: blank = %v, want %v", scale, s.name, got, s.blank)
			}
		}
	}
}

// Off the display is offscreen; under another app's window is covered; under the menu bar,
// the Dock, an invisible window or the app's own window is neither; an element that is not text
// is never marked; a capture of the wrong shape marks nothing.
func TestRenderStatesMarksOffscreenAndCovered(t *testing.T) {
	img := drawScene(t, 1)
	screen := Screen{Width: 400, Height: 300}
	frames := []pointRect{
		{20, 20, 160, 16},  // black text, under another app's window
		{-50, 20, 30, 16},  // off the left edge
		{20, 50, 160, 16},  // grey text, under the Dock and an invisible window
		{220, 20, 160, 16}, // an empty frame that is not text
		{20, 80, 160, 16},  // placeholder text, the other app's window is behind the app's
	}
	texts := []bool{true, true, true, false, true}
	windows := []DesktopWindow{ // front to back
		{Owner: "Notes", PID: 9, Alpha: 1, X: 10, Y: 10, Width: 200, Height: 30},
		{Owner: "Dock", PID: 2, Layer: 20, Alpha: 1, X: 0, Y: 0, Width: 400, Height: 300},
		{Owner: "Overlay", PID: 3, Alpha: 0, X: 0, Y: 40, Width: 400, Height: 40},
		{Owner: "TipSplit", PID: 7, Alpha: 1, X: 0, Y: 0, Width: 400, Height: 300},
		{Owner: "Mail", PID: 11, Alpha: 1, X: 0, Y: 70, Width: 400, Height: 40},
	}
	marks, problem := renderStates(img, screen, frames, texts, windows, 7)
	if problem != "" {
		t.Fatal(problem)
	}
	want := []string{RenderedCovered, RenderedOffscreen, "", "", ""}
	for i := range want {
		if marks[i] != want[i] {
			t.Errorf("element %d = %q, want %q", i, marks[i], want[i])
		}
	}
	// With no window of the app in the list nothing is known about covering.
	marks, _ = renderStates(img, screen, frames[:1], texts[:1], windows[:1], 7)
	if marks[0] != "" {
		t.Errorf("with no window of the app, the element = %q, want unmarked", marks[0])
	}
	if _, problem := renderStates(img, Screen{Width: 400, Height: 400}, frames, texts, nil, 7); problem == "" {
		t.Error("a capture that is not the screen's shape was used")
	}
}

// End to end on the fake machine: a UI read captures the screen once, marks the text drawn in
// the background colour, and the outline a model reads says so. A read with no text captures
// nothing, and a capture that fails leaves the tree unmarked and says why.
func TestUIMarksTextThatIsNotDrawn(t *testing.T) {
	mgr, _, control := newTestManager(t)
	var buf bytes.Buffer
	if err := imagepng.Encode(&buf, drawScene(t, 2)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(base64.StdEncoding.EncodeToString(buf.Bytes())), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUI(t, control, `{"app":{"name":"TipSplit","pid":7},"apps":["TipSplit"],"screen":{"width":400,"height":300},
"truncated":false,"elements":[
{"role":"AXWindow","title":"TipSplit","depth":0,"frame":{"x":0,"y":0,"w":400,"h":300}},
{"role":"AXStaticText","value":"Tip: $8.40","depth":1,"frame":{"x":20,"y":20,"w":160,"h":16}},
{"role":"AXStaticText","value":"Each pays: $49.56","identifier":"perPerson","depth":1,"frame":{"x":20,"y":140,"w":200,"h":30}},
{"role":"AXButton","depth":1,"frame":{"x":220,"y":20,"w":160,"h":16}}]}`)
	mc := readyMachine(t, mgr)

	before := strings.Count(testsupport.Calls(t, control), "screencapture")
	tree, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(testsupport.Calls(t, control), "screencapture") - before; n != 1 {
		t.Errorf("the read captured the screen %d times, want once", n)
	}
	got := []string{}
	for _, e := range tree.Elements {
		got = append(got, e.Rendered)
	}
	if strings.Join(got, ",") != ",,blank," || tree.Unrendered != "" {
		t.Errorf("marks = %q (unrendered %q), want only Each pays blank", got, tree.Unrendered)
	}
	out := tree.Outline()
	for _, want := range []string{`[3] StaticText value="Each pays: $49.56" id="perPerson" [not drawn] center`,
		"[not drawn]: the screen shows no text in its frame"} {
		if !strings.Contains(out, want) {
			t.Errorf("outline = %s\nwant %q", out, want)
		}
	}
	if strings.Contains(out, `"Tip: $8.40" [not drawn]`) {
		t.Errorf("drawn text was marked: %s", out)
	}
	// The step record keeps the mark, so the verdict review can read it (ADR 0027 point 4).
	for _, s := range readSteps(t, mc.Dir) {
		if s.Seq == tree.Step && !strings.Contains(mustJSON(t, s.Output), `"rendered":"blank"`) {
			t.Errorf("step %d output lost the mark: %s", s.Seq, mustJSON(t, s.Output))
		}
	}

	// No text: no capture.
	writeUI(t, control, `{"app":{"name":"TipSplit","pid":7},"apps":["TipSplit"],"screen":{"width":400,"height":300},
"truncated":false,"elements":[{"role":"AXButton","depth":0,"frame":{"x":220,"y":20,"w":160,"h":16}}]}`)
	before = strings.Count(testsupport.Calls(t, control), "screencapture")
	if _, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(testsupport.Calls(t, control), "screencapture") - before; n != 0 {
		t.Errorf("a read with no text captured the screen %d times", n)
	}

	// A capture that cannot be read: the tree is unmarked and says so.
	writeUI(t, control, `{"app":{"name":"TipSplit","pid":7},"apps":["TipSplit"],"screen":{"width":400,"height":300},
"truncated":false,"elements":[{"role":"AXStaticText","value":"Each pays: $49.56","depth":0,"frame":{"x":20,"y":140,"w":200,"h":30}}]}`)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte("bm90IGEgcG5n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err = mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Elements[0].Rendered != "" || !strings.Contains(tree.Outline(), "(text not checked against the screen: the screen capture could not be decoded") {
		t.Errorf("tree = %+v\n%s", tree, tree.Outline())
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
