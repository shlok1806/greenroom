package machine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	if _, err := mgr.Sync(context.Background(), mc.RunID, source, SyncOptions{Dest: dest}); err != nil {
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
	res, err := mgr.Sync(context.Background(), mc.RunID, source, SyncOptions{Dest: "~/x"})
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

// strayTree is issue #188's reused machine: $HOME/work/app holds an earlier branch's tree
// (stray.go, a whole old/ package, a link out of dest, a node_modules the guest built),
// next to files outside dest that nothing may touch. It returns the new branch's source.
func strayTree(t *testing.T, home string) string {
	t.Helper()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(home, "work", "app")
	write(filepath.Join(dest, "main.go"), "old main")
	write(filepath.Join(dest, "stray.go"), "package app")
	write(filepath.Join(dest, "old", "gone.go"), "package old")
	write(filepath.Join(dest, "node_modules", "dep", "index.js"), "built in the guest")
	write(filepath.Join(home, "work", "other", "keep.go"), "another project")
	write(filepath.Join(home, "outside", "precious"), "not the project")
	write(filepath.Join(home, "work", "app-sibling"), "a prefix of dest, not inside it")
	if err := os.Symlink(filepath.Join(home, "outside"), filepath.Join(dest, "outlink")); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	write(filepath.Join(source, "main.go"), "the new branch main")
	write(filepath.Join(source, "sub", "b.go"), "package sub")
	return source
}

// untouched fails unless every file outside dest that strayTree wrote is still there.
func untouched(t *testing.T, home string) {
	t.Helper()
	for _, p := range []string{"work/other/keep.go", "outside/precious", "work/app-sibling"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("%s outside dest was touched: %v", p, err)
		}
	}
}

