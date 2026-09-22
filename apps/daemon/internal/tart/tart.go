// Package tart drives the Tart CLI (https://tart.run) as a subprocess.
package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Client runs tart commands.
type Client struct {
	Bin string
}

// New returns a Client that uses the tart binary on PATH.
func New() *Client { return &Client{Bin: "tart"} }

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

// isTartFailure recognises tart's own error messages so they are not
// mistaken for a failing guest command.
func isTartFailure(stderr string) bool {
	s := strings.TrimSpace(stderr)
	return strings.Contains(s, "is not running") ||
		strings.Contains(s, "Error:") && strings.HasPrefix(s, "Error:") ||
		strings.Contains(s, "guest agent")
}
