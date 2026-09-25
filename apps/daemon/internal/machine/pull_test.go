package machine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// guestTree writes files (name to body, "->target" for a symlink) under the fake guest home.
func guestTree(t *testing.T, home string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if target, ok := strings.CutPrefix(body, "->"); ok {
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func needRsync(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
}

// Pull runs the host's real rsync against a local shell standing in for the guest, so the
// reversed direction, the quoting and the file-or-directory choice are checked by rsync itself.
func TestPullCopiesADirectoryItsLinksAndAFileOutOfTheGuest(t *testing.T) {
	needRsync(t)
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	home := localSSH(t)
	mgr.sshKey = filepath.Join(t.TempDir(), "id_ed25519")
	proj := "my app $(touch pwned)"
	guestTree(t, home, map[string]string{
		proj + "/a.txt":            "alpha",
		proj + "/sub/b.txt":        "beta",
		proj + "/latest":           "->a.txt",
		proj + "/node_modules/big": "left out",
	})

	dest := filepath.Join(t.TempDir(), "out")
	res, err := mgr.Pull(context.Background(), mc.RunID, "~/"+proj, dest, []string{"node_modules"})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Dest != dest || res.Source != "~/"+proj {
		t.Errorf("result = %+v, want dest %s and the source as given", res, dest)
	}
	for name, want := range map[string]string{"a.txt": "alpha", "sub/b.txt": "beta"} {
		if got, err := os.ReadFile(filepath.Join(dest, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if link, err := os.Readlink(filepath.Join(dest, "latest")); err != nil || link != "a.txt" {
		t.Errorf("latest -> %q, %v; want the link kept as a link", link, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "node_modules")); err == nil {
		t.Error("the excluded node_modules was pulled")
	}
	if _, err := os.Stat(filepath.Join(home, "pwned")); err == nil {
		t.Error("the source was run as shell syntax in the guest")
	}

	// A file lands in dest under its own name; a link to a file arrives as the file.
	fileDest := t.TempDir()
	for _, src := range []string{proj + "/sub/b.txt", filepath.Join(home, proj, "latest")} {
		if _, err := mgr.Pull(context.Background(), mc.RunID, src, fileDest, nil); err != nil {
			t.Fatalf("Pull %s: %v", src, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(fileDest, "b.txt")); err != nil || string(got) != "beta" {
		t.Errorf("b.txt = %q, %v", got, err)
	}
	if st, err := os.Lstat(filepath.Join(fileDest, "latest")); err != nil || !st.Mode().IsRegular() {
		t.Errorf("latest pulled on its own is %v, %v; want the file it points at", st, err)
	}
}

// Without a dest the pull lands in the run directory under its own step number, and the step says so.
func TestPullDefaultsToANumberedDirInTheRunAndRecordsTheStep(t *testing.T) {
	needRsync(t)
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	home := localSSH(t)
	mgr.sshKey = filepath.Join(t.TempDir(), "id_ed25519")
	guestTree(t, home, map[string]string{"report.txt": "passed"})

	res, err := mgr.Pull(context.Background(), mc.RunID, "report.txt", "", nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if want := filepath.Join(mc.Dir, fmt.Sprintf("%03d-pull", res.Step)); res.Dest != want {
		t.Errorf("dest = %q, want %q", res.Dest, want)
	}
	if got, err := os.ReadFile(filepath.Join(res.Dest, "report.txt")); err != nil || string(got) != "passed" {
		t.Errorf("report.txt = %q, %v", got, err)
	}
	step := lastStep(t, mc.Dir)
	if step.Tool != "machine_pull" || step.Seq != res.Step || step.Error != "" {
		t.Errorf("last step = %+v, want machine_pull %d with no error", step, res.Step)
	}
	out, _ := step.Output.(map[string]any)
	if out["dest"] != res.Dest || out["source"] != "report.txt" {
		t.Errorf("step output = %v, want the source and the dest", out)
	}

	again, err := mgr.Pull(context.Background(), mc.RunID, "report.txt", "", nil)
	if err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	if again.Step <= res.Step || again.Dest == res.Dest {
		t.Errorf("a second pull reused %s (step %d)", again.Dest, again.Step)
	}
}

func TestPullRefusesAMissingSourceOrARelativeDest(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	localSSH(t)
	before := stepCount(t, mc.Dir)

	_, err := mgr.Pull(context.Background(), mc.RunID, "~/nothing/here", "", nil)
	if !errors.Is(err, ErrNotInGuest) || !strings.Contains(err.Error(), `source "~/nothing/here" does not exist in the guest`) {
		t.Errorf("missing source: %v", err)
	}
	if _, err := mgr.Pull(context.Background(), mc.RunID, " ", "", nil); err == nil || !strings.Contains(err.Error(), "source is required") {
		t.Errorf("empty source: %v", err)
	}
	if _, err := mgr.Pull(context.Background(), mc.RunID, ".", "out", nil); err == nil || !strings.Contains(err.Error(), "absolute path on the host") {
		t.Errorf("relative dest: %v", err)
	}
	if n := stepCount(t, mc.Dir); n != before {
		t.Errorf("a refused pull recorded %d steps", n-before)
	}
	if entries, _ := filepath.Glob(filepath.Join(mc.Dir, "*-pull")); len(entries) != 0 {
		t.Errorf("a refused pull made %v", entries)
	}
}

// The remote path's archive is made by the guest's tar (here the host's, the same bsdtar).
func TestPullArchiveTarsADirectoryOrAFileAndRecordsTheStep(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	home := localSSH(t)
	guestTree(t, home, map[string]string{
		"proj/a.txt":          "alpha",
		"proj/sub/b.txt":      "beta",
		"proj/node_modules/x": "left out in the guest",
		"proj/build/y":        "left out by the client, not the guest",
		"proj/latest":         "->a.txt",
	})

	var buf bytes.Buffer
	opened := 0
	err := mgr.PullArchive(context.Background(), mc.RunID, "~/proj", []string{"node_modules", "build/"},
		func(step int) io.Writer { opened = step; return &buf })
	if err != nil {
		t.Fatalf("PullArchive: %v", err)
	}
	members := tarMembers(t, buf.Bytes())
	want := []string{"./", "./a.txt", "./build/", "./build/y", "./latest", "./sub/", "./sub/b.txt"}
	if strings.Join(members, " ") != strings.Join(want, " ") {
		t.Errorf("members = %v, want %v", members, want)
	}
	for _, m := range members {
		if strings.Contains(m, "._") {
			t.Errorf("macOS metadata member %q in the archive", m)
		}
	}
	step := lastStep(t, mc.Dir)
	if step.Tool != "machine_pull" || step.Seq != opened || opened == 0 {
		t.Errorf("last step = %+v, want machine_pull %d", step, opened)
	}
	calls, _ := os.ReadFile(filepath.Join(control, "calls.log"))
	if !strings.Contains(string(calls), "--exclude node_modules") || strings.Contains(string(calls), "--exclude build/") {
		t.Errorf("guest tar should get the literal name only:\n%s", calls)
	}

	buf.Reset()
	if err := mgr.PullArchive(context.Background(), mc.RunID, "proj/latest", nil, func(int) io.Writer { return &buf }); err != nil {
		t.Fatalf("PullArchive of a file: %v", err)
	}
	if got := tarMembers(t, buf.Bytes()); strings.Join(got, " ") != "./latest" {
		t.Errorf("members = %v, want the one file", got)
	}

	called := false
	err = mgr.PullArchive(context.Background(), mc.RunID, "/no/such/path", nil, func(int) io.Writer { called = true; return io.Discard })
	if !errors.Is(err, ErrNotInGuest) || called {
		t.Errorf("missing source: err %v, opened %v; want ErrNotInGuest before anything is written", err, called)
	}
}

func tarMembers(t *testing.T, data []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg && hdr.Name == "./latest" {
			if b, _ := io.ReadAll(tr); string(b) != "alpha" {
				t.Errorf("./latest holds %q, want the file it points at", b)
			}
		}
		names = append(names, hdr.Name)
	}
	sort.Strings(names)
	return names
}

func stepCount(t *testing.T, dir string) int { return len(readSteps(t, dir)) }

func lastStep(t *testing.T, dir string) Step {
	t.Helper()
	steps := readSteps(t, dir)
	if len(steps) == 0 {
		t.Fatal("no steps recorded")
	}
	return steps[len(steps)-1]
}
