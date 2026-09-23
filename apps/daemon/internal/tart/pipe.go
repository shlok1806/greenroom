package tart

import (
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

// Pipe is a long-lived `tart exec -i` child on plain pipes, for a guest
// command that speaks a binary protocol. Without -t tart needs no pty.
type Pipe struct {
	*child
	stdin  *os.File
	stdout *os.File
	stderr tailBuffer

	closeOnce sync.Once
}

// pipeGrace is how long Close lets the command exit on stdin EOF before killing it.
var pipeGrace = time.Second

// StartPipe runs command in the guest with its stdin and stdout on pipes.
// The caller must drain Stdout; nothing else does.
func (c *Client) StartPipe(name string, command ...string) (*Pipe, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("tart exec -i %s: %w", name, err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_, _ = inR.Close(), inW.Close()
		return nil, fmt.Errorf("tart exec -i %s: %w", name, err)
	}
	p := &Pipe{stdin: inW, stdout: outR}
	cmd := exec.Command(c.Bin, append([]string{"exec", "-i", name}, command...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, &p.stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.child, err = startChild(cmd, nil)
	// Drop the child's ends so stdout reads EOF when the command exits.
	_, _ = inR.Close(), outW.Close()
	if err != nil {
		_, _ = inW.Close(), outR.Close()
		return nil, fmt.Errorf("tart exec -i %s: %w", name, err)
	}
	return p, nil
}

// Write sends bytes to the command's stdin.
func (p *Pipe) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Stdout is the command's output.
func (p *Pipe) Stdout() io.Reader { return p.stdout }

// Done closes once the process has exited.
func (p *Pipe) Done() <-chan struct{} { return p.done }

// Err explains why a finished command stopped: nil for exit 0, else the exit
// status or signal with the tail of stderr.
func (p *Pipe) Err() error {
	if !p.exited() {
		return nil
	}
	msg := strings.TrimSpace(p.stderr.String())
	st := p.cmd.ProcessState
	if st == nil || st.Success() {
		return nil
	}
	what := fmt.Sprintf("exit %d", st.ExitCode())
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		what = "killed by " + ws.Signal().String()
	}
	if msg == "" {
		return fmt.Errorf("tart exec -i: %s", what)
	}
	return fmt.Errorf("tart exec -i: %s: %s", what, msg)
}

// Close ends stdin, gives the command pipeGrace to exit, then kills its
// process group and waits for it. Safe to repeat.
func (p *Pipe) Close() error {
	var err error
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		select {
		case <-p.done:
		case <-time.After(pipeGrace):
		}
		killErr := p.kill()
		var waitErr error
		select {
		case <-p.done:
		case <-time.After(closeWait):
			waitErr = fmt.Errorf("tart exec -i (pid %d) still running %s after kill", p.cmd.Process.Pid, closeWait)
		}
		err = errors.Join(killErr, waitErr, p.stdout.Close())
	})
	return err
}

// tailBuffer keeps the last tailLimit bytes written, so a chatty command cannot grow memory.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailLimit = 16 * 1024

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - tailLimit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
