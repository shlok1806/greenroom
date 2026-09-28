package guestagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Transport is how frames reach the agent: its stdin (Write) and stdout, and the process that
// carries them. *tart.Pipe fits; tests use pipes in process.
type Transport interface {
	io.Writer
	Stdout() io.Reader
	Done() <-chan struct{} // closes when the process has exited
	Err() error            // why it exited, once Done has closed
	Close() error          // ends it (stdin EOF, then a kill) and waits for it
}

// Options are a connection's timings (daemon ADR 0005 points 4 and 7). Zero values take the
// defaults; tests shorten them to milliseconds.
type Options struct {
	HelloTimeout time.Duration // how long Open waits for HELLO (30 s)
	PingInterval time.Duration // how often PING is sent (2 s)
	PongTimeout  time.Duration // no PONG for this long ends the channel (6 s)
	CallGrace    time.Duration // a call waits its deadline plus this, then fails with ErrDeadline (2 s)
	WedgeGrace   time.Duration // a request unanswered at its deadline plus this ends the channel (5 s)
	// OnEvent gets every EVENT, on the reader goroutine: it must return quickly.
	OnEvent func(Event)
	Log     *slog.Logger
}

func (o Options) withDefaults() Options {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.HelloTimeout, 30*time.Second)
	def(&o.PingInterval, 2*time.Second)
	def(&o.PongTimeout, 6*time.Second)
	def(&o.CallGrace, 2*time.Second)
	def(&o.WedgeGrace, 5*time.Second)
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return o
}

// maxBlobBytes caps the BLOB bytes held for unanswered requests at once. A full-screen PNG of a
// Retina 5K display is about 30 MB.
const maxBlobBytes = 64 << 20

// errClosed is why a connection its owner closed ended.
var errClosed = errors.New("the connection was closed")

// Conn is one live connection to a guest agent: requests multiplexed by id, their BLOBs
// assembled, a heartbeat, and deadlines on every call. Any failure of the channel ends the whole
// connection and fails every outstanding call at once; a Conn is never reused after that.
type Conn struct {
	t     Transport
	o     Options
	hello Hello
	caps  map[string]bool
	gen   uint64

	out  chan []byte // frames for the writer; unbuffered, so a taken frame is being written
	ctl  chan []byte // PING and CANCEL, which must never wait behind a caller
	done chan struct{}

	mu        sync.Mutex
	dead      bool
	err       error
	nextID    uint32
	pending   map[uint32]*pending
	blobs     map[uint32]*blobBuf
	blobBytes int
	lastPong  time.Time

	killOnce sync.Once
	wg       sync.WaitGroup // reader, writer and monitor
	closed   chan struct{}  // closes once the transport is closed and every goroutine returned
	closeErr error
}

// pending is one request waiting for its RESPONSE. After its caller gives up it stays, without a
// waiter, until the answer comes or its wedge time passes.
type pending struct {
	op      string
	ch      chan callResult // buffered 1; gets exactly one result
	wedgeAt time.Time
}

type callResult struct {
	resp Response
	err  error
}

type blobBuf struct {
	data     []byte
	next     uint16
	complete bool
}

// Open starts speaking to the agent on t and returns once it has said HELLO with this package's
// Protocol. On any failure t is closed. Gen is 0 for a connection made outside a Supervisor.
func Open(ctx context.Context, t Transport, o Options) (*Conn, error) {
	return open(ctx, t, o, 0)
}

func open(ctx context.Context, t Transport, o Options, gen uint64) (*Conn, error) {
	o = o.withDefaults()
	c := &Conn{t: t, o: o, gen: gen, out: make(chan []byte), ctl: make(chan []byte, 64), done: make(chan struct{}),
		pending: map[uint32]*pending{}, blobs: map[uint32]*blobBuf{}, closed: make(chan struct{})}
	hello := make(chan Hello, 1)
	c.wg.Add(2)
	go c.read(hello)
	go c.write()

	timer := time.NewTimer(o.HelloTimeout)
	defer timer.Stop()
	var err error
	select {
	case h := <-hello:
		c.hello = h
	case <-c.done:
		err = c.Err()
	case <-timer.C:
		err = fmt.Errorf("the guest agent did not say HELLO within %s", o.HelloTimeout)
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		c.kill(err)
		<-c.closed
		return nil, err
	}
	c.caps = make(map[string]bool, len(c.hello.Caps))
	for _, op := range c.hello.Caps {
		c.caps[op] = true
	}
	c.mu.Lock()
	c.lastPong = time.Now()
	c.mu.Unlock()
	c.wg.Add(1)
	go c.monitor()
	return c, nil
}

