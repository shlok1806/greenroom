package machine

// The live screen (ADR 0011): one `greenroom-input --serve` per machine while
// someone watches. Video fans out to viewers; input goes in on the same pipe.

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// Message types of the live screen protocol (ADR 0011).
const (
	ScreenHello    byte = 0x01
	ScreenFormat   byte = 0x02
	ScreenVideo    byte = 0x03
	screenAck      byte = 0x04
	screenLog      byte = 0x05
	screenInput    byte = 0x10
	screenKeyframe byte = 0x11
)

const (
	defaultScreenIdle   = 30 * time.Second
	defaultScreenBuffer = 120 // messages per viewer, about 2 s of video
	screenHelloTimeout  = 30 * time.Second
	screenInputSlack    = 10 * time.Second // on top of what the queued batches take to post
	screenTypeCost      = 2 * time.Millisecond
	screenMaxSleep      = 5000     // ms, the helper's cap on one sleep action
	maxScreenMessage    = 32 << 20 // a garbled length must not allocate the host away
)

// ErrNotReady means the machine cannot serve a live screen now: it is booting,
// failed or being destroyed.
var ErrNotReady = errors.New("machine is not ready")

// errScreenEnded means the stream ended before an input was sent, so a one-shot exec may post it instead.
var errScreenEnded = errors.New("the live screen has ended")

// ScreenMsg is one message of the live screen protocol.
type ScreenMsg struct {
	Type    byte
	Payload []byte
}

// WriteTo writes the message framed as [type u8][length u32 big-endian][payload].
func (s ScreenMsg) WriteTo(w io.Writer) (int64, error) {
	var head [5]byte
	head[0] = s.Type
	binary.BigEndian.PutUint32(head[1:], uint32(len(s.Payload)))
	n, err := w.Write(head[:])
	if err != nil {
		return int64(n), err
	}
	m, err := w.Write(s.Payload)
	return int64(n + m), err
}

func (s ScreenMsg) keyframe() bool {
	return s.Type == ScreenVideo && len(s.Payload) > 0 && s.Payload[0]&1 != 0
}

func readScreenMsg(r io.Reader) (ScreenMsg, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return ScreenMsg{}, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxScreenMessage {
		return ScreenMsg{}, fmt.Errorf("screen message type 0x%02x claims %d bytes", head[0], n)
	}
	msg := ScreenMsg{Type: head[0], Payload: make([]byte, n)}
	if _, err := io.ReadFull(r, msg.Payload); err != nil {
		return ScreenMsg{}, err
	}
	return msg, nil
}

func frameScreenMsg(typ byte, payload []byte) []byte {
	b := make([]byte, 5+len(payload))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:], uint32(len(payload)))
	copy(b[5:], payload)
	return b
}

// ScreenWatch is one viewer of a machine's live screen. C yields HELLO, the
// latest FORMAT, then VIDEO from the next keyframe on. A viewer that falls
// behind loses its backlog and resumes at a keyframe. C closes when the
// stream ends (Err says why) or after Close.
type ScreenWatch struct {
	C <-chan ScreenMsg

	ch     chan ScreenMsg
	s      *screenStream
	synced bool  // guarded by s.mu: a keyframe has been delivered since the last drop
	err    error // guarded by s.mu; set before ch closes
}

// Err is why C closed, or nil while it is open or after Close.
func (w *ScreenWatch) Err() error {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	return w.err
}

// Close stops the viewer. The stream stops once it has had no viewer for its idle time.
func (w *ScreenWatch) Close() {
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watches[w]; !ok {
		return
	}
	delete(s.watches, w)
	close(w.ch)
	if len(s.watches) == 0 {
		s.armIdleLocked()
	}
}

