package openfiles

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTargetIsTheHardLimitCappedPerProcess(t *testing.T) {
	for _, c := range []struct{ hard, perProc, want uint64 }{
		{unix.RLIM_INFINITY, 138240, 138240},
		{65536, 138240, 65536},
		{200000, 138240, 138240},
		{65536, 0, 65536}, // sysctl unreadable: ask for the hard limit
	} {
		if got := target(c.hard, c.perProc); got != c.want {
			t.Errorf("target(%d, %d) = %d, want %d", c.hard, c.perProc, got, c.want)
		}
	}
}

// helperEnv makes the test binary run limitHelper instead of its tests.
const helperEnv = "GREENROOM_OPENFILES_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		limitHelper(mode)
		return
	}
	os.Exit(m.Run())
}

// limitHelper runs in a process started with a soft limit of 256, as launchd starts the daemon.
// It raises the limit or not, then prints what a child sees, what Inherited says and the soft
// limit Raise reported (the value the daemon logs).
func limitHelper(mode string) {
	var reported uint64
	if mode == "raise" {
		l, err := Raise()
		if err != nil {
			fmt.Println("raise:", err)
			os.Exit(1)
		}
		reported = l.Soft
	}
	out, err := exec.Command("/bin/sh", "-c", "ulimit -n").Output()
	if err != nil {
		fmt.Println("ulimit:", err)
		os.Exit(1)
	}
	inherited, ok := Inherited()
	fmt.Printf("child=%s inherited=%d known=%v reported=%d\n", strings.TrimSpace(string(out)), inherited, ok, reported)
}

// runHelper starts this test binary with a soft open file limit of 256 (the hard one untouched,
// as launchd does) in the given mode.
func runHelper(t *testing.T, mode string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `ulimit -Sn 256 && exec "$0"`, os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper (%s): %v\n%s", mode, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Why Raise exists (issue #186): Go raises its own soft limit but gives every child the one the
// process started with, so `tart run` got 256. After Raise a child gets the raised limit.
func TestAChildStartedAfterRaiseInheritsTheRaisedLimit(t *testing.T) {
	if got := runHelper(t, "none"); got != "child=256 inherited=0 known=false reported=0" {
		t.Fatalf("without Raise: %q, want a child at 256 (if Go stopped restoring the limit, Raise may be unneeded)", got)
	}
	got := runHelper(t, "raise")
	var child, inherited, reported uint64
	var known bool
	if _, err := fmt.Sscanf(got, "child=%d inherited=%d known=%t reported=%d", &child, &inherited, &known, &reported); err != nil {
		t.Fatalf("helper printed %q: %v", got, err)
	}
	if !known || child <= 256 || child != inherited {
		t.Fatalf("after Raise: %q, want a child above 256 at the limit Inherited reports", got)
	}
	// The startup log prints Limits.Soft: it must be what a child really gets.
	if reported != child {
		t.Errorf("Raise reported soft=%d, a child got %d", reported, child)
	}
	if perProc, err := unix.SysctlUint32("kern.maxfilesperproc"); err == nil && child > uint64(perProc) {
		t.Errorf("child limit %d is over kern.maxfilesperproc %d", child, perProc)
	}
}

func TestCountSeesFilesTheProcessOpens(t *testing.T) {
	pid := os.Getpid()
	before, err := Count(context.Background(), pid)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	var files []*os.File
	for range 20 {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	t.Cleanup(func() {
		for _, f := range files {
			_ = f.Close()
		}
	})
	after, err := Count(context.Background(), pid)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if after-before < 20 {
		t.Errorf("Count went from %d to %d after opening 20 files", before, after)
	}
}

func TestCountFailsForAProcessThatIsGone(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if n, err := Count(context.Background(), cmd.Process.Pid); err == nil {
		t.Errorf("Count of a reaped pid = %d, want an error", n)
	}
}

func TestCountFDsCountsOnlyNumericDescriptors(t *testing.T) {
	out := "p123\nfcwd\nftxt\nf0\nf1\nf2\nf17\nfmem\n"
	if got := countFDs([]byte(out)); got != 4 {
		t.Errorf("countFDs = %d, want 4", got)
	}
}
