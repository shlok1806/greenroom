package mcpserver

import (
	"encoding/json"
	"os"
	"os/exec"
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

// readSession reads until output arrives or the attempts run out; the fake session is a real subprocess.
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
	if res := h.raw("machine_session_read", map[string]any{"runId": runID, "sessionId": id}); !res.IsError {
		t.Error("reading a closed session succeeded")
	}
}

// drainSession reads until a read is empty, checking each starts where the last stopped. Draining fully keeps
// assertions deterministic: the pty echoes, so one send's output can span reads.
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

// Echo is deliberate: it puts the command next to its output, and the send step records only a byte count.
func TestASessionEchoesWhatIsTypedAtIt(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")
	h.call("machine_session_send", map[string]any{
		"runId": runID, "sessionId": id, "data": "ECHOCHECK\n",
	}, nil)
	out, _ := h.drainSession(runID, id)
	// One copy from the terminal's echo, one from cat.
	if strings.Count(out, "ECHOCHECK") < 2 {
		t.Errorf("the terminal did not echo the input; output was %q", out)
	}
}

// Issue #30, ADR 0017: tart's own tty mode stalls on fast output and wedges the machine, so a
// session must reach tart as `exec -i <vm>` on a plain pipe, never `-t`, and never a host pty. The
// terminal is made in the guest by the wrapper.
func TestASessionNeverAsksTartForATerminal(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "swift build")

	// The forked child logs its call a moment after start returns.
	var stdin string
	for i := 0; i < 50 && stdin == ""; i++ {
		if b, err := os.ReadFile(filepath.Join(h.control, "session-stdin")); err == nil {
			stdin = strings.TrimSpace(string(b))
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if stdin != "pipe" {
		t.Fatalf("tart was given %q for stdin, want a pipe", stdin)
	}
	calls := testsupport.Calls(t, h.control)
	var start string
	for _, c := range strings.Split(calls, "\n") {
		if strings.HasPrefix(c, "exec -i ") && !strings.HasSuffix(c, " --serve") {
			start = c
		}
		if strings.HasPrefix(c, "exec -t ") || strings.HasPrefix(c, "exec -i -t ") {
			t.Errorf("a session asked tart for a terminal: %q", c)
		}
	}
	if !strings.HasPrefix(strings.TrimPrefix(start, "exec -i "), "greenroom-") {
		t.Errorf("the VM name does not follow -i: %q", start)
	}
	if !strings.Contains(calls, "greenroom-session "+id+" swift build") {
		t.Errorf("the session did not carry its id and command; calls were:\n%s", calls)
	}
}

// A session's output lives in a guest file until close, and closing must end the command there too:
// killing the host `tart exec` alone leaves the guest command running.
func TestClosingASessionEndsItsGuestSide(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")
	h.call("machine_session_send", map[string]any{"runId": runID, "sessionId": id, "data": "CLOSEME\n"}, nil)
	if got := h.readSession(runID, id); !strings.Contains(got.Output, "CLOSEME") {
		t.Fatalf("the session gave no output before close: %q", got.Output)
	}
	file := filepath.Join(h.control, "greenroom-session."+id)
	pid, err := os.ReadFile(file + ".pid")
	if err != nil {
		t.Fatalf("the session left no pid file: %v", err)
	}

	h.call("machine_session_close", map[string]any{"runId": runID, "sessionId": id}, nil)

	for _, f := range []string{file, file + ".pid"} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s is still there after close (%v)", filepath.Base(f), err)
		}
	}
	if !strings.Contains(testsupport.Calls(t, h.control), "greenroom-session-close "+id) {
		t.Error("close never ran the guest cleanup")
	}
	// The guest-side script(1) is a host process under the fake tart.
	if out, _ := exec.Command("ps", "-o", "comm=", "-p", strings.TrimSpace(string(pid))).Output(); strings.Contains(string(out), "script") {
		t.Errorf("the session's script(1) (pid %s) still runs after close", strings.TrimSpace(string(pid)))
	}
}

