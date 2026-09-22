package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

const testImage = "ghcr.io/example/base:latest"

// sshAnswers stands in for a guest that accepts ssh.
func sshAnswers(context.Context, string, string) error { return nil }

func newTestManager(t *testing.T, extra ...Option) (*Manager, string, string) {
	t.Helper()
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	// Frames are off unless a test passes its own WithFrameInterval, which wins.
	opts := append([]Option{WithTartBin(bin), WithReadyTimeout(10 * time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0)}, extra...)
	mgr, err := NewManager(root, slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	settleOnCleanup(t, mgr)
	return mgr, root, control
}

// settleOnCleanup waits for every boot to finish, then destroys the machines,
// so nothing writes into the run directories while t.TempDir removes them.
func settleOnCleanup(t *testing.T, mgr *Manager) {
	t.Helper()
	var mu sync.Mutex
	booting := map[string]bool{}
	mgr.Listen(func(ev LifecycleEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch ev.Kind {
		case "created":
			booting[ev.RunID] = true
		case "ready", "failed", "destroyed":
			delete(booting, ev.RunID)
		}
	})
	t.Cleanup(func() {
		deadline := time.Now().Add(30 * time.Second)
		for {
			mu.Lock()
			left := len(booting)
			mu.Unlock()
			if left == 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		for _, mc := range mgr.List() {
			_ = mgr.Destroy(context.Background(), mc.RunID)
		}
	})
}

// readyMachine creates a machine and waits for it, which the fake tart
// answers in well under a second.
func readyMachine(t *testing.T, mgr *Manager) *Machine {
	t.Helper()
	ctx := context.Background()
	mc, err := mgr.Create(ctx, testImage, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := mgr.Wait(ctx, mc.RunID, 20*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status != Ready {
		t.Fatalf("machine is %s, want ready (error %q)", got.Status, got.Error)
	}
	return got
}

func pngBase64(t *testing.T) string { return shotBase64(t, 4, 3) }

func TestNewManagerPreparesTheRoot(t *testing.T) {
	mgr, root, _ := newTestManager(t)
	if _, err := os.Stat(filepath.Join(root, "runs")); err != nil {
		t.Errorf("runs directory missing: %v", err)
	}
	for _, name := range []string{"id_ed25519", "id_ed25519.pub"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	if len(mgr.List()) != 0 {
		t.Errorf("a fresh manager has %d machines, want 0", len(mgr.List()))
	}
}

func TestNewManagerKeepsAnExistingKey(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewManager(root, log, WithTartBin(bin), WithSSHProbe(sshAnswers)); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(root, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(root, log, WithTartBin(bin), WithSSHProbe(sshAnswers)); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(root, "id_ed25519"))
	if !bytes.Equal(first, second) {
		t.Error("the second manager replaced the ssh key")
	}
}

func TestCreateAndWaitReachReady(t *testing.T) {
	mgr, root, control := newTestManager(t)
	mc := readyMachine(t, mgr)

	if mc.IP != "192.168.64.9" {
		t.Errorf("ip = %q, want 192.168.64.9", mc.IP)
	}
	// Rounded to 0.1 s, a fake boot is 0; TestMachineIsNotReadyUntilSSHAnswers covers a real wait.
	if mc.BootSeconds < 0 {
		t.Errorf("bootSeconds = %v, want zero or more", mc.BootSeconds)
	}
	if mc.Name != "greenroom-"+mc.RunID {
		t.Errorf("name = %q, want greenroom-%s", mc.Name, mc.RunID)
	}
	if mc.Image != testImage {
		t.Errorf("image = %q, want %q", mc.Image, testImage)
	}

	log := testsupport.Calls(t, control)
	for _, want := range []string{"clone " + testImage, "run greenroom-", "ip ", "exec greenroom-"} {
		if !strings.Contains(log, want) {
			t.Errorf("the daemon never called tart %q\ncalls:\n%s", want, log)
		}
	}
	if !strings.Contains(log, "authorized_keys") {
		t.Errorf("the ssh key was never installed\ncalls:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", mc.RunID, "manifest.json")); err != nil {
		t.Errorf("run directory has no manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "state.json")); err != nil {
		t.Errorf("state.json was not written: %v", err)
	}
	if got := mgr.List(); len(got) != 1 {
		t.Errorf("List returned %d machines, want 1", len(got))
	}
}

func TestCreateFailsWhenCloneFails(t *testing.T) {
	mgr, root, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-clone")

	_, err := mgr.Create(context.Background(), testImage, false)
	if err == nil {
		t.Fatal("Create returned no error although the clone failed")
	}
	if !strings.Contains(err.Error(), "image not found") {
		t.Errorf("the error does not carry tart's message: %v", err)
	}
	if got := mgr.List(); len(got) != 0 {
		t.Errorf("a failed create left %d machines behind", len(got))
	}
	entries, _ := os.ReadDir(filepath.Join(root, "runs"))
	if len(entries) != 1 {
		t.Fatalf("a failed create wrote %d run directories, want 1", len(entries))
	}
	steps := readSteps(t, filepath.Join(root, "runs", entries[0].Name()))
	if len(steps) != 1 || steps[0].Error == "" {
		t.Errorf("the failed create was not recorded: %+v", steps)
	}
	if man := readManifest(t, filepath.Join(root, "runs", entries[0].Name())); man.DestroyedAt == nil {
		t.Error("a failed clone left the run with no end")
	}
}

func TestWaitRejectsAnUnknownRun(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	if _, err := mgr.Wait(context.Background(), "nope", time.Second); err == nil {
		t.Fatal("Wait accepted an unknown runId")
	}
}

func TestGuestToolsRejectAnUnknownRun(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := mgr.Exec(ctx, "nope", "echo hi", "", time.Second); err == nil {
		t.Error("Exec accepted an unknown runId")
	}
	if _, _, err := mgr.Screenshot(ctx, "nope"); err == nil {
		t.Error("Screenshot accepted an unknown runId")
	}
	if _, err := mgr.Sync(ctx, "nope", t.TempDir(), "", nil); err == nil {
		t.Error("Sync accepted an unknown runId")
	}
	if err := mgr.Destroy(ctx, "nope"); err == nil {
		t.Error("Destroy accepted an unknown runId")
	}
}

func TestExecPassesTheExitCodeThrough(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "exec-exit-7")

	res, err := mgr.Exec(context.Background(), mc.RunID, "exit 7", "", 10*time.Second)
	if err != nil {
		t.Fatalf("a non-zero guest exit must not be an error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("exitCode = %d, want 7", res.ExitCode)
	}
	if res.Stdout == "" || res.Stderr == "" {
		t.Errorf("stdout and stderr were not captured: %+v", res)
	}
	if res.Seconds <= 0 {
		t.Errorf("seconds = %v, want a positive number", res.Seconds)
	}
}

func TestExecQuotesTheWorkingDirectory(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)

	if _, err := mgr.Exec(context.Background(), mc.RunID, "pwd", "work/my app", 10*time.Second); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	log := testsupport.Calls(t, control)
	if !strings.Contains(log, `cd 'work/my app'`) {
		t.Errorf("the working directory was not quoted\ncalls:\n%s", log)
	}
}

func TestExecReportsATartFailureAsAnError(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "fail-exec")

	if _, err := mgr.Exec(context.Background(), mc.RunID, "echo hi", "", 10*time.Second); err == nil {
		t.Fatal("a tart failure must be an error, not an exit code")
	}
}

func TestSyncRejectsBadSources(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"missing directory": filepath.Join(t.TempDir(), "nope"),
		"a regular file":    file,
	} {
		if _, err := mgr.Sync(context.Background(), mc.RunID, source, "", nil); err == nil {
			t.Errorf("Sync accepted %s", name)
		}
	}
}

func TestSyncBuildsTheRsyncCommand(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	// A fake rsync earlier on PATH records its arguments and prints stats.
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + argsFile + "\n" +
		"echo 'Number of files: 3 (reg: 2, dir: 1)'\necho 'Total transferred file size: 9 bytes'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	source := t.TempDir()
	res, err := mgr.Sync(context.Background(), mc.RunID, source, "", []string{"node_modules", ".git"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if want := "work/" + filepath.Base(source); res.Dest != want {
		t.Errorf("dest = %q, want %q", res.Dest, want)
	}
	if !strings.Contains(res.Summary, "Number of files: 3") {
		t.Errorf("summary = %q, want the rsync stats", res.Summary)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the fake rsync never ran: %v", err)
	}
	args := string(got)
	for _, want := range []string{
		"-a --stats", "--exclude node_modules", "--exclude .git",
		"StrictHostKeyChecking=no", "admin@192.168.64.9:'work/",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("rsync arguments have no %q\nargs: %s", want, args)
		}
	}
	// -z makes a local sync ~4x slower (docs/10-build-transport.md).
	if strings.Contains(args, "-az") || strings.Contains(args, "-z") {
		t.Errorf("rsync must not compress to a local VM\nargs: %s", args)
	}
}

func TestSyncPutsAProjectAtThePinnedGuestPath(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + argsFile + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	source := filepath.Join(t.TempDir(), "myapp")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.Sync(context.Background(), mc.RunID, source, "", nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// SwiftPM caches break when moved, so the default dest is a contract.
	want := GuestWorkDir + "/myapp"
	if res.Dest != want {
		t.Fatalf("dest = %q, want %q", res.Dest, want)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the fake rsync never ran: %v", err)
	}
	if !strings.Contains(string(args), "admin@192.168.64.9:'"+want+"'/") {
		t.Errorf("rsync target is not the pinned guest path\nargs: %s", args)
	}
}

func TestCheckTartWarnsWhenTheHostTartIsNotThePinnedVersion(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	bin, control := testsupport.FakeTart(t)

	mgr, err := NewManager(t.TempDir(), log, WithTartBin(bin), WithSSHProbe(sshAnswers))
	if err != nil {
		t.Fatal(err)
	}

	mgr.CheckTart(context.Background())
	if strings.Contains(buf.String(), "WARN") {
		t.Fatalf("the pinned version must not warn: %s", buf.String())
	}

	buf.Reset()
	if err := os.WriteFile(filepath.Join(control, "tart-version"), []byte("2.32.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr.CheckTart(context.Background())
	out := buf.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "2.32.1") || !strings.Contains(out, tart.PinnedVersion) {
		t.Fatalf("a version mismatch must warn and name both versions: %s", out)
	}
}

func TestWithTartBinIgnoresAnEmptyPath(t *testing.T) {
	m := &Manager{tart: &tart.Client{Bin: "resolved-tart"}}
	WithTartBin("")(m)
	if m.tart.Bin != "resolved-tart" {
		t.Fatalf("an empty -tart must not replace the resolved binary, got %q", m.tart.Bin)
	}
	WithTartBin("chosen")(m)
	if m.tart.Bin != "chosen" {
		t.Fatalf("an explicit -tart must be used, got %q", m.tart.Bin)
	}
}

func TestSyncUsesAnExplicitDest(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	res, err := mgr.Sync(context.Background(), mc.RunID, t.TempDir(), "elsewhere/app", nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Dest != "elsewhere/app" {
		t.Errorf("dest = %q, want elsewhere/app", res.Dest)
	}
}

func TestSyncReportsAnRsyncFailure(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'ssh: connect to host port 22: No route to host' >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := mgr.Sync(context.Background(), mc.RunID, t.TempDir(), "", nil)
	if err == nil {
		t.Fatal("Sync returned no error although rsync failed")
	}
	if !strings.Contains(err.Error(), "No route to host") {
		t.Errorf("the error hides the rsync output: %v", err)
	}
}

func TestScreenshotWritesThePNG(t *testing.T) {
	mgr, root, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}

	data, shot, err := mgr.Screenshot(context.Background(), mc.RunID)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	path := shot.Path
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Errorf("the returned bytes are not a PNG: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the PNG was not written: %v", err)
	}
	if !bytes.Equal(onDisk, data) {
		t.Error("the file on disk differs from the returned bytes")
	}
	if !strings.HasPrefix(path, filepath.Join(root, "runs", mc.RunID)) {
		t.Errorf("the PNG landed outside the run directory: %s", path)
	}
	if !strings.HasSuffix(path, "-screenshot.png") {
		t.Errorf("unexpected artifact name: %s", path)
	}
}

func TestScreenshotRejectsOutputThatIsNotBase64(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte("not base64 at all !!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err == nil {
		t.Fatal("Screenshot accepted output that is not base64")
	}
}

func TestDestroyRemovesTheMachine(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)

	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if got := mgr.List(); len(got) != 0 {
		t.Errorf("List returned %d machines after destroy, want 0", len(got))
	}
	log := testsupport.Calls(t, control)
	for _, want := range []string{"stop greenroom-", "delete greenroom-"} {
		if !strings.Contains(log, want) {
			t.Errorf("Destroy never called tart %q\ncalls:\n%s", want, log)
		}
	}
	if err := mgr.Destroy(context.Background(), mc.RunID); err == nil {
		t.Error("a second Destroy returned no error")
	}
}

func TestDestroyDeletesEvenWhenStopFails(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	testsupport.Flag(t, control, "fail-stop")

	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy must still delete after a failed stop: %v", err)
	}
	if !strings.Contains(testsupport.Calls(t, control), "delete greenroom-") {
		t.Error("delete was never attempted after stop failed")
	}
}

func TestDestroyRecordsTheEndOfTheRun(t *testing.T) {
	mgr, root, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	man := readManifest(t, filepath.Join(root, "runs", mc.RunID))
	if man.DestroyedAt == nil {
		t.Error("the manifest has no destroyedAt after destroy")
	}
}

func TestLoadStateReattachesARunningMachine(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, first)
	// The fake reports this VM name as running.
	if err := os.WriteFile(filepath.Join(control, "vmname"), []byte(mc.Name), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatalf("the second manager did not start: %v", err)
	}
	got := second.List()
	if len(got) != 1 {
		t.Fatalf("the second manager sees %d machines, want 1", len(got))
	}
	if got[0].RunID != mc.RunID || got[0].Status != Ready {
		t.Errorf("reattached machine is wrong: %+v", got[0])
	}
}

func TestLoadStateDropsAMachineThatNoLongerRuns(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	readyMachine(t, first)
	testsupport.Flag(t, control, "list-empty")

	second, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	if got := second.List(); len(got) != 0 {
		t.Errorf("the second manager kept %d machines that tart no longer lists", len(got))
	}
}

// Reattaching must carry the manifest forward, not reset Steps or drop the verdict.
func TestLoadStateKeepsTheStepCountAndVerdict(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, first)
	if err := os.WriteFile(filepath.Join(control, "vmname"), []byte(mc.Name), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "runs", mc.RunID)
	before := readManifest(t, dir)
	if before.Steps == 0 {
		t.Fatal("the run recorded no steps, so this test cannot see the regression")
	}
	// A verdict the run had reached before the daemon went away.
	before.Verdict = &session.VerdictState{Seq: 7, Verdict: "pass", Status: session.Accepted}
	data, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0)); err != nil {
		t.Fatalf("the second manager did not start: %v", err)
	}

	after := readManifest(t, dir)
	if after.Steps != before.Steps {
		t.Errorf("step count is %d after reattaching, want %d", after.Steps, before.Steps)
	}
	if after.Verdict == nil || after.Verdict.Verdict != "pass" {
		t.Errorf("the verdict did not survive reattaching: %+v", after.Verdict)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("createdAt moved from %v to %v", before.CreatedAt, after.CreatedAt)
	}
}