// Hello is what the agent announced.
func (c *Conn) Hello() Hello { return c.hello }

// Gen is the connection's generation within its Supervisor: 1 for the first, one more for each
// reconnect. Refs handed out on one connection are scoped to its generation.
func (c *Conn) Gen() uint64 { return c.gen }

// Has reports whether the agent listed op in its caps.
func (c *Conn) Has(op string) bool { return c.caps[op] }

// Done closes when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, or nil while it is live.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection, failing outstanding calls with ErrLost, and waits until the
// transport is closed.
func (c *Conn) Close() error {
	c.kill(errClosed)
	<-c.closed
	return c.closeErr
}

// Call sends r and waits for its answer: the result, the agent's *Error, ErrDeadline once the
// deadline plus CallGrace has passed, ErrLost when the channel ends first, or ctx's error when
// the caller gives up first. Giving up sends CANCEL; a late answer is dropped.
func (c *Conn) Call(ctx context.Context, r Request) (Response, error) {
	if r.Op == "" {
		return Response{}, errors.New("a guest agent request needs an op")
	}
	d := r.Deadline
	if d <= 0 || d > MaxDeadline {
		d = MaxDeadline
	}
	args := r.Args
	if args == nil {
		args = struct{}{}
	}
	p := &pending{op: r.Op, ch: make(chan callResult, 1), wedgeAt: time.Now().Add(d + c.o.WedgeGrace)}
	c.mu.Lock()
	if c.dead {
		err := c.err
		c.mu.Unlock()
		return Response{}, fmt.Errorf("%w: the connection had already ended (%v)", ErrUnavailable, err)
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = p
	c.mu.Unlock()

	payload, err := json.Marshal(wireRequest{ID: id, Op: r.Op, Args: args, DeadlineMS: d.Milliseconds(), Reader: r.Reader, Input: r.Input})
	if err == nil && len(payload) > MaxPayload {
		err = fmt.Errorf("%w: the %s request is %d bytes", ErrFrameTooLarge, r.Op, len(payload))
	}
	if err != nil {
		c.forget(id)
		return Response{}, err
	}
	frame, _ := encodeFrame(TypeRequest, payload)
	select {
	case c.out <- frame:
	case <-c.done:
		c.forget(id)
		return Response{}, fmt.Errorf("%w: the connection ended before %s was sent (%v)", ErrUnavailable, r.Op, c.Err())
	case <-ctx.Done():
		c.forget(id)
		return Response{}, ctx.Err()
	}

	timer := time.NewTimer(d + c.o.CallGrace)
	defer timer.Stop()
	select {
	case res := <-p.ch:
		return res.resp, res.err
	case <-ctx.Done():
		c.cancel(id)
		return Response{}, ctx.Err()
	case <-timer.C:
		c.cancel(id)
		return Response{}, fmt.Errorf("%w: %s gave no answer within %s", ErrDeadline, r.Op, (d + c.o.CallGrace).Round(time.Millisecond))
	}
}

// Pause sends PAUSE: the agent refuses inputs of every seat but holder until Resume.
func (c *Conn) Pause(holder string) error {
	return c.sendJSON(TypePause, map[string]string{"holder": holder})
}

// Resume sends RESUME.
func (c *Conn) Resume() error {
	return c.sendJSON(TypeResume, struct{}{})
}

// sendJSON queues one frame for the writer and waits until it is taken or the channel ends.
func (c *Conn) sendJSON(typ byte, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	frame, err := encodeFrame(typ, payload)
	if err != nil {
		return err
	}
	select {
	case c.out <- frame:
		return nil
	case <-c.done:
		return fmt.Errorf("%w: %v", ErrUnavailable, c.Err())
	}
}

// forget drops a request that was never sent.
func (c *Conn) forget(id uint32) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// cancel tells the agent a caller gave up on request id. The request stays pending, without a
// waiter, so an agent that never answers it is still found wedged.
func (c *Conn) cancel(id uint32) {
	payload, _ := json.Marshal(map[string]uint32{"id": id})
	frame, _ := encodeFrame(TypeCancel, payload)
	select {
	case c.ctl <- frame:
	default: // the writer is far behind; the deadline stops the request anyway
	}
}

// read is the reader goroutine: HELLO first, then every frame the agent sends. Any read error
// or unreadable frame ends the connection.
func (c *Conn) read(hello chan<- Hello) {
	defer c.wg.Done()
	r := bufio.NewReaderSize(c.t.Stdout(), 64<<10)
	greeted := false
	for {
		typ, payload, err := ReadFrame(r)
		if err != nil {
			c.kill(c.readFailure(err))
			return
		}
		if !greeted {
			if err := c.onHello(typ, payload, hello); err != nil {
				c.kill(err)
				return
			}
			greeted = true
			continue
		}
		switch typ {
		case TypeResponse:
			err = c.onResponse(payload)
		case TypeBlob:
			err = c.onBlob(payload)
		case TypeEvent:
			err = c.onEvent(payload)
		case TypePong:
			c.mu.Lock()
			c.lastPong = time.Now()
			c.mu.Unlock()
		default:
			// STREAM is reserved for wave 3, and a newer agent may send kinds this daemon does
			// not know: a readable frame of another type is skipped, never fatal.
		}
		if err != nil {
			c.kill(err)
			return
		}
	}
}

func (c *Conn) onHello(typ byte, payload []byte, hello chan<- Hello) error {
	if typ != TypeHello {
		return fmt.Errorf("the guest agent's first frame was type 0x%02x, not HELLO", typ)
	}
	var h Hello
	if err := json.Unmarshal(payload, &h); err != nil {
		return fmt.Errorf("the guest agent's HELLO is unreadable: %w", err)
	}
	if h.Protocol != Protocol {
		return fmt.Errorf("the guest agent speaks protocol %d and this daemon %d: the input helper in this machine "+
			"does not match the daemon; rebuild the image (scripts/build-image.sh -force)", h.Protocol, Protocol)
	}
	hello <- h
	return nil
}

// readFailure names why reading stopped: the agent's process ended, or the stream broke.
func (c *Conn) readFailure(err error) error {
	if errors.Is(err, ErrFrameTooLarge) {
		return err
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// The process usually exits with its stdout; give it a moment to be reaped so its exit
		// status and stderr name the cause.
		select {
		case <-c.t.Done():
		case <-time.After(200 * time.Millisecond):
		}
		if exit := c.exitError(); exit != nil {
			return fmt.Errorf("the guest agent's channel closed: %w", exit)
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return errors.New("the guest agent's channel closed in the middle of a frame")
		}
		return errors.New("the guest agent's channel closed (EOF)")
	}
	return fmt.Errorf("read from the guest agent: %w", err)
}

// exitError is the transport's exit error once its process has exited.
func (c *Conn) exitError() error {
	select {
	case <-c.t.Done():
		return c.t.Err()
	default:
		return nil
	}
}

func (c *Conn) onResponse(payload []byte) error {
	var r wireResponse
	if err := json.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("a RESPONSE from the guest agent is unreadable: %w", err)
	}
	c.mu.Lock()
	p := c.pending[r.ID]
	blob := c.blobs[r.ID]
	delete(c.pending, r.ID)
	if blob != nil {
		delete(c.blobs, r.ID)
		c.blobBytes -= len(blob.data)
	}
	c.mu.Unlock()
	if p == nil {
		return nil // a request whose caller gave up long ago, and already found not wedged
	}
	var res callResult
	switch {
	case !r.OK && r.Error != nil:
		res.err = r.Error
	case !r.OK:
		res.err = &Error{Code: CodeInternal, Message: "the guest agent failed " + p.op + " without saying why"}
	default:
		res.resp = Response{Result: r.Result, Elapsed: time.Duration(r.MS * float64(time.Millisecond))}
		if r.Blob != nil {
			switch {
			case blob == nil || !blob.complete:
				res.err = fmt.Errorf("the guest agent announced a %d byte blob for %s but did not send all of it", r.Blob.Bytes, p.op)
			case r.Blob.Bytes > 0 && r.Blob.Bytes != len(blob.data):
				res.err = fmt.Errorf("the guest agent announced a %d byte blob for %s and sent %d", r.Blob.Bytes, p.op, len(blob.data))
			default:
				res.resp.Blob, res.resp.BlobMIME = blob.data, r.Blob.MIME
			}
		}
	}
	p.ch <- res
	return nil
}

