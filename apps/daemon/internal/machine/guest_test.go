package machine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// tart exec returns only when every holder of the guest's stdout and stderr
// has closed them. The wrapper gives a command files instead, so a child the
// command leaves running cannot hold the call open. Run for real, in a host
// shell, with a login zsh that reads no dotfiles of this host's user.
func TestExecWrapperReturnsWhileABackgroundChildRuns(t *testing.T) {
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("no /bin/zsh")
	}
	home := t.TempDir()
	cmd := exec.Command("/bin/sh", "-c", execWrapper, "greenroom-exec",
		"echo out; echo err >&2; sleep 20 & (cd / && sleep 20) & exit 3")
	cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Errorf("err = %v, want exit status 3", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the wrapper waited for the command's background children")
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("took %s, want the shell's own time", took)
	}
	if stdout.String() != "out\n" || stderr.String() != "err\n" {
		t.Errorf("stdout %q, stderr %q, want out and err", stdout.String(), stderr.String())
	}
}

func TestExecRunsTheCommandThroughTheWrapper(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, err := mgr.Exec(context.Background(), mc.RunID, "./App &", "", 10*time.Second); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	log := testsupport.Calls(t, control)
	if !strings.Contains(log, `/bin/zsh -lc "$1" >"$d/out" 2>"$d/err" </dev/null`) || !strings.Contains(log, "greenroom-exec ./App &") {
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