func TestLoadStateMovesCorruptStateAside(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(root, slog.New(slog.NewTextHandler(io.Discard, nil)), WithTartBin(bin), WithSSHProbe(sshAnswers))
	if err != nil {
		t.Fatalf("a corrupt state.json stopped the daemon: %v", err)
	}
	if n := len(mgr.List()); n != 0 {
		t.Errorf("started with %d machines, want 0", n)
	}
	aside, _ := filepath.Glob(filepath.Join(root, "state.json.corrupt-*"))
	if len(aside) != 1 {
		t.Fatalf("found %d moved-aside state files, want 1", len(aside))
	}
	if data, _ := os.ReadFile(aside[0]); string(data) != "{not json" {
		t.Errorf("the moved-aside file holds %q, want the corrupt original", data)
	}
}

func TestFinishBootFailsWhenTheIPNeverArrives(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-ip")

	mc, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 30*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status != Failed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.Error == "" {
		t.Error("a failed machine carries no error text")
	}
	// Cleanup runs after Wait returns, so poll for it.
	var log string
	for i := 0; i < 50; i++ {
		log = testsupport.Calls(t, control)
		if strings.Contains(log, "delete greenroom-") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(log, "delete greenroom-") {
		t.Errorf("a failed boot did not delete the VM within 5s\ncalls:\n%s", log)
	}
}

func TestGuestToolsRefuseAFailedMachine(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-ip")
	mc, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Wait(context.Background(), mc.RunID, 30*time.Second); err != nil {
		t.Fatal(err)
	}

	_, err = mgr.Exec(context.Background(), mc.RunID, "echo hi", "", 5*time.Second)
	if err == nil {
		t.Fatal("Exec ran against a failed machine")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("the error does not say the machine failed: %v", err)
	}
}

func TestInstallSSHKeyFailureFailsTheBoot(t *testing.T) {
	mgr, _, control := newTestManager(t)
	// The guest agent answers the readiness probe, then the key install fails.
	testsupport.Flag(t, control, "fail-keyinstall")

	mc, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Failed {
		t.Errorf("status = %q, want failed when the ssh key cannot be installed", got.Status)
	}
}

// The capture-alert record goes in before ready, so it precedes every screenshot and frame.
func TestBootPreApprovesScreenCaptureBeforeReady(t *testing.T) {
	mgr, _, control := newTestManager(t)
	readyMachine(t, mgr)
	calls := testsupport.Calls(t, control)
	if !strings.Contains(calls, "ScreenCaptureApprovals.plist") {
		t.Fatalf("the boot never wrote the screen-capture approval record; calls:\n%s", calls)
	}
}

// Without the record the guest only shows an alert, so the machine must still boot.
func TestCaptureApprovalFailureDoesNotFailTheBoot(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-capture-approval")

	mc, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Ready {
		t.Errorf("status = %q (%s), want ready when the approval record cannot be written", got.Status, got.Error)
	}
}

// Concurrent screenshots must not pick the same file name.
func TestConcurrentScreenshotsGetDistinctFiles(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	const n = 6
	paths := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, shot, err := mgr.Screenshot(context.Background(), mc.RunID)
			paths[i], errs[i] = shot.Path, err
		}(i)
	}
	wg.Wait()

	seen := map[string]int{}
	for i, p := range paths {
		if errs[i] != nil {
			t.Fatalf("screenshot %d failed: %v", i, errs[i])
		}
		seen[p]++
	}
	for p, count := range seen {
		if count > 1 {
			t.Errorf("%d concurrent screenshots share the file %s, so they overwrite each other", count, filepath.Base(p))
		}
	}
}

