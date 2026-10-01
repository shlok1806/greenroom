package machine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// tccGrantApp makes a minimal .app bundle under g.home/work with a real Info.plist and a
// harmless executable, so tccgrant.sh's plutil reads and realpath resolve for real.
func (g *baseGuest) tccGrantApp(t *testing.T, rel, bundleID string) string {
	t.Helper()
	app := filepath.Join(g.home, rel)
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(macos, "TestApp")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	if out, err := exec.Command("plutil", "-create", "xml1", plist).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for k, v := range map[string]string{"CFBundleIdentifier": bundleID, "CFBundleExecutable": "TestApp"} {
		if out, err := exec.Command("plutil", "-insert", k, "-string", v, plist).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	return app
}

// runTCCGrant runs the real tccgrant.sh against g's stub guest (the same TCC.db stubs
// newBaseGuest sets up for base.sh) with app as $1.
func (g *baseGuest) runTCCGrant(t *testing.T, app string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", tccGrantScript, "sh", app)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(g.dir, "bin")+":/usr/bin:/bin", "HOME="+g.home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestTCCGrantScriptGrantsEveryServiceAndReadsBack runs tccgrant.sh for real (ADR 0038, issue
// #252): the run-time twin of base.sh's enumeration, for a bundle id the image cannot know.
func TestTCCGrantScriptGrantsEveryServiceAndReadsBack(t *testing.T) {
	g := newBaseGuest(t)
	app := g.tccGrantApp(t, "work/TestApp.app", "com.example.freshbuild")

	out, err := g.runTCCGrant(t, app)
	if err != nil {
		t.Fatalf("tccgrant.sh: %v\n%s", err, out)
	}
	var grant TCCGrant
	if uerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &grant); uerr != nil {
		t.Fatalf("output is not the expected JSON: %v\n%s", uerr, out)
	}
	if grant.BundleID != "com.example.freshbuild" {
		t.Errorf("bundleId = %q, want com.example.freshbuild", grant.BundleID)
	}
	wantExe, _ := filepath.EvalSymlinks(filepath.Join(app, "Contents", "MacOS", "TestApp"))
	if grant.Executable != wantExe {
		t.Errorf("executable = %q, want %q", grant.Executable, wantExe)
	}
	wantGranted := []string{"kTCCServiceAppleEvents", "kTCCServiceAccessibility", "kTCCServiceScreenCapture",
		"kTCCServiceSystemPolicyDesktopFolder", "kTCCServiceSystemPolicyDocumentsFolder",
		"kTCCServiceSystemPolicyDownloadsFolder", "kTCCServiceCamera", "kTCCServiceMicrophone"}
	for _, svc := range wantGranted {
		found := false
		for _, g := range grant.Granted {
			if g == svc {
				found = true
			}
		}
		if !found {
			t.Errorf("granted %v does not name %s", grant.Granted, svc)
		}
	}

	agent, _ := filepath.EvalSymlinks(filepath.Join(g.dir, "Cellar", "tart-guest-agent", "0.99.0", "bin", "tart-guest-agent"))
	keygen, _ := filepath.EvalSymlinks("/usr/libexec/sshd-keygen-wrapper")
	if keygen == "" {
		keygen = "/usr/libexec/sshd-keygen-wrapper"
	}
	for _, db := range []string{g.sys, g.user} {
		for _, row := range []struct{ client, target string }{
			{agent, "com.example.freshbuild"},
			{keygen, "com.example.freshbuild"},
			{grant.Executable, "com.apple.systemevents"},
			{grant.Executable, "com.example.freshbuild"},
		} {
			if got := g.sql(t, db, "SELECT auth_value FROM access WHERE service='kTCCServiceAppleEvents' AND client='"+row.client+"' AND indirect_object_identifier='"+row.target+"'"); got != "2" {
				t.Errorf("%s: no allowed Apple Events row from %s to %s (got %q)", filepath.Base(db), row.client, row.target, got)
			}
		}
		for _, svc := range wantGranted[1:] { // the family, keyed to the executable alone
			if got := g.sql(t, db, "SELECT auth_value FROM access WHERE service='"+svc+"' AND client='"+grant.Executable+"'"); got != "2" {
				t.Errorf("%s: %s not granted to %s (got %q)", filepath.Base(db), svc, grant.Executable, got)
			}
		}
	}
}

func TestTCCGrantScriptRefusesWhatIsNotAnApp(t *testing.T) {
	g := newBaseGuest(t)
	if _, err := g.runTCCGrant(t, filepath.Join(g.home, "work", "nothing-here.app")); err == nil {
		t.Error("a missing bundle was accepted")
	}
	bare := filepath.Join(g.home, "work", "NoInfo.app", "Contents")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := g.runTCCGrant(t, filepath.Join(g.home, "work", "NoInfo.app")); err == nil {
		t.Errorf("a bundle with no Info.plist was accepted: %s", out)
	}
}

// A write that fails to read back (SIP on, or a moved database) fails loudly rather than
// reporting success, as base.sh's own read-back does.
func TestTCCGrantScriptFailsWhenTheWriteDoesNotTake(t *testing.T) {
	g := newBaseGuest(t)
	app := g.tccGrantApp(t, "work/TestApp.app", "com.example.readonly")
	cmd := exec.Command("/bin/sh", "-c", tccGrantScript, "sh", app)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(g.dir, "bin")+":/usr/bin:/bin", "HOME="+g.home, "BASE_TCC_READONLY=1")
	got, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("a readonly TCC.db was reported as granted: %s", got)
	}
	if !strings.Contains(string(got), "did not take") {
		t.Errorf("the failure does not say the row did not take: %s", got)
	}
}

