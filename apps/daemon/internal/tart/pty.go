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

// sessionWinsize is the terminal size a session's command sees. It must be
// non-zero: `tart exec -t` reads it with TIOCGWINSZ and crashes without one.
// 120 columns because a model reads the output and 80 wraps diagnostics.
var sessionWinsize = unix.Winsize{Row: 40, Col: 120}

// openPTY allocates a host pty pair. macOS requires grant and unlock before
// the slave opens, and names the slave only through TIOCPTYGNAME.
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
	// Set before tart starts: it reads the size immediately.
	if err = unix.IoctlSetWinsize(int(s.Fd()), unix.TIOCSWINSZ, &sessionWinsize); err != nil {
		_ = s.Close()
		return nil, nil, fmt.Errorf("set terminal size: %w", err)
	}
	return m, s, nil
}

// ptyReader reports the EIO macOS returns once the last slave closes (the
// command exited) as a clean io.EOF.
type ptyReader struct{ f *os.File }

func (r ptyReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	if errors.Is(err, unix.EIO) {
		return n, io.EOF
	}
	return n, err
}

// awaitExit blocks until pid exits without reaping it, so the pid cannot be
// reused while the caller still acts on it. ESRCH means it already exited.
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
