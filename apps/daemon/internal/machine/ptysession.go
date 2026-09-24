package machine

// Interactive sessions (ADR 0017): each command runs behind a real pty made in
// the guest, because build tools branch on isatty(). Input goes through one
// long-lived non-tty `tart exec -i`; output comes back from a guest file
// through short `tart exec` reads (sessionguest.go), never streamed through
// tart. The handle is a host process the daemon owns, never a guest pid. A
// command failing inside a session is output, not an error.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

const (
	sessionBufferLimit    = 1 << 20    // output kept in memory per session; older bytes are dropped
	sessionReadLimit      = 256 * 1024 // most one read returns
	maxSessionsPerMachine = 16         // each is a live host process

	// defaultSessionCommand execs so the wrapper shell does not linger.
	defaultSessionCommand = "exec /bin/zsh -li"
	sessionPollInterval   = 250 * time.Millisecond
	sessionSettle         = 150 * time.Millisecond // quiet time that ends a burst of output
	sessionSettleMax      = time.Second            // a steady stream still returns this often

	// maxSessionWait stays under the MCP client's 60 s first-byte timeout.
	maxSessionWait = 50 * time.Second
)

// How the follower paces its reads of the guest file. A full chunk means more
// is waiting, so it reads again at once; after data it looks again soon; with
// nothing new it backs off to sessionFollowIdle. A send or a read wakes it.
// Package vars so tests can shorten them.
var (
	sessionFollowBusy    = 50 * time.Millisecond
	sessionFollowIdleMin = 250 * time.Millisecond
	sessionFollowIdle    = 2 * time.Second
	sessionFollowTimeout = 30 * time.Second // one guest read
	sessionCloseTimeout  = 15 * time.Second // the guest cleanup on close
	// sessionFollowGiveUp is how many failed reads in a row after the command
	// exits end the session without its last output (the guest is gone).
	sessionFollowGiveUp = 5
)

