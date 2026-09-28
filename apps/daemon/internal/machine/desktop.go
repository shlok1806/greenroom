package machine

// The desktop toolkit's calls (daemon ADR 0006): snapshots, finds, actions by ref, waits and
// expectations, sent to the guest agent and recorded as steps. The agent does the mechanics and
// internal/desktop the meaning; this file is what they share here: the one way a toolkit op goes
// to the agent (deskCall), the refs' connection generations (daemon ADR 0005 point 10), the lease
// an action takes, and the agent's errors in words a model acts on. desktopsnap.go has the
// snapshot and find, desktopaction.go the actions, desktopwait.go the waits, expectations and
// cropped screenshots.
//
// Toolkit ops never fall back to a tart exec (daemon ADR 0005 point 13): an action without its
// actionability checks is what the toolkit removes, and a snapshot without the agent has no refs.
// With the channel down they fail with guestagent.ErrUnavailable, which says to try again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
)

// deskState is a machine's toolkit bookkeeping, for each reader: which agent connection handed
// out its latest refs, the highest ref number it has been handed on this machine over every
// connection, and the connection whose counter was raised past that. It lives in inputState and
// has its own lock.
type deskState struct {
	mu     sync.Mutex
	refs   map[string]refOrigin
	high   map[string]int
	raised map[string]refOrigin
}

// saw records that reader was handed refs up to e<n>.
func (d *deskState) saw(reader string, n int) {
	if n <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.high == nil {
		d.high = map[string]int{}
	}
	d.high[reader] = max(d.high[reader], n)
}

// raiseFor is the `next` a refs op must send on connection o before reader's call, and false
// when none is needed: o's counter was raised already, or reader never held a ref on this
// machine (then o counts as raised, since nothing can repeat).
func (d *deskState) raiseFor(reader string, o refOrigin) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.raised[reader] == o {
		return 0, false
	}
	if d.high[reader] == 0 {
		d.markLocked(reader, o)
		return 0, false
	}
	return d.high[reader] + 1, true
}

// raisedOn records that o's counter for reader is past every ref reader was handed.
func (d *deskState) raisedOn(reader string, o refOrigin) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.markLocked(reader, o)
}

func (d *deskState) markLocked(reader string, o refOrigin) {
	if d.raised == nil {
		d.raised = map[string]refOrigin{}
	}
	d.raised[reader] = o
}

// refOrigin is a connection: its boot's supervisor (a reboot starts another) and its generation.
type refOrigin struct {
	sup *guestagent.Supervisor
	gen uint64
}

// origin is the connection reader's latest refs came from, and false when it has none.
func (d *deskState) origin(reader string) (refOrigin, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	o, ok := d.refs[reader]
	return o, ok
}

// note records that reader's latest refs came from o.
func (d *deskState) note(reader string, o refOrigin) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refs == nil {
		d.refs = map[string]refOrigin{}
	}
	d.refs[reader] = o
}

// ErrActionRefused matches a toolkit action an actionability check refused (daemon ADR 0006
// point 4): nothing was posted.
var ErrActionRefused = errors.New("the action was refused by an actionability check")

// ErrStaleRef matches a ref the agent no longer knows: its element is gone, or it came from an
// earlier connection to the agent.
var ErrStaleRef = errors.New("the ref is stale")

// DesktopError is a toolkit op the guest agent answered with an error, in words a model acts on.
// Refusal is set for a refusal (code refused), Candidates for an ambiguous target.
type DesktopError struct {
	Op         string
	Code       string
	Text       string
	Refusal    *desktop.Refusal
	Target     *desktop.Node
	Candidates []desktop.Node
	agent      *guestagent.Error
}

func (e *DesktopError) Error() string { return e.Text }

// Unwrap gives the agent's own error, when there was one.
func (e *DesktopError) Unwrap() error {
	if e.agent == nil {
		return nil
	}
	return e.agent
}

// Is makes errors.Is(err, ErrActionRefused) and errors.Is(err, ErrStaleRef) true for their codes.
func (e *DesktopError) Is(target error) bool {
	switch target {
	case ErrActionRefused:
		return e.Code == guestagent.CodeRefused
	case ErrStaleRef:
		return e.Code == guestagent.CodeStaleRef
	}
	return false
}

