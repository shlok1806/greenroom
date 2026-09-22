package tart

import (
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// The window size has to be readable from the master, because that is exactly
// what `tart exec -t` does with its own stdin before it will talk to a guest.
// Handed a pipe it does not fall back, it dies with "failed to get terminal
// size: Inappropriate ioctl for device" and the session is over before it
// starts. This is that call, without a VM.
func TestAPtyAnswersTheWindowSizeQuestionThatTartAsks(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()

	ws, err := unix.IoctlGetWinsize(int(master.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatalf("TIOCGWINSZ on the master failed, which is what kills tart -t: %v", err)
	}
	if ws.Col != sessionWinsize.Col || ws.Row != sessionWinsize.Row {
		t.Errorf("the terminal is %dx%d, want %dx%d",
			ws.Col, ws.Row, sessionWinsize.Col, sessionWinsize.Row)
	}
}

// A command started on the slave has to see a terminal, or the whole feature
// is pointless: this is the isatty() that xcodebuild and friends branch on.
func TestACommandOnThePtySeesATerminal(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer func() { _ = master.Close() }()

	cmd := exec.Command("/bin/sh", "-c",
		`test -t 0 && echo "STDIN""_TTY"; test -t 1 && echo "STDOUT""_TTY"; tty`)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = slave.Close()
	go func() { _ = cmd.Wait() }()

	// Reading to the end also proves the EIO the master answers with when the
	// command exits is reported as a clean end of stream, not a failure.
	out, err := io.ReadAll(ptyReader{master})
	if err != nil {
		t.Fatalf("reading the session to its end reported an error: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "STDIN_TTY") {
		t.Errorf("the command's stdin was not a terminal; output was %q", text)
	}
	if !strings.Contains(text, "STDOUT_TTY") {
		t.Errorf("the command's stdout was not a terminal; output was %q", text)
	}
	if !strings.Contains(text, "/dev/ttys") {
		t.Errorf("tty(1) did not name a pty; output was %q", text)
	}
}

// A terminal echoes, so a caller sees its own input come back. That is kept
// on purpose, so it is worth pinning: a change that turned it off would
// silently alter what every session's output looks like.
func TestThePtyEchoesWhatIsWrittenToIt(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()

	if _, err := master.Write([]byte("typed\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	n, err := master.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "typed") {
		t.Errorf("the terminal did not echo; read %q", buf[:n])
	}
}
