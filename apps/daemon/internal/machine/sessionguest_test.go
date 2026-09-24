package machine

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These run the session's guest scripts (ADR 0016) for real in a host shell:
// the host is macOS, with the same /usr/bin/script, stat, pgrep and pkill as
// the guest. The login zsh reads no dotfiles of this host's user.

type guestSession struct {
	t     *testing.T
	id    string
	tmp   string
	env   []string
	stdin io.WriteCloser
	cmd   *exec.Cmd
	done  chan struct{}
	out   strings.Builder // the wrapper's own stdout: must stay empty
}

func startGuestSession(t *testing.T, command string) *guestSession {
	t.Helper()
	if _, err := os.Stat("/usr/bin/script"); err != nil {
		t.Skip("no /usr/bin/script")
	}
	home := t.TempDir()
	g := &guestSession{t: t, id: "t" + strconv.Itoa(os.Getpid()) + strconv.FormatInt(time.Now().UnixNano()%1e6, 10),
		tmp: t.TempDir(), done: make(chan struct{})}
	g.env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "TMPDIR="+g.tmp, "TERM=")
	g.cmd = exec.Command("/bin/sh", "-c", sessionWrapper, "greenroom-session", g.id, command)
	g.cmd.Env = g.env
	g.cmd.Stdout = &g.out
	var err error
	if g.stdin, err = g.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := g.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = g.cmd.Wait(); close(g.done) }()
	t.Cleanup(func() {
		g.close()
		_ = g.cmd.Process.Kill()
	})
	return g
}

func (g *guestSession) file() string { return filepath.Join(g.tmp, "greenroom-session."+g.id) }

func (g *guestSession) send(s string) {
	g.t.Helper()
	if _, err := io.WriteString(g.stdin, s); err != nil {
		g.t.Fatal(err)
	}
}

// read runs sessionReadScript as the daemon does and returns its offset line and data.
func (g *guestSession) read(off, limit int) (start int, data string, code int) {
	g.t.Helper()
	return runSessionRead(g.t, g.env, g.id, off, limit)
}

func runSessionRead(t *testing.T, env []string, id string, off, limit int) (start int, data string, code int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", sessionReadScript, "greenroom-session-read", id, strconv.Itoa(off), strconv.Itoa(limit))
	cmd.Env = env
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return 0, "", exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	line, rest, ok := strings.Cut(string(out), "\n")
	if !ok {
		t.Fatalf("the read printed no offset line: %q", out)
	}
	start, err = strconv.Atoi(line)
	if err != nil {
		t.Fatalf("bad offset line %q", line)
	}
	return start, rest, 0
}

