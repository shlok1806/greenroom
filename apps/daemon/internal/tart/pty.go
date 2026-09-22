package tart

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// sessionWinsize is the terminal size a session's command is told it has.
//
// It has to be a real size. `tart exec -t` reads the size from its own stdin
// with TIOCGWINSZ so it can forward it to the guest, and it does not degrade
// when it cannot: handed a pipe it dies with "failed to get terminal size:
// Inappropriate ioctl for device", which is a `try!` in tart's Exec.swift, so
// the session is dead before it starts. A daemon's stdin is never a terminal,
// so a host-side pty with a size set on it is the only way `-t` can work at
// all.
//
// 120x40 rather than the usual 80x24 because the output is read by a model,
// not shown in a window: at 80 columns compilers wrap diagnostics and test
// runners truncate paths, which makes the text harder to read for no gain.
// There is no option for this because nothing needs one yet; add one when a
// caller does.
var sessionWinsize = unix.Winsize{Row: 40, Col: 120}

// openPTY allocates a host-side pseudo-terminal pair.
//
// This is done with x/sys/unix rather than a pty library because x/sys is
// already a dependency of this module and is maintained by the Go project,
// and the whole dance is the four calls below. macOS wants the master
// granted and unlocked before the slave can be opened, and it names the
// slave through an ioctl rather than a derivable path.
func openPTY() (master, slave *os.File, err error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = m.Close()
		}
	}()

	if err = unix.IoctlSetInt(int(m.Fd()), unix.TIOCPTYGRANT, 0); err != nil {
		return nil, nil, fmt.Errorf("grant pty: %w", err)
	}
	if err = unix.IoctlSetInt(int(m.Fd()), unix.TIOCPTYUNLK, 0); err != nil {
		return nil, nil, fmt.Errorf("unlock pty: %w", err)
	}

	var buf [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(),
		unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return nil, nil, fmt.Errorf("name pty: %w", errno)
	}
	name := string(buf[:bytes.IndexByte(buf[:], 0)])

	s, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", name, err)
	}
	// The size goes on before anything starts, because tart reads it the
	// moment it comes up and a 0x0 terminal is what it chokes on.
	if err = unix.IoctlSetWinsize(int(s.Fd()), unix.TIOCSWINSZ, &sessionWinsize); err != nil {
		_ = s.Close()
		return nil, nil, fmt.Errorf("set terminal size: %w", err)
	}
	return m, s, nil
}

// ptyReader reads a pty master and reports the end of the session as EOF.
//
// macOS answers a read on the master with EIO once the last slave is closed,
// which is what happens when the command exits. That is the ordinary end of
// a session, not a fault, and passing it up would turn every finished build
// into a failed one.
type ptyReader struct{ f *os.File }

func (r ptyReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	if err != nil && errors.Is(err, unix.EIO) {
		return n, io.EOF
	}
	return n, err
}

// awaitExit blocks until pid has exited without reaping it, so the pid cannot
// be reused while the caller still acts on it. A process that has already
// exited answers ESRCH, which is the same answer.
func awaitExit(pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(kq) }()
	var change unix.Kevent_t
	unix.SetKevent(&change, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change.Fflags = unix.NOTE_EXIT
	for {
		_, err := unix.Kevent(kq, []unix.Kevent_t{change}, nil, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		break
	}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, nil, events, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}
}
