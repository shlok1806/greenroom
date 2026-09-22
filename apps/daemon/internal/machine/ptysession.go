package machine

// Interactive sessions: one `tart exec -i -t` child per session, kept alive
// between tool calls behind a real pty, because build tools branch on
// isatty(). The handle is a host process the daemon owns, never a guest pid.
// A command failing inside a session is output, not an error.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
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

	// maxSessionWait stays under the MCP client's 60 s first-byte timeout.
	maxSessionWait = 50 * time.Second
)

// PTYSession is one interactive command inside a guest, addressed by (runId, id).
type PTYSession struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"startedAt"`

	proc *tart.Session
	out  *stream

	mu     sync.Mutex // serializes sends and reads so neither interleaves
	offset int64      // how far reads have consumed; guarded by mu
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

// SessionStart runs command in the guest behind a pty and returns the id that
// reaches it again. The command outlives this call.
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

	s := &PTYSession{ID: id, Command: command, StartedAt: started.UTC(), out: newStream(sessionBufferLimit)}
	// Reserve before starting so concurrent starts cannot pass the cap.
	if err := m.reserveSession(mc, s); err != nil {
		return SessionStartResult{}, err
	}
	proc, err := m.tart.StartSession(mc.Name, "/bin/zsh", "-lc", command)
	if err == nil && !m.attachSession(mc, s, proc) {
		_ = proc.Close() // the machine went while tart was starting
		err = fmt.Errorf("no machine for run %q", runID)
	}
	var out SessionStartResult
	if err != nil {
		m.dropSession(mc, id)
	} else {
		out = SessionStartResult{SessionID: id, Command: command, TTY: true}
		go s.out.pump(proc.Output()) // must drain or the command blocks
	}
	out.Step = mc.rec.step("machine_session_start",
		map[string]any{"sessionId": id, "command": command}, out, err, started)
	m.emitStep(runID, out.Step)
	if err != nil {
		return SessionStartResult{}, err
	}
	return out, nil
}

// reserveSession holds a slot for s, refusing a machine that already left the
// map so a start racing Destroy cannot leave an unreachable process.
func (m *Manager) reserveSession(mc *Machine, s *PTYSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.machines[mc.RunID] != mc {
		return fmt.Errorf("no machine for run %q", mc.RunID)
	}
	if mc.sessions == nil {
		mc.sessions = map[string]*PTYSession{}
	}
	if len(mc.sessions) >= maxSessionsPerMachine {
		return fmt.Errorf("run %s already holds %d sessions; close one first",
			mc.RunID, maxSessionsPerMachine)
	}
	mc.sessions[s.ID] = s
	return nil
}

// attachSession gives a reserved session its process, or reports false if
// the machine (and the reservation) went in the meantime.
func (m *Manager) attachSession(mc *Machine, s *PTYSession, proc *tart.Session) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mc.sessions[s.ID] != s {
		return false
	}
	s.proc = proc
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

// SessionRead returns output the caller has not seen. With wait > 0 it polls
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

	s.mu.Lock()
	from := s.offset
	out, err := s.readUntil(ctx, wait)
	if err == nil {
		s.offset = out.NextByte
	}
	s.mu.Unlock()

	out.Step = mc.rec.step("machine_session_read",
		map[string]any{"sessionId": sessionID, "fromByte": from}, truncatedRead(out), err, started)
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

// readUntil reads until there is output, the command has ended, or wait passes.
func (s *PTYSession) readUntil(ctx context.Context, wait time.Duration) (SessionReadResult, error) {
	deadline := time.Now().Add(wait)
	for {
		out := s.readOnce()
		if out.Output != "" || !out.Running || time.Now().After(deadline) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return SessionReadResult{}, ctx.Err()
		case <-time.After(sessionPollInterval):
		}
	}
}

func (s *PTYSession) readOnce() SessionReadResult {
	running := s.proc.Running()
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
		if err := s.proc.Err(); err != nil {
			out.Error = err.Error()
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
		err = s.proc.Close()
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
}

func newStream(limit int) *stream { return &stream{limit: limit} }

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	s.written += int64(len(p))
	if over := len(s.buf) - s.limit; over > 0 {
		s.buf = s.buf[over:]
		s.start += int64(over)
	}
	return len(p), nil
}

// pump copies output into the window until the stream ends; Running reports the end.
func (s *stream) pump(r io.Reader) {
	_, _ = io.CopyBuffer(s, r, make([]byte, 32*1024))
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
