package machine

// The toolkit's waits and evidence (daemon ADR 0006, docs/21 sections 5.5 and 5.7):
// machine_wait_for, machine_expect with its crop, and machine_screenshot cropped to an element, a
// window or a region. Looks, like the snapshot (desktopsnap.go): no lease, a step each.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
)

// DesktopWait is a machine_wait_for.
type DesktopWait struct {
	desktop.WaitResult
	Step    int     `json:"step"`
	Seconds float64 `json:"seconds"`
	Text    string  `json:"-"`
}

// DesktopExpect is a machine_expect: an assertion step (docs/21 section 5.5). Crop is the saved
// crop of its target, when one could be taken.
type DesktopExpect struct {
	desktop.ExpectResult
	Step    int     `json:"step"`
	Seconds float64 `json:"seconds"`
	Crop    string  `json:"crop,omitempty"`
	Text    string  `json:"-"`
}

// DesktopShot is a machine_screenshot cropped to an element, a window or a region.
type DesktopShot struct {
	Shot
	Ref    string        `json:"ref,omitempty"`
	Window string        `json:"window,omitempty"`
	Region []float64     `json:"region,omitempty"`
	Rect   *desktop.Rect `json:"rect,omitempty"` // the crop in guest points, as the agent took it
}

// waitSlack is how much longer than its own timeout a wait or an expectation is given in the
// agent: its trees before and after, and the answer.
const waitSlack = 3 * time.Second

// WaitFor waits in the guest until a target reaches a state (machine_wait_for). A wait that ran
// out is a result (Satisfied false), not an error. The step records the observed value and the
// elapsed time; a secure field's value never.
func (m *Manager) WaitFor(ctx context.Context, runID, reader string, a desktop.WaitArgs) (DesktopWait, error) {
	if err := a.Normalize(); err != nil {
		return DesktopWait{}, err
	}
	var out DesktopWait
	seq, took, err := m.deskLook(ctx, runID, reader, "machine_wait_for", nil,
		func(look context.Context, mc *Machine, _ int) (any, error) {
			wait := time.Duration(a.TimeoutMs)*time.Millisecond + waitSlack
			resp, err := m.deskCall(look, mc, guestagent.Request{Op: "waitFor", Args: a, Reader: reader,
				Deadline: m.deskDeadline(wait)}, refsOf(a.Target.Ref))
			if err = m.deskError(ctx, look, mc, "waitFor", "the wait", a.Target.Ref, m.looks().cap, err); err != nil {
				return map[string]any{"request": withReader(a.Redacted(desktop.WaitResult{}), reader)}, err
			}
			if err := json.Unmarshal(resp.Result, &out.WaitResult); err != nil {
				return nil, fmt.Errorf("the guest agent's wait is unreadable: %w: %.200s", err, resp.Result)
			}
			out.WaitResult = out.Redacted()
			out.Text = desktop.WaitText(a, out.WaitResult)
			return waitEvidence(a, out.WaitResult, out.Text, reader), nil
		})
	out.Step, out.Seconds = seq, round2(took.Seconds())
	if err != nil {
		return DesktopWait{Step: seq}, err
	}
	return out, nil
}

// waitEvidence is a wait's step output (docs/21 section 5.7): the request, what was observed and
// how long it took, the changes since it began, and the text the model read.
func waitEvidence(a desktop.WaitArgs, r desktop.WaitResult, text, reader string) map[string]any {
	out := map[string]any{"request": withReader(a.Redacted(r), reader), "satisfied": r.Satisfied,
		"elapsedMs": r.ElapsedMs, "text": text}
	if r.Node != nil {
		out["node"] = r.Node
	}
	if r.Value != nil {
		out["value"] = *r.Value
	}
	if r.Before != nil && r.After != nil {
		if ch := desktop.Diff(*r.Before, *r.After); len(ch) > 0 {
			out["changes"] = ch
		}
	}
	return out
}

