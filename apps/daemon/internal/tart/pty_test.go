package tart

import (
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// This is the TIOCGWINSZ call `tart exec -t` makes on its stdin; without a
// size it dies with "failed to get terminal size".
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

// The isatty() that xcodebuild and friends branch on.
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

	// Reading to the end also proves the master's EIO on exit becomes EOF.
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

// Echo is deliberate (see Session.Output); pin it.
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