// Issue #3: a machine is ready only once ssh answers.
func TestMachineIsNotReadyUntilSSHAnswers(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	var probes atomic.Int32
	mgr, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithTartBin(bin), WithReadyTimeout(20*time.Second), WithFrameInterval(0),
		WithSSHProbe(func(context.Context, string, string) error {
			if probes.Add(1) <= 2 {
				return errors.New("connection refused")
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)

	mc := readyMachine(t, mgr)
	if got := probes.Load(); got < 3 {
		t.Errorf("the machine reported ready after %d ssh probes, want at least 3", got)
	}
	if mc.BootSeconds <= 0 {
		t.Error("bootSeconds does not cover the wait for ssh")
	}
}

func TestBootFailsWhenSSHNeverAnswers(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	mgr, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithTartBin(bin), WithReadyTimeout(2*time.Second),
		WithSSHProbe(func(context.Context, string, string) error { return errors.New("connection refused") }))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)

	created, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), created.RunID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Failed {
		t.Fatalf("status = %q, want failed when ssh never answers", got.Status)
	}
	if !strings.Contains(got.Error, "ssh") {
		t.Errorf("error = %q, want it to name ssh", got.Error)
	}
}

// The default probe asks the guest over vsock; the daemon never dials a guest.
func TestDefaultSSHProbeAsksTheGuestOverVsock(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	mgr, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithTartBin(bin), WithReadyTimeout(20*time.Second), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)

	mc := readyMachine(t, mgr)
	log := testsupport.Calls(t, control)
	want := "exec " + mc.Name + " /usr/bin/nc -z 127.0.0.1 22"
	if !strings.Contains(log, want) {
		t.Errorf("the default probe never ran %q\ncalls:\n%s", want, log)
	}
}