// screenStream is one running `greenroom-input --serve`.
type screenStream struct {
	pipe      *tart.Pipe
	log       *slog.Logger
	idleAfter time.Duration
	buffer    int
	slack     time.Duration

	hello    chan struct{} // closed when HELLO arrives
	done     chan struct{} // closed when the stream ends
	writes   chan []byte   // framed INPUTs for the writer
	keyframe chan struct{} // one pending KEYFRAME request

	mu       sync.Mutex
	helloMsg ScreenMsg
	format   *ScreenMsg
	watches  map[*ScreenWatch]struct{}
	acks     map[int64]chan string // INPUT id to its ACK's error ("" for success)
	nextID   int64
	backlog  time.Duration // what the INPUTs awaiting an ACK take to post; the helper runs them in order
	lastLog  string        // the helper's last LOG, which names a fatal capture error
	idleGen  int           // invalidates an idle timer that fired after a viewer came back
	ended    bool
	err      error
}

func newScreenStream(pipe *tart.Pipe, log *slog.Logger, idleAfter time.Duration, buffer int, slack time.Duration) *screenStream {
	s := &screenStream{
		pipe: pipe, log: log, idleAfter: idleAfter, buffer: max(buffer, 3), slack: slack,
		hello: make(chan struct{}), done: make(chan struct{}),
		writes: make(chan []byte), keyframe: make(chan struct{}, 1),
		watches: map[*ScreenWatch]struct{}{}, acks: map[int64]chan string{},
	}
	s.mu.Lock()
	s.armIdleLocked() // nobody may ever subscribe, for example if the caller gave up
	s.mu.Unlock()
	go s.read()
	go s.write()
	return s
}

func (s *screenStream) running() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *screenStream) cause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// end stops the stream and closes every viewer with err. Safe to repeat.
func (s *screenStream) end(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endLocked(err)
}

func (s *screenStream) endLocked(err error) {
	if s.ended {
		return
	}
	s.ended, s.err = true, err
	s.idleGen++
	close(s.done)
	for w := range s.watches {
		w.err = err
		close(w.ch)
	}
	s.watches = nil
	go func() { _ = s.pipe.Close() }()
}

func (s *screenStream) armIdleLocked() {
	s.idleGen++
	gen := s.idleGen
	time.AfterFunc(s.idleAfter, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if gen == s.idleGen && len(s.watches) == 0 {
			s.endLocked(errors.New("the live screen stopped: nobody is watching"))
		}
	})
}

// watch adds a viewer, or reports false if the stream has ended.
func (s *screenStream) watch() (*ScreenWatch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return nil, false
	}
	ch := make(chan ScreenMsg, s.buffer)
	w := &ScreenWatch{C: ch, ch: ch, s: s}
	ch <- s.helloMsg
	if s.format != nil {
		ch <- *s.format
	}
	s.watches[w] = struct{}{}
	s.idleGen++
	s.requestKeyframe()
	return w, true
}

func (s *screenStream) requestKeyframe() {
	select {
	case s.keyframe <- struct{}{}:
	default:
	}
}

// read parses the helper's stdout until it ends, then ends the stream.
func (s *screenStream) read() {
	r := bufio.NewReaderSize(s.pipe.Stdout(), 256*1024)
	var err error
	for {
		var msg ScreenMsg
		if msg, err = readScreenMsg(r); err != nil {
			break
		}
		if err = s.dispatch(msg); err != nil {
			break
		}
	}
	select {
	case <-s.pipe.Done():
	case <-time.After(2 * time.Second):
	}
	if perr := s.pipe.Err(); perr != nil || errors.Is(err, io.EOF) {
		err = perr
	}
	if err == nil {
		err = errors.New("the screen helper exited")
	}
	s.mu.Lock()
	if s.lastLog != "" {
		err = fmt.Errorf("%w (last log: %s)", err, s.lastLog)
	}
	s.endLocked(fmt.Errorf("the live screen ended: %w", err))
	s.mu.Unlock()
}

