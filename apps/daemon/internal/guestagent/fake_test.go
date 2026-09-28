package guestagent

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pipeTransport is a Transport to an in-process fake agent over two io.Pipes.
type pipeTransport struct {
	stdin      *io.PipeWriter // the host writes the agent's stdin
	stdout     *io.PipeReader // the host reads the agent's stdout
	agentDone  chan struct{}
	exitErr    atomic.Pointer[error]
	closeCalls atomic.Int32
}

func (p *pipeTransport) Write(b []byte) (int, error) { return p.stdin.Write(b) }
func (p *pipeTransport) Stdout() io.Reader           { return p.stdout }
func (p *pipeTransport) Done() <-chan struct{}       { return p.agentDone }
func (p *pipeTransport) Err() error {
	if e := p.exitErr.Load(); e != nil {
		return *e
	}
	return nil
}

func (p *pipeTransport) Close() error {
	p.closeCalls.Add(1)
	_ = p.stdin.Close()
	select {
	case <-p.agentDone:
	case <-time.After(time.Second):
	}
	_ = p.stdout.Close()
	return nil
}

// fakeAgent speaks the agent's side of the protocol. handle answers each REQUEST on its own
// goroutine; by default it echoes the args as the result.
type fakeAgent struct {
	t      *testing.T
	in     *io.PipeReader
	out    *io.PipeWriter
	tr     *pipeTransport
	hello  Hello
	handle func(a *fakeAgent, r wireRequest)

	noPong atomic.Bool
	wmu    sync.Mutex

	mu     sync.Mutex
	frames []recvFrame // everything the host sent but PINGs
	gotReq chan wireRequest
}

type recvFrame struct {
	typ     byte
	payload string
}

func defaultHello() Hello {
	return Hello{Version: 9, Source: "test", Protocol: Protocol, PID: 42,
		Trusted: Trust{Accessibility: true, Screen: true, PostEvent: true},
		Screen:  Screen{Width: 1024, Height: 768, Scale: 1}, Caps: []string{"echo", "capture", "fail", "hang", "slow"}}
}

// newFakeAgent returns an agent that has not started; start runs it.
func newFakeAgent(t *testing.T) *fakeAgent {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	a := &fakeAgent{t: t, in: inR, out: outW, hello: defaultHello(), gotReq: make(chan wireRequest, 256)}
	a.tr = &pipeTransport{stdin: inW, stdout: outR, agentDone: make(chan struct{})}
	a.handle = func(a *fakeAgent, r wireRequest) { a.respond(r.ID, true, r.Args, nil) }
	return a
}

// start runs the agent's reader loop; it says hello first unless sayHello is false.
func (a *fakeAgent) start(sayHello bool) *fakeAgent {
	go func() {
		defer close(a.tr.agentDone)
		defer func() { _ = a.out.Close() }()
		if sayHello {
			a.sendJSON(TypeHello, a.hello)
		}
		for {
			typ, payload, err := ReadFrame(a.in)
			if err != nil {
				return
			}
			switch typ {
			case TypePing:
				if !a.noPong.Load() {
					a.send(TypePong, payload)
				}
				continue
			case TypeRequest:
				var r wireRequest
				if err := json.Unmarshal(payload, &r); err != nil {
					a.t.Errorf("fake agent: bad request %s", payload)
					return
				}
				a.gotReq <- r
				go a.handle(a, r)
			}
			a.mu.Lock()
			a.frames = append(a.frames, recvFrame{typ, string(payload)})
			a.mu.Unlock()
		}
	}()
	return a
}

// die ends the agent's process: its stdout closes and it reads no more.
func (a *fakeAgent) die(err error) {
	a.tr.exitErr.Store(&err)
	_ = a.out.Close()
	_ = a.in.Close()
}

func (a *fakeAgent) send(typ byte, payload []byte) {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	_ = WriteFrame(a.out, typ, payload)
}

func (a *fakeAgent) sendJSON(typ byte, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		a.t.Error(err)
	}
	a.send(typ, b)
}

func (a *fakeAgent) respond(id uint32, ok bool, result any, e *Error) {
	msg := map[string]any{"id": id, "ok": ok, "ms": 3}
	if ok {
		msg["result"] = result
	} else {
		msg["error"] = e
	}
	a.sendJSON(TypeResponse, msg)
}

// sent returns the frames of type typ the host sent, oldest first.
func (a *fakeAgent) sent(typ byte) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, f := range a.frames {
		if f.typ == typ {
			out = append(out, f.payload)
		}
	}
	return out
}

// fastOptions are connection timings short enough for tests.
func fastOptions() Options {
	return Options{HelloTimeout: time.Second, PingInterval: 20 * time.Millisecond, PongTimeout: 150 * time.Millisecond,
		CallGrace: 50 * time.Millisecond, WedgeGrace: 150 * time.Millisecond}
}

// waitFor polls cond until it holds or d passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func isLost(err error) bool { return errors.Is(err, ErrLost) }
