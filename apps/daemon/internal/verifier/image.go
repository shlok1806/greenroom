package verifier

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// maxVisionWidth keeps the image small enough to send without losing the
// text a verifier must read. A 2048 pixel screenshot is about 1.4 MB as PNG
// and roughly 175 KB as a 1024 pixel JPEG.
const maxVisionWidth = 1024

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