// A boot that times out at the ssh phase must name the last probe error.
func TestSSHTimeoutNamesTheLastProbeError(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	testsupport.Flag(t, control, "ssh-down")
	mgr, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithTartBin(bin), WithReadyTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)

	created, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), created.RunID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Failed {
		t.Fatalf("status = %q, want failed when the guest refuses ssh", got.Status)
	}
	for _, want := range []string{"did not answer", "Connection refused"} {
		if !strings.Contains(got.Error, want) {
			t.Errorf("error = %q, want it to contain %q", got.Error, want)
		}
	}
}

// Concurrent creates must not all pass the capacity check. The fake host runs
// one foreign VM, so a limit of two leaves one slot.
func TestConcurrentCreatesRespectTheHostLimit(t *testing.T) {
	bin, _ := testsupport.FakeTart(t)
	mgr, err := NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithTartBin(bin), WithMaxMachines(2), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	settleOnCleanup(t, mgr)

	const n = 4
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = mgr.Create(context.Background(), testImage, false)
		}(i)
	}
	wg.Wait()

	var ok int
	for _, err := range results {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Errorf("%d of %d concurrent creates succeeded with one free slot, want exactly 1", ok, n)
	}
	if got := len(mgr.List()); got != 1 {
		t.Errorf("the manager holds %d machines, want 1", got)
	}

	// Settle the winner's boot before TempDir cleanup.
	for _, mc := range mgr.List() {
		if _, err := mgr.Wait(context.Background(), mc.RunID, 20*time.Second); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
	}
}

