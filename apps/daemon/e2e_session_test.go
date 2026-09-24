//go:build tart

// Proves on a real VM that a session's command sees a real terminal (builds branch on isatty) and that
// shell state survives between calls. The fake-tart suite can only check what the daemon asks tart for.
// Boots one VM. Run with:
//
//	go test -tags tart -run TestEndToEndSession -v -timeout 12m .
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

func TestEndToEndSession(t *testing.T) {
	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	created, err := mgr.Create(ctx, defaultImage)
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
	if mc.Status != machine.Ready {
		t.Fatalf("machine did not become ready: %+v", mc)
	}
	t.Logf("machine %s booted in %.1fs", runID, mc.BootSeconds)

	start, err := mgr.SessionStart(ctx, runID, "")
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	t.Logf("session %s started", start.SessionID)

	// Markers are split by quotes so the echoed command line cannot satisfy the assertions.
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID,
		"test -t 0 && echo \"STDIN_IS\"\"_A_TTY\" || echo \"STDIN_IS\"\"_A_PIPE\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID,
		"test -t 1 && echo \"STDOUT_IS\"\"_A_TTY\" || echo \"STDOUT_IS\"\"_A_PIPE\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "tty\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}

	// tty's answer is the last thing written, so wait for it rather than an earlier marker.
	out := collect(ctx, t, mgr, runID, start.SessionID, "/dev/ttys", 60*time.Second)
	t.Logf("session output:\n%s", out)

	if strings.Contains(out, "STDIN_IS_A_PIPE") || !strings.Contains(out, "STDIN_IS_A_TTY") {
		t.Error("the session's stdin is not a terminal, so anything that reads input interactively will not work")
	}
	if strings.Contains(out, "STDOUT_IS_A_PIPE") || !strings.Contains(out, "STDOUT_IS_A_TTY") {
		t.Error("the session's stdout is not a terminal, so builds will take their piped code path")
	}
	if !strings.Contains(out, "/dev/ttys") {
		t.Errorf("tty(1) did not name a pty; output was:\n%s", out)
	}
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "stty size\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	size := collect(ctx, t, mgr, runID, start.SessionID, "40 120", 30*time.Second)
	if !strings.Contains(size, "40 120") {
		t.Errorf("the guest terminal is not 40x120; output was:\n%s", size)
	}

	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "cd /tmp && export GREENROOM_MARK=kept\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "echo \"MARK=$GREENROOM_MARK PWD=$PWD \"\"END\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	// Wait for the split marker: the echoed export line already contains "MARK=".
	out = collect(ctx, t, mgr, runID, start.SessionID, " END", 60*time.Second)
	t.Logf("state output:\n%s", out)
	if !strings.Contains(out, "MARK=kept PWD=") {
		t.Error("an exported variable did not survive to the next call")
	}
	if !strings.Contains(out, "PWD=/tmp END") {
		t.Error("the working directory did not survive to the next call")
	}

	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "false; echo \"EX\"\"IT=$?\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	out = collect(ctx, t, mgr, runID, start.SessionID, "EXIT=", 60*time.Second)
	if !strings.Contains(out, "EXIT=1") {
		t.Errorf("a failing command did not report its status as output:\n%s", out)
	}

	if _, err := mgr.SessionClose(ctx, runID, start.SessionID); err != nil {
		t.Fatalf("SessionClose: %v", err)
	}
	if _, err := mgr.SessionRead(ctx, runID, start.SessionID, 0); err == nil {
		t.Error("reading a closed session succeeded")
	} else if !strings.Contains(err.Error(), "no session") {
		t.Errorf("closed session error was %q, want it to name the missing session", err)
	}
}

// collect joins session reads until want shows up or the budget runs out.
func collect(ctx context.Context, t *testing.T, mgr *machine.Manager, runID, sessionID, want string, budget time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	var all strings.Builder
	for time.Now().Before(deadline) {
		res, err := mgr.SessionRead(ctx, runID, sessionID, 5*time.Second)
		if err != nil {
			t.Fatalf("SessionRead: %v", err)
		}
		all.WriteString(res.Output)
		if strings.Contains(all.String(), want) {
			break
		}
	}
	return all.String()
}
