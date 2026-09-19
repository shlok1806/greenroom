package tart

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// waitExit polls until the process reports that it stopped.
func waitExit(t *testing.T, p *Process) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if p.Exited() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the process never reported an exit")
}

func TestProcessReportsWhatTartPrinted(t *testing.T) {
	c := fakeBin(t, "echo 'The number of VMs exceeds the system limit' >&2; exit 1")
	logPath := filepath.Join(t.TempDir(), "vm.log")

	p, err := c.Start("vm-1", logPath, false)
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

	p, err := c.Start("vm-2", logPath, false)
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

	p, err := c.Start("vm-3", logPath, false)
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

	p, err := c.Start("vm-4", logPath, false)
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

	p, err := c.Start("vm-5", logPath, false)
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
	if _, err := c.Start("vm-6", filepath.Join(dir, "missing", "vm.log"), false); err == nil {
		t.Fatal("Start accepted a log path it cannot create")
	}
}

// A watched machine runs its screen over VNC and tart prints the address.
// The address is what lets a person look at the work as it happens.
func TestWatchedProcessReportsItsScreenAddress(t *testing.T) {
	c := fakeBin(t, "echo 'Opening vnc://:word-word@127.0.0.1:60592...'; sleep 5")
	logPath := filepath.Join(t.TempDir(), "vm.log")

	p, err := c.Start("vm-watch", logPath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })

	url := p.VNCURL(5 * time.Second)
	if url != "vnc://:word-word@127.0.0.1:60592" {
		t.Errorf("VNCURL = %q, want the address tart printed without its trailing dots", url)
	}
}

func TestWatchedAndHeadlessUseDifferentTartFlags(t *testing.T) {
	for _, tc := range []struct {
		watch bool
		want  string
	}{{true, "--vnc-experimental"}, {false, "--no-graphics"}} {
		dir := t.TempDir()
		args := filepath.Join(dir, "args")
		bin := filepath.Join(dir, "tart")
		script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + args + "\nexit 0\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		c := &Client{Bin: bin}
		p, err := c.Start("vm", filepath.Join(dir, "vm.log"), tc.watch)
		if err != nil {
			t.Fatal(err)
		}
		waitExit(t, p)
		got, err := os.ReadFile(args)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), tc.want) {
			t.Errorf("watch=%v ran tart with %q, want %q", tc.watch, strings.TrimSpace(string(got)), tc.want)
		}
	}
}

func TestHeadlessProcessHasNoScreenAddress(t *testing.T) {
	c := fakeBin(t, "sleep 5")
	p, err := c.Start("vm-headless", filepath.Join(t.TempDir(), "vm.log"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	if url := p.VNCURL(time.Second); url != "" {
		t.Errorf("VNCURL = %q, want empty for a headless machine", url)
	}
}
