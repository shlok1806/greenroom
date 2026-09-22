package machine

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/image/draw"
)

const (
	// A scrubber frame, not a model input. machine cannot import verifier, so
	// it keeps its own resize (docs/adr/0008-run-recording.md).
	frameWidthCap = 1280
	frameQuality  = 75

	// controlFrameInterval applies while a control lease is live: a hand on
	// the mouse needs faster feedback than a recording (ADR 0009).
	controlFrameInterval = 500 * time.Millisecond
)

// Frame is one line of frames.jsonl. It cites the current step but never
// claims a number of its own.
type Frame struct {
	At    time.Time `json:"at"`
	File  string    `json:"file"` // "<unix-ms>.jpg", relative to <dir>/frames/
	Step  int       `json:"step"`
	Bytes int       `json:"bytes"`
}

// ReadFrames loads a run's frame log. A run with no frames has none.
func ReadFrames(dir string) ([]Frame, error) {
	return readJSONL[Frame](dir, "frames.jsonl")
}

// frameJPEG converts a screenshot PNG to a JPEG no wider than frameWidthCap.
func frameJPEG(pngBytes []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	b := src.Bounds()
	dst := src
	if b.Dx() > frameWidthCap {
		scaled := image.NewRGBA(image.Rect(0, 0, frameWidthCap, b.Dy()*frameWidthCap/b.Dx()))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), src, b, draw.Over, nil)
		dst = scaled
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: frameQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// recordFrames captures the screen until ctx is cancelled or tart exits.
// Failures are logged once per run and never fail it (ADR 0008).
func (m *Manager) recordFrames(ctx context.Context, mc *Machine) {
	dir := filepath.Join(mc.Dir, "frames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.Log.Warn("cannot create the frames directory; recording is off for this run", "runId", mc.RunID, "err", err)
		return
	}
	for ctx.Err() == nil && (mc.proc == nil || !mc.proc.Exited()) {
		m.captureFrame(ctx, mc, dir)
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.captureInterval(mc)):
		}
	}
}

func (m *Manager) captureInterval(mc *Machine) time.Duration {
	if _, held := m.ControlState(mc.RunID); held && m.frameInterval > controlFrameInterval {
		return controlFrameInterval
	}
	return m.frameInterval
}

func (m *Manager) captureFrame(ctx context.Context, mc *Machine, dir string) {
	if err := m.writeFrame(ctx, mc, dir); err != nil && mc.rec.logFrameErrOnce() {
		m.Log.Warn("frame capture failed; will keep retrying silently for this run", "runId", mc.RunID, "err", err)
	}
}

func (m *Manager) writeFrame(ctx context.Context, mc *Machine, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pngBytes, err := m.captureScreen(ctx, mc)
	if err != nil {
		return err
	}
	jpg, err := frameJPEG(pngBytes)
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	file := fmt.Sprintf("%d.jpg", at.UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, file), jpg, 0o644); err != nil {
		return err
	}
	fr := Frame{At: at, File: file, Step: mc.rec.currentStep(), Bytes: len(jpg)}
	if err := mc.rec.appendFrame(fr); err != nil {
		return err
	}
	m.emit(LifecycleEvent{Kind: "frame", RunID: mc.RunID, Frame: &fr})
	return nil
}