// The fake tart must exit when its control directory is removed, or it leaks.
func TestFakeTartExitsWhenItsControlDirectoryGoesAway(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	logPath := filepath.Join(t.TempDir(), "vm.log")
	cl := &tart.Client{Bin: bin}
	proc, err := cl.Start("greenroom-leak-check", logPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if proc.Exited() {
		t.Fatal("the fake exited before the VM was stopped")
	}
	if err := os.RemoveAll(control); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		if proc.Exited() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = proc.Kill()
	t.Fatal("the fake tart kept running after its control directory was removed")
}

// steps.jsonl outranks a manifest that is behind it, so no number is reused.
func TestLoadStateCarriesTheRecordedStepsForwardOverABehindManifest(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, first)
	if err := os.WriteFile(filepath.Join(control, "vmname"), []byte(mc.Name), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "runs", mc.RunID)
	steps, err := ReadSteps(dir)
	if err != nil || len(steps) == 0 {
		t.Fatalf("the run recorded no steps (%v), so this test cannot see the regression", err)
	}
	highest := steps[len(steps)-1].Seq

	// The manifest as a daemon that died mid-run left it.
	man := readManifest(t, dir)
	man.Steps = 0
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatalf("the second manager did not start: %v", err)
	}
	if _, err := second.Exec(context.Background(), mc.RunID, "echo hi", "", 10*time.Second); err != nil {
		t.Fatalf("Exec after reattaching: %v", err)
	}
	after, err := ReadSteps(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, s := range after {
		if seen[s.Seq] {
			t.Fatalf("step %d was handed out twice; the run's evidence overwrites itself", s.Seq)
		}
		seen[s.Seq] = true
	}
	if got := after[len(after)-1].Seq; got != highest+1 {
		t.Errorf("the step after reattaching is %d, want %d", got, highest+1)
	}
}

