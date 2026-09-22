package tart

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitPipe(t *testing.T, p *Pipe) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the pipe never ended")
	}
}

// Bytes go both ways on plain pipes, with tart's flags before the VM name.
func TestPipeCarriesBytesBothWays(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	p, err := sessionBin(t, `printf '%s\n' "$*" > `+args+`; [ -t 0 ] && exit 9; exec cat`).StartPipe("vm", "helper", "--serve")
	if err != nil {
		t.Fatalf("StartPipe: %v", err)
	}
	defer func() { _ = p.Close() }()

	if _, err := p.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	line, err := bufio.NewReader(p.Stdout()).ReadString('\n')
	if err != nil || line != "hello\n" {
		t.Fatalf("read %q, %v; want the echo", line, err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Err(); err != nil {
		t.Errorf("cat ended by stdin EOF reported %v", err)
	}
	got, _ := os.ReadFile(args)
	if strings.TrimSpace(string(got)) != "exec -i vm helper --serve" {
		t.Errorf("tart got %q", got)
	}
}

// A failing command reports its exit and stderr, and stdout reaches EOF.
func TestPipeReportsAFailure(t *testing.T) {
	p, err := sessionBin(t, `echo "helper broke" >&2; exit 3`).StartPipe("vm", "x")
	if err != nil {
		t.Fatalf("StartPipe: %v", err)
	}
	if _, err := io.ReadAll(p.Stdout()); err != nil {
		t.Fatalf("stdout: %v", err)
	}
	waitPipe(t, p)
	err = p.Err()
	if err == nil || !strings.Contains(err.Error(), "exit 3") || !strings.Contains(err.Error(), "helper broke") {
		t.Errorf("Err() = %v, want exit 3 and the stderr", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("closing a finished pipe: %v", err)
	}
}

// A command that ignores stdin EOF is killed with its whole process group.
func TestClosingAPipeKillsItsGroup(t *testing.T) {
	kills := recordKills(t)
	orig := pipeGrace
	pipeGrace = 50 * time.Millisecond
	t.Cleanup(func() { pipeGrace = orig })

	p, err := sessionBin(t, "trap '' HUP; sleep 60 & wait").StartPipe("vm", "x")
	if err != nil {
		t.Fatalf("StartPipe: %v", err)
	}
	pid := p.cmd.Process.Pid
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := kills(); len(got) != 1 || got[0] != pid {
		t.Errorf("Close signalled %v, want the group %d", got, pid)
	}
	if err := p.Err(); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Errorf("Err() = %v, want killed", err)
	}
}
