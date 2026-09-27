package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// guard runs `guarded <min> <command>` from scripts/disk-guard.sh with a df that reports the
// free KB written in the returned file, polling every second.
func guard(t *testing.T, minGB, command string) (*exec.Cmd, string, *strings.Builder) {
	t.Helper()
	bin := t.TempDir()
	freeFile := filepath.Join(bin, "free-kb")
	df := "#!/bin/sh\necho 'Filesystem 1024-blocks Used Available Capacity Mounted'\necho \"/dev/disk3 1 1 $(cat '" + freeFile + "') 1% /\"\n"
	if err := os.WriteFile(filepath.Join(bin, "df"), []byte(df), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", ". scripts/disk-guard.sh; guarded "+minGB+" "+command)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GREENROOM_DISK_POLL=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	return cmd, freeFile, &stderr
}

func setFreeGB(t *testing.T, file string, gb int) {
	t.Helper()
	if err := os.WriteFile(file, []byte(strconv.Itoa(gb*1048576)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Issue #155: build-image.sh checked free space only at its start. Its long steps now run
// under a guard that stops them when the disk falls under the floor, and passes through the
// step's own status otherwise.
func TestTheBuildStopsWhenTheDiskFallsMidStep(t *testing.T) {
	cmd, free, stderr := guard(t, "5", "sleep 30")
	setFreeGB(t, free, 40)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // the first reading found room
	setFreeGB(t, free, 3)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 75 {
			t.Errorf("guarded = %v, want exit 75", err)
		}
		if !strings.Contains(stderr.String(), "only 3 GB free on /, under 5 GB; stopping sleep") {
			t.Errorf("stderr = %q", stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the guard did not stop the step within 10 s of the disk falling")
	}

	cmd, free, _ = guard(t, "5", "sh -c 'exit 3'")
	setFreeGB(t, free, 40)
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("guarded with room = %v, want the step's own exit 3", err)
	}
}
