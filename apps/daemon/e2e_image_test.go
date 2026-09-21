//go:build tart

// Proves PrepareGuest actually saves what it promises (issue #12): clone the
// base image scripts/build-image.sh built, boot it, and time the first
// ScreenOf the same way TestEndToEndInput does against an unprepared image.
// A clone of a prepared image must answer far faster, because the helper is
// already compiled and the ssh key is already installed. Requires
// scripts/build-image.sh to have already built the base image (default name
// greenroom-base; override with GREENROOM_BASE_IMAGE). Run with:
//
//	go test -tags tart -run TestPreparedImageNeedsNoCompile -v -timeout 12m .
package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// greenroomBaseImage is the local VM name scripts/build-image.sh prepares by
// default (its own -name default).
func greenroomBaseImage() string {
	if v := os.Getenv("GREENROOM_BASE_IMAGE"); v != "" {
		return v
	}
	return "greenroom-base"
}

// noCompileCeiling is what a prepared image's first ScreenOf must stay
// under. TestEndToEndInput measures the unprepared cost on a fresh clone of
// defaultImage every time it runs (its own "first ScreenOf ... took" log
// line is the real baseline, since that number moves with host load); this
// project's own measurements have put it at roughly 27-30s, all of it the
// guest's swiftc compile. A prepared image doing that work at all, even
// quickly, would still be seconds, not tens of milliseconds, so 10s is a
// generous ceiling that only a genuinely skipped compile clears.
const noCompileCeiling = 10 * time.Second

func TestPreparedImageNeedsNoCompile(t *testing.T) {
	base := greenroomBaseImage()

	// Confirm the base VM actually exists locally before spending a clone
	// and a boot on it: a missing base is a setup mistake ("run
	// scripts/build-image.sh first"), not a flake worth retrying.
	out, err := exec.Command("tart", "list", "--source", "local", "--quiet").CombinedOutput()
	if err != nil {
		t.Fatalf("tart list: %v: %s", err, out)
	}
	found := false
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name == base {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no local VM named %q. Run scripts/build-image.sh first.\ntart list --source local:\n%s", base, out)
	}

	// Captured before this test's own clone and boot, so that any file the
	// guest genuinely wrote during THIS boot has a later mtime than this
	// timestamp, and anything baked in earlier by scripts/build-image.sh
	// does not. This is the no-compile proof: it reads a timestamp out of
	// the guest's own filesystem with a plain `stat`, not our code's say-so,
	// so it would catch a regression that silently recompiled while still
	// reporting itself installed.
	beforeThisBoot := time.Now()

	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	created, err := mgr.Create(ctx, base, false)
	if err != nil {
		t.Fatalf("Create (clone of %s): %v", base, err)
	}
	runID := created.RunID
	defer func() {
		if err := mgr.Destroy(context.Background(), runID); err != nil {
			t.Logf("Destroy: %v", err)
		}
	}()

	mc, err := mgr.Wait(ctx, runID, 6*time.Minute)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if mc.Status != machine.Ready || mc.IP == "" {
		t.Fatalf("machine did not become ready: %+v", mc)
	}
	t.Logf("machine %s at %s booted in %.1fs, cloned from %s", runID, mc.IP, mc.BootSeconds, base)

	started := time.Now()
	screen, err := mgr.ScreenOf(ctx, runID)
	firstInputLatency := time.Since(started)
	if err != nil {
		t.Fatalf("ScreenOf: %v", err)
	}
	t.Logf("first ScreenOf against the prepared image (%s) took %s; compare against TestEndToEndInput's "+
		"own log line for the unprepared cost", base, firstInputLatency)
	if screen.Width <= 100 || screen.Height <= 100 {
		t.Fatalf("implausible screen size: %+v", screen)
	}
	if firstInputLatency >= noCompileCeiling {
		t.Fatalf("first ScreenOf against the prepared image took %s, want under %s: the image does not look prepared",
			firstInputLatency, noCompileCeiling)
	}

	// The no-compile proof itself: stat the helper binary's mtime inside the
	// guest and require it to predate this test's own clone and boot.
	helperPath := ".greenroom/bin/greenroom-input-" + strconv.Itoa(machine.InputHelperVersion())
	res, err := mgr.Exec(ctx, runID, `stat -f %m "$HOME/`+helperPath+`"`, "", 15*time.Second)
	if err != nil {
		t.Fatalf("stat the helper binary: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("stat %s: exit %d: %s (the prepared image never baked in this version of the helper)",
			helperPath, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	mtimeUnix, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
	if err != nil {
		t.Fatalf("parse helper mtime %q: %v", res.Stdout, err)
	}
	mtime := time.Unix(mtimeUnix, 0)
	t.Logf("helper binary mtime: %s; this test's clone started at %s", mtime, beforeThisBoot)
	if !mtime.Before(beforeThisBoot) {
		t.Fatalf("the helper binary's mtime (%s) is not before this test's own boot (%s): "+
			"it looks freshly compiled, not baked into the image", mtime, beforeThisBoot)
	}
}
