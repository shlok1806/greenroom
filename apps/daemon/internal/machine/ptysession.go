package machine

// Persistent interactive sessions inside a guest, behind a real pty.
//
// Why this is not machine_exec. `tart exec` is one shot: it runs a command,
// waits for it, and returns. Anything that needs state to survive between
// calls has nowhere to live -- a shell's cwd and exported variables, a REPL,
// a debugger, a half-finished `git rebase -i`, a server left in the
// foreground. A session is one `tart exec -i -t` child that keeps running
// between tool calls, so the command outlives any single call.
//
// Why a pty. xcodebuild, swift build, git, npm and most test runners branch
// on isatty(). Down a pipe they change their output, drop colour and
// progress, and often change their buffering. greenroom's claim is that it
// proves a change works, so a build it runs has to be the build the
// developer runs; running it down a pipe quietly makes it a different build.
// tart provides this directly: `-t` allocates a remote pty, `-i` attaches
// stdin. Nothing is installed in the guest for it, so unlike the input
// helper (input.go) there is no baked-in version to keep in step.
//
// Why the handle is not a guest pid. A pid would be wrong twice over: a call
// against a destroyed machine would come back as some stale process's
// ENOENT instead of "machine gone", and a pid the guest has reused would
// point a caller at a different process. The handle here is a host-side
// child process the daemon owns, addressed by (runId, sessionId), so the
// only answers a caller can get are the session or a clean error, and
// teardown is a kill the daemon can actually perform.
//
// What is an error here and what is not. A command failing inside a session
// is not an error: its exit status and its complaints are output, read back
// with SessionRead like anything else, which is what the "a guest command
// that exits non-zero is not an error" invariant requires. An error means
// tart itself failed, or the machine is gone.

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

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

const (
	// sessionBufferLimit is how much of a session's output the daemon keeps
	// in memory. A runaway build can write gigabytes, and the daemon holds
	// none of it beyond this window: the oldest bytes are dropped, and a
	// read that has fallen behind is told how many went, so silence is never
	// mistaken for completeness.
	sessionBufferLimit = 1 << 20 // 1 MiB

	// sessionReadLimit caps how much one read returns, so a caller that has
	// been away cannot be handed the whole window in a single tool result.
	sessionReadLimit = 256 * 1024

	// maxSessionsPerMachine bounds the handles one machine can hold. Each is
	// a live host process, so a caller in a loop must not be able to open
	// them without end.
	maxSessionsPerMachine = 16

	// defaultSessionCommand is an interactive login shell, which is what a
	// caller that does not name a command wants. It execs, so the wrapper
	// shell every session command runs under is replaced rather than left
	// sitting behind this one as a second process.
	defaultSessionCommand = "exec /bin/zsh -li"

	// sessionPollInterval is how often a waiting read looks for new output.
	sessionPollInterval = 250 * time.Millisecond

	// maxSessionWait keeps a waiting read under the MCP client's 60 s
	// first-byte timer, the same ceiling machine_wait works to.
	maxSessionWait = 50 * time.Second
)

