// Package openfiles raises the daemon's open file limit so the `tart run` it starts inherits
// it, and counts the files another process has open (issue #186, daemon ADR 0002). A leaf:
// it imports nothing of the daemon's.
package openfiles

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Limits is the daemon's RLIMIT_NOFILE after Raise. Soft is what every child started from
// then on inherits. There is no "before": Go raises its own soft limit at startup, so
// getrlimit already reports Go's raised value, never the one (launchd's 256) that children
// were given until Raise ran, and that one cannot be read back.
type Limits struct {
	Soft, Hard uint64
	PerProc    uint64 // kern.maxfilesperproc; 0 when unreadable
}

var (
	mu     sync.Mutex
	raised *Limits // what Raise set; nil until it has run
)

// Raise sets the soft RLIMIT_NOFILE to the hard limit, capped at kern.maxfilesperproc (the
// kernel refuses more, and an infinite hard limit is common). It always ends with a
// syscall.Setrlimit call, even when it cannot raise: once a program calls it, Go stops
// restoring the soft limit the process started with (256 under launchd) in every child it
// starts (syscall/rlimit.go, origRlimitNofile), so children inherit exactly the limit
// Inherited reports. Without it `tart run` gets 256. It must be syscall.Setrlimit, not
// unix.Setrlimit: only the former tells the runtime.
func Raise() (Limits, error) {
	var cur syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &cur); err != nil {
		return Limits{}, fmt.Errorf("read the open file limit: %w", err)
	}
	perProc, _ := unix.SysctlUint32("kern.maxfilesperproc")
	want := target(cur.Max, uint64(perProc))
	next := syscall.Rlimit{Cur: want, Max: cur.Max}
	err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &next)
	if err != nil {
		// Keep what the process has, but still through Setrlimit, so children inherit it.
		err = errors.Join(fmt.Errorf("raise the open file limit to %d: %w", want, err),
			syscall.Setrlimit(syscall.RLIMIT_NOFILE, &cur))
	}
	var now syscall.Rlimit
	if gerr := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &now); gerr != nil {
		now = cur
	}
	l := Limits{Soft: now.Cur, Hard: now.Max, PerProc: uint64(perProc)}
	mu.Lock()
	raised = &l
	mu.Unlock()
	return l, err
}

// target is the soft limit Raise asks for: the hard limit, capped at perProc when that is known.
func target(hard, perProc uint64) uint64 {
	if perProc > 0 && (hard == unix.RLIM_INFINITY || hard > perProc) {
		return perProc
	}
	return hard
}

// Inherited is the soft open file limit a child started from now on gets. ok is false until
// Raise has run: before that Go gives children the limit the daemon started with, which this
// process can no longer read.
func Inherited() (limit uint64, ok bool) {
	mu.Lock()
	defer mu.Unlock()
	if raised == nil {
		return 0, false
	}
	return raised.Soft, true
}

// countTimeout bounds one lsof call.
const countTimeout = 5 * time.Second

// Count returns how many file descriptors pid has open, from `lsof -p` (there is no
// proc_pidinfo without cgo). It counts numeric fds only, not lsof's cwd, txt and mem rows,
// so it is the number the kernel checks against RLIMIT_NOFILE.
func Count(ctx context.Context, pid int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, countTimeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "lsof", "-n", "-P", "-w", "-p", fmt.Sprint(pid), "-Ff")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return 0, fmt.Errorf("lsof -p %d: %w", pid, ctx.Err())
	}
	if err != nil {
		// lsof exits 1 with no output for a pid that does not exist.
		return 0, fmt.Errorf("lsof -p %d: %w: %s", pid, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return countFDs(out), nil
}

// countFDs counts the "f<number>" lines of lsof -F f output.
func countFDs(out []byte) int {
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) > 1 && line[0] == 'f' && line[1] >= '0' && line[1] <= '9' {
			n++
		}
	}
	return n
}
