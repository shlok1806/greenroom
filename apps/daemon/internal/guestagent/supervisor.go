package guestagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// SupervisorOptions are a Supervisor's timings. Zero values take the defaults.
type SupervisorOptions struct {
	Conn Options // each connection's; Conn.OnEvent also gets every event
	// Backoff is how long to wait before each start after a loss or a failed start: 0.5, 1, 2,
	// 5 s by default, the last repeating (daemon ADR 0005 point 7).
	Backoff []time.Duration
	// StableAfter is how long a connection must have lived for the backoff to start over
	// (10 s): an agent that dies as soon as it says HELLO is not restarted every half second.
	StableAfter time.Duration
	Log         *slog.Logger
}

var defaultBackoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second}

// Supervisor keeps one connection to a machine's agent: it starts the agent, reconnects with
// backoff whenever the channel ends, numbers each connection (its generation), counts the
// reconnects, and carries what must survive a reconnect (a standing PAUSE). Every loss and every
// reconnect is logged once.
type Supervisor struct {
	start  func(ctx context.Context) (Transport, error)
	o      SupervisorOptions
	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	cur        *Conn
	gen        uint64
	reconnects int
	downSince  time.Time
	lastErr    error
	stalledIn  string
	paused     string // the holder a PAUSE is standing for, or ""
	closed     bool
	changed    chan struct{} // closed and replaced whenever cur changes

	kick    chan struct{} // wakes the control loop
	closing sync.WaitGroup
	done    chan struct{} // closes when the run and control loops have returned
}

// NewSupervisor starts keeping a connection to the agent that start launches. start is called
// again for every reconnect, with a context that ends at Close.
func NewSupervisor(start func(ctx context.Context) (Transport, error), o SupervisorOptions) *Supervisor {
	if len(o.Backoff) == 0 {
		o.Backoff = defaultBackoff
	}
	if o.StableAfter <= 0 {
		o.StableAfter = 10 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Conn.Log == nil {
		o.Conn.Log = o.Log
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Supervisor{start: start, o: o, ctx: ctx, cancel: cancel, downSince: time.Now(),
		changed: make(chan struct{}), kick: make(chan struct{}, 1), done: make(chan struct{})}
	var loops sync.WaitGroup
	loops.Add(2)
	go func() { defer loops.Done(); s.run() }()
	go func() { defer loops.Done(); s.control() }()
	go func() { loops.Wait(); s.closing.Wait(); close(s.done) }()
	return s
}

// Conn returns the live connection, waiting up to wait for one. It fails with ErrUnavailable
// (wrapped with how long the channel has been down and why) when none comes in time or the
// supervisor is closed, and with ctx's error when the caller gives up first.
func (s *Supervisor) Conn(ctx context.Context, wait time.Duration) (*Conn, error) {
	timer := time.NewTimer(max(wait, 0))
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: the guest agent was stopped with its machine's boot", ErrUnavailable)
		}
		cur, changed := s.cur, s.changed
		s.mu.Unlock()
		if cur != nil {
			select {
			case <-cur.Done(): // ended; the run loop has not noticed yet
			default:
				return cur, nil
			}
		}
		select {
		case <-changed:
		case <-cur.doneOrNil():
		case <-timer.C:
			return nil, s.unavailable()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// doneOrNil is Done, or nil (never ready) for no connection.
func (c *Conn) doneOrNil() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.done
}

// unavailable is ErrUnavailable with how long the channel has been down and the last error.
func (s *Supervisor) unavailable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	down := time.Since(s.downSince).Round(100 * time.Millisecond)
	if s.lastErr != nil {
		return fmt.Errorf("%w (down for %s; last error: %v)", ErrUnavailable, down, s.lastErr)
	}
	return fmt.Errorf("%w (down for %s)", ErrUnavailable, down)
}

// Down reports whether there is no live connection, and since when.
func (s *Supervisor) Down() (since time.Time, down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return s.downSince, true
	}
	select {
	case <-s.cur.Done():
		return time.Now(), true
	default:
		return time.Time{}, false
	}
}

// Reconnects counts the connections after the first.
func (s *Supervisor) Reconnects() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconnects
}

// Stalled reports whether the live agent says a walk or capture has run over its watchdog's
// limit (EVENT stalled), and in what ("ax" or "capture"), until it says recovered.
func (s *Supervisor) Stalled() (in string, stalled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stalledIn, s.stalledIn != ""
}

// Pause asks the agent to refuse inputs of every seat but holder. It returns at once; the PAUSE
// is sent by the supervisor's own goroutine, and again on every new connection until Resume.
func (s *Supervisor) Pause(holder string) {
	s.mu.Lock()
	s.paused = holder
	s.mu.Unlock()
	s.wake()
}

// Resume lifts a standing Pause. It returns at once.
func (s *Supervisor) Resume() {
	s.mu.Lock()
	s.paused = ""
	s.mu.Unlock()
	s.wake()
}

// Close stops the supervisor: no more starts, the live connection is closed, and it returns
// once every transport it started is closed. Safe to repeat.
func (s *Supervisor) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.notifyLocked()
	}
	s.mu.Unlock()
	s.cancel()
	<-s.done
}

