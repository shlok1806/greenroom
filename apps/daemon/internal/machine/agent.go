package machine

// The guest agent (daemon ADR 0005): with the desktop toolkit on, one long-lived
// `greenroom-input --agent` per machine boot, reached through one `tart exec -i`, carries every
// desktop look and input the daemon makes, instead of a tart exec each (every exec leaks a
// descriptor in `tart run`, daemon ADR 0002). internal/guestagent owns the wire; this file owns
// when the agent runs and how the existing looks and inputs use it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// WithDesktopToolkit turns the guest agent on (serve and bench run -desktop-toolkit). Off, no
// agent is ever started and every desktop call is a tart exec, as before.
func WithDesktopToolkit(on bool) Option {
	return func(m *Manager) { m.desktopToolkit = on }
}

// DesktopToolkit reports whether machines are driven through the guest agent.
func (m *Manager) DesktopToolkit() bool { return m.desktopToolkit }

// agentTimes are the guest agent's limits. Production uses defaultAgentTimes; tests shorten them.
type agentTimes struct {
	// degradeAfter is how long a legacy read or input waits for a channel that went down before
	// it falls back to its one-shot exec (daemon ADR 0005 point 13).
	degradeAfter time.Duration
	// bootWait is how long boot waits for the first connection before it records agentError and
	// goes on (the supervisor keeps trying).
	bootWait time.Duration
	// sup is the channel's own timings (heartbeat, deadlines, backoff).
	sup guestagent.SupervisorOptions
}

var defaultAgentTimes = agentTimes{degradeAfter: 10 * time.Second, bootWait: 15 * time.Second}

// withAgentTimes replaces the guest agent's limits (tests only).
func withAgentTimes(t agentTimes) Option {
	return func(m *Manager) { m.agentT = t }
}

func (m *Manager) agentTimes() agentTimes {
	if m.agentT.degradeAfter <= 0 {
		return defaultAgentTimes
	}
	return m.agentT
}

// agentLink is a machine boot's guest agent. It belongs to boot gen (Machine.gen): a reboot
// stops it with the old boot and the new boot starts its own.
type agentLink struct {
	sup *guestagent.Supervisor
	gen int
}

// agentScript starts the agent after killing any left behind: the guest agent does not close a
// helper's stdin when its host exec dies (root ADR 0011). The bracket keeps pkill from matching
// this shell's own command line, as in serveScript.
func agentScript() string {
	name := filepath.Base(helperName())
	return fmt.Sprintf(`pkill -f '[%s]%s --agent'; exec "$HOME/%s" --agent`, name[:1], name[1:], helperName())
}

// startAgent starts boot gen's supervisor and links it to mc, unless the machine is going away,
// was rebooted since, or already has one. It never waits for a connection.
func (m *Manager) startAgent(mc *Machine, gen int) *guestagent.Supervisor {
	o := m.agentTimes().sup
	o.Log = m.Log.With("runId", mc.RunID)
	sup := guestagent.NewSupervisor(func(context.Context) (guestagent.Transport, error) {
		p, err := m.tart.StartPipe(mc.Name, "/bin/sh", "-c", agentScript())
		if err != nil {
			return nil, err
		}
		return p, nil
	}, o)
	m.mu.Lock()
	if !m.liveLocked(mc) || mc.gen != gen || mc.agent != nil {
		m.mu.Unlock()
		sup.Close()
		return nil
	}
	mc.agent = &agentLink{sup: sup, gen: gen}
	m.mu.Unlock()
	return sup
}

// bootAgent is boot's start of the agent, right after the helper phase made sure the helper is
// current. It waits a bounded time for the first connection; a failure is agentError in the
// boot step, never fatal, and the supervisor keeps trying. Step key guestAgentSeconds
// (agentSeconds is the tart guest agent's wait).
func (m *Manager) bootAgent(ctx context.Context, mc *Machine, timings map[string]any) {
	at := time.Now()
	defer func() { timings["guestAgentSeconds"] = round1(time.Since(at)) }()
	m.mu.Lock()
	gen := mc.gen
	m.mu.Unlock()
	sup := m.startAgent(mc, gen)
	if sup == nil {
		return // destroyed or rebooted meanwhile
	}
	if _, err := sup.Conn(ctx, m.agentTimes().bootWait); err != nil {
		timings["agentError"] = err.Error()
		m.Log.Warn("the guest agent did not connect during boot; desktop calls use tart exec until it does",
			"runId", mc.RunID, "err", err)
	}
}

// stopAgentLocked unlinks mc's agent and closes it in the background (closing waits for its
// tart exec to end); agentDone closes when it has. The caller holds m.mu.
func (m *Manager) stopAgentLocked(mc *Machine) {
	a := mc.agent
	if a == nil {
		return
	}
	mc.agent = nil
	done := make(chan struct{})
	mc.agentDone = done
	go func() {
		a.sup.Close()
		close(done)
	}()
}

// stopAgent is stopAgentLocked for a boot that failed after the agent started.
func (m *Manager) stopAgent(mc *Machine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopAgentLocked(mc)
}

