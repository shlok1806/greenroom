package machine

import (
	"bytes"
	"context"
	"errors"
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
// Failures are logged once per run and never fail it (ADR 0008). While the guest
// screen does not answer, it backs off (frameBackoff) instead of queueing captures
// behind a hung WindowServer, and logs only when that starts and when it ends
// (daemon ADR 0003).
func (m *Manager) recordFrames(ctx context.Context, mc *Machine) {
	dir := filepath.Join(mc.Dir, "frames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.Log.Warn("cannot create the frames directory; recording is off for this run", "runId", mc.RunID, "err", err)
		return
	}
	backingOff := false
	for ctx.Err() == nil && (mc.proc == nil || !mc.proc.Exited()) {
		m.captureFrame(ctx, mc, dir)
		wait := m.captureInterval(mc)
		streak := mc.input.capture.timeoutStreak()
		switch {
		case streak > 0 && !backingOff:
			backingOff = true
			m.Log.Warn("the guest screen is not answering; frames back off (2 s doubling to 1 min) until a capture works",
				"runId", mc.RunID, "timedOutCaptures", streak)
		case streak == 0 && backingOff:
			backingOff = false
			m.Log.Info("the guest screen answers again; frames are back to their interval", "runId", mc.RunID)
		}
		wait = max(wait, frameBackoff(streak))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (m *Manager) captureInterval(mc *Machine) time.Duration {
	if _, held := m.ControlState(mc.RunID); held && m.frameInterval > controlFrameInterval {
		return controlFrameInterval
	}
	return m.frameInterval
}

// captureFrame logs the run's first failure and the first recovery after it;
// the rest are silent (ADR 0008). A host that sleeps suspends the VM, and the
// first capture after it wakes can fail once with "could not create image
// from display", then work again. A frame skipped because another capture is
// outstanding is no failure, and a timeout is recordFrames' to report.
func (m *Manager) captureFrame(ctx context.Context, mc *Machine, dir string) {
	err := m.writeFrame(ctx, mc, dir)
	switch {
	case errors.Is(err, errCaptureBusy), errors.Is(err, ErrScreenNotAnswering), err != nil && ctx.Err() != nil:
		return
	case err != nil:
		if mc.rec.frameFailed() {
			m.Log.Warn("frame capture failed; will keep retrying, and log once when it recovers", "runId", mc.RunID, "err", err)
		}
		return
	}
	if fails, since, report := mc.rec.frameCaptured(); report {
		m.Log.Info("frame capture recovered; later failures are not logged", "runId", mc.RunID,
			"failedCaptures", fails, "outageSeconds", round1(time.Since(since)))
	}
}

// writeFrame captures one frame unless a capture is already outstanding (errCaptureBusy).
// The capture's own limits bound it (captureScreen), so it needs no deadline here.
func (m *Manager) writeFrame(ctx context.Context, mc *Machine, dir string) error {
	pngBytes, err := m.captureScreen(ctx, mc, false)
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
