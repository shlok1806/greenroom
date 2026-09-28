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
	"path/filepath"
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

// Start boots a VM headless in its own process group so it outlives the daemon,
// with output appended to logPath. Graphics mode is retired (ADR 0016). Audio and
// clipboard sharing are off: by default tart feeds the host's microphone into the guest,
// plays guest sound on the host and syncs the clipboard both ways, so code under test
// could read what the person copied or hear their room.
func (c *Client) Start(name, logPath string) (*Process, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(c.Bin, "run", name, "--no-graphics", "--no-audio", "--no-clipboard")
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

// Pid is the `tart run` process id.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

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

// Exec runs a command in the guest via the guest agent. A non-zero guest exit
// is reported in ExitCode, not as an error: errors mean tart itself failed.
func (c *Client) Exec(ctx context.Context, name string, args ...string) (ExecResult, error) {
	var stdout, stderr bytes.Buffer
	code, err := c.ExecTo(ctx, &stdout, &stderr, name, args...)
	return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}, err
}

// ExecTo is Exec writing the guest's output to stdout and stderr as it
// arrives instead of holding it, so the caller decides how much memory a
// command's output may take (issue #29). The last tailLimit bytes of stderr
// are kept to tell tart's own failure (always its last line) from the guest's.
func (c *Client) ExecTo(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (exitCode int, err error) {
	return runExec(ctx, exec.CommandContext(ctx, c.Bin, append([]string{"exec", name}, args...)...), stdout, stderr, name)
}

// ExecInputTo is ExecTo with stdin attached (`tart exec -i`): the guest command
// reads stdin to its end, then EOF. It carries what must not be in the guest
// command's argv, which every process listing in the guest shows (issue #128).
func (c *Client) ExecInputTo(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) (exitCode int, err error) {
	// tart's flags must precede the VM name or it treats them as part of the command.
	cmd := exec.CommandContext(ctx, c.Bin, append([]string{"exec", "-i", name}, args...)...)
	cmd.Stdin = stdin
	return runExec(ctx, cmd, stdout, stderr, name)
}

// execInterruptWait is how long a cancelled `tart exec` has between SIGINT and SIGKILL.
var execInterruptWait = 3 * time.Second

// interruptOnCancel makes a cancelled context end cmd with SIGINT, and SIGKILL only
// execInterruptWait later (daemon ADR 0002, issue #186). tart cancels its exec on SIGINT and
// cancels the gRPC call, which ends the guest command and lets tart exit cleanly. SIGKILL
// (exec.CommandContext's default) leaves the guest command running. Neither stops tart's fd
// leak: `tart run` keeps one vsock proxy per exec however the exec ends (measured in run
// 20260927-210125-687fa19deff41e76).
func interruptOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = execInterruptWait
}

func runExec(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.Writer, name string) (exitCode int, err error) {
	interruptOnCancel(cmd)
	tail := &tailBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(stderr, tail)
	err = cmd.Run()
	if err == nil {
		return 0, nil
	}
	if ctx.Err() != nil {
		return 0, fmt.Errorf("tart exec %s: %w", name, ctx.Err())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, fmt.Errorf("tart exec %s: %w", name, err)
	}
	if isTartFailure(exitErr.ExitCode(), tail.String()) {
		return 0, fmt.Errorf("tart exec %s: %s", name, strings.TrimSpace(tail.String()))
	}
	return exitErr.ExitCode(), nil
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

// Session is a long-lived `tart exec -i` child on plain pipes that carries a
// guest session's input (ADR 0017). tart's own tty mode (`-t`) is not used:
// in tart 2.37.0 a pty's output streaming through tart stalls and wedges the
// guest agent (issue #30), so the guest makes its own pty and the output comes
// back another way. The command's stdout goes to the host's /dev/null.
type Session struct {
	*child
	stdin     *os.File
	stderr    tailBuffer // tart's own complaints, not the guest's output
	closeOnce sync.Once
	closeErr  error
}

// StartSession runs command in the guest with its stdin on a pipe until it
// exits or Close is called. tart's flags must precede the VM name or it
// treats them as part of the command.
func (c *Client) StartSession(name string, command ...string) (*Session, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("tart exec -i %s: %w", name, err)
	}
	s := &Session{stdin: inW}
	cmd := exec.Command(c.Bin, append([]string{"exec", "-i", name}, command...)...)
	cmd.Stdin, cmd.Stderr = inR, &s.stderr
	// Its own process group, so Close can kill everything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	s.child, err = startChild(cmd, nil)
	_ = inR.Close() // the child's end
	if err != nil {
		_ = inW.Close()
		return nil, fmt.Errorf("tart exec -i %s: %w", name, err)
	}
	return s, nil
}

// Write sends bytes to the command's stdin.
func (s *Session) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Running reports whether the command is still going.
func (s *Session) Running() bool { return !s.exited() }

// Done closes once the process has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

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

// ExitCode is the guest command's exit code, which tart forwards as its own.
// ok is false while the command runs and when Err reports that tart itself
// failed or was killed: then there is no guest exit code to give.
func (s *Session) ExitCode() (code int, ok bool) {
	if s.Running() || s.Err() != nil {
		return 0, false
	}
	st := s.cmd.ProcessState
	if st == nil {
		return 0, false
	}
	return st.ExitCode(), true
}

// closeWait is how long Close waits for a killed session to be reaped.
var closeWait = 5 * time.Second

// Close kills the host process group, unless it has already exited, and
// closes stdin. It does not reach the guest: killing `tart exec` leaves the
// guest command running, so ending it is the caller's job. Safe to repeat.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		killErr := s.kill()
		var waitErr error
		select {
		case <-s.done:
		case <-time.After(closeWait):
			waitErr = fmt.Errorf("tart exec -i (pid %d) still running %s after kill", s.cmd.Process.Pid, closeWait)
		}
		s.closeErr = errors.Join(killErr, waitErr, s.stdin.Close())
	})
	return s.closeErr
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

// Home is where tart keeps its VMs (vms/<name>) and OCI cache: $TART_HOME, else ~/.tart.
func Home() string {
	if h := strings.TrimSpace(os.Getenv("TART_HOME")); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tart"
	}
	return filepath.Join(home, ".tart")
}