// agentSupervisor is mc's live agent supervisor, or nil when the toolkit is off or the machine
// has none (booting, rebooting, going away).
func (m *Manager) agentSupervisor(mc *Machine) *guestagent.Supervisor {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mc.agent == nil {
		return nil
	}
	return mc.agent.sup
}

// agentWait is how long a call waits for sup's channel: until degradeAfter past the moment it
// went down, and not at all once that has passed.
func (m *Manager) agentWait(sup *guestagent.Supervisor) time.Duration {
	since, down := sup.Down()
	if !down {
		return 0
	}
	return max(0, m.agentTimes().degradeAfter-time.Since(since))
}

// agentCall is the one way into mc's guest agent. It waits for a live connection only while the
// channel has been down under degradeAfter (never past ctx), refuses an op the agent's HELLO
// does not list, and returns the connection it used: refs are scoped to its Gen. Nothing was
// sent when the error is guestagent.ErrUnavailable or ErrMissingOp.
func (m *Manager) agentCall(ctx context.Context, mc *Machine, req guestagent.Request) (guestagent.Response, *guestagent.Conn, error) {
	sup := m.agentSupervisor(mc)
	if sup == nil {
		if !m.desktopToolkit {
			return guestagent.Response{}, nil, fmt.Errorf("%w: this daemon runs without -desktop-toolkit", guestagent.ErrUnavailable)
		}
		return guestagent.Response{}, nil, fmt.Errorf("%w: machine %s has no guest agent running", guestagent.ErrUnavailable, mc.RunID)
	}
	// A connection that ends between Conn and the send sent nothing; the next one is waited for
	// like any other, within the same bound.
	for attempt := 0; ; attempt++ {
		conn, err := sup.Conn(ctx, m.agentWait(sup))
		if err != nil {
			return guestagent.Response{}, nil, err
		}
		if !conn.Has(req.Op) {
			return guestagent.Response{}, conn, fmt.Errorf("%w: the guest agent in machine %s (helper %d) does not offer %q, so its input helper "+
				"is older than this daemon: call machine_reboot, whose boot compiles the current helper, or rebuild the image "+
				"(scripts/build-image.sh -force)", guestagent.ErrMissingOp, mc.RunID, conn.Hello().Version, req.Op)
		}
		resp, err := conn.Call(ctx, req)
		if errors.Is(err, guestagent.ErrUnavailable) && attempt < 2 {
			continue
		}
		return resp, conn, err
	}
}

// agentRoute is how a legacy look or input went (daemon ADR 0005 points 12 and 13).
type agentRoute int

const (
	routeExec     agentRoute = iota // no agent (the toolkit is off): the one-shot exec, as always
	routeAgent                      // over the channel; its answer or error is the call's
	routeDegraded                   // the channel was down too long: the one-shot exec, recorded as degraded
)

// viaAgent sends a legacy look or input over the channel when mc has an agent. When the channel
// stayed down past degradeAfter, or ended before the request was sent, nothing was sent and the
// caller falls back to its exec (routeDegraded): an input is never posted twice. A caller that
// gave up gets its ctx's error.
func (m *Manager) viaAgent(ctx context.Context, mc *Machine, req guestagent.Request) (guestagent.Response, agentRoute, error) {
	if m.agentSupervisor(mc) == nil {
		return guestagent.Response{}, routeExec, nil
	}
	resp, _, err := m.agentCall(ctx, mc, req)
	if errors.Is(err, guestagent.ErrUnavailable) && ctx.Err() == nil {
		m.Log.Debug("a desktop call fell back to tart exec: the guest agent is down", "runId", mc.RunID, "op", req.Op, "err", err)
		return guestagent.Response{}, routeDegraded, nil
	}
	return resp, routeAgent, err
}

// agentStalled fails a capture or walk at once while mc's agent reports a stalled screen (daemon
// ADR 0005 point 9), instead of queueing it behind the stalled one.
func (m *Manager) agentStalled(mc *Machine, what string) error {
	sup := m.agentSupervisor(mc)
	if sup == nil {
		return nil
	}
	if in, stalled := sup.Stalled(); stalled {
		return &ScreenNotAnsweringError{What: fmt.Sprintf("%s (the guest agent reports its %s work stalled)", what, in), After: agentStallAfter}
	}
	return nil
}

// agentStallAfter is how long the agent's watchdog lets a walk or capture run before it
// reports a stall.
const agentStallAfter = 10 * time.Second

// agentLookError turns an agent error on a look into what the look's caller returns: a
// deadline is a screen that did not answer in time (daemon ADR 0003). timedOut says so.
func agentLookError(err error, what string, limit time.Duration) (_ error, timedOut bool) {
	var ae *guestagent.Error
	if errors.Is(err, guestagent.ErrDeadline) || errors.As(err, &ae) && ae.Code == guestagent.CodeDeadline {
		return &ScreenNotAnsweringError{What: what, After: limit}, true
	}
	return err, false
}

