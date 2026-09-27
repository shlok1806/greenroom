package tart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrNotRunning is RunPID's answer for a VM no process holds.
var ErrNotRunning = errors.New("no tart process holds the VM")

// RunPID returns the pid of the `tart run` that holds VM name, the way `tart stop` finds it:
// `tart run` keeps an fcntl write lock on the VM's config.json, and F_GETLK names its owner
// (tart's PIDLock). It is how the daemon finds a machine it reattached, whose `tart run` an
// earlier daemon started. It only asks: it never takes the lock, and holding none of its own,
// closing the file releases nothing.
func RunPID(name string) (int, error) {
	path := filepath.Join(Home(), "vms", name, "config.json")
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("find tart run for %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	// Asking for a write lock conflicts with any lock, so the answer names whoever holds one.
	lk := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := unix.FcntlFlock(f.Fd(), unix.F_GETLK, &lk); err != nil {
		return 0, fmt.Errorf("find tart run for %s: lock status of %s: %w", name, path, err)
	}
	if lk.Type == unix.F_UNLCK || lk.Pid <= 0 {
		return 0, fmt.Errorf("find tart run for %s: %w", name, ErrNotRunning)
	}
	return int(lk.Pid), nil
}