// Expect asserts a property of a target, waiting for it up to its timeout (machine_expect). A
// failed expectation is a result (Passed false): it is evidence too. The step records what was
// expected, what was observed (exact), the elapsed time and the verdict, and a crop of the target
// saved as NNN-expect.jpg when one can be taken; a crop that fails never fails the expectation.
func (m *Manager) Expect(ctx context.Context, runID, reader string, a desktop.ExpectArgs) (DesktopExpect, error) {
	if err := a.Normalize(); err != nil {
		return DesktopExpect{}, err
	}
	var out DesktopExpect
	seq, took, err := m.deskLook(ctx, runID, reader, "machine_expect", nil,
		func(look context.Context, mc *Machine, seq int) (any, error) {
			wait := time.Duration(a.TimeoutMs)*time.Millisecond + waitSlack
			resp, err := m.deskCall(look, mc, guestagent.Request{Op: "expect", Args: a, Reader: reader,
				Deadline: m.deskDeadline(wait)}, refsOf(a.Target.Ref))
			if err = m.deskError(ctx, look, mc, "expect", "the expectation", a.Target.Ref, m.looks().cap, err); err != nil {
				return map[string]any{"request": withReader(a.Redacted(desktop.ExpectResult{}), reader)}, err
			}
			if err := json.Unmarshal(resp.Result, &out.ExpectResult); err != nil {
				return nil, fmt.Errorf("the guest agent's expectation is unreadable: %w: %.200s", err, resp.Result)
			}
			out.ExpectResult = out.Redacted(a.Property)
			out.Text = desktop.ExpectText(a, out.ExpectResult)
			ref := a.Target.Ref
			if out.Node != nil && out.Node.Ref != "" {
				ref = out.Node.Ref
			}
			crop, cropErr := m.expectCrop(look, mc, reader, ref, seq)
			out.Crop = crop
			recorded := a.Redacted(out.ExpectResult)
			evidence := map[string]any{"request": withReader(recorded, reader), "expected": recorded.Expected,
				"observed": out.Observed, "elapsedMs": out.ElapsedMs, "passed": out.Passed, "text": out.Text}
			if out.Node != nil {
				evidence["node"] = out.Node
			}
			if crop != "" {
				evidence["crop"] = crop
			} else if cropErr != "" {
				evidence["cropError"] = cropErr
			}
			return evidence, nil
		})
	out.Step, out.Seconds = seq, round2(took.Seconds())
	if err != nil {
		return DesktopExpect{Step: seq}, err
	}
	return out, nil
}

// expectCropLimit bounds the best-effort crop an expectation saves.
const expectCropLimit = 10 * time.Second

// expectCrop saves a JPEG crop of ref (its visible rect and the agent's margin) as the step's
// NNN-expect.jpg. It is best effort: why it could not is returned instead of an error.
func (m *Manager) expectCrop(ctx context.Context, mc *Machine, reader, ref string, seq int) (path, problem string) {
	if ref == "" {
		return "", "the expectation found no element to crop"
	}
	cctx, cancel := context.WithTimeout(ctx, expectCropLimit)
	defer cancel()
	margin := 24
	data, _, err := m.deskCapture(cctx, mc, reader, desktop.CaptureOp{Format: "jpeg", Quality: 0.8, Ref: ref, Margin: &margin}, expectCropLimit)
	if err != nil {
		return "", err.Error()
	}
	if cfg, format, derr := image.DecodeConfig(bytes.NewReader(data)); derr == nil && format == "png" && cfg.Width > 0 {
		if data, err = frameJPEG(data); err != nil {
			return "", err.Error()
		}
	}
	path = mc.rec.artifactPath(seq, "expect", "jpg")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err.Error()
	}
	return path, ""
}

