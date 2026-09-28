package machine

// The toolkit's looks at the tree (daemon ADR 0006): machine_snapshot and machine_find. Like every
// toolkit look (desktopwait.go has the rest) neither takes the lease; each is a look for the
// stale-look rule (issue #124), recorded as its reader's step, and capped at looks().cap whatever
// the caller's ctx allows (daemon ADR 0003).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	imagepng "image/png"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
)

// DesktopSnapshot is a machine_snapshot: the agent's snapshot, and the outline a model reads.
type DesktopSnapshot struct {
	desktop.Snapshot
	Step    int     `json:"step"`
	Seconds float64 `json:"seconds"`
	// Unrendered is why the snapshot's text was not checked against the screen (the ink test,
	// ADR 0027), or empty when it was or had no text to check.
	Unrendered string `json:"unrendered,omitempty"`
	Text       string `json:"-"`
}

// DesktopFind is a machine_find: the matches, and their lines.
type DesktopFind struct {
	desktop.FindResult
	Step    int     `json:"step"`
	Seconds float64 `json:"seconds"`
	Text    string  `json:"-"`
}

// deskLook is the frame every look shares: the machine ready, a step claimed before the call so
// the step is its own (issue #47), the handover count read before it began (issue #124), and the
// call capped. do runs the op on look; it returns the step's output and the look's error.
func (m *Manager) deskLook(ctx context.Context, runID, reader, tool string, input any,
	do func(look context.Context, mc *Machine, seq int) (output any, err error)) (seq int, took time.Duration, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return 0, 0, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return 0, 0, err
	}
	started := time.Now()
	seq = mc.rec.begin()
	at := mc.input.handovers.Load()
	look, cancel := context.WithTimeout(ctx, m.looks().cap)
	output, err := do(look, mc, seq)
	cancel()
	mc.rec.completeAs(seq, reader, nil, tool, input, output, err, started)
	if err == nil {
		mc.noteLook(reader, at)
	}
	m.emitStep(mc.RunID, seq)
	return seq, time.Since(started), err
}

// Snapshot reads the target app's windows as refs (machine_snapshot). The step holds the whole
// structured snapshot. When it has text, the screen is captured once and every element whose
// visible rect shows no ink gets the state notDrawn (the ink test, ADR 0027).
func (m *Manager) Snapshot(ctx context.Context, runID, reader string, a desktop.SnapshotArgs) (DesktopSnapshot, error) {
	if err := a.Normalize(); err != nil {
		return DesktopSnapshot{}, err
	}
	var out DesktopSnapshot
	seq, took, err := m.deskLook(ctx, runID, reader, "machine_snapshot", withReader(a, reader),
		func(look context.Context, mc *Machine, seq int) (any, error) {
			out.Step = seq
			resp, err := m.deskCall(look, mc, guestagent.Request{Op: "snapshot", Args: a, Reader: reader,
				Deadline: m.deskDeadline(m.looks().uiLimit().guest)}, refsOf(append([]string{a.Ref, a.Window}, a.FullText...)...))
			if err = m.deskError(ctx, look, mc, "snapshot", "the snapshot", a.Ref, m.looks().cap, err); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(resp.Result, &out.Snapshot); err != nil {
				return nil, fmt.Errorf("the guest agent's snapshot is unreadable: %w: %.200s", err, resp.Result)
			}
			out.Unrendered = m.inkTest(look, mc, &out.Snapshot)
			return out, nil
		})
	out.Step, out.Seconds = seq, round2(took.Seconds())
	if err != nil {
		return DesktopSnapshot{Step: seq}, err
	}
	out.Text = desktop.Outline(out.Snapshot, desktop.OutlineOptions{Limit: a.Limit})
	if out.Unrendered != "" {
		out.Text += "(text not checked against the screen: " + out.Unrendered + ")\n"
	}
	return out, nil
}

// inkTest marks the snapshot's text elements whose visible rect on a capture of the screen holds
// no ink (StateNotDrawn), reusing the render check's pixel logic (render.go). It returns why
// nothing could be checked, or "" when the snapshot was checked or has no text.
func (m *Manager) inkTest(ctx context.Context, mc *Machine, s *desktop.Snapshot) (unrendered string) {
	frames := make([]pointRect, len(s.Nodes))
	texts := make([]bool, len(s.Nodes))
	some := false
	for i, n := range s.Nodes {
		texts[i] = !unmarkedRoles[n.Role] && !n.Secret() && n.Visible() && (n.Name != "" || n.Value != "")
		frames[i] = pointRect{n.Vis.X(), n.Vis.Y(), n.Vis.W(), n.Vis.H()}
		some = some || texts[i]
	}
	if !some {
		return ""
	}
	data, _, err := m.captureScreen(ctx, mc, true)
	if err != nil {
		return "the screen capture failed: " + err.Error()
	}
	img, err := imagepng.Decode(bytes.NewReader(data))
	if err != nil {
		return "the screen capture could not be decoded: " + err.Error()
	}
	marks, problem := renderStates(img, Screen{Width: s.Screen.Width, Height: s.Screen.Height}, frames, texts, nil, 0)
	if problem != "" {
		return problem
	}
	for i, mark := range marks {
		if mark == RenderedBlank && !s.Nodes[i].Has(desktop.StateNotDrawn) {
			s.Nodes[i].States = append(s.Nodes[i].States, desktop.StateNotDrawn)
		}
	}
	return ""
}

// Find searches the target app's whole tree for text (machine_find), rows scrolled out of view
// included, and returns the matches as refs.
func (m *Manager) Find(ctx context.Context, runID, reader string, a desktop.FindArgs) (DesktopFind, error) {
	if err := a.Normalize(); err != nil {
		return DesktopFind{}, err
	}
	var out DesktopFind
	seq, took, err := m.deskLook(ctx, runID, reader, "machine_find", withReader(a, reader),
		func(look context.Context, mc *Machine, _ int) (any, error) {
			resp, err := m.deskCall(look, mc, guestagent.Request{Op: "find", Args: a, Reader: reader,
				Deadline: m.deskDeadline(m.looks().uiLimit().guest)}, nil)
			if err = m.deskError(ctx, look, mc, "find", "the find", "", m.looks().cap, err); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(resp.Result, &out.FindResult); err != nil {
				return nil, fmt.Errorf("the guest agent's find is unreadable: %w: %.200s", err, resp.Result)
			}
			return out.FindResult, nil
		})
	out.Step, out.Seconds = seq, round2(took.Seconds())
	if err != nil {
		return DesktopFind{Step: seq}, err
	}
	out.Text = desktop.FindText(a, out.FindResult)
	return out, nil
}
