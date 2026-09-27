package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/update.sh at the repo root (root ADR 0033) runs here, as base_test.go runs the guest
// scripts: the real script, copied into a scratch repository whose origin is a bare repository
// beside it, with fake install scripts at the real paths. The copy resolves its repository from
// its own location, so nothing here can reach the real checkout, /Applications or launchd.

// updateFixture is a checkout on main, its origin, and a second clone that pushes to it.
type updateFixture struct {
	t        *testing.T
	work     string // the checkout update.sh runs in
	upstream string // another clone, standing in for everyone else pushing to main
	markers  string // what the fake installs record
}

// fakeInstall records that it ran, in order, and fails when its fail file exists.
const fakeInstall = `#!/bin/sh
echo "fake %[1]s install ran"
echo %[1]s >>"$GREENROOM_TEST_MARKERS/order"
if [ -f "$GREENROOM_TEST_MARKERS/fail-%[1]s" ]; then
  echo "%[1]s build broke" >&2
  exit 1
fi
`

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	f := &updateFixture{t: t, work: filepath.Join(root, "work"), upstream: filepath.Join(root, "upstream"), markers: filepath.Join(root, "markers")}
	if err := os.Mkdir(f.markers, 0o755); err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(root, "origin.git")
	f.git(root, "init", "--quiet", "--bare", "-b", "main", origin)
	f.git(root, "clone", "--quiet", origin, f.upstream)
	f.git(f.upstream, "checkout", "--quiet", "-b", "main")
	f.write(f.upstream, "scripts/update.sh", string(script), 0o755)
	f.write(f.upstream, "apps/daemon/scripts/install.sh", strings.ReplaceAll(fakeInstall, "%[1]s", "daemon"), 0o755)
	f.write(f.upstream, "apps/companion/scripts/install.sh", strings.ReplaceAll(fakeInstall, "%[1]s", "companion"), 0o755)
	f.write(f.upstream, "README.md", "greenroom\n", 0o644)
	f.git(f.upstream, "add", ".")
	f.git(f.upstream, "commit", "--quiet", "-m", "first")
	f.git(f.upstream, "push", "--quiet", "origin", "main")
	f.git(root, "clone", "--quiet", origin, f.work)
	return f
}

// gitEnv keeps the person's git configuration (signing, hooks, templates) out of the scratch
// repositories.
func gitEnv(extra ...string) []string {
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	return append(env, extra...)
}

func (f *updateFixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *updateFixture) write(dir, name, body string, mode os.FileMode) {
	f.t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		f.t.Fatal(err)
	}
}

// push lands a commit on origin/main that the checkout does not have yet.
func (f *updateFixture) push(subject, name, body string) {
	f.t.Helper()
	f.write(f.upstream, name, body, 0o644)
	f.git(f.upstream, "add", ".")
	f.git(f.upstream, "commit", "--quiet", "-m", subject)
	f.git(f.upstream, "push", "--quiet", "origin", "main")
}

// run runs the checkout's own copy of update.sh and returns its output and exit status.
func (f *updateFixture) run(args ...string) (string, int) {
	f.t.Helper()
	cmd := exec.Command(filepath.Join(f.work, "scripts", "update.sh"), args...)
	cmd.Dir = f.t.TempDir()
	cmd.Env = gitEnv("GREENROOM_TEST_MARKERS=" + f.markers)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	}
	f.t.Fatalf("update.sh: %v\n%s", err, out)
	return "", -1
}

func (f *updateFixture) head() string { return f.git(f.work, "rev-parse", "HEAD") }

func (f *updateFixture) installs() string {
	b, err := os.ReadFile(filepath.Join(f.markers, "order"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), ",")
}

func TestUpdateCheckSaysUpToDate(t *testing.T) {
	f := newUpdateFixture(t)
	out, code := f.run("--check")
	main := f.git(f.work, "rev-parse", "--short", "origin/main")
	if code != 0 || out != "main "+main+"\nahead 0\n" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := f.installs(); got != "" {
		t.Fatalf("--check ran installs: %s", got)
	}
}

func TestUpdateCheckListsTheNewCommitsAndChangesNothing(t *testing.T) {
	f := newUpdateFixture(t)
	f.push("verifier: retry unreadable descriptions", "a.txt", "a\n")
	f.push("companion: the builds section", "b.txt", "b\n")
	before := f.head()
	out, code := f.run("--check")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || lines[0] != "main "+f.git(f.upstream, "rev-parse", "--short", "HEAD") || lines[1] != "ahead 2" ||
		!strings.HasPrefix(lines[2], "commit ") || !strings.HasSuffix(lines[2], " companion: the builds section") ||
		!strings.HasSuffix(lines[3], " verifier: retry unreadable descriptions") {
		t.Fatalf("output:\n%s", out)
	}
	if f.head() != before || f.installs() != "" {
		t.Fatalf("--check moved HEAD or installed (%s)", f.installs())
	}
}