// ApproveControl records a machine_approve_control step and returns what the guest script
// granted, through the manager as machine_approve_control uses it.
func TestApproveControlRecordsAStepAndReturnsTheGrant(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)

	grant, step, err := mgr.ApproveControl(context.Background(), mc.RunID, "~/work/TestApp.app")
	if err != nil {
		t.Fatal(err)
	}
	if grant.BundleID != "com.example.testapp" || step == 0 {
		t.Errorf("bundleId %q step %d, want the fake grant's bundle id and a step", grant.BundleID, step)
	}
	if len(grant.Granted) == 0 {
		t.Error("granted is empty")
	}
	if !strings.Contains(testsupport.Calls(t, control), "sh work/TestApp.app") {
		t.Errorf("the path did not reach the script as an argument\n%s", testsupport.Calls(t, control))
	}

	if _, _, err := mgr.ApproveControl(context.Background(), mc.RunID, "/Applications/TestApp"); err == nil {
		t.Error("a path that is not an .app bundle was accepted")
	}

	testsupport.Flag(t, control, "fail-tcc-grant")
	if _, _, err := mgr.ApproveControl(context.Background(), mc.RunID, "~/work/TestApp.app"); err == nil {
		t.Error("a failed grant was reported as approved")
	}
}

// runTCCGrantScoped runs the real tccgrant.sh with scope as its second argument.
func (g *baseGuest) runTCCGrantScoped(t *testing.T, app, scope string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", tccGrantScript, "sh", app, scope)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(g.dir, "bin")+":/usr/bin:/bin", "HOME="+g.home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The home scope, which the verifier's call uses, approves an app built or synced in the run
// under the guest home, and nothing that resolves outside it: not a pre-installed app, not a
// link in the home to one, and not a bundle whose executable is a link to a system binary
// (ADR 0044, issue #269).
func TestTCCGrantScriptInHomeScopeApprovesOnlyWhatResolvesUnderTheHome(t *testing.T) {
	g := newBaseGuest(t)

	inside := g.tccGrantApp(t, "work/Built.app", "com.example.built")
	if out, err := g.runTCCGrantScoped(t, inside, "home"); err != nil {
		t.Fatalf("an app under the home was refused: %v\n%s", err, out)
	}
	if out, err := g.runTCCGrantScoped(t, "work/Built.app", "home"); err != nil {
		t.Fatalf("a home-relative path was refused: %v\n%s", err, out)
	}

	outside := g.tccGrantApp(t, "../Applications/Installed.app", "com.example.preinstalled")
	link := filepath.Join(g.home, "work", "Linked.app")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	borrowed := g.tccGrantApp(t, "work/Borrowed.app", "com.example.borrowed")
	exe := filepath.Join(borrowed, "Contents", "MacOS", "TestApp")
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", exe); err != nil {
		t.Fatal(err)
	}

	for name, app := range map[string]string{"an app outside the home": outside, "a link out of the home": link,
		"an executable linked out of the home": borrowed} {
		out, err := g.runTCCGrantScoped(t, app, "home")
		if err == nil {
			t.Errorf("%s was approved: %s", name, out)
			continue
		}
		if !strings.Contains(out, "outside the guest home") {
			t.Errorf("%s: the refusal does not say why: %s", name, out)
		}
	}
	for _, id := range []string{"com.example.preinstalled", "com.example.borrowed"} {
		if got := g.sql(t, g.sys, "SELECT count(*) FROM access WHERE indirect_object_identifier='"+id+"'"); got != "0" {
			t.Errorf("a refused app still has %s Apple Events rows for %s", got, id)
		}
	}

	// Unscoped, as the coder's machine_approve_control is, the same app outside is approved.
	if out, err := g.runTCCGrant(t, outside); err != nil {
		t.Errorf("the unscoped grant refused an app outside the home: %v\n%s", err, out)
	}
	if out, err := g.runTCCGrantScoped(t, inside, "everywhere"); err == nil {
		t.Errorf("an unknown scope was accepted: %s", out)
	}
}

// ApproveControlInHome is the verifier's call: the guest script gets the home scope, and the
// step names the seat that approved (ADR 0044, issue #269).
func TestApproveControlInHomeScopesTheScriptAndRecordsTheSeat(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)

	grant, step, err := mgr.ApproveControlInHome(context.Background(), mc.RunID, HolderVerifier, "work/TestApp.app")
	if err != nil {
		t.Fatal(err)
	}
	if grant.BundleID != "com.example.testapp" || step == 0 {
		t.Errorf("bundleId %q step %d, want the fake grant's bundle id and a step", grant.BundleID, step)
	}
	if !strings.Contains(testsupport.Calls(t, control), "sh work/TestApp.app home") {
		t.Errorf("the script was not given the home scope\n%s", testsupport.Calls(t, control))
	}
	if s := stepsOf(t, mgr, mc.RunID)[step]; s.Tool != "machine_approve_control" || s.By != HolderVerifier {
		t.Errorf("step %d = %s by %q, want machine_approve_control by the verifier", step, s.Tool, s.By)
	}

	if _, _, err := mgr.ApproveControlInHome(context.Background(), mc.RunID, HolderVerifier, "work/NotAnApp"); err == nil {
		t.Error("a path that is not an .app bundle was accepted")
	}
}
