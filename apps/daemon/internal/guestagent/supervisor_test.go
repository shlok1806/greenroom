package guestagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// agentFarm starts a fresh fake agent for every start call and remembers them.
type agentFarm struct {
	t      *testing.T
	mu     sync.Mutex
	agents []*fakeAgent
	starts []time.Time
	fail   error // while set, starts fail
	setup  func(a *fakeAgent)
}

func (f *agentFarm) start(context.Context) (Transport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, time.Now())
	if f.fail != nil {
		return nil, f.fail
	}
	a := newFakeAgent(f.t)
	if f.setup != nil {
		f.setup(a)
	}
	f.agents = append(f.agents, a.start(true))
	return a.tr, nil
}

func (f *agentFarm) agent(i int) *fakeAgent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents[i]
}

func (f *agentFarm) count() (starts, agents int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts), len(f.agents)
}

func (f *agentFarm) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

func fastSupervisor(f *agentFarm, backoff ...time.Duration) *Supervisor {
	if len(backoff) == 0 {
		backoff = []time.Duration{10 * time.Millisecond}
	}
	return NewSupervisor(f.start, SupervisorOptions{Conn: fastOptions(), Backoff: backoff, StableAfter: time.Hour})
}

func TestSupervisorReconnectsWithANewGeneration(t *testing.T) {
	f := &agentFarm{t: t}
	s := fastSupervisor(f)
	defer s.Close()
	c1, err := s.Conn(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c1.Gen() != 1 || s.Reconnects() != 0 {
		t.Fatalf("first connection: gen %d, reconnects %d", c1.Gen(), s.Reconnects())
	}
	if _, down := s.Down(); down {
		t.Fatal("Down while connected")
	}
	f.agent(0).die(errors.New("killed"))
	<-c1.Done()
	c2, err := s.Conn(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c2 == c1 || c2.Gen() != 2 || s.Reconnects() != 1 {
		t.Fatalf("after a loss: gen %d, reconnects %d", c2.Gen(), s.Reconnects())
	}
	if _, err := c2.Call(context.Background(), Request{Op: "echo", Args: 1, Deadline: time.Second}); err != nil {
		t.Fatal(err)
	}
	// The dead connection's transport was closed.
	waitFor(t, time.Second, "the old transport closed", func() bool { return f.agent(0).tr.closeCalls.Load() > 0 })
}

func TestSupervisorBacksOffBetweenFailedStartsAndSaysWhy(t *testing.T) {
	f := &agentFarm{t: t}
	f.setFail(errors.New("tart exec -i: exit 1: VM is not running"))
	backoff := []time.Duration{30 * time.Millisecond, 60 * time.Millisecond, 120 * time.Millisecond}
	s := fastSupervisor(f, backoff...)
	defer s.Close()
	waitFor(t, 2*time.Second, "five starts", func() bool { n, _ := f.count(); return n >= 5 })
	f.mu.Lock()
	starts := append([]time.Time(nil), f.starts...)
	f.mu.Unlock()
	want := []time.Duration{30, 60, 120, 120}
	for i, w := range want {
		if gap := starts[i+1].Sub(starts[i]); gap < w*time.Millisecond {
			t.Fatalf("start %d came %s after the one before, want at least %d ms (backoff %v)", i+2, gap, w, backoff)
		}
	}
	_, err := s.Conn(context.Background(), 10*time.Millisecond)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "VM is not running") || !strings.Contains(err.Error(), "down for") {
		t.Fatalf("got %v, want ErrUnavailable with the last error", err)
	}
	since, down := s.Down()
	if !down || time.Since(since) < 100*time.Millisecond {
		t.Fatalf("Down: %v since %s", down, since)
	}
	f.setFail(nil)
	if _, err := s.Conn(context.Background(), 2*time.Second); err != nil {
		t.Fatalf("the agent came back but Conn says %v", err)
	}
}

func TestConnWaitsForAReconnectWithinItsWait(t *testing.T) {
	f := &agentFarm{t: t}
	s := fastSupervisor(f, 100*time.Millisecond)
	defer s.Close()
	c1, err := s.Conn(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f.agent(0).die(errors.New("killed"))
	<-c1.Done()
	start := time.Now()
	if _, err := s.Conn(context.Background(), 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no wait while reconnecting: got %v, want ErrUnavailable", err)
	}
	c2, err := s.Conn(context.Background(), time.Second)
	if err != nil || c2.Gen() != 2 {
		t.Fatalf("got %v, %v", c2, err)
	}
	if took := time.Since(start); took < 50*time.Millisecond {
		t.Fatalf("the reconnect came after %s, before its backoff", took)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.agent(1).die(errors.New("killed"))
	<-c2.Done()
	if _, err := s.Conn(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("a caller that gave up got %v", err)
	}
}

func TestSupervisorResendsAStandingPauseAfterAReconnect(t *testing.T) {
	f := &agentFarm{t: t}
	s := fastSupervisor(f)
	defer s.Close()
	c1, err := s.Conn(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s.Pause("human")
	waitFor(t, time.Second, "PAUSE", func() bool { return len(f.agent(0).sent(TypePause)) == 1 })
	f.agent(0).die(errors.New("killed"))
	<-c1.Done()
	if _, err := s.Conn(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "PAUSE on the new agent", func() bool { return len(f.agent(1).sent(TypePause)) == 1 })
	if got := f.agent(1).sent(TypePause)[0]; got != `{"holder":"human"}` {
		t.Fatalf("got %s", got)
	}
	s.Resume()
	waitFor(t, time.Second, "RESUME", func() bool { return len(f.agent(1).sent(TypeResume)) == 1 })
	// A later connection starts unpaused and gets nothing.
	f.agent(1).die(errors.New("killed"))
	waitFor(t, time.Second, "a third agent", func() bool { _, n := f.count(); return n == 3 })
	if _, err := s.Conn(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if p, r := f.agent(2).sent(TypePause), f.agent(2).sent(TypeResume); len(p)+len(r) != 0 {
		t.Fatalf("an unpaused reconnect sent PAUSE %v and RESUME %v", p, r)
	}
}

func TestSupervisorTracksStalledFromTheLiveAgent(t *testing.T) {
	f := &agentFarm{t: t}
	s := fastSupervisor(f)
	defer s.Close()
	c1, err := s.Conn(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f.agent(0).sendJSON(TypeEvent, map[string]any{"kind": "stalled", "in": "capture", "seconds": 11})
	waitFor(t, time.Second, "stalled", func() bool { _, st := s.Stalled(); return st })
	if in, _ := s.Stalled(); in != "capture" {
		t.Fatalf("stalled in %q", in)
	}
	f.agent(0).sendJSON(TypeEvent, map[string]any{"kind": "recovered", "in": "capture"})
	waitFor(t, time.Second, "recovered", func() bool { _, st := s.Stalled(); return !st })
	// A stall does not outlive its agent.
	f.agent(0).sendJSON(TypeEvent, map[string]any{"kind": "stalled", "in": "ax", "seconds": 10})
	waitFor(t, time.Second, "stalled again", func() bool { _, st := s.Stalled(); return st })
	f.agent(0).die(errors.New("killed"))
	<-c1.Done()
	if _, err := s.Conn(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if in, st := s.Stalled(); st {
		t.Fatalf("a new agent inherited the old one's stall in %q", in)
	}
}

func TestSupervisorCloseStopsEverything(t *testing.T) {
	f := &agentFarm{t: t}
	s := fastSupervisor(f)
	c, err := s.Conn(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("the live connection outlived Close")
	}
	if f.agent(0).tr.closeCalls.Load() == 0 {
		t.Fatal("the transport was not closed")
	}
	if _, err := s.Conn(context.Background(), time.Second); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("Conn after Close: %v", err)
	}
	n, _ := f.count()
	time.Sleep(50 * time.Millisecond)
	if m, _ := f.count(); m != n {
		t.Fatalf("started %d more agents after Close", m-n)
	}
	s.Close() // again: a no-op
}

func TestAConnectionThatLivedResetsTheBackoff(t *testing.T) {
	f := &agentFarm{t: t}
	s := NewSupervisor(f.start, SupervisorOptions{Conn: fastOptions(),
		Backoff: []time.Duration{10 * time.Millisecond, 400 * time.Millisecond}, StableAfter: 30 * time.Millisecond})
	defer s.Close()
	for i := range 3 {
		c, err := s.Conn(context.Background(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Millisecond) // lived past StableAfter
		start := time.Now()
		f.agent(i).die(errors.New("killed"))
		<-c.Done()
		if _, err := s.Conn(context.Background(), time.Second); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took > 300*time.Millisecond {
			t.Fatalf("reconnect %d took %s: a stable connection's loss should wait the first backoff", i+1, took)
		}
	}
}
