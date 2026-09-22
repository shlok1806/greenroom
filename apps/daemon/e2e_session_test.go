//go:build tart

// End-to-end proof that an interactive session really gets a terminal.
//
// This is the one claim about sessions that only a real VM can settle. The
// whole reason greenroom runs a build in a session rather than through
// machine_exec is that xcodebuild, swift build, git and most test runners
// branch on isatty(): down a pipe they produce different output, and a
// product whose claim is that it proves a change works must not quietly run
// a different build than the developer does. The fake-tart suite can only
// check that the daemon asks tart for a pty; whether the guest command then
// sees one is a fact about tart and the guest, not about our code.
//
// It also checks the other half of a session, the half machine_exec cannot
// do: that state survives between calls. Boots exactly one VM; a second may
// already be running on the host, and Apple allows two. Run with:
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
	if mc.Status != machine.Ready {
		t.Fatalf("machine did not become ready: %+v", mc)
	}
	t.Logf("machine %s booted in %.1fs", runID, mc.BootSeconds)

	// 1. The claim: a command in a session sees a terminal on stdin and on
	// stdout. `test -t` is the shell's own isatty(), so this is the guest
	// answering about itself, not us reporting what we asked for.
	start, err := mgr.SessionStart(ctx, runID, "")
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	t.Logf("session %s started", start.SessionID)

	// The markers are split by a quote so that the command line, which the
	// terminal echoes back into the output, cannot itself satisfy the
	// assertions below. Without this the test would pass on the echo alone
	// and prove nothing.
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID,
		"test -t 0 && echo \"STDIN_IS\"\"_A_TTY\" || echo \"STDIN_IS\"\"_A_PIPE\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID,
		"test -t 1 && echo \"STDOUT_IS\"\"_A_TTY\" || echo \"STDOUT_IS\"\"_A_PIPE\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	// tty(1) names the pty itself, which is the plainest evidence there is.
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "tty\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}

	out := collect(ctx, t, mgr, runID, start.SessionID, "STDOUT_IS_A_TTY", 60*time.Second)
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
	// The window size the daemon sets has to reach the guest, or build output
	// wraps at whatever tart defaulted to.
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "stty size\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	size := collect(ctx, t, mgr, runID, start.SessionID, "40 120", 30*time.Second)
	if !strings.Contains(size, "40 120") {
		t.Errorf("the guest terminal is not 40x120; output was:\n%s", size)
	}

	// 2. The other half: state survives between calls, which is what
	// machine_exec cannot do. Each send here is a separate round trip.
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "cd /tmp && export GREENROOM_MARK=kept\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "echo \"MARK=$GREENROOM_MARK PWD=$PWD\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	out = collect(ctx, t, mgr, runID, start.SessionID, "MARK=", 60*time.Second)
	t.Logf("state output:\n%s", out)
	if !strings.Contains(out, "MARK=kept") {
		t.Error("an exported variable did not survive to the next call")
	}
	if !strings.Contains(out, "PWD=/tmp") {
		t.Error("the working directory did not survive to the next call")
	}

	// 3. A command that fails inside the session is a result, not an error.
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "false; echo \"EXIT=$?\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	out = collect(ctx, t, mgr, runID, start.SessionID, "EXIT=", 60*time.Second)
	if !strings.Contains(out, "EXIT=1") {
		t.Errorf("a failing command did not report its status as output:\n%s", out)
	}

	if _, err := mgr.SessionClose(ctx, runID, start.SessionID); err != nil {
		t.Fatalf("SessionClose: %v", err)
	}
	// The handle is gone, and the error says so in terms of the session.
	if _, err := mgr.SessionRead(ctx, runID, start.SessionID, 0); err == nil {
		t.Error("reading a closed session succeeded")
	} else if !strings.Contains(err.Error(), "no session") {
		t.Errorf("closed session error was %q, want it to name the missing session", err)
	}
}

// collect reads until want shows up or the budget runs out, joining what the
// separate reads returned. A session's output arrives whenever the guest
// flushes it, so one read is not enough to prove anything.
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
