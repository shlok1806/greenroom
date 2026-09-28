package tart

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeBin writes a script that stands in for the tart binary. body runs for
// the `run` subcommand.
func fakeBin(t *testing.T, body string) *Client {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tart")
	script := "#!/bin/sh\ncase \"$1\" in\n  run) " + body + " ;;\n  *) exit 0 ;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Client{Bin: path}
}

// waitExit blocks until the process reports that it stopped. The bound is
// generous because the VM suite runs these next to booting guests, where even
// a one-line script can take seconds to start and exit.
func waitExit(t *testing.T, p *Process) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the process never reported an exit")
	}
	if !p.Exited() {
		t.Fatal("Exited is false after the process stopped")
	}
}

func TestProcessReportsWhatTartPrinted(t *testing.T) {
	c := fakeBin(t, "echo 'The number of VMs exceeds the system limit' >&2; exit 1")
	logPath := filepath.Join(t.TempDir(), "vm.log")

	p, err := c.Start("vm-1", logPath)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitExit(t, p)

	err = p.Err()
	if err == nil {
		t.Fatal("Err returned nil for a process that exited")
	}
	if !strings.Contains(err.Error(), "exceeds the system limit") {
		t.Errorf("Err = %v, want the message tart printed", err)
	}
	if !strings.Contains(err.Error(), "vm-1") {
		t.Errorf("Err = %v, want the machine name", err)
	}
}

func TestProcessKeepsOnlyTheLastLinesOfTheLog(t *testing.T) {
	c := fakeBin(t, "for i in 1 2 3 4 5 6; do echo \"line $i\"; done; exit 1")
	logPath := filepath.Join(t.TempDir(), "vm.log")

	p, err := c.Start("vm-2", logPath)
	if err != nil {
		t.Fatal(err)
	}
	waitExit(t, p)

	msg := p.Err().Error()
	for _, want := range []string{"line 4", "line 5", "line 6"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Err = %q, want it to hold %q", msg, want)
		}
	}
	for _, unwanted := range []string{"line 1", "line 2", "line 3"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("Err = %q, want it to drop %q", msg, unwanted)
		}
	}
}

func TestProcessStillExplainsASilentExit(t *testing.T) {
	c := fakeBin(t, "exit 3")
	logPath := filepath.Join(t.TempDir(), "vm.log")

	p, err := c.Start("vm-3", logPath)
	if err != nil {
		t.Fatal(err)
	}
	waitExit(t, p)

	err = p.Err()
	if err == nil {
		t.Fatal("Err returned nil for a process that exited")
	}
	if !strings.Contains(err.Error(), "vm-3") || !strings.Contains(err.Error(), "exited") {
		t.Errorf("Err = %v, want it to name the machine and say that it exited", err)
	}
}

func TestProcessThatKeepsRunningHasNoError(t *testing.T) {
	c := fakeBin(t, "sleep 30")
	logPath := filepath.Join(t.TempDir(), "vm.log")

	p, err := c.Start("vm-4", logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })

	if p.Exited() {
		t.Error("Exited reported true for a process that is still running")
	}
	if err := p.Err(); err != nil {
		t.Errorf("Err = %v, want nil while the VM runs", err)
	}
}

func TestProcessIsSafeToPollFromManyGoroutines(t *testing.T) {
	c := fakeBin(t, "echo bye >&2; exit 1")
	logPath := filepath.Join(t.TempDir(), "vm.log")

	p, err := c.Start("vm-5", logPath)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if p.Exited() {
					_ = p.Err()
				}
			}
		}()
	}
	wg.Wait()
	waitExit(t, p)
	if p.Err() == nil {
		t.Error("Err returned nil after the process exited")
	}
}

func TestStartRejectsAnUnwritableLogPath(t *testing.T) {
	c := fakeBin(t, "exit 0")
	dir := t.TempDir()
	if _, err := c.Start("vm-6", filepath.Join(dir, "missing", "vm.log")); err == nil {
		t.Fatal("Start accepted a log path it cannot create")
	}
}

// Graphics mode is retired (ADR 0016, issue #7): every VM boots headless.
func TestStartBootsHeadless(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "tart")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + args + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Client{Bin: bin}
	p, err := c.Start("vm", filepath.Join(dir, "vm.log"))
	if err != nil {
		t.Fatal(err)
	}
	waitExit(t, p)
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if want := "run vm --no-graphics --no-audio --no-clipboard"; strings.TrimSpace(string(got)) != want {
		t.Errorf("Start ran tart with %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

// tart runs in its own process group, so Kill must end all of it.
func TestKillEndsTheWholeProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	c := fakeBin(t, "sleep 60 & echo $! > "+pidFile+"; wait")
	p, err := c.Start("vm-group", filepath.Join(t.TempDir(), "vm.log"))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	for i := 0; i < 100 && pid == 0; i++ {
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the stand-in tart never started its child")
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitExit(t, p)
	for i := 0; i < 100; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("Kill left tart's child %d running", pid)
}
