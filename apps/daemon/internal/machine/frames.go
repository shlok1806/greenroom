package machine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

// frameWidthCap and frameQuality bound the JPEG each frame is stored as.
// This is a scrubber frame, not a vision-model input, so it can afford to be
// bigger and coarser than internal/verifier's own PNG-to-JPEG conversion;
// machine sits below verifier in the layering (see CLAUDE.md), so it cannot
// import that package and keeps its own small copy of the resize logic
// instead.
const (
	frameWidthCap = 1280
	frameQuality  = 75

	// controlFrameInterval is how often the screen is captured while a person
	// is driving it (ADR 0009). A recording made for a reviewer can afford to
	// be two seconds behind; a hand on a mouse cannot, because the picture is
	// the only feedback there is. It applies only while a lease is live, so
	// the cost is paid exactly while someone is watching for it.
	controlFrameInterval = 500 * time.Millisecond
)

// Frame is one line of frames.jsonl: a screen capture taken while a machine
// was ready. It is evidence, not a step: it never claims a number in
// steps.jsonl, only cites the one that was current when it was taken.
type Frame struct {
	At    time.Time `json:"at"`
	File  string    `json:"file"` // "<unix-ms>.jpg", relative to <dir>/frames/
	Step  int       `json:"step"`
	Bytes int       `json:"bytes"`
}

// ReadFrames loads a run's frame log, the way ReadSteps loads its step log.
// A run with no frames yet, or a run recorded with -frame-interval 0, has
// none.
func ReadFrames(dir string) ([]Frame, error) {
	f, err := os.Open(filepath.Join(dir, "frames.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return []Frame{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	frames := []Frame{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var fr Frame
		if err := json.Unmarshal(sc.Bytes(), &fr); err != nil {
			return nil, fmt.Errorf("parse frames.jsonl line %d: %w", len(frames)+1, err)
		}
		frames = append(frames, fr)
	}
	return frames, sc.Err()
}

// frameJPEG converts a guest screenshot PNG to a JPEG capped at
// frameWidthCap, small enough that a ten minute run's timelapse stays in the
// tens of megabytes (docs/adr/0008-run-recording.md).
func frameJPEG(pngBytes []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	b := src.Bounds()
	dst := src
	if b.Dx() > frameWidthCap {
		h := b.Dy() * frameWidthCap / b.Dx()
		scaled := image.NewRGBA(image.Rect(0, 0, frameWidthCap, h))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), src, b, draw.Over, nil)
		dst = scaled
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: frameQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// recordFrames captures the guest screen every frameInterval, from the
// moment the machine is ready until ctx is cancelled (Destroy, see
// startFrames in manager.go) or the tart process exits. It is evidence
// alongside steps.jsonl, not a step itself: it claims no step number and
// never writes to steps.jsonl.
//
// A capture failure is logged once for the run and otherwise swallowed: per
// ADR 0008, "capture pauses when it cannot help" but never fails the run.
func (m *Manager) recordFrames(ctx context.Context, mc *Machine) {
	dir := filepath.Join(mc.Dir, "frames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.Log.Warn("cannot create the frames directory; recording is off for this run", "runId", mc.RunID, "err", err)
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if mc.proc != nil && mc.proc.Exited() {
			return
		}
		m.captureFrame(ctx, mc, dir)
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.captureInterval(mc)):
		}
	}
}

// captureInterval is how long to wait before the next capture: the run's
// configured interval normally, and controlFrameInterval while somebody holds
// the screen, which needs the picture to keep up with their hand.
func (m *Manager) captureInterval(mc *Machine) time.Duration {
	if _, held := m.ControlState(mc.RunID); held && m.frameInterval > controlFrameInterval {
		return controlFrameInterval
	}
	return m.frameInterval
}

// captureFrame takes and records one frame. Every error path logs at most
// once per run (recorder.logFrameErrOnce) and returns without touching
// frames.jsonl, so a bad capture this interval does not stop the next one.
func (m *Manager) captureFrame(ctx context.Context, mc *Machine, dir string) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pngBytes, err := m.captureScreen(cctx, mc)
	if err != nil {
		m.logFrameCaptureErr(mc, err)
		return
	}
	jpg, err := frameJPEG(pngBytes)
	if err != nil {
		m.logFrameCaptureErr(mc, err)
		return
	}
	at := time.Now().UTC()
	file := fmt.Sprintf("%d.jpg", at.UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, file), jpg, 0o644); err != nil {
		m.logFrameCaptureErr(mc, err)
		return
	}
	fr := Frame{At: at, File: file, Step: mc.rec.currentStep(), Bytes: len(jpg)}
	if err := mc.rec.appendFrame(fr); err != nil {
		m.logFrameCaptureErr(mc, err)
		return
	}
	m.emit(LifecycleEvent{Kind: "frame", RunID: mc.RunID, Frame: &fr})
}

// logFrameCaptureErr logs a frame-capture failure once per run.
func (m *Manager) logFrameCaptureErr(mc *Machine, err error) {
	if mc.rec.logFrameErrOnce() {
		m.Log.Warn("frame capture failed; will keep retrying silently for this run", "runId", mc.RunID, "err", err)
	}
}
