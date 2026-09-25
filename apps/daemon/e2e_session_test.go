//go:build tart

// Proves on a real VM that a session's command sees a real terminal (builds branch on isatty), that
// shell state survives between calls, that ^C interrupts, and that a 3 MB flood finishes without
// wedging the machine (issue #30). The fake-tart suite can only check what the daemon asks tart for.
// Boots one VM. Run with:
//
//	go test -tags tart -run TestEndToEndSession -v -timeout 12m .
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

func TestEndToEndSession(t *testing.T) {
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

	// ^C reaches the terminal's line discipline and interrupts the foreground command.
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "sleep 120\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	time.Sleep(time.Second)
	interrupted := time.Now()
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "\x03"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	if _, err := mgr.SessionSend(ctx, runID, start.SessionID, "echo \"INTER\"\"RUPTED\"\n"); err != nil {
		t.Fatalf("SessionSend: %v", err)
	}
	out = collect(ctx, t, mgr, runID, start.SessionID, "INTERRUPTED", 30*time.Second)
	if !strings.Contains(out, "INTERRUPTED") || time.Since(interrupted) > 20*time.Second {
		t.Errorf("^C did not interrupt sleep 120 (%s); output was:\n%s", time.Since(interrupted), out)
	}

	if _, err := mgr.SessionClose(ctx, runID, start.SessionID); err != nil {
		t.Fatalf("SessionClose: %v", err)
	}
	if _, err := mgr.SessionRead(ctx, runID, start.SessionID, 0); err == nil {
		t.Error("reading a closed session succeeded")
	} else if !strings.Contains(err.Error(), "no session") {
		t.Errorf("closed session error was %q, want it to name the missing session", err)
	}

	floodSession(ctx, t, mgr, runID)
	closeKillsSession(ctx, t, mgr, runID, true)
	closeKillsSession(ctx, t, mgr, runID, false)
}

// closeKillsSession proves machine_session_close ends the session's guest
// processes and removes its files. Killing the host tart exec reaches none of
// them. With settle false the close races the guest wrapper, which must then
// start nothing.
func closeKillsSession(ctx context.Context, t *testing.T, mgr *machine.Manager, runID string, settle bool) {
	t.Helper()
	secs := 377
	if !settle {
		secs = 388
	}
	probe := fmt.Sprintf("pgrep -fl 'slee[p] %d' || echo NO\"\"NE", secs)
	start, err := mgr.SessionStart(ctx, runID, fmt.Sprintf("sleep %d", secs))
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if settle {
		deadline := time.Now().Add(30 * time.Second)
		for {
			res, err := mgr.Exec(ctx, runID, probe, "", 20*time.Second)
			if err == nil && !strings.Contains(res.Stdout, "NONE") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("sleep %d never started in the guest: %+v %v", secs, res, err)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	t0 := time.Now()
	if _, err := mgr.SessionClose(ctx, runID, start.SessionID); err != nil {
		t.Fatalf("SessionClose: %v", err)
	}
	t.Logf("close (settled=%v) took %.1fs", settle, time.Since(t0).Seconds())
	// A wrapper that lost the race could still start after close returned.
	time.Sleep(5 * time.Second)
	res, err := mgr.Exec(ctx, runID, probe+"; ls \"${TMPDIR:-/tmp}\"/greenroom-session."+start.SessionID+"* 2>/dev/null; true", "", 20*time.Second)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	t.Logf("after close (settled=%v): %q", settle, res.Stdout)
	if !strings.Contains(res.Stdout, "NONE") {
		t.Errorf("sleep %d survived machine_session_close: %q", secs, res.Stdout)
	}
	if strings.Contains(res.Stdout, "greenroom-session.") {
		t.Errorf("session files survived machine_session_close: %q", res.Stdout)
	}
}

// floodSession is issue #30: a session printing 3 MB at full speed stalled
// tart's tty stream after ~200 KB and made every other guest call fail. It
// must finish, and machine_exec must answer while it runs.
func floodSession(ctx context.Context, t *testing.T, mgr *machine.Manager, runID string) {
	t.Helper()
	started := time.Now()
	flood, err := mgr.SessionStart(ctx, runID, `yes | head -c 3000000; echo "DO""NE"`)
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	alive := make(chan error, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		execCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		t0 := time.Now()
		res, err := mgr.Exec(execCtx, runID, "echo alive", "", 20*time.Second)
		if err == nil && !strings.Contains(res.Stdout, "alive") {
			err = errors.New("printed " + res.Stdout)
		}
		t.Logf("machine_exec during the flood took %.1fs", time.Since(t0).Seconds())
		alive <- err
	}()

	var tail string
	var total int64
	finished := false
	for time.Since(started) < 90*time.Second {
		res, err := mgr.SessionRead(ctx, runID, flood.SessionID, 5*time.Second)
		if err != nil {
			t.Fatalf("SessionRead: %v", err)
		}
		total += int64(len(res.Output)) + res.Dropped
		tail = (tail + res.Output)[max(0, len(tail)+len(res.Output)-64):]
		if !res.Running && res.Pending == 0 {
			finished = true
			break
		}
	}
	t.Logf("flood: %d bytes read or dropped in %.1fs", total, time.Since(started).Seconds())
	if !finished || !strings.Contains(tail, "DONE") {
		t.Errorf("the flood did not finish with DONE within 90 s; last output %q", tail)
	}
	if err := <-alive; err != nil {
		t.Errorf("machine_exec failed while a session flooded: %v", err)
	}
	if _, err := mgr.SessionClose(ctx, runID, flood.SessionID); err != nil {
		t.Errorf("SessionClose: %v", err)
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