// agentScreen reads the display size over the channel (op screen).
func (m *Manager) agentScreen(ctx context.Context, mc *Machine) (Screen, agentRoute, error) {
	lim := m.looks().captureLimit()
	resp, route, err := m.viaAgent(ctx, mc, guestagent.Request{Op: "screen", Deadline: lim.guest})
	if route != routeAgent {
		return Screen{}, route, nil
	}
	if err != nil {
		err, _ = agentLookError(err, "the screen size read", lim.guest)
		return Screen{}, route, fmt.Errorf("read the screen size: %w", err)
	}
	var s guestagent.Screen
	if err := json.Unmarshal(resp.Result, &s); err != nil {
		return Screen{}, route, fmt.Errorf("read the screen size: %w: %.200s", err, resp.Result)
	}
	if s.Width <= 0 || s.Height <= 0 {
		return Screen{}, route, fmt.Errorf("the machine reports a %dx%d screen", s.Width, s.Height)
	}
	return Screen{Width: s.Width, Height: s.Height}, route, nil
}

// agentCapture takes one PNG of the whole screen over the channel (op capture).
func (m *Manager) agentCapture(ctx context.Context, mc *Machine, lim lookLimit) (png []byte, route agentRoute, timedOut bool, err error) {
	resp, route, err := m.viaAgent(ctx, mc, guestagent.Request{Op: "capture", Args: map[string]any{"format": "png"}, Deadline: lim.guest})
	if route != routeAgent {
		return nil, route, false, nil
	}
	if err != nil {
		err, timedOut = agentLookError(err, "the screen capture", lim.guest)
		if !timedOut {
			err = fmt.Errorf("screen capture failed: %w", err)
		}
		return nil, route, timedOut, err
	}
	if len(resp.Blob) == 0 {
		return nil, route, false, errors.New("screen capture failed: the guest agent's capture carried no image")
	}
	return resp.Blob, route, false, nil
}

// agentDesktop reads the on-screen windows and running apps over the channel (op desktop).
func (m *Manager) agentDesktop(ctx context.Context, mc *Machine) (Desktop, agentRoute, error) {
	lim := m.looks().captureLimit()
	resp, route, err := m.viaAgent(ctx, mc, guestagent.Request{Op: "desktop", Deadline: lim.guest})
	if route != routeAgent {
		return Desktop{}, route, nil
	}
	if err != nil {
		err, _ = agentLookError(err, "the desktop read", lim.guest)
		return Desktop{}, route, fmt.Errorf("read the desktop: %w", err)
	}
	var d Desktop
	if err := json.Unmarshal(resp.Result, &d); err != nil {
		return Desktop{}, route, fmt.Errorf("read the desktop: %w: %.200s", err, resp.Result)
	}
	return d, route, nil
}

// desktopRead is the render check's desktop read: over the channel, or the one-shot helper.
func (m *Manager) desktopRead(ctx context.Context, mc *Machine) (Desktop, bool, error) {
	d, route, err := m.agentDesktop(ctx, mc)
	if route == routeAgent {
		return d, false, err
	}
	d, err = readDesktop(ctx, m.tart, mc.Name)
	return d, route == routeDegraded, err
}

// agentShell runs one of the daemon's own scripts in the guest over the channel (op sh), killed
// at timeout. It is for fixed scripts only, never tool input.
func (m *Manager) agentShell(ctx context.Context, mc *Machine, timeout time.Duration, script string, args ...string) (tart.ExecResult, agentRoute, error) {
	resp, route, err := m.viaAgent(ctx, mc, guestagent.Request{Op: "sh", Deadline: timeout + 2*time.Second,
		Args: map[string]any{"script": script, "args": args, "timeoutMs": timeout.Milliseconds()}})
	if route != routeAgent || err != nil {
		return tart.ExecResult{}, route, err
	}
	var out struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Exit     int    `json:"exit"`
		TimedOut bool   `json:"timedOut"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return tart.ExecResult{}, route, fmt.Errorf("the guest agent's sh answer is unreadable: %w", err)
	}
	if out.TimedOut {
		return tart.ExecResult{}, route, fmt.Errorf("the script ran past its %s limit in the guest and was stopped", timeout)
	}
	return tart.ExecResult{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.Exit}, route, nil
}

// agentHolderPauses reports whether a lease of holder pauses the agent (daemon ADR 0005 point
// 14): any seat but the two agents, which the lease itself keeps apart.
func agentHolderPauses(holder string) bool { return holder != HolderCoder && holder != HolderVerifier }

// pauseAgentLocked sends PAUSE for a fresh take by holder; the caller holds m.mu.
func (m *Manager) pauseAgentLocked(mc *Machine, holder string) {
	if mc.agent != nil && agentHolderPauses(holder) {
		mc.agent.sup.Pause(holder)
	}
}

// resumeAgentLocked sends RESUME when holder's lease ends (given back or lapsed); the caller
// holds m.mu.
func (m *Manager) resumeAgentLocked(mc *Machine, holder string) {
	if mc.agent != nil && agentHolderPauses(holder) {
		mc.agent.sup.Resume()
	}
}
