//go:build tart

// The verifier's approval on a real guest (ADR 0044, issue #269): an app built in the run under
// the home is approved through the verifier's scoped call, and osascript run as the verifier
// then drives it with no "wants access to control" prompt; a pre-installed app is refused.
// Needs a local image built by scripts/build-image.sh (GREENROOM_BASE_IMAGE, default
// greenroom-base). Run with:
//
//	go test -tags tart -run TestEndToEndVerifierApprovesARunBuiltApp -v -timeout 15m .
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// buildRunApp builds an accessory app with an AppKit run loop under ~/work, so it stays up and
// answers Apple Events, as an app under test does.
const buildRunApp = `set -e
d="$HOME/work/ApproveCheck" && rm -rf "$d" && mkdir -p "$d/ApproveCheck.app/Contents/MacOS"
cat > "$d/ApproveCheck.app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>com.greenroom.e2e.approvecheck</string>
	<key>CFBundleExecutable</key><string>ApproveCheck</string>
	<key>CFBundleName</key><string>ApproveCheck</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>LSUIElement</key><true/>
	<key>NSAppleScriptEnabled</key><true/>
</dict>
</plist>
PLIST
printf 'import AppKit\nNSApplication.shared.run()\n' > "$d/main.swift"
swiftc -o "$d/ApproveCheck.app/Contents/MacOS/ApproveCheck" "$d/main.swift"
open "$d/ApproveCheck.app"
n=0; until pgrep -x ApproveCheck >/dev/null || [ $n -ge 100 ]; do sleep 0.1; n=$((n+1)); done
pgrep -x ApproveCheck >/dev/null`

func TestEndToEndVerifierApprovesARunBuiltApp(t *testing.T) {
	mgr, err := machine.NewManager(t.TempDir(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	waitForAFreeSlot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
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

	built, err := mgr.ExecAs(ctx, runID, machine.HolderVerifier, buildRunApp, "", 3*time.Minute)
	if err != nil || built.ExitCode != 0 {
		t.Fatalf("building the app: %v exit %d\n%s\n%s", err, built.ExitCode, built.Stdout, built.Stderr)
	}

	grant, step, err := mgr.ApproveControlInHome(ctx, runID, machine.HolderVerifier, "work/ApproveCheck/ApproveCheck.app")
	if err != nil {
		t.Fatalf("the verifier's approval of an app under the home: %v", err)
	}
	if grant.BundleID != "com.greenroom.e2e.approvecheck" || step == 0 {
		t.Errorf("grant %+v step %d, want the app's bundle id and a step", grant, step)
	}

	// count windows, never get name: AppleScript answers an application's name from its bundle
	// without sending an Apple Event, so it never prompts and proves nothing (measured live).
	// Without the grant this blocks on the prompt until the guest kills it at 60 s (exit 124).
	asked, err := mgr.ExecAs(ctx, runID, machine.HolderVerifier,
		`osascript -e 'tell application id "com.greenroom.e2e.approvecheck" to count windows'`, "", 60*time.Second)
	if err != nil || asked.ExitCode != 0 || strings.TrimSpace(asked.Stdout) != "0" {
		t.Errorf("osascript as the verifier after the approval: %v exit %d timedOut %v\n%s\n%s",
			err, asked.ExitCode, asked.TimedOut, asked.Stdout, asked.Stderr)
	}

	if _, _, err := mgr.ApproveControlInHome(ctx, runID, machine.HolderVerifier, "/System/Applications/Calculator.app"); err == nil ||
		!strings.Contains(err.Error(), "outside the guest home") {
		t.Errorf("the verifier approved a pre-installed app, or did not say why not: %v", err)
	}

	_, _ = mgr.ExecAs(ctx, runID, machine.HolderVerifier, "pkill -x ApproveCheck", "", 30*time.Second)
}