func (c *Conn) onBlob(payload []byte) error {
	id, seq, last, data, err := DecodeBlobChunk(payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == 0 || id > c.nextID {
		return fmt.Errorf("the guest agent sent a BLOB for request %d, which was never sent", id)
	}
	b := c.blobs[id]
	if b == nil {
		b = &blobBuf{}
		c.blobs[id] = b
	}
	switch {
	case b.complete:
		return fmt.Errorf("the guest agent sent a BLOB chunk for request %d after its last", id)
	case seq != b.next:
		return fmt.Errorf("the guest agent sent BLOB chunk %d of request %d where %d was due", seq, id, b.next)
	}
	c.blobBytes += len(data)
	if c.blobBytes > maxBlobBytes {
		return fmt.Errorf("the guest agent sent over %d MiB of blobs not yet answered", maxBlobBytes>>20)
	}
	b.data = append(b.data, data...)
	b.next++
	b.complete = last
	return nil
}

func (c *Conn) onEvent(payload []byte) error {
	var e struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(payload, &e); err != nil {
		return fmt.Errorf("an EVENT from the guest agent is unreadable: %w", err)
	}
	if c.o.OnEvent != nil && e.Kind != "" {
		c.o.OnEvent(Event{Kind: e.Kind, Payload: json.RawMessage(payload)})
	}
	return nil
}

// write is the writer goroutine: one frame at a time, callers' frames and control frames alike.
// A write error ends the connection.
func (c *Conn) write() {
	defer c.wg.Done()
	for {
		var frame []byte
		select {
		case frame = <-c.ctl:
		case frame = <-c.out:
		case <-c.done:
			return
		}
		if _, err := c.t.Write(frame); err != nil {
			c.kill(fmt.Errorf("write to the guest agent: %w", err))
			return
		}
	}
}

// monitor sends PING every PingInterval and ends the connection when no PONG came for
// PongTimeout, when a request is unanswered past its deadline plus WedgeGrace, or when the
// agent's process exits.
func (c *Conn) monitor() {
	defer c.wg.Done()
	ping := time.NewTicker(c.o.PingInterval)
	defer ping.Stop()
	check := time.NewTicker(max(time.Millisecond, min(250*time.Millisecond, c.o.PongTimeout/4, c.o.WedgeGrace/4)))
	defer check.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-c.t.Done():
			err := c.t.Err()
			if err == nil {
				err = errors.New("exit 0")
			}
			c.kill(fmt.Errorf("the guest agent's process ended: %w", err))
			return
		case now := <-ping.C:
			payload, _ := json.Marshal(map[string]int64{"t": now.UnixMilli()})
			frame, _ := encodeFrame(TypePing, payload)
			select {
			case c.ctl <- frame:
			default: // the writer is stuck; the PONG timeout below ends the channel
			}
		case now := <-check.C:
			if err := c.health(now); err != nil {
				c.kill(err)
				return
			}
		}
	}
}

