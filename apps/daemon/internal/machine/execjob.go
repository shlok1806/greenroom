package machine

// machine_exec commands outlive the call that starts them (ADR 0015, issue
// #39): an MCP client gives up on a call that sends nothing for 60 s, so a
// call waits at most a bounded time and a command still going is collected
// later by its execId. Each stream keeps a bounded head and tail (issue #29).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ExecHeadLimit and ExecTailLimit bound what a machine_exec result keeps of
// each stream: its first and its last bytes, with a marker between them.
// The machine_exec tool description states them.
const (
	ExecHeadLimit = 8 * 1024
	ExecTailLimit = 24 * 1024
)

// maxKeptExecs is how many finished commands a machine keeps for
// ExecWait; older ones are forgotten first.
const maxKeptExecs = 32

// ExecStatus is where a started command stands: still running, or finished
// with its result. ExitCode is absent while it runs.
type ExecStatus struct {
	ExecID          string  `json:"execId"`
	Running         bool    `json:"running"`
	ExitCode        *int    `json:"exitCode,omitempty"`
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	StdoutBytes     int64   `json:"stdoutBytes"`
	StderrBytes     int64   `json:"stderrBytes"`
	StdoutTruncated bool    `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool    `json:"stderrTruncated,omitempty"`
	TimedOut        bool    `json:"timedOut,omitempty"`
	Seconds         float64 `json:"seconds"` // so far, while it runs
	Step            int     `json:"step"`    // the command's step in steps.jsonl, written when it ends
}

// execJob is one command running or run in a guest. res and err are written
// before done closes and never after.
type execJob struct {
	id      string
	step    int
	started time.Time
	cancel  context.CancelFunc
	done    chan struct{}
	res     ExecResult
	err     error
}

func (j *execJob) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

func (j *execJob) status() ExecStatus {
	if !j.finished() {
		return ExecStatus{ExecID: j.id, Running: true, Seconds: time.Since(j.started).Seconds(), Step: j.step}
	}
	r := j.res
	st := ExecStatus{
		ExecID: j.id, Stdout: r.Stdout, Stderr: r.Stderr,
		StdoutBytes: r.StdoutBytes, StderrBytes: r.StderrBytes,
		StdoutTruncated: r.StdoutTruncated, StderrTruncated: r.StderrTruncated,
		TimedOut: r.TimedOut, Seconds: r.Seconds, Step: r.Step,
	}
	if j.err == nil {
		code := r.ExitCode
		st.ExitCode = &code
	}
	return st
}

// ExecStart starts command in the guest's login shell, optionally in cwd, and
// returns at once. The command runs on after the caller leaves, until it
// exits, its timeout, or the machine goes; ExecWait collects it. Its step
// number is claimed now and its record written when it ends.
func (m *Manager) ExecStart(ctx context.Context, runID, command, cwd string, timeout time.Duration) (ExecStatus, error) {
	j, err := m.startExec(ctx, runID, command, cwd, timeout)
	if err != nil {
		return ExecStatus{}, err
	}
	return j.status(), nil
}

// ExecWait waits up to wait for a started command to finish and reports
// where it stands. A tart failure is the error, as from Exec.
func (m *Manager) ExecWait(ctx context.Context, runID, execID string, wait time.Duration) (ExecStatus, error) {
	mc, err := m.get(runID)
	if err != nil {
		return ExecStatus{}, err
	}
	m.mu.Lock()
	j := mc.execs[execID]
	m.mu.Unlock()
	if j == nil {
		return ExecStatus{}, fmt.Errorf("no command %q on run %s: execId comes from machine_exec, and only the last %d finished commands are kept",
			execID, runID, maxKeptExecs)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-j.done:
		return j.status(), j.err
	case <-timer.C:
		return j.status(), nil
	case <-ctx.Done():
		return ExecStatus{}, ctx.Err()
	}
}

// startExec claims the step, registers the job and starts the command.
func (m *Manager) startExec(ctx context.Context, runID, command, cwd string, timeout time.Duration) (*execJob, error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return nil, err
	}
	id, err := newExecID()
	if err != nil {
		return nil, err
	}
	// Detached from the caller: the command must survive the call that started it.
	base := context.WithoutCancel(ctx)
	jobCtx, cancel := context.WithCancel(base)
	guestSeconds := 0
	if timeout > 0 {
		guestSeconds = int(math.Ceil(timeout.Seconds()))
		// The guest's watchdog ends the command; this deadline only catches a stuck tart.
		cancel()
		jobCtx, cancel = context.WithTimeout(base, timeout+execHostGrace)
	}
	j := &execJob{id: id, started: time.Now(), cancel: cancel, done: make(chan struct{})}
	if err := m.addExec(mc, j); err != nil {
		cancel()
		return nil, err
	}
	j.step = mc.rec.begin() // claimed first, so output.step matches seq (issue #47)

	script := command
	if cd := cdCommand(cwd); cd != "" {
		script = cd + " && " + command
	}
	go func() {
		defer cancel()
		var stdout, stderr headTail
		// The wrapper and the command go on stdin, never in a guest argv (issue #128).
		code, err := m.tart.ExecInputTo(jobCtx, strings.NewReader(execScript(script)), &stdout, &stderr, mc.Name,
			append(slices.Clone(execShell), strconv.Itoa(guestSeconds))...)
		out := ExecResult{ExecID: id, ExitCode: code, Seconds: time.Since(j.started).Seconds(), Step: j.step}
		out.Stdout, out.StdoutBytes, out.StdoutTruncated = stdout.result()
		out.Stderr, out.StderrBytes, out.StderrTruncated = stderr.result()
		out.TimedOut = err == nil && code == execTimedOutExit && strings.Contains(out.Stderr, execTimedOutNote)
		mc.rec.complete(j.step, "machine_exec", map[string]any{"command": command, "cwd": cwd, "execId": id},
			truncatedForLog(out), err, j.started)
		j.res, j.err = out, err
		close(j.done)
		m.emitStep(mc.RunID, j.step)
	}()
	return j, nil
}

// addExec registers j, forgetting the oldest finished commands beyond
// maxKeptExecs. It refuses a machine that already left the map, so no
// command outlives detachLocked.
func (m *Manager) addExec(mc *Machine, j *execJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.machines[mc.RunID] != mc || mc.destroying {
		return fmt.Errorf("no machine for run %q", mc.RunID)
	}
	if mc.execs == nil {
		mc.execs = map[string]*execJob{}
	}
	for len(mc.execs) >= maxKeptExecs {
		var oldest *execJob
		for _, o := range mc.execs {
			if o.finished() && (oldest == nil || o.started.Before(oldest.started)) {
				oldest = o
			}
		}
		if oldest == nil {
			break // all running; each is its own call's work, never dropped
		}
		delete(mc.execs, oldest.id)
	}
	mc.execs[j.id] = j
	return nil
}

func newExecID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("name a command: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// headTail keeps the first ExecHeadLimit and the last ExecTailLimit bytes
// written to it and counts the rest, so a command's output takes bounded
// memory however much it prints.
type headTail struct {
	head  []byte
	tail  []byte // everything after head, trimmed to its last ExecTailLimit bytes now and then
	total int64
}

func (h *headTail) Write(p []byte) (int, error) {
	n := len(p)
	h.total += int64(n)
	if room := ExecHeadLimit - len(h.head); room > 0 {
		k := min(room, len(p))
		h.head = append(h.head, p[:k]...)
		p = p[k:]
	}
	h.tail = append(h.tail, p...)
	if len(h.tail) > 2*ExecTailLimit {
		h.tail = append(h.tail[:0], h.tail[len(h.tail)-ExecTailLimit:]...)
	}
	return n, nil
}

// result is the kept text, the full byte count, and whether bytes were left
// out. The cut never splits a UTF-8 character; the marker says how much is missing.
func (h *headTail) result() (text string, total int64, truncated bool) {
	if int64(len(h.head)+len(h.tail)) == h.total && len(h.tail) <= ExecTailLimit {
		return string(h.head) + string(h.tail), h.total, false
	}
	head := h.head[:len(h.head)-partialRuneTail(h.head)]
	tail := h.tail[len(h.tail)-ExecTailLimit:]
	for i := 0; i < utf8.UTFMax && len(tail) > 0 && !utf8.RuneStart(tail[0]); i++ {
		tail = tail[1:]
	}
	left := h.total - int64(len(head)) - int64(len(tail))
	return fmt.Sprintf("%s\n[greenroom: %d bytes left out here; the command wrote %d]\n%s", head, left, h.total, tail),
		h.total, true
}

// partialRuneTail is how many trailing bytes of b are an incomplete UTF-8 character.
func partialRuneTail(b []byte) int {
	for k := len(b) - 1; k >= 0 && k >= len(b)-utf8.UTFMax; k-- {
		if utf8.RuneStart(b[k]) {
			if utf8.FullRune(b[k:]) {
				return 0
			}
			return len(b) - k
		}
	}
	return 0
}
