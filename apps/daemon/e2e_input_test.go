//go:build tart

// End-to-end test that ADR 0009's computer use actually works against a real
// Tart VM: a control lease, a mouse move whose result the OS itself reports
// (not our helper's say-so), and a keystroke that reaches a real app in the
// guest's login session (proven by a file on disk, not by Input returning no
// error). Boots exactly one VM; a second may already be running on the host,
// and Apple allows two. Run with:
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
	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// 1. Boot one real VM, and make sure it is destroyed even if the rest of
	// the test fails.
	created, err := mgr.Create(ctx, defaultImage, false)
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

	// 2. ScreenOf reports a plausible resolution. This first call also
	// compiles the guest helper, so it is slow -- the number matters for
	// issue #12.
	started := time.Now()
	screen, err := mgr.ScreenOf(ctx, runID)
	firstInputLatency := time.Since(started)
	if err != nil {
		t.Fatalf("ScreenOf: %v", err)
	}
	t.Logf("screen: %dx%d; first ScreenOf (compiles the helper in the guest) took %s", screen.Width, screen.Height, firstInputLatency)
	if screen.Width <= 100 || screen.Height <= 100 {
		t.Fatalf("implausible screen size: %+v", screen)
	}

	// 3. Take the lease. A second holder must be refused while it is held,
	// and ControlState must name the holder.
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

	// 4. Pointer proof: post one move to a known fraction, then ask the OS
	// itself -- from a separate guest process, not our helper -- where the
	// pointer is. This is independent of whether our code thinks it moved
	// the mouse.
	const fx, fy = 0.25, 0.75
	if _, err := mgr.Input(ctx, runID, "e2e", []machine.InputAction{
		{Type: "move", X: floatPtr(fx), Y: floatPtr(fy)},
	}); err != nil {
		t.Fatalf("Input(move): %v", err)
	}

	// swift -e runs a single expression as a script without a project; it is
	// available on every image that ships swiftc, which installInputHelper
	// already requires.
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

	// 5. Keyboard proof: give a real app focus, then post one batch that
	// types a command and presses Return. The file can only exist if real
	// keystrokes reached a real app in the guest's login session.
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

	// 6. Evidence proof: one machine_input step per batch posted above (the
	// move, and the type+return), whatever a batch's length (ADR 0009).
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

	// 7. Release the lease; the screen is free and Input is refused.
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