// health is why the connection must end now, or nil.
func (c *Conn) health(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if quiet := now.Sub(c.lastPong); quiet > c.o.PongTimeout {
		return fmt.Errorf("the guest agent sent no PONG for %s", quiet.Round(time.Millisecond))
	}
	for id, p := range c.pending {
		if now.After(p.wedgeAt) {
			return fmt.Errorf("the guest agent did not answer %s (request %d) by its deadline plus %s, so it is wedged", p.op, id, c.o.WedgeGrace)
		}
	}
	return nil
}

// kill ends the connection once: every outstanding call fails with ErrLost at once, Done
// closes, and the transport is closed in the background (it may take a second to stop).
func (c *Conn) kill(cause error) {
	c.killOnce.Do(func() {
		c.mu.Lock()
		c.dead, c.err = true, cause
		pend := c.pending
		c.pending, c.blobs, c.blobBytes = map[uint32]*pending{}, map[uint32]*blobBuf{}, 0
		c.mu.Unlock()
		lost := fmt.Errorf("%w (%v)", ErrLost, cause)
		for _, p := range pend {
			p.ch <- callResult{err: lost}
		}
		close(c.done)
		if cause != errClosed {
			c.o.Log.Debug("guest agent connection ended", "gen", c.gen, "err", cause)
		}
		go func() {
			c.closeErr = c.t.Close()
			c.wg.Wait()
			close(c.closed)
		}()
	})
}
