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

// maxVisionWidth keeps screen text legible: a 2048px PNG (~1.4 MB) becomes a
// ~175 KB JPEG.
const maxVisionWidth = 1024

// shotGeometry states the picture's size (and Retina scale, if known) so a
// brain gives click fractions of the picture, not guessed desktop pixels.
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

// toJPEG shrinks a screenshot for the vision model, which takes it inline.
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