// waitFor reads the whole file until it holds want.
func (g *guestSession) waitFor(want string) string {
	g.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, data, _ := g.read(0, 1<<20)
		if strings.Contains(data, want) {
			return data
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("the session never printed %q; it printed:\n%s", want, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (g *guestSession) close() {
	cmd := exec.Command("/bin/sh", "-c", sessionCloseScript, "greenroom-session-close", g.id)
	cmd.Env = g.env
	_ = cmd.Run()
}

func (g *guestSession) exited(within time.Duration) bool {
	select {
	case <-g.done:
		return true
	case <-time.After(within):
		return false
	}
}

// The command sees a real terminal of the daemon's size, while nothing comes
// out of the wrapper's stdout: all output goes to the file.
func TestASessionCommandSeesATerminalAndPrintsOnlyToItsFile(t *testing.T) {
	g := startGuestSession(t, `test -t 0 && test -t 1 && echo "IS""TTY"; stty size; echo "TERM=$TERM"; exit 7`)
	if !g.exited(15 * time.Second) {
		t.Fatal("the session did not end")
	}
	out := g.waitFor("TERM=")
	if !strings.Contains(out, "ISTTY") {
		t.Errorf("the command's stdin and stdout were not a terminal; it printed:\n%s", out)
	}
	if !strings.Contains(out, "40 120") {
		t.Errorf("the terminal is not 40x120; it printed:\n%s", out)
	}
	if !strings.Contains(out, "TERM=xterm-256color") {
		t.Errorf("an empty TERM was not set; it printed:\n%s", out)
	}
	if code := g.cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("the wrapper exited %d, want the command's 7", code)
	}
	if g.out.Len() != 0 {
		t.Errorf("the wrapper wrote %d bytes to tart's stdout, want none: %q", g.out.Len(), g.out.String())
	}
}

// Input goes through the pty's line discipline: a line runs, and ^C
// interrupts the foreground command rather than reaching it as a byte.
func TestASessionTakesInputAndCtrlCInterrupts(t *testing.T) {
	g := startGuestSession(t, "exec /bin/zsh -i")
	g.send("echo \"HEL\"\"LO\"\n")
	g.waitFor("HELLO")
	g.send("sleep 30; echo \"SLE\"\"PT\"\n")
	time.Sleep(500 * time.Millisecond)
	sent := time.Now()
	g.send("\x03")
	g.send("echo \"AFTER\"\"INT\"\n")
	out := g.waitFor("AFTERINT")
	if took := time.Since(sent); took > 5*time.Second {
		t.Errorf("^C took %s to interrupt sleep 30", took)
	}
	if strings.Contains(out, "SLEPT") {
		t.Error("^C did not interrupt the command: the rest of its line ran")
	}
}

// The read starts where asked, caps at the limit, skips to the last limit
// bytes when the file is further ahead, and tells a missing file apart.
func TestTheSessionReadStartsWhereAskedAndSkipsAFlood(t *testing.T) {
	tmp := t.TempDir()
	env := append(os.Environ(), "TMPDIR="+tmp)
	if _, _, code := runSessionRead(t, env, "none", 0, 10); code != sessionReadMissing {
		t.Errorf("a missing file exited %d, want %d", code, sessionReadMissing)
	}
	if err := os.WriteFile(filepath.Join(tmp, "greenroom-session.r"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		off, limit int
		start      int
		data       string
	}{
		{0, 100, 0, "0123456789"},
		{3, 100, 3, "3456789"},
		{0, 4, 6, "6789"}, // more than a window ahead: the last 4
		{8, 4, 8, "89"},
		{10, 4, 10, ""},
	} {
		start, data, code := runSessionRead(t, env, "r", c.off, c.limit)
		if code != 0 || start != c.start || data != c.data {
			t.Errorf("read(%d, %d) = %d %q exit %d, want %d %q", c.off, c.limit, start, data, code, c.start, c.data)
		}
	}
}

// Close ends the command and whatever it left in the background on the
// terminal, even when it ignores HUP, and removes the files. Only a pid that
// is still script is signalled.
func TestClosingASessionEndsItsProcessesAndRemovesItsFiles(t *testing.T) {
	g := startGuestSession(t, `trap "" HUP; sleep 301 & echo "CHILD=$!"; sleep 300`)
	out := g.waitFor("CHILD=")
	child := strings.TrimSpace(strings.SplitN(strings.SplitN(out, "CHILD=", 2)[1], "\n", 2)[0])
	g.close()
	if !g.exited(10 * time.Second) {
		t.Fatal("the session still runs after close")
	}
	if err := exec.Command("kill", "-0", child).Run(); err == nil {
		_ = exec.Command("kill", "-9", child).Run()
		t.Errorf("the background child %s outlived close", child)
	}
	for _, f := range []string{g.file(), g.file() + ".pid"} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s survived close (%v)", filepath.Base(f), err)
		}
	}

	// A pid file naming something other than script is not signalled.
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill() }()
	if err := os.WriteFile(g.file()+".pid", []byte(strconv.Itoa(other.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	g.close()
	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("close signalled pid %d, which was not the session's script: %v", other.Process.Pid, err)
	}
}