// staleConnectionError is a ref from an earlier connection to the agent (daemon ADR 0005 point 10).
func staleConnectionError(op, ref string) *DesktopError {
	return &DesktopError{Op: op, Code: guestagent.CodeStaleRef,
		Text: fmt.Sprintf("%s is from an earlier connection to the guest agent (it restarted); take a new machine_snapshot", ref)}
}

// deskDeadline is the agent deadline for an op that wants want: never past the call's cap less
// the channel's grace, so the agent's own answer (with what it knows) arrives before the cap.
func (m *Manager) deskDeadline(want time.Duration) time.Duration {
	ceiling := m.looks().cap - 3*time.Second
	if ceiling < time.Second {
		ceiling = m.looks().cap // tests shorten the cap
	}
	return max(time.Second, min(want, ceiling, guestagent.MaxDeadline))
}

// deskCall sends one toolkit op to mc's agent. refs are the refs the request names: when the
// reader's refs came from another connection than the live one they are refused before anything
// is sent, and a stale_ref or not_found from a connection other than theirs is said the same way.
// A successful answer makes the connection the origin of the reader's refs, since every toolkit
// result names elements by ref.
func (m *Manager) deskCall(ctx context.Context, mc *Machine, req guestagent.Request, refs []string) (guestagent.Response, error) {
	sup := m.agentSupervisor(mc)
	origin, had := mc.input.desk.origin(req.Reader)
	if len(refs) > 0 && had && sup != nil {
		if origin.sup != sup {
			return guestagent.Response{}, staleConnectionError(req.Op, refs[0])
		}
		conn, err := sup.Conn(ctx, m.agentWait(sup))
		if err != nil {
			return guestagent.Response{}, err
		}
		if conn.Gen() != origin.gen {
			return guestagent.Response{}, staleConnectionError(req.Op, refs[0])
		}
	}
	resp, conn, err := m.agentCallPrepared(ctx, mc, req, func(conn *guestagent.Conn) error {
		return m.raiseRefs(ctx, mc, sup, conn, req.Reader)
	})
	var ae *guestagent.Error
	if err == nil {
		mc.input.desk.saw(req.Reader, desktop.HighestRef(resp.Result))
	} else if errors.As(err, &ae) {
		mc.input.desk.saw(req.Reader, desktop.HighestRef(ae.Detail))
	}
	if err != nil && len(refs) > 0 && had && conn != nil && errors.As(err, &ae) &&
		(ae.Code == guestagent.CodeStaleRef || ae.Code == guestagent.CodeNotFound) &&
		(origin.sup != sup || origin.gen != conn.Gen()) {
		// The channel came back between the check and the send: the new agent never knew the ref.
		return resp, staleConnectionError(req.Op, refs[0])
	}
	if err == nil && conn != nil {
		mc.input.desk.note(req.Reader, refOrigin{sup: sup, gen: conn.Gen()})
	}
	return resp, err
}

// refsRaiseDeadline bounds the refs op, which only moves a counter.
const refsRaiseDeadline = 2 * time.Second

// raiseRefs makes conn's agent number reader's new refs past every ref reader was handed on this
// machine (op refs), once per connection, before reader's first toolkit call on it. A new agent
// (a reconnect, a reboot) starts its tables at e1, so without it an old ref the caller kept
// could name another element; with it, an old ref is one this reader does not hold.
func (m *Manager) raiseRefs(ctx context.Context, mc *Machine, sup *guestagent.Supervisor, conn *guestagent.Conn, reader string) error {
	o := refOrigin{sup: sup, gen: conn.Gen()}
	next, need := mc.input.desk.raiseFor(reader, o)
	if !need {
		return nil
	}
	if !conn.Has("refs") {
		return fmt.Errorf("%w: the guest agent in machine %s (helper %d) does not offer \"refs\", so its refs could repeat after a "+
			"reconnect: call machine_reboot, whose boot compiles the current helper, or rebuild the image "+
			"(scripts/build-image.sh -force)", guestagent.ErrMissingOp, mc.RunID, conn.Hello().Version)
	}
	if _, err := conn.Call(ctx, guestagent.Request{Op: "refs", Reader: reader, Deadline: refsRaiseDeadline,
		Args: map[string]any{"reader": reader, "next": next}}); err != nil {
		return fmt.Errorf("raise the guest agent's refs past e%d: %w", next-1, err)
	}
	mc.input.desk.raisedOn(reader, o)
	return nil
}

