// Package tart drives the Tart CLI (https://tart.run) as a subprocess.
package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Client runs tart commands.
type Client struct {
	Bin    string
	source Source // set only by New/NewAt; empty for a Client built directly
}

// New returns a Client driving the pinned tart if installed, else tart on PATH.
func New() *Client { return NewAt("") }

// NewAt returns a Client driving bin, resolved as Resolve does.
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
	return strings.TrimSpace(out), err
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

// Process is a running `tart run`. tart stays in the foreground for the life
// of the VM, so an exit means the VM is gone and the log says why.
type Process struct {
	*child
	name    string
	logPath string
}

const logTailLines = 3

// Start boots a VM in its own process group so it outlives the daemon, with
// output appended to logPath. watch runs it with a VNC screen, else headless.
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
	ch, err := startChild(cmd, func() { _ = logFile.Close() })
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("tart run %s: %w", name, err)
	}
	return &Process{child: ch, name: name, logPath: logPath}, nil
}

// Kill ends the tart process group directly; the normal path is `tart stop`.
func (p *Process) Kill() error { return p.kill() }

// Exited reports whether the tart process has stopped.
func (p *Process) Exited() bool { return p.exited() }

// Wait blocks until the process stops. Safe from any number of goroutines.
func (p *Process) Wait() { <-p.done }

// Err explains why the process stopped, preferring the tail of tart's log
// because that names the real cause (for example the host VM limit).
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

// VNCURL waits up to timeout for tart to log its "vnc://" address and returns
// it, or "" for a headless VM or one that exited.
func (p *Process) VNCURL(timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(p.logPath); err == nil {
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

// Exec runs a command in the guest via the guest agent. A non-zero guest exit
// is reported in ExitCode, not as an error: errors mean tart itself failed.
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
	if isTartFailure(exitErr.ExitCode(), res.Stderr) {
		return res, fmt.Errorf("tart exec %s: %s", name, strings.TrimSpace(res.Stderr))
	}
	res.ExitCode = exitErr.ExitCode()
	return res, nil
}

// tartErrorLine matches the line tart 2.37 prints last when it fails itself:
// ArgumentParser's "Error: ..." or one of the RuntimeErrors exec can raise.
var tartErrorLine = regexp.MustCompile(`^(Error: .*|VM ".*" is not running|the specified VM ".*" does not exist|Failed to connect to the VM using its control socket: .*)$`)

// isTartFailure tells tart's own failure from a failing guest command. tart
// forwards a guest's exit code and prints nothing of its own; it fails with
// exit 1, 2 (no such VM, not running) or 64 (usage) and one final stderr line.
// A guest that exits 1 after printing "Error: ..." last is still ambiguous.
func isTartFailure(code int, stderr string) bool {
	switch code {
	case 1, 2, 64:
	default:
		return false
	}
	s := strings.TrimSpace(stderr)
	return tartErrorLine.MatchString(strings.TrimSpace(s[strings.LastIndexByte(s, '\n')+1:]))
}

// Session is a long-lived `tart exec -i -t` child giving a guest command a
// real terminal. It must run behind a host pty, never pipes: see openPTY.
type Session struct {
	*child
	master *os.File
	stderr bytes.Buffer // tart's own complaints, not the guest's output
}

// StartSession runs command in the guest behind a pty until it exits or Close
// is called. tart's flags must precede the VM name or it treats them as part
// of the command.
func (c *Client) StartSession(name string, command ...string) (*Session, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, fmt.Errorf("tart exec -i -t %s: %w", name, err)
	}

	cmd := exec.Command(c.Bin, append([]string{"exec", "-i", "-t", name}, command...)...)
	s := &Session{master: master}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, &s.stderr
	// A new session with the pty as controlling terminal makes tart a group
	// leader, so Close can kill everything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	s.child, err = startChild(cmd, nil)
	// Drop the parent's slave so the master sees EOF when the command exits.
	_ = slave.Close()
	if err != nil {
		_ = master.Close()
		return nil, fmt.Errorf("tart exec -i -t %s: %w", name, err)
	}
	return s, nil
}

// Output is the command's terminal output, including the pty's echo of what
// was written. The caller must drain it; nothing else does.
func (s *Session) Output() io.Reader { return ptyReader{s.master} }

// Write sends bytes to the command's terminal input.
func (s *Session) Write(p []byte) (int, error) { return s.master.Write(p) }

// Running reports whether the command is still going.
func (s *Session) Running() bool { return !s.exited() }

// Err explains why a finished session stopped, if tart itself failed. tart
// forwards the guest's exit code, so a non-zero exit is a result; tart being
// killed by a signal, or printing one of its own errors, is a failure.
func (s *Session) Err() error {
	if s.Running() {
		return nil
	}
	// stderr and ProcessState are complete once done is closed.
	msg := strings.TrimSpace(s.stderr.String())
	st := s.cmd.ProcessState
	if st == nil {
		return nil
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		if msg == "" {
			return fmt.Errorf("tart exec was killed by %s", ws.Signal())
		}
		return fmt.Errorf("tart exec was killed by %s: %s", ws.Signal(), msg)
	}
	if isTartFailure(st.ExitCode(), msg) {
		return fmt.Errorf("tart exec: %s", msg)
	}
	return nil
}

// closeWait is how long Close waits for a killed session to be reaped.
var closeWait = 5 * time.Second

// Close kills the command's process group, unless it has already exited,
// then releases the terminal.
func (s *Session) Close() error {
	killErr := s.kill()
	var waitErr error
	select {
	case <-s.done:
	case <-time.After(closeWait):
		waitErr = fmt.Errorf("tart exec (pid %d) still running %s after kill", s.cmd.Process.Pid, closeWait)
	}
	// Closing the master last lets the reader drain the command's final output.
	return errors.Join(killErr, waitErr, s.master.Close())
}

// child is a started tart process leading its own process group, which can
// be signalled without racing the reuse of its pid.
type child struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error // written before done is closed

	// reaping is set under mu just before the child is reaped. kill signals
	// only while it is false and holds mu to do so.
	mu      sync.Mutex
	reaping bool
}

// startChild starts cmd and reaps it in the background, calling onExit (if
// set) before done closes.
func startChild(cmd *exec.Cmd, onExit func()) (*child, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &child{cmd: cmd, done: make(chan struct{})}
	watch := watchExit
	go func() {
		// Observe the exit without reaping so the pid stays ours until
		// reaping is set.
		if err := watch(cmd.Process.Pid); err != nil {
			// Degraded: a kill could now race the reap, but a child that can
			// never be killed would be worse.
			slog.Warn("cannot watch tart for its exit", "pid", cmd.Process.Pid, "err", err)
		} else {
			c.setReaping()
		}
		c.waitErr = cmd.Wait()
		c.setReaping()
		if onExit != nil {
			onExit()
		}
		close(c.done)
	}()
	return c, nil
}

func (c *child) setReaping() {
	c.mu.Lock()
	c.reaping = true
	c.mu.Unlock()
}

// kill signals the process group unless the child has already exited.
func (c *child) kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reaping {
		return nil
	}
	return killGroup(c.cmd.Process.Pid)
}

func (c *child) exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// watchExit and killGroup are variables so tests can fail the watch and
// observe which groups are signalled.
var watchExit = awaitExit


var killGroup = func(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group %d: %w", pgid, err)
	}
	return nil
}
