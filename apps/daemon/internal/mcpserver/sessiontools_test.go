package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// startSession opens a session and returns its id.
func (h *harness) startSession(runID, command string) string {
	h.t.Helper()
	args := map[string]any{"runId": runID}
	if command != "" {
		args["command"] = command
	}
	var out machine.SessionStartResult
	h.call("machine_session_start", args, &out)
	if out.SessionID == "" {
		h.t.Fatal("machine_session_start returned no sessionId")
	}
	return out.SessionID
}

// readSession reads until something comes back or the attempts run out. The
// fake session is a real subprocess, so its output arrives through a pipe and
// a pump goroutine rather than instantly.
func (h *harness) readSession(runID, sessionID string) machine.SessionReadResult {
	h.t.Helper()
	var out machine.SessionReadResult
	for i := 0; i < 10; i++ {
		h.call("machine_session_read", map[string]any{
			"runId": runID, "sessionId": sessionID, "waitSeconds": 2,
		}, &out)
		if out.Output != "" {
			return out
		}
	}
	return out
}

// A session is the one thing machine_exec cannot do: state that outlives a
// single call. This drives the whole path the way a coder would.
func TestASessionCarriesStateBetweenToolCalls(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")

	var sent machine.SessionSendResult
	h.call("machine_session_send", map[string]any{
		"runId": runID, "sessionId": id, "data": "hello from the session\n",
	}, &sent)
	if sent.Bytes != len("hello from the session\n") {
		t.Errorf("sent %d bytes, want %d", sent.Bytes, len("hello from the session\n"))
	}

	got := h.readSession(runID, id)
	if !strings.Contains(got.Output, "hello from the session") {
		t.Fatalf("the session did not give back what was sent: %q", got.Output)
	}
	if !got.Running {
		t.Error("the session reports it has ended, but nothing stopped it")
	}

	var closed machine.SessionCloseResult
	h.call("machine_session_close", map[string]any{"runId": runID, "sessionId": id}, &closed)
	if closed.SessionID != id {
		t.Errorf("close answered for %q, want %q", closed.SessionID, id)
	}
	// The handle is gone, so the next call must say so rather than hang.
	if res := h.raw("machine_session_read", map[string]any{"runId": runID, "sessionId": id}); !res.IsError {
		t.Error("reading a closed session succeeded")
	}
}

// drainSession reads until a read comes back with nothing, and returns
// everything it saw along with the last result. Draining fully is what makes
// the offset assertions below deterministic: a terminal echoes, so a send
// produces output twice, and a test that read only once could see the halves
// land in different reads.
func (h *harness) drainSession(runID, sessionID string) (string, machine.SessionReadResult) {
	h.t.Helper()
	var all strings.Builder
	var last machine.SessionReadResult
	for i := 0; i < 20; i++ {
		var out machine.SessionReadResult
		h.call("machine_session_read", map[string]any{
			"runId": runID, "sessionId": sessionID, "waitSeconds": 1,
		}, &out)
		if out.FromByte != last.NextByte && i > 0 {
			h.t.Errorf("read %d started at byte %d, want %d where the last one stopped",
				i, out.FromByte, last.NextByte)
		}
		last = out
		if out.Output == "" {
			break
		}
		all.WriteString(out.Output)
	}
	return all.String(), last
}

// Output is read by position and never drained twice: each read continues
// where the last stopped, so nothing is returned again and nothing is skipped.
func TestASessionReadNeverReturnsTheSameOutputTwice(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")

	h.call("machine_session_send", map[string]any{
		"runId": runID, "sessionId": id, "data": "MARKERONE\n",
	}, nil)
	first, afterFirst := h.drainSession(runID, id)
	if !strings.Contains(first, "MARKERONE") {
		t.Fatalf("the first reads missed the output: %q", first)
	}
	if afterFirst.Pending != 0 {
		t.Errorf("%d bytes still pending after draining", afterFirst.Pending)
	}

	// Everything sent so far has been read, so anything the next reads return
	// can only be new.
	h.call("machine_session_send", map[string]any{
		"runId": runID, "sessionId": id, "data": "MARKERTWO\n",
	}, nil)
	second, _ := h.drainSession(runID, id)
	if strings.Contains(second, "MARKERONE") {
		t.Errorf("a later read repeated output an earlier one already gave: %q", second)
	}
	if !strings.Contains(second, "MARKERTWO") {
		t.Errorf("the later reads missed the new output: %q", second)
	}
}

// A terminal echoes what is typed at it, which is deliberate: it is what a
// real terminal does, and it puts the command next to its output in the run
// record, which matters because the send itself records only a byte count.
func TestASessionEchoesWhatIsTypedAtIt(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")
	h.call("machine_session_send", map[string]any{
		"runId": runID, "sessionId": id, "data": "ECHOCHECK\n",
	}, nil)
	out, _ := h.drainSession(runID, id)
	// The fake session gives back what it is sent, so the echo shows up as a
	// second copy: one from the terminal, one from the command.
	if strings.Count(out, "ECHOCHECK") < 2 {
		t.Errorf("the terminal did not echo the input; output was %q", out)
	}
}

