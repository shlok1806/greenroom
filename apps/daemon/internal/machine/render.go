package machine

import (
	"bytes"
	"context"
	"image"
	imagepng "image/png"
	"math"
	"strings"
	"sync"
)

// Text that is not drawn (ADR 0027). The accessibility tree says what an app claims is on the
// screen; text drawn in the background colour, off the display or under another window is in
// the tree all the same. After each UI read with text in it, the daemon captures the screen
// once and marks every text element whose frame shows nothing a person could read.

// Rendered marks on a UIElement.
const (
	RenderedBlank     = "blank"     // its frame on the screen holds no ink
	RenderedOffscreen = "offscreen" // its frame is outside the display
	RenderedCovered   = "covered"   // another app's window is over its whole frame
)

// renderedMarks is how Outline shows each mark.
var renderedMarks = map[string]string{
	RenderedBlank:     "[not drawn]",
	RenderedOffscreen: "[offscreen]",
	RenderedCovered:   "[covered]",
}

// The ink test. A pixel is ink when one of its channels differs from the frame's most common
// colour (its background) by more than inkDelta of 255; a frame is blank with fewer than minInk
// ink pixels. Secondary and placeholder text differ from their background by 60 or more, so
// 24 keeps them; text drawn in the background colour, or within a few levels of it, has none.
const (
	inkDelta = 24
	minInk   = 4
)

// unmarkedRoles are containers whose frame is a whole window or pane: a title there is not
// text drawn in that frame.
var unmarkedRoles = map[string]bool{
	"Window": true, "Application": true, "ScrollArea": true, "SplitGroup": true, "Sheet": true,
	"Drawer": true, "Browser": true, "Outline": true, "Table": true, "List": true, "WebArea": true,
}

// systemOwners draw over every app on purpose (menu bar, Dock, Control Center); their windows
// never count as covering an element. Some span the whole screen and draw nothing there.
func systemOwner(owner string) bool { return allowedOwners[owner] || desktopOwners[owner] }

// pointRect is a frame in screen points, top-left origin.
type pointRect struct{ X, Y, W, H float64 }

// hasText reports whether element i of raw says something a person reads.
func (raw rawUITree) hasText(i int) bool {
	e := raw.Elements[i]
	return !unmarkedRoles[strings.TrimPrefix(e.Role, "AX")] && (e.Title != "" || e.Label != "" || e.Value != "")
}

// markRendered captures the screen and the window list once and marks tree's text elements
// that are not drawn. A read with no text element costs nothing; a capture that fails leaves
// the tree unmarked and says why in Unrendered.
func (m *Manager) markRendered(ctx context.Context, mc *Machine, raw rawUITree, tree *UITree) {
	texts := make([]bool, len(raw.Elements))
	some := false
	for i := range raw.Elements {
		texts[i] = raw.hasText(i)
		some = some || texts[i]
	}
	if !some {
		return
	}
	var desk Desktop
	var deskErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		desk, deskErr = readDesktop(ctx, m.tart, mc.Name)
	}()
	data, err := m.captureScreen(ctx, mc, true)
	wg.Wait()
	if err != nil {
		tree.Unrendered = "the screen capture failed: " + err.Error()
		return
	}
	img, err := imagepng.Decode(bytes.NewReader(data))
	if err != nil {
		tree.Unrendered = "the screen capture could not be decoded: " + err.Error()
		return
	}
	if deskErr != nil {
		m.Log.Debug("no window list for the covered check", "runId", mc.RunID, "err", deskErr)
		desk.Windows = nil
	}
	frames := make([]pointRect, len(raw.Elements))
	for i, e := range raw.Elements {
		frames[i] = pointRect{e.Frame.X, e.Frame.Y, e.Frame.W, e.Frame.H}
	}
	marks, problem := renderStates(img, raw.Screen, frames, texts, desk.Windows, raw.App.PID)
	if problem != "" {
		tree.Unrendered = problem
		return
	}
	for i, mark := range marks {
		tree.Elements[i].Rendered = mark
	}
}

