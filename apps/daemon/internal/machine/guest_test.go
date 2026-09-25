package machine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Sync through the host's real rsync, with ssh replaced by a local shell, so
// the quoting is tested by the programs that parse it: a state root with a
// space and a dest with shell syntax must both arrive intact.
func TestSyncSurvivesSpacesAndShellSyntax(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	bin := t.TempDir()
	argsFile := filepath.Join(bin, "ssh-args")
	// Like ssh: drop the options and host, join the rest, hand it to a shell.
	ssh := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in -i|-o) shift 2 ;; *) break ;; esac; done\n" +
		"shift\ncd \"$HOME\" && exec sh -c \"$*\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	mgr.sshKey = filepath.Join(t.TempDir(), "state root", "id_ed25519")

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "f"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := "my app $(touch pwned)"
	if _, err := mgr.Sync(context.Background(), mc.RunID, source, dest, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(home, dest, "f")); err != nil || string(got) != "hi" {
		t.Errorf("the file did not arrive at %q: %q, %v", dest, got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "pwned")); err == nil {
		t.Error("the dest was run as shell syntax on the guest")
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ssh never ran: %v", err)
	}
	if !strings.Contains(string(args), "-i\n"+mgr.sshKey+"\n") {
		t.Errorf("ssh did not get the key path whole:\n%s", args)
	}
}

// A shell's "~/" is the guest home. rsync over ssh does not expand it inside
// quotes, so before the fix a dest of ~/x made a directory named "~".
func TestSyncReadsATildeAsTheGuestHome(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	home := localSSH(t)
	mgr.sshKey = filepath.Join(t.TempDir(), "id_ed25519")

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "f"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.Sync(context.Background(), mc.RunID, source, "~/x", nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Dest != "x" {
		t.Errorf("dest = %q, want x", res.Dest)
	}
	if got, err := os.ReadFile(filepath.Join(home, "x", "f")); err != nil || string(got) != "hi" {
		t.Errorf("the file did not arrive at $HOME/x: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "~")); err == nil {
		t.Error("the sync made a directory named ~ in the guest home")
	}
}

// localSSH puts a fake ssh first on PATH that runs the remote command in a
// local shell in a fresh HOME, which it returns: rsync's real quoting is then
// parsed by a real shell, as on the guest.
func localSSH(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	ssh := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -i|-o) shift 2 ;; *) break ;; esac; done\n" +
		"shift\ncd \"$HOME\" && exec sh -c \"$*\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// runWrapper runs execWrapper for real in a host shell, with a login zsh that
// reads no dotfiles of this host's user. Every pid the command writes to
// $HOME/pids is killed when the test ends, so no child outlives it. A mktemp
// shim first on PATH records the dir the wrapper makes in $HOME/dirs, so
// wrapperDirsLeft checks that dir alone: other processes on a shared host make
// /tmp/greenroom-exec.* dirs too.
func runWrapper(t *testing.T, command string, timeoutSeconds int) (stdout, stderr string, code int, took time.Duration) {
	stdout, stderr, code, took, _ = runWrapperIn(t, command, timeoutSeconds)
	return stdout, stderr, code, took
}

func runWrapperIn(t *testing.T, command string, timeoutSeconds int) (stdout, stderr string, code int, took time.Duration, home string) {
	t.Helper()
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("no /bin/zsh")
	}
	home = t.TempDir()
	bin := t.TempDir()
	shim := "#!/bin/sh\nd=$(/usr/bin/mktemp \"$@\") || exit\necho \"$d\" >>\"$HOME/dirs\"\necho \"$d\"\n"
	if err := os.WriteFile(filepath.Join(bin, "mktemp"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pids, _ := os.ReadFile(filepath.Join(home, "pids"))
		for _, f := range strings.Fields(string(pids)) {
			if pid, err := strconv.Atoi(f); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	cmd := exec.Command("/bin/sh", "-c", execWrapper, "greenroom-exec", command, strconv.Itoa(timeoutSeconds))
	cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the wrapper did not return")
	}
	return out.String(), errOut.String(), code, time.Since(started), home
}

// wrapperDirsLeft is the temp dirs the wrapper run with this home made and
// left in place. It fails if the wrapper made none, so a shim that stopped
// being called cannot pass.
func wrapperDirsLeft(t *testing.T, home string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "dirs"))
	if err != nil {
		t.Fatalf("the wrapper made no temp dir through mktemp: %v", err)
	}
	var left []string
	for _, d := range strings.Fields(string(data)) {
		if _, err := os.Stat(d); err == nil {
			left = append(left, d)
		}
	}
	return left
}

