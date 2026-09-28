package guestagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func openFake(t *testing.T, a *fakeAgent) *Conn {
	t.Helper()
	c, err := Open(context.Background(), a.start(true).tr, fastOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestOpenReturnsTheAgentsHello(t *testing.T) {
	a := newFakeAgent(t)
	c := openFake(t, a)
	h := c.Hello()
	if h.Version != 9 || h.PID != 42 || h.Screen.Width != 1024 || !h.Trusted.PostEvent {
		t.Fatalf("got %+v", h)
	}
	if !c.Has("capture") || c.Has("snapshot") {
		t.Fatalf("Has does not follow the caps %v", h.Caps)
	}
	if c.Gen() != 0 {
		t.Fatalf("a connection outside a supervisor has gen %d, want 0", c.Gen())
	}
}

func TestOpenRefusesAnAgentThatDoesNotSayHello(t *testing.T) {
	cases := []struct {
		name  string
		first func(a *fakeAgent)
		want  string
	}{
		{"another frame first", func(a *fakeAgent) { a.sendJSON(TypeEvent, map[string]string{"kind": "log"}) }, "not HELLO"},
		{"another protocol", func(a *fakeAgent) {
			h := defaultHello()
			h.Protocol = 2
			a.sendJSON(TypeHello, h)
		}, "protocol 2"},
		{"silence", func(*fakeAgent) {}, "did not say HELLO"},
		{"exits", func(a *fakeAgent) { a.die(errors.New("exit 127: greenroom-input-9: not found")) }, "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newFakeAgent(t)
			a.start(false)
			go tc.first(a) // io.Pipe is synchronous: the write waits for Open's reader
			o := fastOptions()
			o.HelloTimeout = 100 * time.Millisecond
			c, err := Open(context.Background(), a.tr, o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, %v; want an error saying %q", c, err, tc.want)
			}
			if a.tr.closeCalls.Load() == 0 {
				t.Fatal("a refused transport was not closed")
			}
		})
	}
}

func TestCallReturnsTheResultAndSendsTheRequestAsTheADRSays(t *testing.T) {
	a := newFakeAgent(t)
	c := openFake(t, a)
	resp, err := c.Call(context.Background(), Request{Op: "echo", Args: map[string]int{"n": 3}, Deadline: 2 * time.Second,
		Reader: "verifier", Input: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Result) != `{"n":3}` || resp.Elapsed != 3*time.Millisecond {
		t.Fatalf("got %s after %s", resp.Result, resp.Elapsed)
	}
	req := <-a.gotReq
	var raw map[string]any
	payload, _ := json.Marshal(req)
	_ = json.Unmarshal(payload, &raw)
	want := map[string]any{"id": 1.0, "op": "echo", "args": map[string]any{"n": 3.0}, "deadlineMs": 2000.0, "reader": "verifier", "input": true}
	if fmt.Sprint(raw) != fmt.Sprint(want) {
		t.Fatalf("the request went out as %v, want %v", raw, want)
	}
	// No args still sends an object the agent can decode.
	if _, err := c.Call(context.Background(), Request{Op: "echo"}); err != nil {
		t.Fatal(err)
	}
	if r := <-a.gotReq; fmt.Sprint(r.Args) != "map[]" || r.DeadlineMS != MaxDeadline.Milliseconds() {
		t.Fatalf("a bare request went out with args %v and deadline %d ms", r.Args, r.DeadlineMS)
	}
}

func TestCallAssemblesABlobSplitOverChunks(t *testing.T) {
	a := newFakeAgent(t)
	png := bytes.Repeat([]byte("PNGDATA!"), (2*MaxBlobChunk+999)/8)
	a.handle = func(a *fakeAgent, r wireRequest) {
		for _, chunk := range BlobChunks(r.ID, png) {
			a.send(TypeBlob, chunk)
		}
		a.sendJSON(TypeResponse, map[string]any{"id": r.ID, "ok": true, "result": map[string]int{"width": 1024},
			"blob": map[string]any{"bytes": len(png), "mime": "image/png"}, "ms": 12})
	}
	c := openFake(t, a)
	resp, err := c.Call(context.Background(), Request{Op: "capture", Deadline: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resp.Blob, png) || resp.BlobMIME != "image/png" || string(resp.Result) != `{"width":1024}` {
		t.Fatalf("got %d blob bytes (%s) and %s, want %d", len(resp.Blob), resp.BlobMIME, resp.Result, len(png))
	}
}

func TestAnIncompleteBlobFailsTheCallNotTheChannel(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(a *fakeAgent, r wireRequest) {
		a.send(TypeBlob, EncodeBlobChunk(r.ID, 0, false, []byte("half")))
		a.sendJSON(TypeResponse, map[string]any{"id": r.ID, "ok": true, "result": map[string]int{},
			"blob": map[string]any{"bytes": 8, "mime": "image/png"}})
	}
	c := openFake(t, a)
	if _, err := c.Call(context.Background(), Request{Op: "capture", Deadline: time.Second}); err == nil || !strings.Contains(err.Error(), "did not send all") {
		t.Fatalf("got %v", err)
	}
	select {
	case <-c.Done():
		t.Fatalf("the channel ended: %v", c.Err())
	default:
	}
}

func TestAnOutOfOrderBlobChunkEndsTheChannel(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(a *fakeAgent, r wireRequest) { a.send(TypeBlob, EncodeBlobChunk(r.ID, 1, true, []byte("x"))) }
	c := openFake(t, a)
	_, err := c.Call(context.Background(), Request{Op: "capture", Deadline: time.Second})
	if !isLost(err) || !strings.Contains(err.Error(), "chunk 1") {
		t.Fatalf("got %v, want ErrLost naming the chunk", err)
	}
}

func TestCallReturnsTheAgentsError(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(a *fakeAgent, r wireRequest) {
		a.respond(r.ID, false, nil, &Error{Code: CodeRefused, Message: "e41 is covered by e70", Detail: json.RawMessage(`{"reason":"covered"}`)})
	}
	c := openFake(t, a)
	_, err := c.Call(context.Background(), Request{Op: "fail", Deadline: time.Second})
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeRefused || string(ae.Detail) != `{"reason":"covered"}` {
		t.Fatalf("got %v", err)
	}
}

func TestAnUnansweredCallFailsAfterItsDeadlineAndTheChannelEndsAfterTheWedgeGrace(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(*fakeAgent, wireRequest) {} // never answers
	c := openFake(t, a)
	start := time.Now()
	_, err := c.Call(context.Background(), Request{Op: "hang", Deadline: 100 * time.Millisecond})
	took := time.Since(start)
	if !errors.Is(err, ErrDeadline) {
		t.Fatalf("got %v, want ErrDeadline", err)
	}
	if took < 150*time.Millisecond || took > time.Second {
		t.Fatalf("the call failed after %s; want its deadline plus the grace, 150 ms", took)
	}
	waitFor(t, time.Second, "CANCEL", func() bool { return len(a.sent(TypeCancel)) == 1 })
	if got := a.sent(TypeCancel)[0]; got != `{"id":1}` {
		t.Fatalf("CANCEL carried %s", got)
	}
	select {
	case <-c.Done():
		if !strings.Contains(c.Err().Error(), "wedged") || time.Since(start) < 250*time.Millisecond {
			t.Fatalf("the channel ended after %s with %v", time.Since(start), c.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a request unanswered past its deadline plus the wedge grace did not end the channel")
	}
}

func TestACallerThatGivesUpSendsCancelAndGetsItsContextsError(t *testing.T) {
	a := newFakeAgent(t)
	answer := make(chan uint32, 1)
	a.handle = func(a *fakeAgent, r wireRequest) {
		if r.Op == "slow" {
			answer <- r.ID
			return
		}
		a.respond(r.ID, true, r.Args, nil)
	}
	c := openFake(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-a.gotReq; cancel() }()
	_, err := c.Call(ctx, Request{Op: "slow", Deadline: 5 * time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	waitFor(t, time.Second, "CANCEL", func() bool { return len(a.sent(TypeCancel)) == 1 })
	// The late answer is dropped and the channel goes on.
	a.respond(<-answer, true, "late", nil)
	if _, err := c.Call(context.Background(), Request{Op: "echo", Args: 1, Deadline: time.Second}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCallsAnsweredOutOfOrderGetTheirOwnAnswers(t *testing.T) {
	a := newFakeAgent(t)
	const n = 40
	var mu sync.Mutex
	var held []wireRequest
	a.handle = func(a *fakeAgent, r wireRequest) {
		mu.Lock()
		held = append(held, r)
		if len(held) < n {
			mu.Unlock()
			return
		}
		all := held
		mu.Unlock()
		for i := len(all) - 1; i >= 0; i-- { // newest first
			a.respond(all[i].ID, true, all[i].Args, nil)
		}
	}
	c := openFake(t, a)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Call(context.Background(), Request{Op: "echo", Args: i, Deadline: 2 * time.Second})
			if err == nil && string(resp.Result) != fmt.Sprint(i) {
				err = fmt.Errorf("call %d got %s", i, resp.Result)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoPongEndsTheChannelAfterThePongTimeout(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(*fakeAgent, wireRequest) {}
	c := openFake(t, a)
	a.noPong.Store(true)
	start := time.Now()
	_, err := c.Call(context.Background(), Request{Op: "hang", Deadline: 10 * time.Second})
	took := time.Since(start)
	if !isLost(err) || !strings.Contains(err.Error(), "PONG") {
		t.Fatalf("got %v, want ErrLost for a missing PONG", err)
	}
	if took > time.Second {
		t.Fatalf("a silent agent was found after %s; the PONG timeout is 150 ms", took)
	}
	if a.tr.closeCalls.Load() == 0 {
		waitFor(t, time.Second, "the transport closed", func() bool { return a.tr.closeCalls.Load() > 0 })
	}
}

func TestEOFFailsEveryPendingCallAtOnce(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(*fakeAgent, wireRequest) {}
	c := openFake(t, a)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Call(context.Background(), Request{Op: "hang", Deadline: 30 * time.Second, Input: true})
			errs <- err
		}()
	}
	for range 3 {
		<-a.gotReq
	}
	start := time.Now()
	a.die(errors.New("killed by signal: killed"))
	wg.Wait()
	close(errs)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("pending calls failed %s after EOF, want at once", took)
	}
	for err := range errs {
		if !isLost(err) || !strings.Contains(err.Error(), "may or may not have been posted") || !strings.Contains(err.Error(), "killed") {
			t.Fatalf("got %v, want ErrLost naming the exit", err)
		}
	}
	if _, err := c.Call(context.Background(), Request{Op: "echo"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a call on an ended connection got %v, want ErrUnavailable (nothing sent)", err)
	}
}

func TestAnOversizeFrameFromTheAgentEndsTheChannel(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(a *fakeAgent, _ wireRequest) {
		a.wmu.Lock()
		defer a.wmu.Unlock()
		_, _ = a.out.Write([]byte{TypeResponse, 0xFF, 0, 0, 0})
	}
	c := openFake(t, a)
	if _, err := c.Call(context.Background(), Request{Op: "echo", Deadline: time.Second}); !isLost(err) || !errors.Is(c.Err(), ErrFrameTooLarge) {
		t.Fatalf("got %v and %v", err, c.Err())
	}
}

func TestAnUnreadableResponseEndsTheChannel(t *testing.T) {
	a := newFakeAgent(t)
	a.handle = func(a *fakeAgent, _ wireRequest) { a.send(TypeResponse, []byte("{not json")) }
	c := openFake(t, a)
	if _, err := c.Call(context.Background(), Request{Op: "echo", Deadline: time.Second}); !isLost(err) || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("got %v", err)
	}
}

func TestPauseAndResumeFrames(t *testing.T) {
	a := newFakeAgent(t)
	c := openFake(t, a)
	if err := c.Pause("human"); err != nil {
		t.Fatal(err)
	}
	if err := c.Resume(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "PAUSE and RESUME", func() bool { return len(a.sent(TypeResume)) == 1 })
	if p := a.sent(TypePause); len(p) != 1 || p[0] != `{"holder":"human"}` || a.sent(TypeResume)[0] != `{}` {
		t.Fatalf("PAUSE %v, RESUME %v", p, a.sent(TypeResume))
	}
}

func TestEventsReachOnEventAndUnknownFramesAreSkipped(t *testing.T) {
	a := newFakeAgent(t)
	got := make(chan Event, 4)
	o := fastOptions()
	o.OnEvent = func(e Event) { got <- e }
	c, err := Open(context.Background(), a.start(true).tr, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	a.send(TypeStream, []byte{1, 2, 3})
	a.send(0x7F, []byte("from a newer agent"))
	a.sendJSON(TypeEvent, map[string]any{"kind": "stalled", "in": "capture", "seconds": 10})
	select {
	case e := <-got:
		if e.Kind != EventStalled || !strings.Contains(string(e.Payload), `"in":"capture"`) {
			t.Fatalf("got %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	if _, err := c.Call(context.Background(), Request{Op: "echo", Args: 1, Deadline: time.Second}); err != nil {
		t.Fatal(err)
	}
}