// renderStates marks each text element (texts[i]) of frames, in points of screen, against img,
// a capture of that screen, and windows, the on-screen windows front to back, where pid's are
// the app's own. problem is why nothing could be marked.
func renderStates(img image.Image, screen Screen, frames []pointRect, texts []bool, windows []DesktopWindow, pid int) (marks []string, problem string) {
	b := img.Bounds()
	if screen.Width <= 0 || screen.Height <= 0 || b.Dx() <= 0 {
		return nil, "the screen size is unknown"
	}
	// Image pixels per point: 1 on the tahoe guest, 2 on a Retina display. Measured, never assumed.
	scale := float64(b.Dx()) / float64(screen.Width)
	if math.Abs(float64(b.Dy())/float64(screen.Height)-scale) > 0.02*scale {
		return nil, "the capture's shape does not match the screen"
	}
	marks = make([]string, len(frames))
	for i, f := range frames {
		if !texts[i] {
			continue
		}
		switch {
		case f.X+f.W <= 0 || f.Y+f.H <= 0 || f.X >= float64(screen.Width) || f.Y >= float64(screen.Height):
			marks[i] = RenderedOffscreen
		case covered(f, windows, pid):
			marks[i] = RenderedCovered
		case blankAt(img, f, scale):
			marks[i] = RenderedBlank
		}
	}
	return marks, ""
}

// covered reports whether a window in front of the app's window that holds f covers all of f.
// The app's window is its frontmost one holding f's center; windows in front of it that are the
// system's (menu bar, Dock) or draw nothing (alpha 0) do not count. With no such window of the
// app in the list, nothing is known and f is not covered.
func covered(f pointRect, windows []DesktopWindow, pid int) bool {
	cx, cy := f.X+f.W/2, f.Y+f.H/2
	own := -1
	for i, w := range windows {
		if w.PID == pid && cx >= w.X && cx <= w.X+w.Width && cy >= w.Y && cy <= w.Y+w.Height {
			own = i
			break
		}
	}
	for _, w := range windows[:max(own, 0)] {
		if w.PID == pid || w.Alpha <= 0 || w.Width <= 1 || w.Height <= 1 || systemOwner(w.Owner) {
			continue
		}
		const slack = 0.5
		if f.X >= w.X-slack && f.Y >= w.Y-slack && f.X+f.W <= w.X+w.Width+slack && f.Y+f.H <= w.Y+w.Height+slack {
			return true
		}
	}
	return false
}

// blankAt reports whether frame f (points) holds no ink in img, at scale pixels per point. The
// frame is inset by a point so a neighbour's edge or focus ring does not count as its ink. A
// frame too small to judge is not blank.
func blankAt(img image.Image, f pointRect, scale float64) bool {
	inset := max(1, int(math.Round(scale)))
	r := image.Rect(int(math.Floor(f.X*scale))+inset, int(math.Floor(f.Y*scale))+inset,
		int(math.Ceil((f.X+f.W)*scale))-inset, int(math.Ceil((f.Y+f.H)*scale))-inset).Intersect(img.Bounds())
	if r.Dx() < 3 || r.Dy() < 3 {
		return false
	}
	return inkPixels(img, r, minInk) < minInk
}

// inkPixels counts the pixels of r that differ from its most common colour by more than
// inkDelta in any channel, stopping at enough.
func inkPixels(img image.Image, r image.Rectangle, enough int) int {
	// The background is the most common colour at 5 bits a channel. A large frame is sampled
	// on a grid for it; the ink count reads every pixel.
	step := 1
	for r.Dx()*r.Dy()/(step*step) > 40000 {
		step++
	}
	counts := map[uint16]int{}
	sums := map[uint16][3]int{}
	for y := r.Min.Y; y < r.Max.Y; y += step {
		for x := r.Min.X; x < r.Max.X; x += step {
			c := rgb8(img, x, y)
			k := uint16(c[0]>>3)<<10 | uint16(c[1]>>3)<<5 | uint16(c[2]>>3)
			counts[k]++
			s := sums[k]
			s[0], s[1], s[2] = s[0]+int(c[0]), s[1]+int(c[1]), s[2]+int(c[2])
			sums[k] = s
		}
	}
	var bg uint16
	best := -1
	for k, n := range counts {
		if n > best || n == best && k < bg {
			bg, best = k, n
		}
	}
	s := sums[bg]
	mean := [3]int{s[0] / best, s[1] / best, s[2] / best}
	ink := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := rgb8(img, x, y)
			for ch := range 3 {
				if d := int(c[ch]) - mean[ch]; d > inkDelta || d < -inkDelta {
					ink++
					break
				}
			}
			if ink >= enough {
				return ink
			}
		}
	}
	return ink
}

// rgb8 is the colour of img at x, y, 8 bits a channel.
func rgb8(img image.Image, x, y int) [3]uint8 {
	switch p := img.(type) {
	case *image.NRGBA:
		i := p.PixOffset(x, y)
		return [3]uint8{p.Pix[i], p.Pix[i+1], p.Pix[i+2]}
	case *image.RGBA:
		i := p.PixOffset(x, y)
		return [3]uint8{p.Pix[i], p.Pix[i+1], p.Pix[i+2]}
	}
	r, g, b, _ := img.At(x, y).RGBA()
	return [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}