// The pty is the reason this feature exists: xcodebuild and friends branch on
// isatty(), so a session that is not given a terminal is a different build.
// The flags have to come before the VM name or tart reads them as part of the
// command.
func TestASessionAsksTartForATerminal(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.startSession(runID, "swift build")

	// Starting a session only forks the child, so the call it makes reaches
	// the log a moment later.
	var line string
	for i := 0; i < 50 && line == ""; i++ {
		for _, c := range strings.Split(testsupport.Calls(t, h.control), "\n") {
			if strings.HasPrefix(c, "exec -i -t ") {
				line = c
			}
		}
		if line == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if line == "" {
		t.Fatalf("no `tart exec -i -t` call was made; calls were:\n%s", testsupport.Calls(t, h.control))
	}
	if !strings.Contains(line, "swift build") {
		t.Errorf("the session did not carry the command: %q", line)
	}
	// The VM name must follow the flags, not precede them.
	rest := strings.TrimPrefix(line, "exec -i -t ")
	if !strings.HasPrefix(rest, "greenroom-") {
		t.Errorf("the VM name does not follow the flags: %q", line)
	}

	// Asking for -t is not enough. Real tart reads the window size from its
	// own stdin to forward to the guest and dies when that is a pipe, so the
	// daemon has to hand it a host pty with a size already set. Driving it
	// through pipes made every session dead on arrival once; this is the
	// assertion that keeps it fixed without booting a VM.
	var stdin string
	for i := 0; i < 50 && stdin == ""; i++ {
		if b, err := os.ReadFile(filepath.Join(h.control, "session-stdin")); err == nil {
			stdin = strings.TrimSpace(string(b))
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !strings.HasPrefix(stdin, "tty") {
		t.Fatalf("tart was given %q for stdin, want a terminal", stdin)
	}
	if !strings.Contains(stdin, "40 120") {
		t.Errorf("the terminal reported size %q, want the 40x120 the daemon sets", stdin)
	}
}

// A destroyed machine takes its sessions with it, and says so in those terms.
// A handle that answered with something about a missing process would send a
// caller looking for a fault that is not there.
func TestSessionsDieWithTheirMachine(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")

	h.call("machine_destroy", map[string]any{"runId": runID}, nil)

	for _, tool := range []string{"machine_session_send", "machine_session_read", "machine_session_close"} {
		args := map[string]any{"runId": runID, "sessionId": id}
		if tool == "machine_session_send" {
			args["data"] = "anyone there\n"
		}
		res := h.raw(tool, args)
		if !res.IsError {
			t.Errorf("%s answered for a destroyed machine", tool)
			continue
		}
		if msg := text(res); !strings.Contains(msg, "no machine for run") {
			t.Errorf("%s said %q, want it to name the missing machine", tool, msg)
		}
	}
}

func TestUnknownSessionIsAReadableError(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	res := h.raw("machine_session_read", map[string]any{"runId": runID, "sessionId": "nope"})
	if !res.IsError {
		t.Fatal("an unknown sessionId was accepted")
	}
	if msg := text(res); !strings.Contains(msg, "no session") {
		t.Errorf("error was %q, want it to name the missing session", msg)
	}
}

// A command that has finished is not waited on, and its output is still there
// to collect.
func TestAFinishedSessionStopsReportingItselfRunning(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if err := os.WriteFile(filepath.Join(h.control, "session-output"), []byte("build succeeded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testsupport.Flag(t, h.control, "session-exits")

	id := h.startSession(runID, "xcodebuild")
	got := h.readSession(runID, id)
	if !strings.Contains(got.Output, "build succeeded") {
		t.Errorf("the output of a finished command was lost: %q", got.Output)
	}
	if got.Running {
		t.Error("a command that has exited is still reported as running")
	}
}

// A session tart refuses must not look healthy. tart reports this by exiting
// straight away rather than by failing the spawn, so what the caller has to
// see is a session that is not running.
func TestASessionTartRefusesDoesNotLookHealthy(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	testsupport.Flag(t, h.control, "fail-session")

	res := h.raw("machine_session_start", map[string]any{"runId": runID})
	if res.IsError {
		return // refused outright, which is also a correct answer
	}
	var start machine.SessionStartResult
	data, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(data, &start); err != nil {
		t.Fatal(err)
	}
	var got machine.SessionReadResult
	for i := 0; i < 20 && (i == 0 || got.Running); i++ {
		h.call("machine_session_read", map[string]any{
			"runId": runID, "sessionId": start.SessionID, "waitSeconds": 1,
		}, &got)
	}
	if got.Running {
		t.Error("a session tart refused still reports itself running")
	}
}

// Every session call is evidence, like every other tool call.
func TestSessionCallsAreRecordedAsSteps(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")
	h.call("machine_session_send", map[string]any{"runId": runID, "sessionId": id, "data": "x\n"}, nil)
	h.readSession(runID, id)
	h.call("machine_session_close", map[string]any{"runId": runID, "sessionId": id}, nil)

	steps, err := machine.ReadSteps(filepath.Join(h.root, "runs", runID))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"machine_session_start": false, "machine_session_send": false,
		"machine_session_read": false, "machine_session_close": false,
	}
	for _, s := range steps {
		if _, ok := want[s.Tool]; ok {
			want[s.Tool] = true
		}
	}
	for tool, found := range want {
		if !found {
			t.Errorf("%s left no step in the run record", tool)
		}
	}
}

// What a caller types into a session can be a password. The record keeps the
// shape of what happened without keeping the secret.
func TestSessionSendRecordsItsSizeButNotItsContent(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")
	const secret = "hunter2-not-in-the-record\n"
	h.call("machine_session_send", map[string]any{"runId": runID, "sessionId": id, "data": secret}, nil)

	raw, err := os.ReadFile(filepath.Join(h.root, "runs", runID, "steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Error("the run record holds what was typed into the session")
	}
	if !strings.Contains(string(raw), "machine_session_send") {
		t.Error("the send left no step at all")
	}
}
