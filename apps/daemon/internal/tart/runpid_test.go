package tart

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// lockHelperEnv makes the test binary hold a write lock on a file, as `tart run` holds its
// VM's config.json, instead of running the tests.
const lockHelperEnv = "GREENROOM_TART_LOCK_HELPER"

func TestMain(m *testing.M) {
	if path := os.Getenv(lockHelperEnv); path != "" {
		holdLock(path)
		return
	}
	os.Exit(m.Run())
}

// holdLock takes tart's PIDLock on path, says so on stdout and holds it until stdin closes.
func holdLock(path string) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	lk := unix.Flock_t{Type: unix.F_WRLCK}
	if err := unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lk); err != nil {
		fmt.Println("lock:", err)
		os.Exit(1)
	}
	fmt.Println("locked")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestRunPIDNamesTheProcessHoldingTheVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TART_HOME", home)
	config := filepath.Join(home, "vms", "vm", "config.json")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RunPID("vm"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RunPID of a VM nobody holds: %v, want ErrNotRunning", err)
	}

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+config)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "locked" {
		t.Fatalf("lock helper said %q", line)
	}

	pid, err := RunPID("vm")
	if err != nil || pid != cmd.Process.Pid {
		t.Fatalf("RunPID = %d, %v; want the holder %d", pid, err, cmd.Process.Pid)
	}
	// Asking must not have disturbed the lock.
	if again, err := RunPID("vm"); err != nil || again != pid {
		t.Errorf("second RunPID = %d, %v", again, err)
	}
}

func TestRunPIDOfAMissingVMFails(t *testing.T) {
	t.Setenv("TART_HOME", t.TempDir())
	if _, err := RunPID("nope"); err == nil {
		t.Error("RunPID of a VM that does not exist returned no error")
	}
}
