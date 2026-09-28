package desktop

import (
	"math"
	"strings"
)

// ShotArgs are machine_screenshot's toolkit arguments (daemon ADR 0006 point 9): a crop to an
// element (ref, with a margin), to a window (its ref or its title), or to a region of the screen
// in fractions. With none of them the screenshot is the whole screen, as it always was.
type ShotArgs struct {
	Ref    string    `json:"ref,omitempty"`
	Window string    `json:"window,omitempty"`
	Region []float64 `json:"region,omitempty"`
	Margin *int      `json:"margin,omitempty"`
}

// MaxShotMargin is the widest margin a crop takes around its element, in points.
const MaxShotMargin = 200

const regionHelp = "pass [x, y, w, h] as fractions of the screen, 0 to 1, such as [0.25, 0.25, 0.5, 0.5]"

// Crops reports whether the arguments ask for less than the whole screen.
func (a ShotArgs) Crops() bool { return a.Ref != "" || a.Window != "" || len(a.Region) > 0 }

// WindowRef reports whether Window names a window by its ref rather than by its title.
func (a ShotArgs) WindowRef() bool { return refPattern.MatchString(a.Window) }

// Normalize validates: at most one of ref, window and region, a well formed ref, a region inside
// the screen, and a margin only for a ref or a window. A margin over the maximum is clamped.
func (a *ShotArgs) Normalize() error {
	n := 0
	for _, set := range []bool{a.Ref != "", a.Window != "", len(a.Region) > 0} {
		if set {
			n++
		}
	}
	if n > 1 {
		return argErr("ref", "pass one of ref, window or region, not several")
	}
	if a.Ref != "" {
		if err := validateRef("ref", a.Ref); err != nil {
			return err
		}
	}
	if a.Window != "" && strings.TrimSpace(a.Window) == "" {
		return argErr("window", "blank; pass the ref of a window (such as \"e1\") or its title")
	}
	if len(a.Region) > 0 {
		if err := validRegion(a.Region); err != nil {
			return err
		}
	}
	if a.Margin != nil {
		switch {
		case a.Ref == "" && a.Window == "":
			return argErr("margin", "a margin is for a crop to ref or window; leave it out")
		case *a.Margin < 0:
			return argErr("margin", "%d is negative; pass points of margin, 0 to %d", *a.Margin, MaxShotMargin)
		case *a.Margin > MaxShotMargin:
			m := MaxShotMargin
			a.Margin = &m
		}
	}
	return nil
}

func validRegion(r []float64) error {
	if len(r) != 4 {
		return argErr("region", "has %d numbers; %s", len(r), regionHelp)
	}
	for _, v := range r {
		if !finite(v) {
			return argErr("region", "holds a number that is not finite; %s", regionHelp)
		}
	}
	x, y, w, h := r[0], r[1], r[2], r[3]
	const slack = 1e-9
	switch {
	case x < 0 || y < 0 || x > 1 || y > 1:
		return argErr("region", "starts off the screen; %s", regionHelp)
	case w <= 0 || h <= 0:
		return argErr("region", "has no area: w and h must be above 0; %s", regionHelp)
	case x+w > 1+slack || y+h > 1+slack:
		return argErr("region", "reaches past the edge of the screen (x + w and y + h must be at most 1); %s", regionHelp)
	}
	return nil
}

// CaptureOp is the `capture` op's arguments as the agent reads them: a crop is a ref with a
// margin, or a rect in guest points.
type CaptureOp struct {
	Format  string  `json:"format"`
	Quality float64 `json:"quality,omitempty"` // JPEG quality, 0 to 1
	Ref     string  `json:"ref,omitempty"`
	Margin  *int    `json:"margin,omitempty"`
	Rect    *Rect   `json:"rect,omitempty"`
}

// Op is the capture of normalized arguments on screen s, as PNG. A window named by its title has
// no ref yet: the caller resolves it first (ShotArgs.Window becomes a ref) or the op crops nothing.
func (a ShotArgs) Op(s Screen) CaptureOp {
	op := CaptureOp{Format: "png", Margin: a.Margin}
	switch {
	case a.Ref != "":
		op.Ref = a.Ref
	case a.WindowRef():
		op.Ref = a.Window
		if op.Margin == nil {
			zero := 0 // a window is cropped to its own frame
			op.Margin = &zero
		}
	case len(a.Region) == 4:
		w, h := float64(s.Width), float64(s.Height)
		r := Rect{math.Round(a.Region[0] * w), math.Round(a.Region[1] * h), math.Round(a.Region[2] * w), math.Round(a.Region[3] * h)}
		op.Rect = &r
	}
	return op
}