func (s *screenStream) dispatch(msg ScreenMsg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch msg.Type {
	case ScreenHello:
		select {
		case <-s.hello:
		default:
			s.helloMsg = msg
			close(s.hello)
		}
	case ScreenFormat:
		s.format = &msg
		s.broadcastLocked(msg)
	case ScreenVideo:
		s.broadcastLocked(msg)
	case screenAck:
		var ack struct {
			ID    int64  `json:"id"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(msg.Payload, &ack); err != nil {
			return fmt.Errorf("unreadable ACK %q: %w", msg.Payload, err)
		}
		ch, ok := s.acks[ack.ID]
		if !ok {
			s.log.Warn("screen helper acknowledged an input nobody is waiting for", "ack", string(msg.Payload))
			break
		}
		ch <- ack.Error
		delete(s.acks, ack.ID)
	case screenLog:
		s.lastLog = string(msg.Payload)
		s.log.Info("screen helper", "log", s.lastLog)
	default:
		return fmt.Errorf("unexpected screen message type 0x%02x", msg.Type)
	}
	return nil
}

// broadcastLocked never blocks: a viewer whose buffer is full loses its
// backlog and restarts from the latest FORMAT and the next keyframe.
func (s *screenStream) broadcastLocked(msg ScreenMsg) {
	for w := range s.watches {
		if msg.Type == ScreenVideo && !w.synced {
			if !msg.keyframe() {
				continue
			}
			w.synced = true
		}
		select {
		case w.ch <- msg:
			continue
		default:
		}
		for len(w.ch) > 0 {
			select {
			case <-w.ch:
			default:
			}
		}
		if s.format != nil && msg.Type != ScreenFormat {
			w.ch <- *s.format
		}
		if msg.keyframe() {
			w.ch <- msg
			continue
		}
		if msg.Type == ScreenFormat {
			w.ch <- msg
		}
		w.synced = false
		s.requestKeyframe()
	}
}

// write owns the helper's stdin.
func (s *screenStream) write() {
	for {
		var b []byte
		select {
		case <-s.done:
			return
		case b = <-s.writes:
		case <-s.keyframe:
			b = frameScreenMsg(screenKeyframe, nil)
		}
		if _, err := s.pipe.Write(b); err != nil {
			s.end(fmt.Errorf("the live screen ended: write to the helper: %w", err))
			return
		}
	}
}

// input posts actions (already in guest points) and waits for their ACK. It
// returns errScreenEnded only when nothing was sent.
func (s *screenStream) input(ctx context.Context, actions []InputAction) error {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return errScreenEnded
	}
	s.nextID++
	id := s.nextID
	ack := make(chan string, 1)
	s.acks[id] = ack
	cost := inputCost(actions)
	s.backlog += cost
	wait := s.backlog + s.slack
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.acks, id)
		s.backlog -= cost
		s.mu.Unlock()
	}()

	payload, err := json.Marshal(struct {
		ID      int64         `json:"id"`
		Actions []InputAction `json:"actions"`
	}{id, actions})
	if err != nil {
		return err
	}
	sendCtx, cancelSend := context.WithTimeout(ctx, wait)
	defer cancelSend()
	select {
	case s.writes <- frameScreenMsg(screenInput, payload):
	case <-s.done:
		return errScreenEnded
	case <-sendCtx.Done():
		return fmt.Errorf("send input to the live screen: %w", sendCtx.Err())
	}
	// Concurrent batches can reach the helper in another order than they
	// registered, so the ACK deadline counts the backlog as of the send: every
	// batch sent ahead of this one is still in it.
	s.mu.Lock()
	wait = s.backlog + s.slack
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	select {
	case msg := <-ack:
		if msg != "" {
			return errors.New(msg)
		}
		return nil
	case <-s.done:
		return fmt.Errorf("the input may not have been posted: %w", s.cause())
	case <-ctx.Done():
		return fmt.Errorf("the machine did not acknowledge the input: %w", ctx.Err())
	}
}

// inputCost is about how long the helper takes to post actions: its sleeps
// and the pause after each typed key.
// focusCost is the most a focus action waits for its app to come to the front (the helper's
// focusWait), with room for the raise (daemon ADR 0009).
const focusCost = 1500 * time.Millisecond

func inputCost(actions []InputAction) time.Duration {
	var d time.Duration
	for _, a := range actions {
		switch strings.ToLower(a.Type) {
		case "sleep":
			d += time.Duration(min(max(a.MS, 0), screenMaxSleep)) * time.Millisecond
		case "type":
			d += time.Duration(utf8.RuneCountInString(a.Text)) * screenTypeCost
		case "focus":
			d += focusCost
		}
	}
	return d
}

// WatchScreen starts the machine's live screen if it is not running and adds
// a viewer. It returns once the helper has said HELLO, so a caller can still
// answer with an error status. The caller must Close the watch.
func (m *Manager) WatchScreen(ctx context.Context, runID string) (*ScreenWatch, error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		s, err := m.screenStream(ctx, mc)
		if err != nil {
			return nil, err
		}
		select {
		case <-s.hello:
		case <-s.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(screenHelloTimeout):
			s.end(errors.New("the screen helper never said hello"))
			return nil, fmt.Errorf("the live screen did not start within %s", screenHelloTimeout)
		}
		if w, ok := s.watch(); ok {
			return w, nil
		}
		// It ended before or just after HELLO; a stream that died at once is
		// retried once, since the next start may find a fresh helper.
		if attempt == 1 {
			return nil, s.cause()
		}
	}
}

// screenStream returns the machine's running stream, starting one if needed.
func (m *Manager) screenStream(ctx context.Context, mc *Machine) (*screenStream, error) {
	mc.input.screenMu.Lock()
	defer mc.input.screenMu.Unlock()
	m.mu.Lock()
	s, live, status := mc.screen, m.liveLocked(mc), mc.Status
	m.mu.Unlock()
	if !live || status != Ready {
		return nil, fmt.Errorf("%w: machine %s is %s", ErrNotReady, mc.RunID, describeStatus(live, status))
	}
	if s != nil && s.running() {
		return s, nil
	}
	if _, err := m.ensureInput(ctx, mc); err != nil {
		return nil, err
	}
	m.ensureCaptureApproval(ctx, mc)
	pipe, err := m.tart.StartPipe(mc.Name, "/bin/sh", "-c", serveScript())
	if err != nil {
		return nil, fmt.Errorf("start the live screen: %w", err)
	}
	s = newScreenStream(pipe, m.Log.With("runId", mc.RunID), m.screenIdle, m.screenBuffer, m.screenInputSlack)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.liveLocked(mc) {
		s.end(errors.New("the machine is gone"))
		return nil, fmt.Errorf("%w: machine %s is being destroyed", ErrNotReady, mc.RunID)
	}
	if mc.Status != Ready { // a reboot began while it started
		s.end(errors.New("the machine is " + string(mc.Status)))
		return nil, fmt.Errorf("%w: machine %s is %s", ErrNotReady, mc.RunID, mc.Status)
	}
	mc.screen = s
	return s, nil
}

// serveScript starts the helper's serve mode after killing any left behind:
// the guest agent does not close a helper's stdin when its host exec dies, so
// on a still screen an orphan never notices. The bracket keeps pkill from
// matching this shell's own command line.
func serveScript() string {
	name := filepath.Base(helperName())
	return fmt.Sprintf(`pkill -f '[%s]%s --serve'; exec "$HOME/%s" --serve`, name[:1], name[1:], helperName())
}

func describeStatus(live bool, status Status) string {
	if !live {
		return "being destroyed"
	}
	return string(status)
}

// liveScreen returns the machine's stream if one is running.
func (m *Manager) liveScreen(mc *Machine) *screenStream {
	m.mu.Lock()
	s := mc.screen
	m.mu.Unlock()
	if s != nil && s.running() {
		return s
	}
	return nil
}
