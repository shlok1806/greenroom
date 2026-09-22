package verifier

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// maxVisionWidth keeps the image small enough to send without losing the
// text a verifier must read. A 2048 pixel screenshot is about 1.4 MB as PNG
// and roughly 175 KB as a 1024 pixel JPEG.
const maxVisionWidth = 1024

// shotGeometry tells a brain what it is looking at.
//
// A model that has just been shown a screen reports what it sees in that
// picture's own pixels, and clicks are posted as a fraction of the screen,
// so the numbers it needs are the picture's width and height. Saying them
// out loud is the difference between a model dividing by the right figure
// and guessing: the guest hands back a Retina image, twice the desktop's
// point size, and a model that assumes the desktop size aims at half the
// position it meant. The scale is included when the daemon knows it, because
// it is the fact that explains the doubling to anyone reading the run later.
func shotGeometry(shot machine.Shot) string {
	if shot.Width <= 0 || shot.Height <= 0 {
		return "The image's size could not be read; give click coordinates as a fraction of the picture you were shown."
	}
	s := fmt.Sprintf("The image is %dx%d pixels", shot.Width, shot.Height)
	if shot.Scale > 0 {
		s += fmt.Sprintf(" at a scale of %g, so the desktop is %gx%g points",
			shot.Scale, float64(shot.Width)/shot.Scale, float64(shot.Height)/shot.Scale)
	}
	return s + ". Give click coordinates as a fraction of that picture, 0 to 1, not in pixels."
}

// toJPEG converts a guest screenshot to a JPEG small enough for the vision
// model, because the endpoint carries the image inline as base64.
func toJPEG(pngBytes []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	b := src.Bounds()
	dst := src
	if b.Dx() > maxVisionWidth {
		h := b.Dy() * maxVisionWidth / b.Dx()
		scaled := image.NewRGBA(image.Rect(0, 0, maxVisionWidth, h))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), src, b, draw.Over, nil)
		dst = scaled
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}