// Issue #188: mirror deletes what an earlier sync left in dest, keeps excluded paths and
// never reaches outside dest, not even through a symlink in it.
func TestSyncMirrorDeletesStraysInsideDestOnly(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	home := localSSH(t)
	mgr.sshKey = filepath.Join(t.TempDir(), "id_ed25519")
	source := strayTree(t, home)

	res, err := mgr.Sync(context.Background(), mc.RunID, source, SyncOptions{Dest: "work/app", Exclude: []string{"node_modules"}, Mirror: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dest := filepath.Join(home, "work", "app")
	for _, p := range []string{"stray.go", "old", "outlink"} {
		if _, err := os.Lstat(filepath.Join(dest, p)); err == nil {
			t.Errorf("mirror left the stray %s", p)
		}
	}
	for p, want := range map[string]string{"main.go": "the new branch main", "sub/b.go": "package sub", "node_modules/dep/index.js": "built in the guest"} {
		if got, err := os.ReadFile(filepath.Join(dest, p)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", p, got, err, want)
		}
	}
	untouched(t, home)
	if !res.Mirror || res.Strays == nil || *res.Strays != 4 {
		t.Fatalf("result = %+v, want mirror and 4 strays (stray.go, old/, old/gone.go, outlink)", res)
	}
	got := strings.Join(slices.Sorted(slices.Values(res.StrayPaths)), " ")
	if got != "old/ old/gone.go outlink stray.go" {
		t.Errorf("strayPaths = %q", got)
	}
	if !strings.Contains(res.Summary, "mirror deleted 4 paths") {
		t.Errorf("summary = %q, want the deletions", res.Summary)
	}

	// Mirrored again, nothing is left to delete.
	res, err = mgr.Sync(context.Background(), mc.RunID, source, SyncOptions{Dest: "work/app", Exclude: []string{"node_modules"}, Mirror: true})
	if err != nil || res.Strays == nil || *res.Strays != 0 || len(res.StrayPaths) != 0 {
		t.Errorf("second mirror = %+v, %v; want no strays", res, err)
	}
}

// Without mirror nothing is deleted, but the result counts and names the strays, leaving out
// excluded paths, so the agent knows what else its build will see.
func TestSyncWithoutMirrorCountsStraysAndDeletesNothing(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	home := localSSH(t)
	mgr.sshKey = filepath.Join(t.TempDir(), "id_ed25519")
	source := strayTree(t, home)

	res, err := mgr.Sync(context.Background(), mc.RunID, source, SyncOptions{Dest: "work/app", Exclude: []string{"node_modules"}})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dest := filepath.Join(home, "work", "app")
	for _, p := range []string{"stray.go", "old/gone.go", "outlink", "node_modules/dep/index.js"} {
		if _, err := os.Lstat(filepath.Join(dest, p)); err != nil {
			t.Errorf("a sync without mirror deleted %s: %v", p, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(dest, "main.go")); err != nil || string(got) != "the new branch main" {
		t.Errorf("main.go = %q, %v; want the new content", got, err)
	}
	untouched(t, home)
	if res.Mirror || res.Strays == nil || *res.Strays != 4 {
		t.Fatalf("result = %+v, want 4 strays and no mirror", res)
	}
	if got := strings.Join(slices.Sorted(slices.Values(res.StrayPaths)), " "); got != "old/ old/gone.go outlink stray.go" {
		t.Errorf("strayPaths = %q, want no node_modules path: it is excluded", got)
	}
	if !strings.Contains(res.Summary, "strays in dest: 4") || !strings.Contains(res.Summary, "mirror true") {
		t.Errorf("summary = %q, want the count and how to delete them", res.Summary)
	}

	// Without the exclude, the guest's node_modules is a stray too.
	res, err = mgr.Sync(context.Background(), mc.RunID, source, SyncOptions{Dest: "work/app"})
	if err != nil || res.Strays == nil || *res.Strays != 7 {
		t.Errorf("unexcluded = %+v, %v; want 7 strays (node_modules/, dep/ and index.js added)", res, err)
	}
}

// StrayPaths is capped; Strays is the full count.
func TestSyncCapsStrayPaths(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	home := localSSH(t)
	mgr.sshKey = filepath.Join(t.TempDir(), "id_ed25519")
	dest := filepath.Join(home, "work", "app")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range maxStrayPaths + 5 {
		if err := os.WriteFile(filepath.Join(dest, "f"+strconv.Itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := mgr.Sync(context.Background(), mc.RunID, t.TempDir(), SyncOptions{Dest: "work/app"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Strays == nil || *res.Strays != maxStrayPaths+5 || len(res.StrayPaths) != maxStrayPaths {
		t.Errorf("strays = %v, %d paths; want %d and %d", res.Strays, len(res.StrayPaths), maxStrayPaths+5, maxStrayPaths)
	}
}

// rsync's receiver follows a symlinked destination, so a mirror into one would empty its
// target: the guest's guard refuses it before rsync runs.
func TestSyncMirrorRefusesADestThroughASymlink(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	for name, link := range map[string]string{"dest itself": "work/app", "a parent": "work"} {
		t.Run(name, func(t *testing.T) {
			mgr, _, _ := newTestManager(t)
			mc := readyMachine(t, mgr)
			home := localSSH(t)
			mgr.sshKey = filepath.Join(t.TempDir(), "id_ed25519")
			target := filepath.Join(home, "outside")
			if err := os.MkdirAll(filepath.Join(target, "app"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{"precious", "app/precious"} {
				if err := os.WriteFile(filepath.Join(target, p), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(home, link)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(home, link)); err != nil {
				t.Fatal(err)
			}
			_, err := mgr.Sync(context.Background(), mc.RunID, t.TempDir(), SyncOptions{Dest: "work/app", Mirror: true})
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("Sync = %v, want a refusal naming the symlink", err)
			}
			for _, p := range []string{"precious", "app/precious"} {
				if _, err := os.Stat(filepath.Join(target, p)); err != nil {
					t.Errorf("the symlink's target lost %s: %v", p, err)
				}
			}
		})
	}
}

// A mirror refuses a dest it must not empty before anything reaches the guest.
func TestSyncMirrorRefusesADangerousDest(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	bin := t.TempDir()
	ran := filepath.Join(bin, "ran")
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte("#!/bin/sh\ntouch '"+ran+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, dest := range []string{"work", "myapp", "~/work", "~", "Library/Preferences", ".ssh/keys", "~/.config/app", "work/..", "/tmp/app"} {
		if _, err := mgr.Sync(context.Background(), mc.RunID, t.TempDir(), SyncOptions{Dest: dest, Mirror: true}); err == nil {
			t.Errorf("mirror into %q was not refused", dest)
		}
		if err := CheckMirrorDest(dest); err == nil {
			t.Errorf("CheckMirrorDest(%q) = nil, want a refusal", dest)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("rsync ran for a refused mirror")
	}
	for _, dest := range []string{"", "work/app", "~/work/app", "projects/a/b"} {
		if err := CheckMirrorDest(dest); err != nil {
			t.Errorf("CheckMirrorDest(%q) = %v, want nil", dest, err)
		}
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

// runWrapper runs execShell with execScript on stdin for real in a host shell, with a login zsh that
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
	cmd := exec.Command(execShell[0], append(slices.Clone(execShell[1:]), strconv.Itoa(timeoutSeconds))...)
	cmd.Stdin = strings.NewReader(execScript(command))
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

// Issue #128: the command and the wrapper go on stdin, so the guest's argv is
// the short execShell line and nothing of the command.
func TestExecRunsTheCommandThroughTheWrapper(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, err := mgr.Exec(context.Background(), mc.RunID, "./App & xyz-token", "~/work", 10*time.Second); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	var execs []string
	for _, line := range strings.Split(testsupport.Calls(t, control), "\n") {
		if strings.Contains(line, "greenroom-exec") {
			execs = append(execs, line)
		}
	}
	if want := "exec -i " + mc.Name + " /bin/sh -s greenroom-exec 10"; len(execs) != 1 || execs[0] != want {
		t.Errorf("machine_exec ran %q, want exactly %q", execs, want)
	}
	if calls := testsupport.Calls(t, control); strings.Contains(calls, "xyz-token") || strings.Contains(calls, "disable log") {
		t.Errorf("the command or the wrapper reached tart's argv\ncalls:\n%s", calls)
	}
	stdin := testsupport.ExecStdin(t, control)
	if !strings.Contains(stdin, "\ncd \"$HOME\"/'work' && ./App & xyz-token\n") || !strings.Contains(stdin, execWrapperTail) {
		t.Errorf("stdin did not carry the wrapper and the command\nstdin:\n%s", stdin)
	}
}

// Issue #128: no process the wrapper starts carries the command, so a pgrep
// for a pattern in the command no longer finds the wrapper itself.
func TestExecWrapperKeepsTheCommandOutOfEveryArgv(t *testing.T) {
	// Any match is printed, so a failure names the process that carried the pattern.
	stdout, stderr, code, _ := runWrapper(t, `for p in $(pgrep -f greenroom-unique-token-xyz); do ps -o pid=,args= -p $p; done
pgrep -f greenroom-unique-token-xyz >/dev/null; echo "pgrep $?"
ps -o args= -p $$ -p $PPID`, 0)
	listing, found := strings.CutPrefix(stdout, "pgrep 1\n")
	if code != 0 || !found {
		t.Fatalf("stdout %q, stderr %q, exit %d; want pgrep to find nothing (exit 1)", stdout, stderr, code)
	}
	// The listing is the wrapper's sh and the login zsh, both short.
	lines := strings.Split(strings.TrimSpace(listing), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "/bin/sh -s greenroom-exec 0") || !strings.HasPrefix(lines[1], "/bin/zsh -lc ") {
		t.Errorf("ps lines %q, want the wrapper's sh and zsh", lines)
	}
	for _, line := range lines {
		if len(line) > 100 || strings.Contains(line, "pgrep") {
			t.Errorf("process line %q is long or carries the command", line)
		}
	}
}

// The command reaches zsh byte for byte: quotes, dollars, backslashes and a
// line that looks like a heredoc end are not the shell's to read.
func TestExecWrapperPassesTheCommandVerbatim(t *testing.T) {
	stdout, stderr, code, _ := runWrapper(t, "printf '%s|' 'a $b \\c' \"q'uote\"\ncat <<'EOF'\nGREENROOM_CMD_X\nEOF\necho \"args $#\"", 0)
	if want := "a $b \\c|q'uote|GREENROOM_CMD_X\nargs 0\n"; stdout != want || code != 0 {
		t.Errorf("stdout %q (stderr %q, exit %d), want %q", stdout, stderr, code, want)
	}
}

// zsh still parses the whole command before running any of it, as `zsh -c`
// did, and its errors keep the command's own line numbers (issue #40).
func TestExecWrapperKeepsZshsParseAndLineNumbers(t *testing.T) {
	_, stderr, code, _ := runWrapper(t, "true\nnosuchcmd-greenroom", 0)
	if code != 127 || !strings.Contains(stderr, ":2: command not found: nosuchcmd-greenroom") {
		t.Errorf("stderr %q, exit %d; want line 2's command not found and exit 127", stderr, code)
	}
	stdout, stderr, code, _ := runWrapper(t, "echo ran\n(( 1 +", 0)
	if stdout != "" || code == 0 || !strings.Contains(stderr, ":2: parse error") {
		t.Errorf("stdout %q, stderr %q, exit %d; want a parse error on line 2 and nothing run", stdout, stderr, code)
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
