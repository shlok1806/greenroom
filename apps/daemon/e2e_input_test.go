//go:build tart

// Proves computer use (ADR 0009) on a real VM: the lease, a pointer move the guest OS reports, and keystrokes
// that reach a real app. Boots one VM. Run with:
//
//	go test -tags tart -run TestEndToEndInput -v -timeout 12m .
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

func floatPtr(v float64) *float64 { return &v }

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestEndToEndInput(t *testing.T) {
	waitForAFreeSlot(t)
	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	created, err := mgr.Create(ctx, greenroomBaseImage())
	if err != nil {
		t.Fatalf("Create: %v", err)
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
	t.Logf("machine %s at %s booted in %.1fs", runID, mc.IP, mc.BootSeconds)

	// The first ScreenOf compiles the guest helper unless the image baked it (issue #12).
	started := time.Now()
	screen, err := mgr.ScreenOf(ctx, runID)
	firstInputLatency := time.Since(started)
	if err != nil {
		t.Fatalf("ScreenOf: %v", err)
	}
	t.Logf("screen: %dx%d; first ScreenOf took %s", screen.Width, screen.Height, firstInputLatency)
	if screen.Width <= 100 || screen.Height <= 100 {
		t.Fatalf("implausible screen size: %+v", screen)
	}

	control, fresh, err := mgr.TakeControl(runID, "e2e", 0)
	if err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if !fresh || control.Holder != "e2e" {
		t.Fatalf("lease is %+v, want a fresh lease held by e2e", control)
	}
	if _, _, err := mgr.TakeControl(runID, "someone-else", 0); !errors.Is(err, machine.ErrControlHeld) {
		t.Fatalf("a second holder got %v while the lease was held, want ErrControlHeld", err)
	}
	if state, held := mgr.ControlState(runID); !held || state.Holder != "e2e" {
		t.Fatalf("ControlState is %+v held=%v, want e2e holding it", state, held)
	}

	// Pointer proof: move, then ask the guest OS from a separate process where the pointer is.
	const fx, fy = 0.25, 0.75
	if _, err := mgr.Input(ctx, runID, "e2e", []machine.InputAction{
		{Type: "move", X: floatPtr(fx), Y: floatPtr(fy)},
	}); err != nil {
		t.Fatalf("Input(move): %v", err)
	}

	pointerCmd := `swift -e 'import CoreGraphics; let p = CGEvent(source: nil)!.location; print(Int(p.x), Int(p.y))'`
	pos, err := mgr.Exec(ctx, runID, pointerCmd, "", 90*time.Second)
	if err != nil {
		t.Fatalf("read pointer position: %v", err)
	}
	if pos.ExitCode != 0 {
		t.Fatalf("swift -e to read the pointer failed: exit %d: stderr=%q stdout=%q", pos.ExitCode, pos.Stderr, pos.Stdout)
	}
	var gotX, gotY int
	if _, err := fmt.Sscanf(strings.TrimSpace(pos.Stdout), "%d %d", &gotX, &gotY); err != nil {
		t.Fatalf("parse pointer position %q: %v", pos.Stdout, err)
	}
	wantX := int(fx * float64(screen.Width))
	wantY := int(fy * float64(screen.Height))
	t.Logf("pointer: moved to fraction (%.2f,%.2f) of a %dx%d screen -> wanted ~(%d,%d), OS reports (%d,%d)",
		fx, fy, screen.Width, screen.Height, wantX, wantY, gotX, gotY)
	if absInt(gotX-wantX) > 2 || absInt(gotY-wantY) > 2 {
		t.Fatalf("OS reports the pointer at (%d,%d), want within 2px of (%d,%d)", gotX, gotY, wantX, wantY)
	}

	// Keyboard proof: the file exists only if the keystrokes reached Terminal in the login session.
	if _, err := mgr.Exec(ctx, runID, "open -a Terminal", "", 30*time.Second); err != nil {
		t.Fatalf("open -a Terminal: %v", err)
	}
	time.Sleep(3 * time.Second)

	const proofFile = "/tmp/greenroom-input-proof.txt"
	if _, err := mgr.Exec(ctx, runID, "rm -f "+proofFile, "", 15*time.Second); err != nil {
		t.Fatalf("clear old proof file: %v", err)
	}
	if _, err := mgr.Input(ctx, runID, "e2e", []machine.InputAction{
		{Type: "type", Text: "echo greenroom-input-ok > " + proofFile},
		{Type: "key", Key: "return"},
	}); err != nil {
		t.Fatalf("Input(type+return): %v", err)
	}

	var proof machine.ExecResult
	deadline := time.Now().Add(30 * time.Second)
	for {
		proof, err = mgr.Exec(ctx, runID, "cat "+proofFile, "", 15*time.Second)
		if err == nil && proof.ExitCode == 0 && strings.Contains(proof.Stdout, "greenroom-input-ok") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proof file never showed the typed text within 30s: %+v (last err=%v)", proof, err)
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("keyboard proof: %s contains %q", proofFile, strings.TrimSpace(proof.Stdout))

	// One machine_input step per batch, whatever its length (ADR 0009).
	steps, err := machine.ReadSteps(mgr.RunDir(runID))
	if err != nil {
		t.Fatalf("ReadSteps: %v", err)
	}
	inputSteps := 0
	for _, s := range steps {
		if s.Tool == "machine_input" {
			inputSteps++
		}
	}
	if inputSteps != 2 {
		t.Fatalf("recorded %d machine_input steps, want 2 (one move batch, one type+key batch)", inputSteps)
	}

	released, held, err := mgr.ReleaseControl(runID, "e2e")
	if err != nil || !held || released.Holder != "e2e" {
		t.Fatalf("ReleaseControl: released=%+v held=%v err=%v", released, held, err)
	}
	if _, held := mgr.ControlState(runID); held {
		t.Fatal("ControlState still reports a holder after release")
	}
	if _, err := mgr.Input(ctx, runID, "e2e", []machine.InputAction{
		{Type: "move", X: floatPtr(0.5), Y: floatPtr(0.5)},
	}); !errors.Is(err, machine.ErrNoControl) {
		t.Fatalf("Input after release got %v, want ErrNoControl", err)
	}
}
