// Package tart drives the Tart CLI (https://tart.run) as a subprocess.
package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Client runs tart commands.
type Client struct {
	Bin string

	// source records which candidate Bin came from, for the startup log
	// line. It is only ever set by New: a Client built directly, which is
	// what tests do, reports an empty source and that is honest.
	source Source
}

// New returns a Client driving the pinned tart if it is installed, falling
// back to PATH. See version.go for why the daemon pins one at all.
func New() *Client {
	r := Resolve("")
	return &Client{Bin: r.Bin, source: r.Source}
}

// NewAt returns a Client driving an explicitly chosen binary, with the same
// resolution rules applied when the choice is empty.
func NewAt(bin string) *Client {
	r := Resolve(bin)
	return &Client{Bin: r.Bin, source: r.Source}
}

// VM is one row of `tart list`.
type VM struct {
	Source string `json:"Source"`
	Name   string `json:"Name"`
	State  string `json:"State"`
}

// ExecResult is the outcome of a command run inside a guest.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tart %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Clone makes a copy-on-write clone of src named dst.
func (c *Client) Clone(ctx context.Context, src, dst string) error {
	_, err := c.run(ctx, "clone", src, dst)
	return err
}

// Delete removes a local VM.
func (c *Client) Delete(ctx context.Context, name string) error {
	_, err := c.run(ctx, "delete", name)
	return err
}

// Stop shuts a running VM down.
func (c *Client) Stop(ctx context.Context, name string) error {
	_, err := c.run(ctx, "stop", name)
	return err
}

// IP returns the guest's IP address, or an error if it has none yet.
func (c *Client) IP(ctx context.Context, name string) (string, error) {
	out, err := c.run(ctx, "ip", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// List returns all local VMs.
func (c *Client) List(ctx context.Context) ([]VM, error) {
	out, err := c.run(ctx, "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var vms []VM
	if err := json.Unmarshal([]byte(out), &vms); err != nil {
		return nil, fmt.Errorf("parse tart list: %w", err)
	}
	return vms, nil
}

// VNCURL returns the address of the VM's screen, once tart has printed it.
// It is empty for a machine started without graphics. tart writes the line
// "Opening vnc://..." to its log a moment after start, so this waits.
func (p *Process) VNCURL(timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(p.logPath)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if i := strings.Index(line, "vnc://"); i >= 0 {
					return strings.TrimRight(strings.TrimSpace(line[i:]), ".")
				}
			}
		}
		if p.Exited() || time.Now().After(deadline) {
			return ""
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Process is a running `tart run` subprocess. A VM that cannot start makes
// tart exit at once and print the reason to its log, so callers watch the
// process while they wait for the guest to come up.
type Process struct {
	name    string
	logPath string
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error // written before done is closed
}

// Kill stops the tart process. A VM normally goes away through `tart stop`,
// so this is for the cases where the subprocess must be ended directly.
func (p *Process) Kill() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// Exited reports whether the tart process has stopped. A VM that is running
// keeps its process alive, so an exit during boot means the VM is gone.
func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Wait blocks until the process stops. It is how a caller watches a VM for
// the whole of its life rather than polling Exited, and it may be called by
// any number of goroutines: the channel it waits on is only ever closed.
func (p *Process) Wait() {
	<-p.done
}

// Err explains why the process stopped. It prefers what tart printed,
// because that names the real cause, for example the host VM limit.
func (p *Process) Err() error {
	if !p.Exited() {
		return nil
	}
	if msg := p.tail(); msg != "" {
		return fmt.Errorf("tart run %s exited: %s", p.name, msg)
	}
	if p.waitErr != nil {
		return fmt.Errorf("tart run %s exited: %w", p.name, p.waitErr)
	}
	return fmt.Errorf("tart run %s exited", p.name)
}

// tail returns the last few lines that tart wrote, which is where it puts
// the reason a VM could not start.
func (p *Process) tail() string {
	data, err := os.ReadFile(p.logPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > logTailLines {
		lines = lines[len(lines)-logTailLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "; "))
}

const logTailLines = 3

// Start boots a VM in its own process group so it outlives the daemon.
// Output goes to logPath. The caller does not wait on the process; a
// goroutine reaps it and records why it stopped.
//
// A watched machine runs its screen over VNC so a person can see the work as
// it happens. An unwatched machine runs headless, which is the default,
// because a screen costs the host work that a machine nobody looks at does
// not need.
func (c *Client) Start(name, logPath string, watch bool) (*Process, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	mode := "--no-graphics"
	if watch {
		mode = "--vnc-experimental"
	}
	cmd := exec.Command(c.Bin, "run", name, mode)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("tart run %s: %w", name, err)
	}
	p := &Process{name: name, logPath: logPath, cmd: cmd, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		_ = logFile.Close()
		close(p.done)
	}()
	return p, nil
}

// Exec runs a command inside the guest through the Tart guest agent. A
// non-zero exit status from the guest command is reported in ExitCode, not
// as an error. Errors are for failures of tart itself (VM not running, agent
// unreachable, context cancelled).
func (c *Client) Exec(ctx context.Context, name string, args ...string) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, c.Bin, append([]string{"exec", name}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("tart exec %s: %w", name, ctx.Err())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return res, fmt.Errorf("tart exec %s: %w", name, err)
	}
	if isTartFailure(res.Stderr) {
		return res, fmt.Errorf("tart exec %s: %s", name, strings.TrimSpace(res.Stderr))
	}
	res.ExitCode = exitErr.ExitCode()
	return res, nil
}

// Session is a command running inside a guest behind a pseudo-terminal, with
// its input and output attached to this process for as long as it lives.
//
// It exists because `Exec` is one shot: it runs a command, waits, and
// returns, so nothing that needs state between calls has anywhere to live.
// A Session is the long-running `tart exec -i -t` child itself, so the handle
// the daemon keeps is a host process it owns rather than a process id inside
// the guest that it would have to trust and could not reliably kill.
//
// `-t` is what makes isatty() true for the guest command, which xcodebuild,
// swift build, git and most test runners branch on. `-i` attaches stdin, so
// the command can be typed at.
//
// tart is driven through a host-side pty rather than through pipes, and that
// is not a detail: `-t` makes tart read its own stdin's terminal size to
// forward to the guest, and a pipe makes it die outright rather than fall
// back. See openPTY in pty.go. The daemon writes to the master to type at the
// command and reads the master to collect its output.
type Session struct {
	cmd    *exec.Cmd
	master *os.File
	stderr bytes.Buffer // tart's own complaints, not the guest's output
	done   chan struct{}
	mu     sync.Mutex
	err    error // written before done is closed

	// reaping is set, under mu, once the command has exited and just before
	// it is reaped. Close only signals while it is false and holds mu while
	// it does, so a signal can only ever reach a process that is still ours:
	// until the reap its pid, which is also its process group id, cannot be
	// handed to anyone else.
	reaping bool
}

// StartSession runs command inside the guest behind a remote pty. The
// returned Session stays alive until the command exits or Close is called.
//
// The flags go before the VM name: `tart exec [-i] [-t] <name> <command>...`.
// Putting them after the name would make tart read them as part of the
// command.
func (c *Client) StartSession(name string, command ...string) (*Session, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, fmt.Errorf("tart exec -i -t %s: %w", name, err)
	}

	args := append([]string{"exec", "-i", "-t", name}, command...)
	cmd := exec.Command(c.Bin, args...)
	// tart's stdin and stdout are the terminal. Its stderr is kept apart so
	// that tart's own complaints can explain a session that would not start
	// rather than being mixed into the guest command's output.
	cmd.Stdin, cmd.Stdout = slave, slave
	s := &Session{cmd: cmd, master: master, done: make(chan struct{})}
	cmd.Stderr = &s.stderr
	// Its own session, with the pty for a controlling terminal, so Close ends
	// the command and anything it started rather than only the tart process
	// in front of them. Setsid also makes it a process group leader, which is
	// what lets Close signal the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		return nil, fmt.Errorf("tart exec -i -t %s: %w", name, err)
	}
	// The parent's copy of the slave goes now, so that when the command exits
	// the last slave is closed and a read on the master ends the stream
	// instead of blocking forever.
	_ = slave.Close()

	go func() {
		// Wait for the exit without reaping first, so the pid stays ours
		// until reaping is set. If that cannot be watched, Close stops
		// signalling at all rather than risk a pid that may be reused.
		_ = awaitExit(cmd.Process.Pid)
		s.mu.Lock()
		s.reaping = true
		s.mu.Unlock()
		err := cmd.Wait()
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	}()
	return s, nil
}