// A reattach that finds the VM gone stamps the run's end from its evidence.
func TestLoadStateRecordsTheEndOfADroppedRun(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, first)
	dir := filepath.Join(root, "runs", mc.RunID)
	if man := readManifest(t, dir); man.DestroyedAt != nil {
		t.Fatal("the run already has an end before the daemon restarted")
	}
	log2, err := ReadStepLog(dir)
	if err != nil || log2.Count == 0 {
		t.Fatalf("the run recorded no steps: %v", err)
	}
	testsupport.Flag(t, control, "list-empty")

	if _, err := NewManager(root, log, WithTartBin(bin), WithReadyTimeout(10*time.Second), WithSSHProbe(sshAnswers), WithFrameInterval(0)); err != nil {
		t.Fatal(err)
	}
	man := readManifest(t, dir)
	if man.DestroyedAt == nil {
		t.Fatal("a dropped machine left the run with no end")
	}
	if want := log2.Last; !man.DestroyedAt.Equal(want) {
		t.Errorf("the run ends at %v, want the end of its last step at %v", man.DestroyedAt, want)
	}
}

func TestAFailedBootEndsTheRun(t *testing.T) {
	mgr, root, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-ip")
	// The end is stamped after Wait returns, so wait for the failed event.
	failed := make(chan struct{})
	var once sync.Once
	stop := mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind == "failed" {
			once.Do(func() { close(failed) })
		}
	})
	defer stop()
	mc, err := mgr.Create(context.Background(), testImage, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Failed {
		t.Fatalf("machine is %s, want failed", got.Status)
	}
	select {
	case <-failed:
	case <-time.After(20 * time.Second):
		t.Fatal("no failed event")
	}
	man := readManifest(t, filepath.Join(root, "runs", mc.RunID))
	if man.DestroyedAt == nil {
		t.Error("a failed boot left the run with no end")
	}
}

// A tart process that exits after boot means the machine is gone.
func TestAMachineWhoseProcessExitsIsFailed(t *testing.T) {
	mgr, root, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	stopped := make(chan struct{})
	var once sync.Once
	stop := mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind == "stopped" && ev.RunID == mc.RunID {
			once.Do(func() { close(stopped) })
		}
	})
	defer stop()

	testsupport.Flag(t, control, "stopped")

	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("no stopped event for a machine whose tart process exited")
	}
	if len(mgr.List()) > 0 {
		t.Error("the daemon still lists a machine whose tart process exited")
	}
	if log := testsupport.Calls(t, control); !strings.Contains(log, "delete "+mc.Name) {
		t.Errorf("a machine that stopped on its own left its VM behind\ncalls:\n%s", log)
	}
	man := readManifest(t, filepath.Join(root, "runs", mc.RunID))
	if man.DestroyedAt == nil {
		t.Error("a machine that stopped on its own left the run with no end")
	}
}