// PTYSession is one interactive command inside a guest, addressed by (runId, id).
type PTYSession struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"startedAt"`

	proc *tart.Session
	out  *stream

	poke         chan struct{}      // wakes the follower; buffered 1
	ended        chan struct{}      // closed once the command exited and its output is all in out
	stopFollower context.CancelFunc // ends the follower early (close, machine gone)
	followErr    error              // why the last output could not be read; written before ended closes

	mu     sync.Mutex // serializes writes, and each read with its offset update; never held while waiting
	offset int64      // how far reads have consumed; guarded by mu
}

func newPTYSession(id, command string, started time.Time) *PTYSession {
	return &PTYSession{
		ID: id, Command: command, StartedAt: started.UTC(),
		out:   newStream(sessionBufferLimit),
		poke:  make(chan struct{}, 1),
		ended: make(chan struct{}),
	}
}

// wake asks the follower to read the guest file now.
func (s *PTYSession) wake() {
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

// running is true until the command has exited and all its output is read.
func (s *PTYSession) running() bool {
	select {
	case <-s.ended:
		return false
	default:
		return true
	}
}

// stop ends the follower and the host `tart exec`. It does not reach the guest.
func (s *PTYSession) stop() error {
	if s.stopFollower != nil {
		s.stopFollower()
	}
	if s.proc == nil {
		return nil
	}
	return s.proc.Close()
}

// SessionStartResult names the session a caller keeps to reach the command again.
type SessionStartResult struct {
	SessionID string `json:"sessionId"`
	Command   string `json:"command"`
	TTY       bool   `json:"tty"`
	Step      int    `json:"step"`
}

// SessionSendResult reports what went into a session's input.
type SessionSendResult struct {
	SessionID string `json:"sessionId"`
	Bytes     int    `json:"bytes"`
	Step      int    `json:"step"`
}

// SessionReadResult is output since the caller's last read.
type SessionReadResult struct {
	SessionID string `json:"sessionId"`
	Output    string `json:"output"`
	FromByte  int64  `json:"fromByte"`
	NextByte  int64  `json:"nextByte"`
	Pending   int64  `json:"pending"`           // written but not yet returned
	Dropped   int64  `json:"dropped,omitempty"` // lost because the caller fell behind
	Running   bool   `json:"running"`
	// ExitCode is how the command ended (issue #62); absent while it runs and
	// when tart failed, which Error explains.
	ExitCode *int `json:"exitCode,omitempty"`
	// Error is why tart ended the session; empty for a command that finished.
	Error string `json:"error,omitempty"`
	Step  int    `json:"step"`
}

// SessionCloseResult confirms a session is gone.
type SessionCloseResult struct {
	SessionID string `json:"sessionId"`
	Step      int    `json:"step"`
}

func newSessionID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("name a session: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// session finds a live session. A destroyed machine answers "no machine for run".
func (m *Manager) session(runID, sessionID string) (*Machine, *PTYSession, error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := mc.sessions[sessionID]
	if !ok || s.proc == nil {
		return nil, nil, fmt.Errorf("no session %q on run %s", sessionID, runID)
	}
	return mc, s, nil
}

// SessionStart runs command in the guest behind a guest-side pty and returns
// the id that reaches it again. The command outlives this call.
func (m *Manager) SessionStart(ctx context.Context, runID, command string) (SessionStartResult, error) {
	mc, err := m.get(runID)
	if err != nil {
		return SessionStartResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return SessionStartResult{}, err
	}
	if strings.TrimSpace(command) == "" {
		command = defaultSessionCommand
	}
	id, err := newSessionID()
	if err != nil {
		return SessionStartResult{}, err
	}
	started := time.Now()

	s := newPTYSession(id, command, started)
	// Reserve before starting so concurrent starts cannot pass the cap.
	if err := m.reserveSession(mc, s); err != nil {
		return SessionStartResult{}, err
	}
	proc, err := m.tart.StartSession(mc.Name, "/bin/sh", "-c", sessionWrapper, "greenroom-session", id, command)
	if err == nil && !m.attachSession(mc, s, proc) {
		_ = proc.Close() // the machine went while tart was starting; its VM goes with it
		err = fmt.Errorf("no machine for run %q", runID)
	}
	var out SessionStartResult
	if err != nil {
		m.dropSession(mc, id)
	} else {
		out = SessionStartResult{SessionID: id, Command: command, TTY: true}
	}
	out.Step = mc.rec.step("machine_session_start",
		map[string]any{"sessionId": id, "command": command}, out, err, started)
	m.emitStep(runID, out.Step)
	if err != nil {
		return SessionStartResult{}, err
	}
	return out, nil
}

// follow copies the session's guest file into its window until the command
// has exited and the file is read to its end, then closes s.ended. It reads
// through short non-tty `tart exec` calls, the path machine_exec uses.
func (m *Manager) follow(ctx context.Context, mc *Machine, s *PTYSession) {
	defer s.out.touch() // a waiting read sees the end at once
	defer close(s.ended)
	var off int64 // guest file offset of the next byte wanted
	pause := time.Duration(0)
	failures := 0
	exit := s.proc.Done() // wakes the follower once, when the command exits
	for {
		if pause > 0 {
			t := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				t.Stop()
				s.followErr = errors.New("the session was closed")
				return
			case <-s.poke:
			case <-exit:
				exit = nil
			case <-t.C:
			}
			t.Stop()
		} else if ctx.Err() != nil {
			s.followErr = errors.New("the session was closed")
			return
		}
		exited := !s.proc.Running() // before the read, so the read sees everything
		n, full, err := m.readGuestSession(ctx, mc, s, &off)
		switch {
		case err != nil:
			failures++
			if exited && failures >= sessionFollowGiveUp {
				s.followErr = fmt.Errorf("the last output could not be read: %w", err)
				return
			}
			if failures == 1 {
				m.Log.Warn("cannot read a session's output; retrying", "run", mc.RunID, "session", s.ID, "err", err)
			}
			pause = min(sessionFollowIdle, sessionFollowIdleMin<<min(failures, 4))
			continue
		case exited && n == 0:
			return // everything the command printed is in the window
		case full:
			pause = 0
		case n > 0:
			pause = sessionFollowBusy
		case pause < sessionFollowIdleMin:
			pause = sessionFollowIdleMin
		default:
			pause = min(pause*2, sessionFollowIdle)
		}
		if failures > 0 {
			m.Log.Info("reading a session's output again", "run", mc.RunID, "session", s.ID)
		}
		failures = 0
	}
}

// readGuestSession appends what the guest file holds past *off to the window,
// skipping ahead (and counting it dropped) when the guest is more than a
// window ahead. It reports how many bytes arrived and whether the read was
// capped, which means more is waiting.
func (m *Manager) readGuestSession(ctx context.Context, mc *Machine, s *PTYSession, off *int64) (n int, full bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, sessionFollowTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code, err := m.tart.ExecTo(ctx, &stdout, &stderr, mc.Name, "/bin/sh", "-c", sessionReadScript,
		"greenroom-session-read", s.ID, strconv.FormatInt(*off, 10), strconv.Itoa(sessionBufferLimit))
	if err != nil {
		return 0, false, err
	}
	if code == sessionReadMissing {
		return 0, false, nil // not created yet, or tart failed before the wrapper ran
	}
	if code != 0 {
		return 0, false, fmt.Errorf("reading the session file exited %d: %s", code, strings.TrimSpace(stderr.String()))
	}
	out := stdout.Bytes()
	nl := bytes.IndexByte(out, '\n')
	if nl < 0 {
		return 0, false, fmt.Errorf("reading the session file printed no offset: %q", truncateForRecord(string(out)))
	}
	start, perr := strconv.ParseInt(string(out[:nl]), 10, 64)
	if perr != nil || start < *off {
		return 0, false, fmt.Errorf("reading the session file printed a bad offset %q", out[:nl])
	}
	data := out[nl+1:]
	if start > *off {
		s.out.skip(start - *off)
	}
	if len(data) > 0 {
		_, _ = s.out.Write(data)
	}
	*off = start + int64(len(data))
	return len(data), len(data) >= sessionBufferLimit, nil
}

// cleanupGuestSession ends the guest side of sessions and removes their files,
// all in one guest exec. A failure is logged: the handles are forgotten either way.
func (m *Manager) cleanupGuestSession(mc *Machine, ids ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionCloseTimeout)
	defer cancel()
	var stderr bytes.Buffer
	args := append([]string{"/bin/sh", "-c", sessionCloseScript, "greenroom-session-close"}, ids...)
	code, err := m.tart.ExecTo(ctx, io.Discard, &stderr, mc.Name, args...)
	if err == nil && code != 0 {
		err = fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		m.Log.Warn("cannot end a session in the guest; its command may still run", "run", mc.RunID, "sessions", ids, "err", err)
	}
}

// reserveSession holds a slot for s, refusing a machine that already left the
// map so a start racing Destroy cannot leave an unreachable process. Only
// running sessions count toward the cap; ended ones stay readable until a
// full machine needs their slots.
func (m *Manager) reserveSession(mc *Machine, s *PTYSession) error {
	ended, err := m.reserveSessionLocked(mc, s)
	if len(ended) > 0 {
		ids := make([]string, 0, len(ended))
		for _, old := range ended {
			_ = old.stop() // already exited; this only reaps the host process
			ids = append(ids, old.ID)
		}
		go m.cleanupGuestSession(mc, ids...) // only removes files; the start does not wait on the guest
	}
	return err
}

func (m *Manager) reserveSessionLocked(mc *Machine, s *PTYSession) (ended []*PTYSession, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.machines[mc.RunID] != mc {
		return nil, fmt.Errorf("no machine for run %q", mc.RunID)
	}
	if mc.sessions == nil {
		mc.sessions = map[string]*PTYSession{}
	}
	if len(mc.sessions) >= maxSessionsPerMachine {
		for id, old := range mc.sessions {
			if old.proc != nil && !old.running() {
				ended = append(ended, old)
				delete(mc.sessions, id)
			}
		}
	}
	if len(mc.sessions) >= maxSessionsPerMachine {
		return ended, fmt.Errorf("run %s already runs %d sessions; close one first",
			mc.RunID, maxSessionsPerMachine)
	}
	mc.sessions[s.ID] = s
	return ended, nil
}

// attachSession gives a reserved session its process and starts copying its
// output, or reports false if the machine (and the reservation) went in the
// meantime. Under m.mu, so detachLocked always sees the follower to stop.
func (m *Manager) attachSession(mc *Machine, s *PTYSession, proc *tart.Session) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mc.sessions[s.ID] != s {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.proc, s.stopFollower = proc, cancel
	go m.follow(ctx, mc, s)
	return true
}

func (m *Manager) dropSession(mc *Machine, id string) *PTYSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := mc.sessions[id]
	delete(mc.sessions, id)
	return s
}

// SessionSend writes data into a session's input exactly as given; the
// caller adds its own newline.
func (m *Manager) SessionSend(ctx context.Context, runID, sessionID, data string) (SessionSendResult, error) {
	mc, s, err := m.session(runID, sessionID)
	if err != nil {
		return SessionSendResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return SessionSendResult{}, err
	}
	started := time.Now()

	s.mu.Lock()
	if !s.proc.Running() {
		err = fmt.Errorf("session %s has ended", sessionID)
	} else if _, werr := io.WriteString(s.proc, data); werr != nil {
		err = fmt.Errorf("write to session %s: %w", sessionID, werr)
	}
	s.mu.Unlock()
	s.wake() // the answer (and the echo) is coming

	out := SessionSendResult{SessionID: sessionID}
	if err == nil {
		out.Bytes = len(data)
	}
	// Only the byte count is recorded: sessions carry secrets, and the echoed
	// output already shows what happened.
	out.Step = mc.rec.step("machine_session_send",
		map[string]any{"sessionId": sessionID, "bytes": len(data)}, out, err, started)
	m.emitStep(runID, out.Step)
	if err != nil {
		return SessionSendResult{}, err
	}
	return out, nil
}

// SessionRead returns output the caller has not seen. With wait > 0 it waits
// until something arrives or the wait ends; the whole wait is one step.
func (m *Manager) SessionRead(ctx context.Context, runID, sessionID string, wait time.Duration) (SessionReadResult, error) {
	mc, s, err := m.session(runID, sessionID)
	if err != nil {
		return SessionReadResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return SessionReadResult{}, err
	}
	wait = min(wait, maxSessionWait)
	started := time.Now()

	s.wake()
	out, err := s.readUntil(ctx, wait)
	out.Step = mc.rec.step("machine_session_read",
		map[string]any{"sessionId": sessionID, "fromByte": out.FromByte}, truncatedRead(out), err, started)
	m.emitStep(runID, out.Step)
	if err != nil {
		return SessionReadResult{}, err
	}
	return out, nil
}

// truncatedRead trims a read for the record; its byte range still says
// exactly what was covered.
func truncatedRead(r SessionReadResult) SessionReadResult {
	r.Output = truncateForRecord(r.Output)
	return r
}

// readUntil reads until output has settled, the command has ended, or wait
// passes, consuming what it returns. It holds s.mu only per attempt, so a
// send can answer a prompt while a read waits.
func (s *PTYSession) readUntil(ctx context.Context, wait time.Duration) (SessionReadResult, error) {
	deadline := time.Now().Add(wait)
	var first time.Time // when output was first seen
	quiet := false
	for {
		changed := s.out.changed() // before reading, so a write in between still wakes us
		s.mu.Lock()
		out := s.readOnce()
		now := time.Now()
		if out.Output != "" && first.IsZero() {
			first = now
		}
		// A burst (a command's echo, then its output) comes back as one read.
		done := quiet || !out.Running || out.Pending > 0 || !now.Before(deadline) ||
			(!first.IsZero() && now.Sub(first) >= sessionSettleMax)
		if done {
			s.offset = out.NextByte
		}
		s.mu.Unlock()
		if done {
			return out, nil
		}
		// The follower touches the stream when the session ends; the poll is a backstop.
		pause := sessionPollInterval
		if !first.IsZero() {
			pause = sessionSettle
		}
		select {
		case <-ctx.Done():
			return SessionReadResult{SessionID: s.ID, FromByte: out.FromByte}, ctx.Err()
		case <-changed:
		case <-time.After(min(pause, time.Until(deadline))):
			quiet = !first.IsZero()
		}
	}
}

func (s *PTYSession) readOnce() SessionReadResult {
	running := s.running()
	text, next, pending, dropped := s.out.readText(s.offset, sessionReadLimit, running)
	out := SessionReadResult{
		SessionID: s.ID,
		Output:    text,
		FromByte:  s.offset,
		NextByte:  next,
		Pending:   pending,
		Dropped:   dropped,
		Running:   running,
	}
	if !running {
		// ended is closed, so the process has exited and followErr is final.
		if err := s.proc.Err(); err != nil {
			out.Error = err.Error()
		} else if code, ok := s.proc.ExitCode(); ok {
			out.ExitCode = &code
		}
		if s.followErr != nil {
			out.Error = strings.TrimPrefix(out.Error+"; "+s.followErr.Error(), "; ")
		}
	}
	return out
}

// SessionClose ends the command and forgets the handle. It skips awaitReady
// so tidying up works on a machine that stopped answering.
func (m *Manager) SessionClose(ctx context.Context, runID, sessionID string) (SessionCloseResult, error) {
	mc, _, err := m.session(runID, sessionID)
	if err != nil {
		return SessionCloseResult{}, err
	}
	started := time.Now()
	if s := m.dropSession(mc, sessionID); s != nil && s.proc != nil {
		s.stopFollower()
		m.cleanupGuestSession(mc, s.ID) // ends the guest command, so tart exits on its own
		err = s.stop()
	}
	out := SessionCloseResult{SessionID: sessionID}
	out.Step = mc.rec.step("machine_session_close",
		map[string]any{"sessionId": sessionID}, out, err, started)
	m.emitStep(runID, out.Step)
	if err != nil {
		return SessionCloseResult{}, err
	}
	return out, nil
}

// stream is a bounded window on unbounded output, read by absolute position
// so readers never consume each other's bytes and always learn what was dropped.
type stream struct {
	mu      sync.Mutex
	buf     []byte
	start   int64 // absolute position of buf[0]
	written int64 // absolute bytes ever written
	limit   int
	wake    chan struct{} // closed and replaced on every write
}

func newStream(limit int) *stream { return &stream{limit: limit, wake: make(chan struct{})} }

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	s.written += int64(len(p))
	if over := len(s.buf) - s.limit; over > 0 {
		s.buf = s.buf[over:]
		s.start += int64(over)
	}
	close(s.wake)
	s.wake = make(chan struct{})
	return len(p), nil
}

// changed is closed by the next write.
func (s *stream) changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wake
}

// skip counts n bytes as written without keeping them, emptying the window:
// the source was more than a window ahead. Readers see them as dropped.
func (s *stream) skip(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written += n
	s.buf = s.buf[:0]
	s.start = s.written
	close(s.wake)
	s.wake = make(chan struct{})
}

// touch wakes waiting readers without writing, so they see a change of state.
func (s *stream) touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.wake)
	s.wake = make(chan struct{})
}

// read returns at most limit bytes from off, where to continue, how much is
// still waiting, and how much was dropped before off.
func (s *stream) read(off int64, limit int) (data []byte, next, pending, dropped int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < s.start {
		dropped = s.start - off
		off = s.start
	}
	off = min(off, s.written)
	from := int(off - s.start)
	end := min(len(s.buf), from+limit)
	data = append([]byte(nil), s.buf[from:end]...)
	next = off + int64(len(data))
	return data, next, s.written - next, dropped
}

// readText is read, cleaned. While more may follow, an incomplete tail (split
// escape, UTF-8 rune or CR LF) is held back for the next read.
func (s *stream) readText(off int64, limit int, running bool) (text string, next, pending, dropped int64) {
	raw, next, pending, dropped := s.read(off, limit)
	if running || pending > 0 {
		held := incompleteTail(raw)
		raw = raw[:len(raw)-held]
		next -= int64(held)
		pending += int64(held)
	}
	return cleanTTY(string(raw)), next, pending, dropped
}

// maxHeldEscape bounds how much of an unterminated escape a read holds back.
const maxHeldEscape = 4096

// incompleteTail reports how many trailing bytes of b cannot be cleaned
// until more arrive.
func incompleteTail(b []byte) int {
	for i := 0; i < len(b); {
		if b[i] != 0x1b {
			i++
			continue
		}
		end := escapeEnd(b, i)
		if end < 0 {
			if len(b)-i <= maxHeldEscape {
				return len(b) - i
			}
			return 0
		}
		i = end
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		return 1
	}
	for k := len(b) - 1; k >= 0 && k >= len(b)-utf8.UTFMax; k-- {
		if utf8.RuneStart(b[k]) {
			if !utf8.FullRune(b[k:]) {
				return len(b) - k
			}
			return 0
		}
	}
	return 0
}

// escapeEnd returns the index just past the escape sequence at b[i], or -1 if
// b ends first. It matches the shapes cleanTTY strips.
func escapeEnd(b []byte, i int) int {
	if i+1 >= len(b) {
		return -1
	}
	switch b[i+1] {
	case '[':
		j := i + 2
		for j < len(b) && b[j] >= 0x30 && b[j] <= 0x3f {
			j++
		}
		for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
			j++
		}
		if j >= len(b) {
			return -1
		}
		return j + 1
	case ']':
		for j := i + 2; j < len(b); j++ {
			switch b[j] {
			case 0x07:
				return j + 1
			case 0x1b:
				if j+1 >= len(b) {
					return -1
				}
				if b[j+1] == '\\' {
					return j + 2
				}
				return j
			}
		}
		return -1
	default:
		return i + 2
	}
}

var (
	oscRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`) // window titles
	csiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)           // colour, cursor, clears
	escRE = regexp.MustCompile(`\x1b[@-Z\\-_]`)                     // remaining two-byte escapes
)

// cleanTTY strips terminal control codes on the way out, so the guest command
// still sees a real terminal. A lone CR (progress redraw) becomes a newline
// rather than running updates together.
func cleanTTY(s string) string {
	if s == "" {
		return ""
	}
	s = oscRE.ReplaceAllString(s, "")
	s = csiRE.ReplaceAllString(s, "")
	s = escRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ToValidUTF8(s, "�") // tool results must be valid UTF-8
}