// A command that prints far faster than anyone reads must neither stall nor hide its end: the reader
// learns what was dropped and still gets the last bytes and the exit.
func TestAFloodingSessionSkipsToItsEndAndSaysWhatItDropped(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	flood := strings.Repeat("y\n", 1_500_000) + "DONE\n" // 3 MB, three windows
	if err := os.WriteFile(filepath.Join(h.control, "session-output"), []byte(flood), 0o644); err != nil {
		t.Fatal(err)
	}
	testsupport.Flag(t, h.control, "session-exits")
	id := h.startSession(runID, "yes | head -c 3000000; echo DONE")

	var all strings.Builder
	var dropped int64
	var last machine.SessionReadResult
	for i := 0; i < 40 && (i == 0 || last.Running || last.Pending > 0); i++ {
		last = machine.SessionReadResult{} // omitted fields must not keep the previous read's values
		h.call("machine_session_read", map[string]any{"runId": runID, "sessionId": id, "waitSeconds": 2}, &last)
		all.WriteString(last.Output)
		dropped += last.Dropped
	}
	if last.Running {
		t.Fatal("the flooding session never finished")
	}
	if !strings.HasSuffix(all.String(), "DONE\n") {
		t.Errorf("the last output was lost; it ends %q", all.String()[max(0, all.Len()-20):])
	}
	if dropped == 0 {
		t.Error("3 MB passed through a 1 MiB window and nothing was reported dropped")
	}
	if got := int64(all.Len()) + dropped; got != int64(len(flood)) {
		t.Errorf("read %d bytes and dropped %d, which is %d; the command printed %d",
			all.Len(), dropped, got, len(flood))
	}
}

// A destroyed machine's sessions answer "no machine for run", not a process error.
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
	// A read wakes on output, which can land just before the command exits.
	for i := 0; i < 20 && got.Running; i++ {
		h.call("machine_session_read", map[string]any{"runId": runID, "sessionId": id, "waitSeconds": 1}, &got)
	}
	if got.Running {
		t.Error("a command that has exited is still reported as running")
	}
	if got.Error != "" {
		t.Errorf("a command that finished was reported as a tart failure: %q", got.Error)
	}
}

// Issue #62: a finished session says how its command ended, as machine_exec does.
func TestAFinishedSessionReportsItsExitCode(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	testsupport.Flag(t, h.control, "session-exits")
	if err := os.WriteFile(filepath.Join(h.control, "session-exit-code"), []byte("7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := h.startSession(runID, "exit 7")

	var raw map[string]any
	for i := 0; i < 20 && (i == 0 || raw["running"] == true); i++ {
		h.call("machine_session_read", map[string]any{"runId": runID, "sessionId": id, "waitSeconds": 1}, &raw)
	}
	if raw["running"] != false {
		t.Fatalf("the session never finished: %v", raw)
	}
	if code, ok := raw["exitCode"].(float64); !ok || code != 7 {
		t.Errorf("a session whose command exited 7 read back %v, want exitCode 7", raw)
	}
	if raw["error"] != nil {
		t.Errorf("a non-zero exit was reported as a tart failure: %v", raw["error"])
	}
}

// A running session has no exit code yet, and says so by leaving the field out.
func TestARunningSessionHasNoExitCode(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	id := h.startSession(runID, "")
	var raw map[string]any
	h.call("machine_session_read", map[string]any{"runId": runID, "sessionId": id, "waitSeconds": 1}, &raw)
	if raw["running"] != true {
		t.Fatalf("the session is not running: %v", raw)
	}
	if _, ok := raw["exitCode"]; ok {
		t.Errorf("a running session reported an exit code: %v", raw)
	}
}

// tart refuses a session by exiting at once, so the caller must see it not running, with tart's reason.
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
	if !strings.Contains(got.Error, "VM is not running") {
		t.Errorf("a session tart refused gave error %q, want tart's own reason", got.Error)
	}
}

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

// Session input can be a password, so the record keeps its size only.
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