func (s *Supervisor) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// notifyLocked wakes every Conn waiter. The caller holds s.mu.
func (s *Supervisor) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// run is the connect loop: start, open, hold until the channel ends, back off, again.
func (s *Supervisor) run() {
	step := 0 // index into Backoff for the next wait
	var wait time.Duration
	quietFailures := false
	for {
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-t.C:
			case <-s.ctx.Done():
				t.Stop()
				return
			}
		}
		if s.ctx.Err() != nil {
			return
		}
		conn, err := s.connect()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			s.lastErr = err
			s.mu.Unlock()
			if !quietFailures {
				s.o.Log.Warn("cannot start the guest agent; retrying with backoff", "err", err)
				quietFailures = true
			} else {
				s.o.Log.Debug("cannot start the guest agent", "err", err)
			}
			wait, step = s.backoff(step), step+1
			continue
		}
		quietFailures = false
		opened := time.Now()
		s.up(conn)
		select {
		case <-conn.Done():
		case <-s.ctx.Done():
			_ = conn.Close()
			s.down(errClosed)
			return
		}
		cause := conn.Err()
		s.closing.Add(1)
		go func() { defer s.closing.Done(); _ = conn.Close() }()
		s.down(cause)
		s.o.Log.Warn("the guest agent's channel was lost; reconnecting", "gen", conn.Gen(),
			"after", time.Since(opened).Round(time.Millisecond), "err", cause)
		if time.Since(opened) >= s.o.StableAfter {
			step = 0
		}
		wait, step = s.backoff(step), step+1
	}
}

func (s *Supervisor) backoff(step int) time.Duration {
	return s.o.Backoff[min(step, len(s.o.Backoff)-1)]
}

// connect starts the agent and opens a connection to it as the next generation.
func (s *Supervisor) connect() (*Conn, error) {
	t, err := s.start(s.ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	gen := s.gen + 1
	s.mu.Unlock()
	o := s.o.Conn
	o.OnEvent = func(e Event) { s.onEvent(gen, e) }
	return open(s.ctx, t, o, gen)
}

// up makes conn the live connection.
func (s *Supervisor) up(conn *Conn) {
	s.mu.Lock()
	s.cur, s.gen, s.stalledIn, s.lastErr = conn, conn.Gen(), "", nil
	down := time.Since(s.downSince)
	if conn.Gen() > 1 {
		s.reconnects++
	}
	s.notifyLocked()
	s.mu.Unlock()
	h := conn.Hello()
	if conn.Gen() > 1 {
		s.o.Log.Info("the guest agent reconnected", "gen", conn.Gen(), "downFor", down.Round(time.Millisecond), "pid", h.PID)
	} else {
		s.o.Log.Debug("the guest agent connected", "version", h.Version, "source", h.Source, "pid", h.PID, "caps", len(h.Caps))
	}
	if !h.Trusted.Accessibility || !h.Trusted.Screen || !h.Trusted.PostEvent {
		s.o.Log.Warn("the guest agent lacks a permission the image should grant", "accessibility", h.Trusted.Accessibility,
			"screen", h.Trusted.Screen, "postEvent", h.Trusted.PostEvent)
	}
	s.wake() // a standing PAUSE goes to the new agent
}

// down records that the live connection ended with cause.
func (s *Supervisor) down(cause error) {
	s.mu.Lock()
	s.cur, s.downSince, s.lastErr, s.stalledIn = nil, time.Now(), cause, ""
	s.notifyLocked()
	s.mu.Unlock()
}

// onEvent handles an EVENT of connection gen: the watchdog's stalled and recovered (daemon ADR
// 0005 point 9), and the agent's own log lines.
func (s *Supervisor) onEvent(gen uint64, e Event) {
	var f struct {
		In      string  `json:"in"`
		Seconds float64 `json:"seconds"`
		Message string  `json:"message"`
	}
	_ = json.Unmarshal(e.Payload, &f)
	switch e.Kind {
	case EventStalled, EventRecovered:
		s.mu.Lock()
		current := s.gen == gen && s.cur != nil
		was := s.stalledIn
		if current {
			if e.Kind == EventStalled {
				s.stalledIn = f.In
				if s.stalledIn == "" {
					s.stalledIn = "unknown"
				}
			} else {
				s.stalledIn = ""
			}
		}
		s.mu.Unlock()
		switch {
		case !current:
		case e.Kind == EventStalled && was == "":
			s.o.Log.Warn("the guest agent reports the guest's screen stalled", "in", f.In, "seconds", f.Seconds, "gen", gen)
		case e.Kind == EventRecovered && was != "":
			s.o.Log.Info("the guest agent reports the guest's screen answers again", "in", f.In, "gen", gen)
		}
	case EventLog:
		s.o.Log.Info("guest agent: "+f.Message, "gen", gen)
	}
	if s.o.Conn.OnEvent != nil {
		s.o.Conn.OnEvent(e)
	}
}

// control is the loop that sends PAUSE and RESUME, so callers never block on the channel and
// the frames go out in the order the state changed. Each new connection starts unpaused.
func (s *Supervisor) control() {
	var conn *Conn
	sent := ""
	for {
		select {
		case <-s.kick:
		case <-s.ctx.Done():
			return
		}
		s.mu.Lock()
		cur, want := s.cur, s.paused
		s.mu.Unlock()
		if cur == nil {
			continue
		}
		if cur != conn {
			conn, sent = cur, ""
		}
		if want == sent {
			continue
		}
		var err error
		if want != "" {
			err = conn.Pause(want)
		} else {
			err = conn.Resume()
		}
		if err == nil {
			sent = want
		}
	}
}
