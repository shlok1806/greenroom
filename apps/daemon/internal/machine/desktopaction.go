package machine

// The toolkit's actions (daemon ADR 0006): machine_press, machine_type, machine_set_value,
// machine_key and machine_scroll. Each takes the lease per call (deskLease), runs in the agent
// with its actionability checks and auto-wait, and returns its own effect: the step records it as
// Step.Effect of itself, so no UI read follows it. A refusal is the step's error, with the
// structured refusal in its output.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
)

// DesktopAction is a press, type, set_value or key: the agent's result, its effect, and the text a
// model reads. Point is where the pointer went, as fractions of the screen.
type DesktopAction struct {
	Step    int                  `json:"step"`
	Seconds float64              `json:"seconds"`
	Result  desktop.ActionResult `json:"result"`
	Effect  desktop.Effect       `json:"effect"`
	Point   *[2]float64          `json:"point,omitempty"`
	Text    string               `json:"-"`
}

// DesktopScroll is a machine_scroll by ref.
type DesktopScroll struct {
	Step    int                  `json:"step"`
	Seconds float64              `json:"seconds"`
	Result  desktop.ScrollResult `json:"result"`
	Effect  desktop.Effect       `json:"effect"`
	Text    string               `json:"-"`
}

// EffectKind is the action's effect as its step records it: EffectChanged, EffectNone,
// EffectUnknown or EffectQuit.
func (a DesktopAction) EffectKind() string { return stepEffectKind(a.Effect) }

// EffectKind is the scroll's effect as its step records it.
func (s DesktopScroll) EffectKind() string { return stepEffectKind(s.Effect) }

// actionSlack is how much longer than its actionability timeout an action is given in the agent:
// the input, the settle (at most 2 s) and the trees before and after.
const actionSlack = 5 * time.Second

// Press presses an element by ref, or a point with a reason (machine_press).
func (m *Manager) Press(ctx context.Context, runID, holder string, a desktop.PressArgs) (DesktopAction, error) {
	if err := a.Normalize(); err != nil {
		return DesktopAction{}, err
	}
	act := desktop.Action{Op: desktop.OpPress, Ref: a.Ref, Button: a.Button, Count: a.Count, Mods: a.Mods}
	return m.act(ctx, runID, holder, "machine_press", act, a.TimeoutMs, 0, refsOf(a.Ref),
		func(s Screen) any { return a.Op(deskScreen(s)) })
}

// Type types text into an element by ref, or into the focus, paced, and reads it back
// (machine_type).
func (m *Manager) Type(ctx context.Context, runID, holder string, a desktop.TypeArgs) (DesktopAction, error) {
	if err := a.Normalize(); err != nil {
		return DesktopAction{}, err
	}
	act := desktop.Action{Op: desktop.OpType, Ref: a.Ref, Text: a.Text, Via: a.Via, Replace: a.Replace, Submit: a.Submit}
	// Typing takes its pace for every character on top of the checks.
	typing := time.Duration(utf8.RuneCountInString(a.Text)) * time.Duration(a.Pace()+10) * time.Millisecond
	return m.act(ctx, runID, holder, "machine_type", act, a.TimeoutMs, typing, refsOf(a.Ref),
		func(Screen) any { return a.Op() })
}

// SetValue sets an element's value through accessibility (machine_set_value).
func (m *Manager) SetValue(ctx context.Context, runID, holder string, a desktop.SetValueArgs) (DesktopAction, error) {
	if err := a.Normalize(); err != nil {
		return DesktopAction{}, err
	}
	act := desktop.Action{Op: desktop.OpSetValue, Ref: a.Ref, Value: a.Value}
	return m.act(ctx, runID, holder, "machine_set_value", act, a.TimeoutMs, 0, refsOf(a.Ref),
		func(Screen) any { return a })
}