// deskCapture takes one capture op for reader through the capture gate (one capture per machine
// at a time, daemon ADR 0003), approvals checked first as every capture does.
func (m *Manager) deskCapture(ctx context.Context, mc *Machine, reader string, op desktop.CaptureOp, limit time.Duration) ([]byte, desktop.CaptureResult, error) {
	if err := m.agentStalled(mc, "the screen capture"); err != nil {
		return nil, desktop.CaptureResult{}, err
	}
	g := &mc.input.capture
	epoch, err := g.acquire(ctx, true, m.looks().captureLimit().guest)
	if err != nil {
		return nil, desktop.CaptureResult{}, err
	}
	timedOut, ok := false, false
	defer func() { g.release(epoch, timedOut, ok) }()
	m.ensureCaptureApproval(ctx, mc)
	var refs []string
	if op.Ref != "" {
		refs = []string{op.Ref}
	}
	resp, err := m.deskCall(ctx, mc, guestagent.Request{Op: "capture", Args: op, Reader: reader,
		Deadline: m.deskDeadline(m.looks().captureLimit().guest)}, refs)
	if err != nil {
		err = m.deskError(ctx, ctx, mc, "capture", "the screen capture", op.Ref, limit, err)
		timedOut = errors.Is(err, ErrScreenNotAnswering)
		return nil, desktop.CaptureResult{}, err
	}
	var r desktop.CaptureResult
	_ = json.Unmarshal(resp.Result, &r)
	if len(resp.Blob) == 0 {
		return nil, r, errors.New("the guest agent's capture carried no image")
	}
	ok = true
	return resp.Blob, r, nil
}

// ScreenshotOf is machine_screenshot with the toolkit's crops: to an element (ref, with a margin),
// a window (its ref, or its title, found with a snapshot) or a region of the screen. Without
// any it is ScreenshotAs, the whole screen as always. The crop is saved as NNN-screenshot.png.
func (m *Manager) ScreenshotOf(ctx context.Context, runID, reader string, a desktop.ShotArgs) ([]byte, DesktopShot, error) {
	if err := a.Normalize(); err != nil {
		return nil, DesktopShot{}, err
	}
	if !a.Crops() {
		data, shot, err := m.ScreenshotAs(ctx, runID, reader)
		return data, DesktopShot{Shot: shot}, err
	}
	var data []byte
	out := DesktopShot{Ref: a.Ref, Window: a.Window, Region: a.Region}
	seq, _, err := m.deskLook(ctx, runID, reader, "machine_screenshot", withReader(a, reader),
		func(look context.Context, mc *Machine, seq int) (any, error) {
			out.Step = seq
			screen, err := m.ensureInput(look, mc)
			if err != nil {
				return out, err
			}
			if a.Window != "" && !a.WindowRef() {
				ref, err := m.windowRef(ctx, look, mc, reader, a.Window)
				if err != nil {
					return out, err
				}
				a.Window = ref
				out.Window = ref
			}
			var r desktop.CaptureResult
			data, r, err = m.deskCapture(look, mc, reader, a.Op(deskScreen(screen)), m.looks().cap)
			if err != nil {
				return out, err
			}
			if !r.Rect.IsZero() {
				rect := r.Rect
				out.Rect = &rect
			}
			out.Path = mc.rec.artifactPath(seq, "screenshot", "png")
			if err := os.WriteFile(out.Path, data, 0o644); err != nil {
				return out, err
			}
			out.Bytes = len(data)
			out.Width, out.Height, _ = m.geometryOf(mc, data)
			out.Scale = r.Scale
			return out, nil
		})
	out.Step = seq
	if err != nil {
		return nil, DesktopShot{Shot: Shot{Step: seq}}, err
	}
	return data, out, nil
}

// windowRef finds the ref of the target app's window titled title, with a small snapshot.
func (m *Manager) windowRef(parent, ctx context.Context, mc *Machine, reader, title string) (string, error) {
	args := desktop.SnapshotArgs{Window: title, Limit: 1}
	if err := args.Normalize(); err != nil {
		return "", err
	}
	resp, err := m.deskCall(ctx, mc, guestagent.Request{Op: "snapshot", Args: args, Reader: reader,
		Deadline: m.deskDeadline(m.looks().uiLimit().guest)}, nil)
	if err = m.deskError(parent, ctx, mc, "snapshot", "the window lookup", "", m.looks().cap, err); err != nil {
		return "", err
	}
	var s desktop.Snapshot
	if err := json.Unmarshal(resp.Result, &s); err != nil {
		return "", fmt.Errorf("the guest agent's snapshot is unreadable: %w", err)
	}
	for _, n := range s.Nodes {
		if n.IsWindow() {
			return n.Ref, nil
		}
	}
	return "", &DesktopError{Op: "capture", Code: guestagent.CodeNotFound,
		Text: fmt.Sprintf("no window titled %q; take a machine_snapshot to see the windows, and pass one's ref as window", title)}
}
