//go:build tart

// A prompt stops the verifier's command on a real guest (ADR 0047, issue #283): an app built in
// the run and not approved raises the Apple Events prompt when osascript drives it. The verifier's
// exec comes back at the first look, stopped, naming the app, instead of waiting until osascript's
// own two-minute Apple Event timeout or the exec's five-minute one. Once the prompt has timed out
// a wait for it is not stopped, the approval (ADR 0044) takes, and the same command answers.
// Needs a local image built by scripts/build-image.sh (GREENROOM_BASE_IMAGE, default
// greenroom-base). Run with:
//
//	go test -tags tart -run TestEndToEndAPromptStopsTheVerifiersCommand -v -timeout 15m .
package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

func TestEndToEndAPromptStopsTheVerifiersCommand(t *testing.T) {
	mgr, err := machine.NewManager(t.TempDir(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	waitForAFreeSlot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 13*time.Minute)
	defer cancel()

	created, err := mgr.Create(ctx, greenroomBaseImage())
	if err != nil {
		t.Fatalf("Create (clone of %s): %v", greenroomBaseImage(), err)
	}
	runID := created.RunID
	defer func() {
		if err := mgr.Destroy(context.Background(), runID); err != nil {
			t.Logf("Destroy: %v", err)
		}
	}()
	if mc, err := mgr.Wait(ctx, runID, 6*time.Minute); err != nil || mc.Status != machine.Ready {
		t.Fatalf("machine not ready: %+v %v", mc, err)
	}

	build := strings.NewReplacer("ApproveCheck", "PromptCheck", "approvecheck", "promptcheck").Replace(buildRunApp)
	built, err := mgr.ExecAs(ctx, runID, machine.HolderVerifier, build, "", 3*time.Minute)
	if err != nil || built.ExitCode != 0 {
		t.Fatalf("building the app: %v exit %d\n%s\n%s", err, built.ExitCode, built.Stdout, built.Stderr)
	}

	// count windows needs the grant (ADR 0044's measurements); with none it raises the prompt.
	const ask = `osascript -e 'tell application id "com.greenroom.e2e.promptcheck" to count windows'`
	started := time.Now()
	stopped, err := mgr.ExecWatched(ctx, runID, machine.HolderVerifier, ask, "", 5*time.Minute)
	took := time.Since(started)
	if err != nil {
		t.Fatalf("the blocked command: %v", err)
	}
	t.Logf("the blocked command came back after %s: exit %d, stopped %v, desktop %+v", took, stopped.ExitCode, stopped.StoppedForPrompt, stopped.Desktop)
	if !stopped.StoppedForPrompt || took > time.Minute {
		t.Fatalf("the command was not stopped for the prompt within a minute (%s): %+v", took, stopped)
	}
	if stopped.Desktop == nil || len(stopped.Desktop.Prompts) == 0 ||
		!strings.Contains(stopped.Desktop.Prompts[0].Text, "wants access to control “PromptCheck”") {
		t.Fatalf("the result does not say which app prompted: %+v", stopped.Desktop)
	}

	// The prompt stays until it times out, about 2 minutes after it appeared, and its timeout
	// writes a denial over any approval made before it. Waiting for it is not stopped.
	wait := max(10, 130-int(stopped.Seconds))
	waited, err := mgr.ExecWatched(ctx, runID, machine.HolderVerifier, "sleep "+strconv.Itoa(wait), "",
		time.Duration(wait)*time.Second+time.Minute)
	if err != nil || waited.StoppedForPrompt || waited.ExitCode != 0 {
		t.Fatalf("the wait for the prompt to go was stopped or failed: %+v %v", waited, err)
	}

	if _, _, err := mgr.ApproveControlInHome(ctx, runID, machine.HolderVerifier, "work/PromptCheck/PromptCheck.app"); err != nil {
		t.Fatalf("the verifier's approval: %v", err)
	}
	answered, err := mgr.ExecWatched(ctx, runID, machine.HolderVerifier, ask, "", time.Minute)
	if err != nil || answered.StoppedForPrompt || answered.ExitCode != 0 || strings.TrimSpace(answered.Stdout) != "0" {
		t.Errorf("osascript after the approval: %v exit %d stopped %v\n%s\n%s",
			err, answered.ExitCode, answered.StoppedForPrompt, answered.Stdout, answered.Stderr)
	}
	_, _ = mgr.ExecAs(ctx, runID, machine.HolderVerifier, "pkill -x PromptCheck", "", 30*time.Second)
}