func TestUpdateCheckSaysWhyAnUpdateWouldRefuse(t *testing.T) {
	f := newUpdateFixture(t)
	f.push("next", "a.txt", "a\n")
	f.write(f.work, "notes.txt", "mine\n", 0o644)
	out, code := f.run("--check")
	if code != 0 || !strings.Contains(out, "ahead 1\n") || !strings.Contains(out, "refused: the checkout at ") ||
		!strings.Contains(out, "has local changes") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// Every refusal leaves the checkout where it was and installs nothing.
func TestUpdateRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *updateFixture)
		want  string
	}{
		{"a changed file", func(f *updateFixture) { f.write(f.work, "README.md", "edited\n", 0o644) }, "has local changes"},
		{"an untracked file", func(f *updateFixture) { f.write(f.work, "scratch.txt", "x\n", 0o644) }, "has local changes"},
		{"a staged file", func(f *updateFixture) {
			f.write(f.work, "staged.txt", "x\n", 0o644)
			f.git(f.work, "add", "staged.txt")
		}, "has local changes"},
		{"another branch", func(f *updateFixture) { f.git(f.work, "checkout", "--quiet", "-b", "feature") }, "is not on main"},
		{"a detached head", func(f *updateFixture) { f.git(f.work, "checkout", "--quiet", "--detach") }, "is not on main"},
		{"a main that diverged", func(f *updateFixture) {
			f.write(f.work, "local.txt", "x\n", 0o644)
			f.git(f.work, "add", ".")
			f.git(f.work, "commit", "--quiet", "-m", "local only")
		}, "main has commits origin/main does not; it cannot fast-forward"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpdateFixture(t)
			f.push("next", "a.txt", "a\n")
			tc.setup(f)
			before := f.head()
			out, code := f.run()
			if code != 2 || !strings.Contains(out, "refused: ") || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want 2 and %q:\n%s", code, tc.want, out)
			}
			if f.head() != before {
				t.Fatal("a refusal moved HEAD")
			}
			if got := f.installs(); got != "" {
				t.Fatalf("a refusal ran installs: %s", got)
			}
		})
	}
}

// The fast-forward rewrites update.sh itself; the run that started finishes as it began.
func TestUpdateFastForwardsThenInstallsTheDaemonThenTheCompanion(t *testing.T) {
	f := newUpdateFixture(t)
	f.push("a change", "a.txt", "a\n")
	script, err := os.ReadFile(filepath.Join(f.upstream, "scripts", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	f.push("update.sh changes too", "scripts/update.sh", "#!/usr/bin/env bash\n"+strings.Repeat("# padding that moves every line of the old script\n", 200)+string(script))
	out, code := f.run()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if f.head() != f.git(f.upstream, "rev-parse", "HEAD") {
		t.Fatalf("HEAD is not origin/main:\n%s", out)
	}
	if got := f.installs(); got != "daemon,companion" {
		t.Fatalf("installs %q, want the daemon then the Companion:\n%s", got, out)
	}
	var steps []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "step: ") {
			steps = append(steps, strings.TrimPrefix(line, "step: "))
		}
	}
	want := []string{"check the checkout", "fetch origin", "fast-forward main (2 new commits)", "install the daemon", "install the Companion"}
	if strings.Join(steps, "|") != strings.Join(want, "|") {
		t.Fatalf("steps %q, want %q:\n%s", steps, want, out)
	}
	if !strings.Contains(out, "fake daemon install ran\n") || !strings.HasSuffix(out, "done: main is at "+f.git(f.work, "rev-parse", "--short", "HEAD")+"\n") {
		t.Fatalf("output:\n%s", out)
	}
}

// Already on origin/main, an update still reinstalls: a checkout can be current while the
// installed builds are not (the stale daemon of 2026-09-26).
func TestUpdateReinstallsWhenAlreadyCurrent(t *testing.T) {
	f := newUpdateFixture(t)
	out, code := f.run()
	if code != 0 || !strings.Contains(out, "step: fast-forward main (0 new commits)\n") || f.installs() != "daemon,companion" {
		t.Fatalf("exit %d, installs %q:\n%s", code, f.installs(), out)
	}
}

func TestUpdateStopsAtTheFailingStep(t *testing.T) {
	for _, tc := range []struct {
		fail, step, installs string
	}{
		{"daemon", "install the daemon", "daemon"},
		{"companion", "install the Companion", "daemon,companion"},
	} {
		t.Run(tc.fail, func(t *testing.T) {
			f := newUpdateFixture(t)
			f.push("next", "a.txt", "a\n")
			f.write(f.markers, "fail-"+tc.fail, "", 0o644)
			out, code := f.run()
			if code != 1 || !strings.HasSuffix(out, "failed: "+tc.step+"\n") || !strings.Contains(out, tc.fail+" build broke") {
				t.Fatalf("exit %d, want 1 and %q:\n%s", code, tc.step, out)
			}
			if got := f.installs(); got != tc.installs {
				t.Fatalf("installs %q, want %q", got, tc.installs)
			}
		})
	}
}

func TestUpdateFailsWhenTheFetchFails(t *testing.T) {
	for _, args := range [][]string{nil, {"--check"}} {
		f := newUpdateFixture(t)
		f.git(f.work, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
		before := f.head()
		out, code := f.run(args...)
		if code != 1 || !strings.HasSuffix(out, "failed: fetch origin\n") || f.head() != before || f.installs() != "" {
			t.Fatalf("%v: exit %d, installs %q:\n%s", args, code, f.installs(), out)
		}
	}
}

func TestUpdateRefusesAnUnknownArgument(t *testing.T) {
	f := newUpdateFixture(t)
	out, code := f.run("--force")
	if code != 64 || !strings.Contains(out, "usage: scripts/update.sh [--check]") || f.installs() != "" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}