// Output is the stream the guest command writes to. The caller reads it to
// exhaustion in its own goroutine; nothing else drains it.
//
// A terminal echoes what is typed at it, so a caller reading this sees its
// own input come back before the command's reply. That is kept rather than
// turned off: it is what a real terminal does, it makes the run record show
// the command next to the output it produced, and a program that wants its
// input hidden, a password prompt above all, turns echo off itself the way it
// would on any terminal. Forcing echo off here would break that and make
// every prompt silent.
func (s *Session) Output() io.Reader { return ptyReader{s.master} }

// Write sends bytes to the command's terminal input, exactly as given.
func (s *Session) Write(p []byte) (int, error) { return s.master.Write(p) }

// Running reports whether the command is still going.
func (s *Session) Running() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// Err explains why a finished session stopped, preferring what tart printed.
// A command that exited non-zero is not an error here for the same reason it
// is not in Exec: a failing build is a result, not an infrastructure fault.
func (s *Session) Err() error {
	if s.Running() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := strings.TrimSpace(s.stderr.String())
	if msg != "" && isTartFailure(msg) {
		return fmt.Errorf("tart exec: %s", msg)
	}
	return nil
}

// Close ends the command and releases the terminal. Closing a session whose
// command has already exited is not an error, and sends no signal: once the
// command has been reaped its pid, and so its process group id, may belong
// to an unrelated process.
func (s *Session) Close() error {
	s.mu.Lock()
	if !s.reaping && s.cmd.Process != nil {
		// Negative pid is the process group, so the guest command goes with
		// the tart process in front of it.
		killGroup(s.cmd.Process.Pid)
	}
	s.mu.Unlock()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
	// The master goes last, so anything the command wrote on its way out has
	// already been read, and closing it ends the reader's pump.
	return s.master.Close()
}

// killGroup kills a session's whole process group. It is a variable so a
// test can see which groups Close signals.
var killGroup = func(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// isTartFailure recognises tart's own error messages so they are not
// mistaken for a failing guest command.
func isTartFailure(stderr string) bool {
	s := strings.TrimSpace(stderr)
	return strings.Contains(s, "is not running") ||
		strings.Contains(s, "Error:") && strings.HasPrefix(s, "Error:") ||
		strings.Contains(s, "guest agent")
}