// refsOf is the refs among names, for deskCall: a window may be named by title, which is no ref.
func refsOf(names ...string) []string {
	var out []string
	for _, n := range names {
		if desktop.Ref(n).Validate() == nil {
			out = append(out, n)
		}
	}
	return out
}

// deskError turns a toolkit op's failure into what its caller returns. The op ran on look, a
// context capped from parent at limit; what names the op in a timeout ("the snapshot"). ref is the
// ref the call named, for a refusal's sentence.
func (m *Manager) deskError(parent, look context.Context, mc *Machine, op, what, ref string, limit time.Duration, err error) error {
	if err == nil {
		return nil
	}
	if err = lookError(parent, look, err, what, limit); errors.Is(err, ErrScreenNotAnswering) {
		return err
	}
	var de *DesktopError
	if errors.As(err, &de) {
		return err
	}
	if errors.Is(err, guestagent.ErrDeadline) {
		return &ScreenNotAnsweringError{What: what, After: limit}
	}
	var ae *guestagent.Error
	if !errors.As(err, &ae) {
		return err
	}
	e := &DesktopError{Op: op, Code: ae.Code, agent: ae}
	msg := strings.TrimSpace(ae.Message)
	switch ae.Code {
	case guestagent.CodeRefused:
		var d struct {
			desktop.Refusal
			Target *desktop.Node `json:"target"`
		}
		_ = json.Unmarshal(ae.Detail, &d)
		d.Message = msg
		e.Refusal, e.Target = &d.Refusal, d.Target
		e.Text = desktop.RefusalText(ref, d.Target, d.Refusal)
	case guestagent.CodeStaleRef:
		e.Text = withAdvice(orText(msg, ref+" no longer names an element"), "take a new machine_snapshot (or machine_find) for a current ref")
	case guestagent.CodeNotFound:
		e.Text = withAdvice(orText(msg, "nothing matched"), "take a machine_snapshot or machine_find to see what is there")
	case guestagent.CodeAmbiguous:
		var d struct {
			Candidates []desktop.Node `json:"candidates"`
		}
		_ = json.Unmarshal(ae.Detail, &d)
		e.Candidates = d.Candidates
		e.Text = orText(msg, "more than one element fits")
		if len(d.Candidates) > 0 {
			names := make([]string, 0, len(d.Candidates))
			for _, c := range d.Candidates {
				names = append(names, desktop.Label(c))
			}
			e.Text += "; the candidates are " + strings.Join(names, ", ") + ": name one by its ref"
		} else {
			e.Text = withAdvice(e.Text, "name one by its ref (machine_find lists them)")
		}
	case guestagent.CodePaused:
		// A human took the screen while this ran (daemon ADR 0005 point 14): the agent stopped it.
		holder, until := "human", time.Now().Add(ControlTTL)
		if c, held := m.ControlState(mc.RunID); held {
			holder, until = c.Holder, c.Expires
		}
		return &ScreenTakenError{Holder: holder, Until: until}
	case guestagent.CodeNotResponding:
		e.Text = orText(msg, "the app does not answer accessibility") + "; it is not responding (hung or busy). " +
			"Check it with machine_exec (ps, sample), wait and try again, or recover with machine_reboot, which restarts the machine and keeps the run"
	case guestagent.CodeDeadline:
		return &ScreenNotAnsweringError{What: what, After: limit}
	case guestagent.CodeNotTrusted:
		e.Text = orText(msg, "the guest agent lacks a permission") + "; this machine's image is missing a grant: call machine_reboot, or rebuild the image"
	case guestagent.CodeBadRequest:
		e.Text = "the guest agent refused the " + op + " arguments: " + orText(msg, "bad request")
	case guestagent.CodeCancelled:
		e.Text = "the " + op + " was cancelled before it finished: " + orText(msg, "cancelled")
	default:
		e.Text = fmt.Sprintf("the %s failed: %s", op, ae.Error())
	}
	return e
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// withAdvice appends what to do next unless msg already names a snapshot.
func withAdvice(msg, advice string) string {
	if strings.Contains(msg, "machine_snapshot") {
		return msg
	}
	return msg + "; " + advice
}

// withReader is a request as a step records it: the tool's arguments and the reader.
func withReader(args any, reader string) map[string]any {
	out := map[string]any{}
	if b, err := json.Marshal(args); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	if reader != "" {
		out["reader"] = reader
	}
	return out
}
