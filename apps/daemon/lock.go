package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const lockName = "daemon.lock"

// lockRoot takes an exclusive lock on root for the life of the process, so two daemons never
// share one root: a second one would reattach the same machines, start a verifier on every
// run and append into transcripts the first daemon is numbering (issue #63). The kernel drops
// the lock when the process exits, however it exits, so a stale file never blocks a restart.
func lockRoot(root string) (release func(), err error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(root, lockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := ""
		if b, rerr := os.ReadFile(path); rerr == nil {
			if pid := strings.TrimSpace(string(b)); pid != "" {
				holder = " (pid " + pid + ")"
			}
		}
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another greenroom daemon%s is already serving root %s; stop it, or pass a different -root", holder, root)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() { _ = f.Close() }, nil
}