// PTYSession is one interactive command inside a guest, addressed by
// (runId, id).
type PTYSession struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"startedAt"`

	proc *tart.Session
	out  *stream

	// mu serializes this session's sends and reads. A session is an ordered
	// conversation: two sends must not interleave their bytes, and two reads
	// must not both claim the same output.
	mu sync.Mutex

	// offset is how far into the output stream this session has been read.
	// A read returns what follows and moves it on, so nothing comes back
	// twice.
	offset int64
}

// SessionStartResult names the session a caller keeps to reach the command
// again.
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

// SessionReadResult is output since the caller's last read, with enough
// bookkeeping to know whether to read again.
type SessionReadResult struct {
	SessionID string `json:"sessionId"`
	Output    string `json:"output"`
	FromByte  int64  `json:"fromByte"`
	NextByte  int64  `json:"nextByte"`
	Pending   int64  `json:"pending"`           // written but not yet returned
	Dropped   int64  `json:"dropped,omitempty"` // lost because the caller fell behind
	Running   bool   `json:"running"`
	Step      int    `json:"step"`
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

// session finds a live session, or says which of the two things is missing.
// A destroyed machine answers "no machine for run", from m.get, rather than
// anything about sessions: the machine is what went away.
func (m *Manager) session(runID, sessionID string) (*Machine, *PTYSession, error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := mc.sessions[sessionID]
	if !ok {
		return nil, nil, fmt.Errorf("no session %q on run %s", sessionID, runID)
	}
	return mc, s, nil
}

// SessionStart runs command in the guest behind a pty and returns the id that
// reaches it again. The command keeps running after this call returns; that
// is the whole point.
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
	// Reserve the slot before starting anything, so several callers at once
	// cannot go past the cap, and give it back if the start fails.
	if err := m.reserveSession(mc, s); err != nil {
		return SessionStartResult{}, err
	}

	// The command goes through a login shell so that a caller can send an
	// ordinary command line rather than an argv, the same bargain
	// machine_exec makes. It is one argument, so nothing in it is split or
	// re-read by a shell on the way.
	proc, startErr := m.tart.StartSession(mc.Name, "/bin/zsh", "-lc", command)
	out := SessionStartResult{SessionID: id, Command: command, TTY: true}
	if startErr != nil {
		m.dropSession(mc, id)
		out = SessionStartResult{}
	} else {
		s.proc = proc
		// Nothing else drains the pipe, and a command that fills it would
		// block forever, so the pump runs for the life of the session.
		go s.out.pump(proc.Output())
	}
	out.Step = mc.rec.step("machine_session_start",
		map[string]any{"sessionId": id, "command": command}, out, startErr, started)
	m.emitStep(runID, out.Step)
	if startErr != nil {
		return SessionStartResult{}, startErr
	}
	return out, nil
}

func (m *Manager) reserveSession(mc *Machine, s *PTYSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func (m *Manager) dropSession(mc *Machine, id string) *PTYSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := mc.sessions[id]
	delete(mc.sessions, id)
	return s
}

// closeSessions ends every session a machine holds. Destroy calls it, so a
// destroyed machine leaves no session process behind and every handle into
// it stops answering at the same moment.
func (m *Manager) closeSessions(mc *Machine) {
	m.mu.Lock()
	live := make([]*PTYSession, 0, len(mc.sessions))
	for _, s := range mc.sessions {
		live = append(live, s)
	}
	mc.sessions = nil
	m.mu.Unlock()
	for _, s := range live {
		if s.proc != nil {
			_ = s.proc.Close()
		}
	}
}

// SessionSend writes data into a session's input, exactly as given. A caller
// that wants a command run adds its own newline: sending "ls" and "ls\n" are
// different things to a shell, and this is not the layer to guess which was
// meant.
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
	var sendErr error
	if !s.proc.Running() {
		sendErr = fmt.Errorf("session %s has ended", sessionID)
	} else if _, err := io.WriteString(s.proc, data); err != nil {
		sendErr = fmt.Errorf("write to session %s: %w", sessionID, err)
	}
	s.mu.Unlock()

	out := SessionSendResult{SessionID: sessionID, Bytes: len(data)}
	if sendErr != nil {
		out = SessionSendResult{SessionID: sessionID}
	}
	// What was sent is not recorded, only how much. A session is how a
	// caller types secrets into a machine, and the run record is read by
	// people who are not the caller; the output, which is recorded, is
	// enough to reconstruct what happened.
	out.Step = mc.rec.step("machine_session_send",
		map[string]any{"sessionId": sessionID, "bytes": len(data)}, out, sendErr, started)
	m.emitStep(runID, out.Step)
	if sendErr != nil {
		return SessionSendResult{}, sendErr
	}
	return out, nil
}

// SessionRead returns output the caller has not seen. With wait above zero it
// polls until something new arrives or the wait runs out, so a caller that
// has just started a build does not have to spin on empty reads; the whole
// wait is one step in the record, not one per poll.
func (m *Manager) SessionRead(ctx context.Context, runID, sessionID string, wait time.Duration) (SessionReadResult, error) {
	mc, s, err := m.session(runID, sessionID)
	if err != nil {
		return SessionReadResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return SessionReadResult{}, err
	}
	if wait > maxSessionWait {
		wait = maxSessionWait
	}
	started := time.Now()

	s.mu.Lock()
	from := s.offset
	out, readErr := s.readUntil(ctx, wait)
	if readErr == nil {
		s.offset = out.NextByte
	}
	s.mu.Unlock()

	if readErr != nil {
		out = SessionReadResult{}
	}
	// The byte range goes in the input side of the record, so successive
	// reads line up into one transcript. The output is trimmed for the
	// record the way machine_exec trims its own: the caller still gets all
	// of it, but a long build read in 256 KiB helpings would otherwise leave
	// tens of megabytes in steps.jsonl, which the API reads whole.
	out.Step = mc.rec.step("machine_session_read",
		map[string]any{"sessionId": sessionID, "fromByte": from}, truncatedRead(out), readErr, started)
	m.emitStep(runID, out.Step)
	if readErr != nil {
		return SessionReadResult{}, readErr
	}
	return out, nil
}

// truncatedRead trims a read for the run record. The byte range it carries
// still says exactly what was covered, so a trimmed entry is not a gap in the
// evidence, only a shorter quote of it.
func truncatedRead(r SessionReadResult) SessionReadResult {
	const max = 64 * 1024
	if len(r.Output) > max {
		r.Output = r.Output[:max] + "\n...[truncated]"
	}
	return r
}

// readUntil reads once, then keeps looking while the caller is willing to
// wait and the command is still running. A finished command is not waited on:
// its output is all there, so nothing more is coming.
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
	raw, next, pending, dropped := s.out.read(s.offset, sessionReadLimit)
	return SessionReadResult{
		SessionID: s.ID,
		Output:    cleanTTY(string(raw)),
		FromByte:  s.offset,
		NextByte:  next,
		Pending:   pending,
		Dropped:   dropped,
		Running:   s.proc.Running(),
	}
}

// SessionClose ends the command and forgets the handle. Closing a session
// whose command has already exited is not an error: a caller tidying up
// should not have to know whether its command finished on its own.
func (m *Manager) SessionClose(ctx context.Context, runID, sessionID string) (SessionCloseResult, error) {
	mc, _, err := m.session(runID, sessionID)
	if err != nil {
		return SessionCloseResult{}, err
	}
	started := time.Now()
	// No awaitReady: closing is how a caller tidies up, and it must work on
	// a machine that has stopped answering. The process being killed is the
	// daemon's own.
	var closeErr error
	if s := m.dropSession(mc, sessionID); s != nil && s.proc != nil {
		closeErr = s.proc.Close()
	}
	out := SessionCloseResult{SessionID: sessionID}
	out.Step = mc.rec.step("machine_session_close",
		map[string]any{"sessionId": sessionID}, out, closeErr, started)
	m.emitStep(runID, out.Step)
	if closeErr != nil {
		return SessionCloseResult{}, closeErr
	}
	return out, nil
}

// --- output buffering ---

// stream is a bounded window on an unbounded output stream.
//
// Reads are by absolute position, not draining: a read says where it got to
// and the next one carries on there, so two readers never take output from
// each other and a caller can always tell what it has seen. The window is
// what keeps a runaway build out of the daemon's memory: once it is full the
// oldest bytes go, and a caller that has fallen behind them is told how many
// were dropped rather than being handed a gap it cannot see.
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

// pump copies the session's output into the window until the command ends.
// A read error is the end of the stream, which Running already reports, so
// there is nothing here to hand anyone.
func (s *stream) pump(r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = s.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// read returns at most limit bytes from off, and reports where to continue,
// how much is still waiting, and how much was dropped before off.
func (s *stream) read(off int64, limit int) (data []byte, next, pending, dropped int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < s.start {
		dropped = s.start - off
		off = s.start
	}
	if off > s.written {
		off = s.written
	}
	from := int(off - s.start)
	end := len(s.buf)
	if end-from > limit {
		end = from + limit
	}
	data = append([]byte(nil), s.buf[from:end]...)
	next = off + int64(len(data))
	return data, next, s.written - next, dropped
}

// --- output cleaning ---

var (
	// Operating System Command: ESC ] ... BEL, or ESC ] ... ESC backslash.
	// Shells use it to set the window title on every prompt.
	oscRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	// Control Sequence Introducer: colour, cursor moves, line clears,
	// bracketed paste.
	csiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	// Two-character escapes left over once the above are gone.
	escRE = regexp.MustCompile(`\x1b[@-Z\\-_]`)
)

// cleanTTY turns a pty's bytes into text worth reading.
//
// A pty carries everything a terminal emulator would act on: colour, cursor
// moves, line clears, window titles, bracketed paste. To a model reading
// build output those are noise that costs tokens and hides the message. They
// go here rather than in the guest so that the command still sees a real
// terminal, which is the whole point of the pty: what is stripped is only
// what the daemon hands on, and every read records the byte range it
// covered, so a person can line the text back up against the original.
//
// A pty ends its lines with carriage return and newline, so those pairs
// become plain newlines. A lone carriage return is a redraw of the current
// line, which progress bars emit constantly; it becomes a newline too,
// because dropping it would run every update of a progress bar together into
// one unreadable line.
func cleanTTY(s string) string {
	if s == "" {
		return ""
	}
	s = oscRE.ReplaceAllString(s, "")
	s = csiRE.ReplaceAllString(s, "")
	s = escRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// A build can print a broken byte sequence and a tool result has to be
	// valid UTF-8.
	return strings.ToValidUTF8(s, "�")
}
