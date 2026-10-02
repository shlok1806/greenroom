package machine

import (
	"context"
	"encoding/json"
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

// tccPrompt is the screen issue #283 measured on a real guest: the TCC prompt drawn by
// UserNotificationCenter (not a regular app) at layer 8, over an accessory app under test.
const (
	tccPrompt = `{"windows":[
 {"owner":"Window Server","name":"Menubar","layer":24,"alpha":1,"x":0,"y":0,"width":1024,"height":30,"pid":194},
 {"owner":"UserNotificationCenter","name":"","layer":8,"alpha":1,"x":382,"y":118,"width":260,"height":256,"pid":1132},
 {"owner":"Dock","name":"Dock","layer":20,"alpha":1,"x":0,"y":0,"width":1024,"height":768,"pid":386}],
 "apps":[{"name":"Finder","bundleId":"com.apple.finder","pid":391}]}`
	tccPromptText = "1132\t“tart-guest-agent” wants access to control “PromptCheck”. Allowing control will provide " +
		"access to documents and data in “PromptCheck”, and to perform actions within that app. \n"
)

func TestReportFindsAPromptButNotAnAppsOwnAlertAMenuOrABanner(t *testing.T) {
	d := Desktop{
		Windows: []DesktopWindow{
			{Owner: "UserNotificationCenter", Layer: 8, Alpha: 1, Width: 260, Height: 256, PID: 1132},
			{Owner: "TextEdit", Name: "Save changes?", Layer: 8, Alpha: 1, Width: 300, Height: 120, PID: 500},
			{Owner: "MenuApp", Layer: 101, Alpha: 1, Width: 200, Height: 300, PID: 600},
			{Owner: "Notification Center", Name: "banner", Layer: 8, Alpha: 1, Width: 350, Height: 80, PID: 462},
			{Owner: "PromptCheck", Name: "main", Layer: 0, Alpha: 1, Width: 400, Height: 300, PID: 700},
		},
		Apps: []DesktopApp{{Name: "Finder", BundleID: "com.apple.finder", PID: 391}, {Name: "TextEdit", BundleID: "com.apple.TextEdit", PID: 500}},
	}
	r := d.Report()
	if len(r.Prompts) != 1 || r.Prompts[0].Owner != "UserNotificationCenter" {
		t.Errorf("prompts = %+v, want only UserNotificationCenter's", r.Prompts)
	}
	if r.Clean || len(r.UnexpectedWindows) != 5 {
		t.Errorf("every unexpected window is still a finding: %+v", r.UnexpectedWindows)
	}
}

func TestPromptTextIsOneLine(t *testing.T) {
	got := cleanPromptText("“tart-guest-agent” wants access to control “PromptCheck”.  Data in “PromptCheck”, and more.  ")
	if want := "“tart-guest-agent” wants access to control “PromptCheck”. Data in “PromptCheck”, and more."; got != want {
		t.Errorf("cleanPromptText = %q, want %q", got, want)
	}
}

// watchedMachine is a ready machine whose ExecWatched looks every 100 ms.
func watchedMachine(t *testing.T) (*Manager, *Machine, string) {
	t.Helper()
	mgr, _, control := newTestManager(t, WithPromptLook(100*time.Millisecond))
	return mgr, readyMachine(t, mgr), control
}

// Issue #283: a command the verifier runs that an app blocks on a TCC prompt comes back within
// a look or two, stopped, saying which app prompted, instead of waiting out its timeout.
func TestExecWatchedStopsACommandBlockedOnAPrompt(t *testing.T) {
	mgr, mc, control := watchedMachine(t)
	writeControl(t, control, "exec-sleep", "30")
	writeControl(t, control, "desktop.json", tccPrompt)
	writeControl(t, control, "prompt-text", tccPromptText)

	started := time.Now()
	res, err := mgr.ExecWatched(context.Background(), mc.RunID, HolderVerifier,
		`osascript -e 'tell application id "com.greenroom.e2e.promptcheck" to count windows'`, "", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("the command came back after %s; a prompt should stop it at the first look", took)
	}
	if !res.StoppedForPrompt || res.ExitCode != 143 || !strings.Contains(res.Stdout, "output before the stop") {
		t.Errorf("result %+v, want stopped for the prompt with exit 143 and the output so far", res)
	}
	if res.Desktop == nil || len(res.Desktop.Prompts) != 1 || !strings.Contains(res.Desktop.Prompts[0].Text, "control “PromptCheck”") {
		t.Fatalf("the result does not say which app prompted: %+v", res.Desktop)
	}
	if stops, _ := os.ReadFile(filepath.Join(control, "exec-stops")); strings.TrimSpace(string(stops)) != "TERM" {
		t.Errorf("stops sent %q, want one TERM (the command ended on it)", stops)
	}
	steps, err := mgr.Steps(mc.RunID)
	if err != nil {
		t.Fatal(err)
	}
	step := steps[len(steps)-1]
	output, _ := json.Marshal(step.Output)
	if step.Tool != "machine_exec" || step.By != HolderVerifier || !strings.Contains(string(output), `"stoppedForPrompt":true`) ||
		!strings.Contains(string(output), "PromptCheck") {
		t.Errorf("the step does not record the stop and the prompt: %s %s %s", step.Tool, step.By, output)
	}
	live, _ := mgr.get(mc.RunID)
	mgr.mu.Lock()
	desktop := live.Desktop
	mgr.mu.Unlock()
	if desktop == nil || len(desktop.Prompts) != 1 {
		t.Error("the machine's own Desktop field does not carry the prompt, so machine_wait and the Companion miss it")
	}
	if calls := testsupport.Calls(t, control); strings.Contains(calls, "killall") || strings.Contains(calls, "click") {
		t.Errorf("the prompt itself was touched instead of only reported:\n%s", calls)
	}
}

// A slow command on a clean screen is looked at and left alone.
func TestExecWatchedLeavesASlowCommandOnACleanScreenAlone(t *testing.T) {
	mgr, mc, control := watchedMachine(t)
	writeControl(t, control, "exec-sleep", "1")
	res, err := mgr.ExecWatched(context.Background(), mc.RunID, HolderVerifier, "sleep 1", "", time.Minute)
	if err != nil || res.ExitCode != 0 || res.StoppedForPrompt || res.Desktop != nil {
		t.Fatalf("ExecWatched = %+v, %v; want the command's own end", res, err)
	}
	if _, err := os.Stat(filepath.Join(control, "exec-stops")); err == nil {
		t.Error("a command on a clean screen was stopped")
	}
	if n := strings.Count(testsupport.Calls(t, control), "--desktop"); n < 3 {
		t.Errorf("%d --desktop reads; the watch did not look while the command ran", n)
	}
}

// A prompt stops one command. It stays on screen until it is answered or times out, so the next
// command (a wait for it to go, a build) runs to its end; once a look finds it gone, the same
// request raised again stops a command again.
func TestExecWatchedStopsACommandOncePerPrompt(t *testing.T) {
	mgr, mc, control := watchedMachine(t)
	writeControl(t, control, "exec-sleep", "30")
	writeControl(t, control, "desktop.json", tccPrompt)
	writeControl(t, control, "prompt-text", tccPromptText)
	if res, err := mgr.ExecWatched(context.Background(), mc.RunID, HolderVerifier, "osascript", "", time.Minute); err != nil || !res.StoppedForPrompt {
		t.Fatalf("first command: %+v, %v", res, err)
	}

	writeControl(t, control, "exec-sleep", "1")
	res, err := mgr.ExecWatched(context.Background(), mc.RunID, HolderVerifier, "sleep 1", "", time.Minute)
	if err != nil || res.StoppedForPrompt || res.ExitCode != 0 {
		t.Fatalf("a command run while the reported prompt is still up was stopped: %+v, %v", res, err)
	}

	if err := os.Remove(filepath.Join(control, "desktop.json")); err != nil { // the prompt timed out
		t.Fatal(err)
	}
	if res, err := mgr.ExecWatched(context.Background(), mc.RunID, HolderVerifier, "sleep 1", "", time.Minute); err != nil || res.StoppedForPrompt {
		t.Fatalf("clean screen: %+v, %v", res, err)
	}
	writeControl(t, control, "desktop.json", tccPrompt)
	writeControl(t, control, "exec-sleep", "30")
	if res, err := mgr.ExecWatched(context.Background(), mc.RunID, HolderVerifier, "osascript", "", time.Minute); err != nil || !res.StoppedForPrompt {
		t.Fatalf("the same request raised again after it went did not stop the command: %+v, %v", res, err)
	}
}

// The MCP path reports the same prompt, text included, and never stops the command: its caller
// collects it with machine_exec_wait and decides (ADR 0038 point 3, ADR 0047).
func TestExecWaitReportsThePromptAndNeverStopsTheCommand(t *testing.T) {
	mgr, mc, control := watchedMachine(t)
	writeControl(t, control, "exec-sleep", "3")
	writeControl(t, control, "desktop.json", tccPrompt)
	writeControl(t, control, "prompt-text", tccPromptText)
	st, err := mgr.ExecStart(context.Background(), mc.RunID, "osascript", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.ExecWait(context.Background(), mc.RunID, st.ExecID, 200*time.Millisecond)
	if err != nil || !got.Running {
		t.Fatalf("ExecWait = %+v, %v; want still running", got, err)
	}
	if got.Desktop == nil || len(got.Desktop.Prompts) != 1 || !strings.Contains(got.Desktop.Prompts[0].Text, "PromptCheck") {
		t.Errorf("the MCP result does not say which app prompted: %+v", got.Desktop)
	}
	done, err := mgr.ExecWait(context.Background(), mc.RunID, st.ExecID, 10*time.Second)
	if err != nil || done.Running || done.ExitCode == nil || *done.ExitCode != 0 {
		t.Errorf("the command did not run to its own end: %+v, %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(control, "exec-stops")); err == nil {
		t.Error("the MCP path stopped a command")
	}
}

// The stop script, for real on the host's sh and zsh: it finds the wrapper by its execId, ends
// the command's whole process group (a child included), and the wrapper still prints the output
// so far and exits as the command did. Another wrapper's command is left alone.
func TestStopExecScriptEndsOnlyItsOwnCommand(t *testing.T) {
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("no /bin/zsh")
	}
	home := t.TempDir()
	start := func(id, command string) (*exec.Cmd, *strings.Builder, chan error) {
		cmd := exec.Command(execShell[0], append(slices.Clone(execShell[1:]), "60", id)...)
		cmd.Stdin = strings.NewReader(execScript(command))
		cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home)
		var out strings.Builder
		cmd.Stdout = &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return cmd, &out, done
	}
	_, out, done := start("aaaa11112222", `echo before; sleep 50 & echo $! > "$HOME/child"; wait`)
	_, _, otherDone := start("bbbb33334444", `sleep 50`)

	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(filepath.Join(home, "child")); err == nil && strings.TrimSpace(string(b)) != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never started its child")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, err := exec.Command("/bin/sh", "-c", stopExecScript, "greenroom-exec-stop", "aaaa11112222", "TERM").CombinedOutput(); err != nil {
		t.Fatalf("stop script: %v: %s", err, out)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 143 {
			t.Errorf("the stopped wrapper ended with %v, want exit 143 (TERM)", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stop did not end the command")
	}
	if !strings.Contains(out.String(), "before") {
		t.Errorf("the output so far was lost: %q", out.String())
	}
	b, _ := os.ReadFile(filepath.Join(home, "child"))
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
		time.Sleep(200 * time.Millisecond)
		if syscall.Kill(pid, 0) == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Error("the command's child outlived the stop")
		}
	}
	select {
	case err := <-otherDone:
		t.Errorf("another wrapper's command was stopped too: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}