// Key presses a key, into an element by ref or into the frontmost app (machine_key).
func (m *Manager) Key(ctx context.Context, runID, holder string, a desktop.KeyArgs) (DesktopAction, error) {
	if err := a.Normalize(); err != nil {
		return DesktopAction{}, err
	}
	act := desktop.Action{Op: desktop.OpKey, Ref: a.Ref, Key: a.Key, Mods: a.Mods}
	return m.act(ctx, runID, holder, "machine_key", act, a.TimeoutMs, 0, refsOf(a.Ref),
		func(Screen) any { return a })
}

// act runs one action op for holder under its per-call lease and records it. timeoutMs is the
// actionability timeout, extra any time the op takes beyond the checks and the settle (typing),
// refs the refs it names, and wire its arguments as the agent reads them on screen s.
func (m *Manager) act(ctx context.Context, runID, holder, tool string, act desktop.Action, timeoutMs int,
	extra time.Duration, refs []string, wire func(s Screen) any) (DesktopAction, error) {
	mc, err := m.get(runID)
	if err != nil {
		return DesktopAction{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return DesktopAction{}, err
	}
	release, err := m.deskLease(mc, holder)
	if err != nil {
		return DesktopAction{}, err
	}
	defer release()
	screen, err := m.ensureInput(ctx, mc)
	if err != nil {
		return DesktopAction{}, err
	}

	started := time.Now()
	seq := mc.rec.begin()
	out := DesktopAction{Step: seq}
	look, cancel := context.WithTimeout(ctx, m.looks().cap)
	defer cancel()
	want := time.Duration(timeoutMs)*time.Millisecond + actionSlack + extra
	resp, err := m.deskCall(look, mc, guestagent.Request{Op: act.Op, Args: wire(screen), Reader: holder, Input: true,
		Deadline: m.deskDeadline(want)}, refs)
	what := "the " + tool
	err = m.deskError(ctx, look, mc, act.Op, what, act.Ref, m.looks().cap, err)
	if err == nil {
		if uerr := json.Unmarshal(resp.Result, &out.Result); uerr != nil {
			err = fmt.Errorf("the guest agent's %s result is unreadable (the input may have been posted): %w: %.200s", act.Op, uerr, resp.Result)
		}
	}
	var effect *StepEffect
	var output any
	if err != nil {
		// Without a result nothing says whether the target was a secure field, so the text and the
		// value are recorded as their lengths only (catalog I15).
		output = actionFailure(act.Redacted(desktop.ActionResult{Secret: true}), err)
	} else {
		out.Effect = desktop.EffectOf(out.Result)
		out.Point = fractions(out.Result.Point, screen)
		out.Text = desktop.ActionText(act, out.Result, deskScreen(screen))
		effect = &StepEffect{Of: seq, Kind: stepEffectKind(out.Effect), Summary: out.Effect.Text()}
		output = actionEvidence(act.Redacted(out.Result), out, screen)
	}
	recorded := act.Redacted(desktop.ActionResult{Secret: true})
	if err == nil {
		recorded = act.Redacted(out.Result)
	}
	out.Seconds = round2(time.Since(started).Seconds())
	mc.rec.completeAs(seq, holder, effect, tool, actionRecord(recorded, holder, timeoutMs), output, err, started)
	m.emitStep(mc.RunID, seq)
	if err != nil {
		return DesktopAction{Step: seq, Seconds: out.Seconds}, err
	}
	return out, nil
}

// stepEffectKind is an effect as a step records it; an app that quit is EffectQuit (ADR 0028), so
// the verdict review's quit rule reads a toolkit action as it reads an effect read.
func stepEffectKind(e desktop.Effect) string {
	switch {
	case e.AppGone:
		return EffectQuit
	case e.Kind == desktop.EffectChanged:
		return EffectChanged
	case e.Kind == desktop.EffectNone:
		return EffectNone
	}
	return EffectUnknown
}

// actionRecord is an action's request as its step records it: never the raw text or value (the
// caller passes the redacted action), with the seat and the timeout.
func actionRecord(a desktop.Action, reader string, timeoutMs int) map[string]any {
	out := map[string]any{"op": a.Op, "reader": reader, "timeoutMs": timeoutMs}
	for k, v := range map[string]string{"ref": a.Ref, "button": a.Button, "text": a.Text, "via": a.Via,
		"submit": a.Submit, "value": a.Value, "key": a.Key} {
		if v != "" {
			out[k] = v
		}
	}
	if a.Op == desktop.OpSetValue {
		out["value"] = a.Value // an empty value clears the field, which is worth recording
	}
	if a.Count > 1 {
		out["count"] = a.Count
	}
	if len(a.Mods) > 0 {
		out["mods"] = a.Mods
	}
	if a.Replace {
		out["replace"] = true
	}
	return out
}

// actionEvidence is an action's step output (docs/21 section 5.7, wave 1's part): the request
// (redacted), the resolved target, the point used, the checks with their waits, the agent's notes,
// and the effect with the text the model saw. The trees themselves are left out: the effect's
// changes are what they say.
func actionEvidence(act desktop.Action, out DesktopAction, screen Screen) map[string]any {
	r := out.Result
	ev := map[string]any{"request": actionRecord(act, "", 0), "effect": out.Effect, "text": out.Text, "screen": screen}
	delete(ev["request"].(map[string]any), "reader")
	delete(ev["request"].(map[string]any), "timeoutMs")
	if r.Target != nil {
		ev["target"] = r.Target
	}
	if out.Point != nil {
		ev["point"] = out.Point
		ev["pointsTried"] = len(r.Tried)
	}
	for k, v := range map[string]any{"via": r.Via, "typed": r.Typed} {
		if v != "" {
			ev[k] = v
		}
	}
	if len(r.Checks) > 0 {
		ev["checks"] = r.Checks
	}
	if r.WaitedMs > 0 {
		ev["waitedMs"] = r.WaitedMs
	}
	if len(r.Notes) > 0 {
		ev["notes"] = r.Notes
	}
	if r.Overlay != nil {
		ev["overlay"] = r.Overlay
	}
	if r.ReadBack != nil {
		ev["readBack"] = *r.ReadBack
	}
	if r.ReadBackOK != nil {
		ev["readBackOK"] = *r.ReadBackOK
	}
	if r.ReResolved {
		ev["reResolved"] = true
	}
	if r.IsSecret() {
		ev["secret"] = true
	}
	return ev
}

// actionFailure is a failed action's step output: the request, and the refusal when a check
// refused it, so the bench can count its class.
func actionFailure(act desktop.Action, err error) map[string]any {
	out := map[string]any{"request": actionRecord(act, "", 0)}
	delete(out["request"].(map[string]any), "reader")
	delete(out["request"].(map[string]any), "timeoutMs")
	var de *DesktopError
	if errors.As(err, &de) {
		out["code"] = de.Code
		if de.Refusal != nil {
			out["refusal"] = de.Refusal
		}
		if de.Target != nil {
			out["target"] = de.Target
		}
		if len(de.Candidates) > 0 {
			out["candidates"] = de.Candidates
		}
	}
	return out
}

// Scroll scrolls a container, or the one around an element, to a place (machine_scroll by ref).
func (m *Manager) Scroll(ctx context.Context, runID, holder string, a desktop.ScrollArgs) (DesktopScroll, error) {
	if err := a.Normalize(); err != nil {
		return DesktopScroll{}, err
	}
	mc, err := m.get(runID)
	if err != nil {
		return DesktopScroll{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return DesktopScroll{}, err
	}
	release, err := m.deskLease(mc, holder)
	if err != nil {
		return DesktopScroll{}, err
	}
	defer release()

	started := time.Now()
	seq := mc.rec.begin()
	out := DesktopScroll{Step: seq}
	look, cancel := context.WithTimeout(ctx, m.looks().cap)
	defer cancel()
	want := time.Duration(a.TimeoutMs)*time.Millisecond + actionSlack
	resp, err := m.deskCall(look, mc, guestagent.Request{Op: "scroll", Args: a, Reader: holder, Input: true,
		Deadline: m.deskDeadline(want)}, refsOf(a.Ref, a.To.Ref))
	err = m.deskError(ctx, look, mc, "scroll", "the machine_scroll", a.Ref, m.looks().cap, err)
	if err == nil {
		if uerr := json.Unmarshal(resp.Result, &out.Result); uerr != nil {
			err = fmt.Errorf("the guest agent's scroll result is unreadable (it may have scrolled): %w: %.200s", uerr, resp.Result)
		}
	}
	request := withReader(a, holder)
	var effect *StepEffect
	var output any
	if err != nil {
		output = actionFailure(desktop.Action{Op: "scroll", Ref: a.Ref}, err)
	} else {
		out.Effect = desktop.EffectOfScroll(out.Result)
		out.Text = desktop.ScrollText(out.Result)
		effect = &StepEffect{Of: seq, Kind: stepEffectKind(out.Effect), Summary: out.Text}
		ev := map[string]any{"request": withReader(a, ""), "container": out.Result.Container, "from": out.Result.From,
			"to": out.Result.To, "atEnd": out.Result.AtEnd, "steps": out.Result.Steps, "via": out.Result.Via,
			"visible": out.Result.Visible, "effect": out.Effect, "text": out.Text}
		if out.Result.Target != nil {
			ev["target"] = out.Result.Target
		}
		output = ev
	}
	out.Seconds = round2(time.Since(started).Seconds())
	mc.rec.completeAs(seq, holder, effect, "machine_scroll", request, output, err, started)
	m.emitStep(mc.RunID, seq)
	if err != nil {
		return DesktopScroll{Step: seq, Seconds: out.Seconds}, err
	}
	return out, nil
}

// errVerifierTurn refuses the coder's input while greenroom's verifier is in a turn (issue #82).
var errVerifierTurn = errors.New("greenroom's verifier is in the middle of a turn on this machine and is using " +
	"the screen; wait for its reply or verdict with agent_wait, or send a note, then try again")

// errDeskStaleLook is ErrStaleLook in the toolkit's words: its looks are snapshots.
var errDeskStaleLook = fmt.Errorf("%w (machine_snapshot, machine_find, machine_wait_for, machine_expect and machine_screenshot are looks too)", ErrStaleLook)

// deskScreen is s as internal/desktop takes it.
func deskScreen(s Screen) desktop.Screen { return desktop.Screen{Width: s.Width, Height: s.Height} }

// fractions is a guest point as fractions of screen s, rounded like every tool's coordinates.
func fractions(p *desktop.Point, s Screen) *[2]float64 {
	if p == nil {
		return nil
	}
	x, y := p.Fractions(deskScreen(s))
	return &[2]float64{round3(x), round3(y)}
}

// deskLease takes holder's per-call lease for one toolkit action, as InputAs does (root ADR 0009,
// daemon ADR 0006 point 11): the coder is refused during a verifier turn (#82), another seat's
// lease is a ScreenTakenError, and the verifier is refused after a handover until it looks again
// (#124). release gives back a lease this call took and never one it did not: a human who took
// the screen during the action keeps it (see TakeControl).
func (m *Manager) deskLease(mc *Machine, holder string) (release func(), err error) {
	if holder == HolderCoder && m.inVerifierTurn(mc.RunID) {
		return nil, errVerifierTurn
	}
	mc.input.asMu.Lock()
	current, fresh, err := m.TakeControl(mc.RunID, holder, 0)
	if errors.Is(err, ErrControlHeld) {
		mc.input.asMu.Unlock()
		return nil, &ScreenTakenError{Holder: current.Holder, Until: current.Expires}
	}
	if err != nil {
		mc.input.asMu.Unlock()
		return nil, err
	}
	release = func() {
		if fresh {
			// Refused, and so harmless, when another seat holds the screen by now.
			_, _, _ = m.ReleaseControl(mc.RunID, holder)
		}
		mc.input.asMu.Unlock()
	}
	if holder == HolderVerifier && mc.staleLook(holder) {
		release()
		return nil, errDeskStaleLook
	}
	if err := m.claimActions(mc, holder, 1); err != nil {
		release()
		if c, held := m.ControlState(mc.RunID); held && c.Holder != holder {
			return nil, &ScreenTakenError{Holder: c.Holder, Until: c.Expires}
		}
		return nil, err
	}
	return release, nil
}
