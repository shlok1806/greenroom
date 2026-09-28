package summary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// t0 is when every test run starts: the TipSplit run of docs/20's Figma screens.
var t0 = time.Date(2026, 9, 23, 4, 41, 38, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// builder makes an Input the way the daemon records a run: messages numbered in order, the
// verdict worked out by a real conversation store from them.
type builder struct {
	t      *testing.T
	in     Input
	nowSet bool // else now is 10 s after the newest record
}

func newRun(t *testing.T, id string) *builder {
	t.Helper()
	return &builder{t: t, in: Input{RunID: id, CreatedAt: t0}}
}

func (b *builder) named(name, source string) *builder {
	b.in.Name, b.in.Source = name, source
	return b
}

func (b *builder) now(sec int) *builder {
	b.in.Now, b.nowSet = at(sec), true
	return b
}

// latest is the newest time anything in the run records.
func (b *builder) latest() time.Time {
	t := b.in.CreatedAt
	for _, m := range b.in.Messages {
		t = laterOf(t, m.At)
	}
	for _, s := range b.in.Steps {
		t = laterOf(t, s.At)
	}
	for _, f := range b.in.Frames {
		t = laterOf(t, f.At)
	}
	if b.in.EndedAt != nil {
		t = laterOf(t, *b.in.EndedAt)
	}
	return t
}

// live gives the run a live machine in status st.
func (b *builder) live(st machine.Status) *builder {
	b.in.Machine = &LiveMachine{Status: st}
	return b
}

func (b *builder) boot(phases ...string) *builder {
	for i, p := range phases {
		b.in.Machine.Boot = append(b.in.Machine.Boot, machine.BootPhase{Phase: p, At: at(i)})
	}
	return b
}

func (b *builder) controlledBy(seat string) *builder {
	b.in.Machine.Controller = seat
	return b
}

func (b *builder) lowOnFiles() *builder {
	b.in.Machine.LowOnFiles = true
	return b
}

func (b *builder) ended(sec int) *builder {
	e := at(sec)
	b.in.EndedAt = &e
	return b
}

func (b *builder) msg(from session.From, kind session.Kind, text string, sec int, mods ...func(*session.Message)) *builder {
	m := session.Message{Seq: len(b.in.Messages) + 1, At: at(sec), From: from, Kind: kind, Text: text}
	for _, mod := range mods {
		mod(&m)
	}
	b.in.Messages = append(b.in.Messages, m)
	return b
}

func (b *builder) event(text string, sec int) *builder {
	return b.msg(session.System, session.Event, text, sec)
}

func (b *builder) task(text string, sec int) *builder {
	return b.msg(session.Coder, session.Task, text, sec)
}

// plan declares checks by their criteria, as declare_checks does.
func (b *builder) plan(sec int, criteria ...string) *builder {
	checks := make([]session.Check, 0, len(criteria))
	for i, c := range criteria {
		checks = append(checks, session.Check{ID: "c" + string(rune('1'+i)), Criterion: c})
	}
	return b.msg(session.Verifier, session.Progress, "", sec, func(m *session.Message) { m.Checks = checks })
}

// verdict reports outcome with checks, as report_verdict does.
func (b *builder) verdict(outcome string, sec int, checks ...session.Check) *builder {
	return b.msg(session.Verifier, session.Verdict, "The verifier's reasons.", sec, func(m *session.Message) {
		m.Verdict, m.Checks = outcome, checks
	})
}

// lastVerdict is the seq of the newest verdict message.
func (b *builder) lastVerdict() int {
	for i := len(b.in.Messages) - 1; i >= 0; i-- {
		if b.in.Messages[i].Kind == session.Verdict {
			return b.in.Messages[i].Seq
		}
	}
	b.t.Fatal("no verdict to reply to")
	return 0
}

func (b *builder) accept(from session.From, sec int) *builder {
	seq := b.lastVerdict()
	return b.msg(from, session.Accept, "", sec, func(m *session.Message) { m.ReplyTo = seq })
}

func (b *builder) dispute(from session.From, sec int) *builder {
	seq := b.lastVerdict()
	return b.msg(from, session.Dispute, "That is wrong.", sec, func(m *session.Message) { m.ReplyTo = seq })
}

func (b *builder) finish(outcome string, sec int) *builder {
	f := &session.Finish{Outcome: outcome, Summary: "Changed it.", At: at(sec)}
	b.in.Finish = f
	return b.msg(session.System, session.Event, session.FinishText(*f), sec, func(m *session.Message) { m.Finish = f })
}

func (b *builder) step(tool string, sec int, input, output any, err string) *builder {
	st := machine.Step{Seq: len(b.in.Steps) + 1, At: at(sec), Tool: tool, Error: err}
	if input != nil {
		st.Input = decoded(b.t, input)
	}
	if output != nil {
		st.Output = decoded(b.t, output)
	}
	b.in.Steps = append(b.in.Steps, st)
	return b
}

// click is a machine_input step clicking at (x, y).
func (b *builder) click(sec int, x, y float64) *builder {
	return b.step("machine_input", sec, map[string]any{"actions": []map[string]any{{"type": "click", "x": x, "y": y}}}, nil, "")
}

// uiRead is a machine_ui step of app listing elements.
func (b *builder) uiRead(sec int, app string, elements ...machine.UIElement) *builder {
	return b.step("machine_ui", sec, nil, machine.UITree{App: app, Elements: elements}, "")
}

func (b *builder) screenshot(sec int) *builder {
	n := len(b.in.Steps) + 1
	path := filepath.Join("/runs", b.in.RunID, pad3(n)+"-screenshot.png")
	return b.step("machine_screenshot", sec, nil, map[string]any{"path": path, "step": n}, "")
}

func (b *builder) frame(sec, step int) *builder {
	fr := machine.Frame{At: at(sec), File: fmt.Sprintf("%d.jpg", at(sec).UnixMilli()), Step: step}
	b.in.Frames = append(b.in.Frames, fr)
	last := fr
	b.in.LastFrame = &last
	return b
}

func pad3(n int) string { return fmt.Sprintf("%03d", n) }

// decoded is v as steps.jsonl gives it back: plain JSON values.
func decoded(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// build writes the conversation to disk and reads the verdict back through session.Open, so
// the verdict state is the daemon's own.
func (b *builder) build() Input {
	b.t.Helper()
	if !b.nowSet {
		b.in.Now = b.latest().Add(10 * time.Second)
	}
	dir := b.t.TempDir()
	f, err := os.Create(filepath.Join(dir, "conversation.jsonl"))
	if err != nil {
		b.t.Fatal(err)
	}
	for _, m := range b.in.Messages {
		line, err := json.Marshal(m)
		if err != nil {
			b.t.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			b.t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		b.t.Fatal(err)
	}
	store, err := session.Open(dir, session.DefaultMaxDisputes)
	if err != nil {
		b.t.Fatal(err)
	}
	b.in.Verdict = store.Verdict()
	if st := store.Finished(); st != nil && b.in.Finish == nil {
		b.in.Finish = st
	}
	return b.in
}

func (b *builder) derive() Summary {
	b.t.Helper()
	return Derive(b.build())
}

func pass(id, criterion, observed string, evidence ...int) session.Check {
	return session.Check{ID: id, Criterion: criterion, Status: session.CheckPass, Observed: observed, Evidence: evidence}
}

func fail(id, criterion, observed string, evidence ...int) session.Check {
	return session.Check{ID: id, Criterion: criterion, Status: session.CheckFail, Observed: observed, Evidence: evidence}
}

func unchecked(id, criterion string) session.Check {
	return session.Check{ID: id, Criterion: criterion, Status: session.CheckUnchecked, Observed: "Not verified."}
}