// tart exec returns only when every holder of the guest's stdout and stderr
// has closed them. The wrapper gives a command files instead, so a child the
// command leaves running cannot hold the call open, and keeps running.
func TestExecWrapperReturnsWhileABackgroundChildRuns(t *testing.T) {
	stdout, stderr, code, took, home := runWrapperIn(t,
		`echo out; echo err >&2; sleep 20 & echo $! >> "$HOME/pids"; (cd / && exec sleep 20) & echo $! >> "$HOME/pids"; exit 3`, 600)
	if code != 3 || took > 5*time.Second {
		t.Errorf("exit %d after %s, want exit 3 in the shell's own time", code, took)
	}
	if stdout != "out\n" || stderr != "err\n" {
		t.Errorf("stdout %q, stderr %q, want out and err and no job notices", stdout, stderr)
	}
	if left := wrapperDirsLeft(t, home); len(left) > 0 {
		t.Errorf("temp dirs left behind: %v", left)
	}
}

// Issue #28: at the timeout the guest kills the command and its children, and the
// output so far comes back with exit 124 and a note.
func TestExecWrapperTimeoutKillsTheTreeAndKeepsTheOutput(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	stdout, stderr, code, took, home := runWrapperIn(t,
		`echo before; (sleep 4; touch `+marker+`) & echo $! >> "$HOME/pids"; for i in 1 2 3 4 5 6; do echo tick $i; sleep 1; done; touch `+marker, 2)
	if code != execTimedOutExit || took > 5*time.Second {
		t.Errorf("exit %d after %s, want %d at about 2 s", code, took, execTimedOutExit)
	}
	if !strings.HasPrefix(stdout, "before\ntick 1\n") {
		t.Errorf("stdout %q, want what the command printed before the timeout", stdout)
	}
	if !strings.Contains(stderr, execTimedOutNote+"2 s") {
		t.Errorf("stderr %q, want the timeout note", stderr)
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command or its background child kept running past the timeout")
	}
	if left := wrapperDirsLeft(t, home); len(left) > 0 {
		t.Errorf("temp dirs left behind: %v", left)
	}
}

// A command that ignores TERM is killed 5 s later.
func TestExecWrapperTimeoutKillsACommandThatIgnoresTerm(t *testing.T) {
	_, stderr, code, took := runWrapper(t, `trap "" TERM; echo $$ >> "$HOME/pids"; while :; do sleep 1; done`, 1)
	if code != execTimedOutExit || took > 10*time.Second || !strings.Contains(stderr, execTimedOutNote) {
		t.Errorf("exit %d after %s, stderr %q; want 124 within about 6 s", code, took, stderr)
	}
}

// Issue #40: `log` is /usr/bin/log, as in Terminal. zsh has a log builtin that
// macOS disables in /etc/zshrc, which a non-interactive zsh never reads.
func TestExecWrapperRunsLogAsTheCommandNotTheZshBuiltin(t *testing.T) {
	stdout, stderr, code, _ := runWrapper(t, `whence -w log`, 0)
	if strings.Contains(stdout, "builtin") {
		t.Errorf("whence -w log = %q (stderr %q, exit %d), want the command, not zsh's builtin", stdout, stderr, code)
	}
}

func TestExecRunsTheCommandThroughTheWrapper(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, err := mgr.Exec(context.Background(), mc.RunID, "./App &", "", 10*time.Second); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	log := testsupport.Calls(t, control)
	if !strings.Contains(log, `/bin/zsh -lc "disable log 2>/dev/null; $1" >"$d/out" 2>"$d/err" </dev/null 3>&- &`) || !strings.Contains(log, "greenroom-exec ./App & 10") {
		t.Errorf("the command did not run behind the wrapper\ncalls:\n%s", log)
	}
}

// A cwd is entered as a shell would: ~ is the guest home, anything else is
// one quoted path. Run for real against a throwaway HOME.
func TestCdCommandReadsATildeAsTheHome(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"work/my app", "~"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	for cwd, want := range map[string]string{
		"~":              real,
		"~/":             real,
		"~/work/my app":  filepath.Join(real, "work/my app"),
		"work/my app":    filepath.Join(real, "work/my app"),
		"/":              "/",
		"$(touch pwned)": "", // refused by cd, never run
	} {
		cmd := exec.Command("/bin/sh", "-c", "cd \"$HOME\" && "+cdCommand(cwd)+" && pwd -P")
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, _ := cmd.Output()
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("cwd %q: pwd = %q, want %q (command %s)", cwd, got, want, cdCommand(cwd))
		}
	}
	if _, err := os.Stat(filepath.Join(home, "pwned")); err == nil {
		t.Error("a cwd was run as shell syntax")
	}
	if cdCommand("") != "" {
		t.Errorf("no cwd must mean no cd, got %q", cdCommand(""))
	}
}
