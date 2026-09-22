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
	name    string
	logPath string
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error // written before done is closed
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

// Kill ends the tart process directly; the normal path is `tart stop`.
func (p *Process) Kill() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// Exited reports whether the tart process has stopped.
func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

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
	if isTartFailure(res.Stderr) {
		return res, fmt.Errorf("tart exec %s: %s", name, strings.TrimSpace(res.Stderr))
	}
	res.ExitCode = exitErr.ExitCode()
	return res, nil
}

// Session is a long-lived `tart exec -i -t` child giving a guest command a
// real terminal. It must run behind a host pty, never pipes: see openPTY.
type Session struct {
	cmd    *exec.Cmd
	master *os.File
	stderr bytes.Buffer // tart's own complaints, not the guest's output
	done   chan struct{}

	// reaping is set under mu just before the child is reaped. Close signals
	// only while it is false and holds mu to do so, so it never signals a pid
	// (and process group) that may have been reused.
	mu      sync.Mutex
	reaping bool
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
	s := &Session{cmd: cmd, master: master, done: make(chan struct{})}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, &s.stderr
	// A new session with the pty as controlling terminal makes tart a group
	// leader, so Close can kill everything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		return nil, fmt.Errorf("tart exec -i -t %s: %w", name, err)
	}
	// Drop the parent's slave so the master sees EOF when the command exits.
	_ = slave.Close()

	go func() {
		// Observe the exit without reaping so the pid stays ours until
		// reaping is set. If kqueue fails, Close just stops signalling.
		_ = awaitExit(cmd.Process.Pid)
		s.mu.Lock()
		s.reaping = true
		s.mu.Unlock()
		_ = cmd.Wait()
		close(s.done)
	}()
	return s, nil
}

// Output is the command's terminal output, including the pty's echo of what
// was written. The caller must drain it; nothing else does.
func (s *Session) Output() io.Reader { return ptyReader{s.master} }

// Write sends bytes to the command's terminal input.
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

// Err explains why a finished session stopped, if tart itself failed. tart
// forwards the guest's exit code, so a non-zero exit is a result; tart being
// killed by a signal, or printing one of its own errors, is a failure.
func (s *Session) Err() error {
	if s.Running() {
		return nil
	}
	// stderr and ProcessState are complete once done is closed.
	msg := strings.TrimSpace(s.stderr.String())
	if st := s.cmd.ProcessState; st != nil {
		if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			if msg == "" {
				return fmt.Errorf("tart exec was killed by %s", ws.Signal())
			}
			return fmt.Errorf("tart exec was killed by %s: %s", ws.Signal(), msg)
		}
	}
	if msg != "" && isTartFailure(msg) {
		return fmt.Errorf("tart exec: %s", msg)
	}
	return nil
}

// Close kills the command's process group, unless it has already exited,
// then releases the terminal.
func (s *Session) Close() error {
	s.mu.Lock()
	if !s.reaping {
		killGroup(s.cmd.Process.Pid)
	}
	s.mu.Unlock()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
	// Closing the master last lets the reader drain the command's final output.
	return s.master.Close()
}

// killGroup is a variable so tests can observe which groups Close signals.
var killGroup = func(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// isTartFailure recognises tart's own error messages so they are not mistaken
// for a failing guest command.
func isTartFailure(stderr string) bool {
	s := strings.TrimSpace(stderr)
	return strings.HasPrefix(s, "Error:") ||
		strings.Contains(s, "is not running") ||
		strings.Contains(s, "guest agent")
}
