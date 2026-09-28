package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// fastAgent are guest agent limits short enough for tests: a channel is found dead in 300 ms, a
// read degrades 300 ms after it went down, and a lost agent is restarted after 50 ms.
func fastAgent() agentTimes {
	return agentTimes{degradeAfter: 300 * time.Millisecond, bootWait: 5 * time.Second, sup: guestagent.SupervisorOptions{
		Conn: guestagent.Options{PingInterval: 50 * time.Millisecond, PongTimeout: 300 * time.Millisecond,
			CallGrace: 200 * time.Millisecond, WedgeGrace: 300 * time.Millisecond},
		Backoff: []time.Duration{50 * time.Millisecond}, StableAfter: time.Hour}}
}

// agentManager is a test manager with the desktop toolkit on.
func agentManager(t *testing.T, times agentTimes, extra ...Option) (*Manager, string) {
	t.Helper()
	mgr, _, control := newTestManager(t, append([]Option{WithDesktopToolkit(true), withAgentTimes(times)}, extra...)...)
	return mgr, control
}

// smallDesktop makes the fake guest a 64x48 screen showing one app with one text element, so a
// UI read runs the render check (a capture and a window list) too.
func smallDesktop(t *testing.T, control string) {
	t.Helper()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(control, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("screen", "64x48")
	write("shot.b64", shotBase64(t, 64, 48))
	write("ui.json", `{"app":{"name":"Calc","bundleId":"com.example.calc","pid":7},"apps":["Finder","Calc"],`+
		`"screen":{"width":64,"height":48},"elements":[{"role":"AXStaticText","title":"Total","depth":0,`+
		`"frame":{"x":4,"y":4,"w":20,"h":10}}],"truncated":false}`)
}

// tartCalls is calls.log as one entry per tart invocation: a script argument spans lines, so
// a line continues the call before it unless it starts a new one.
func tartCalls(t *testing.T, control string) []string {
	t.Helper()
	var calls []string
	for _, line := range strings.Split(strings.TrimRight(testsupport.Calls(t, control), "\n"), "\n") {
		starts := strings.HasPrefix(line, "exec greenroom-") || strings.HasPrefix(line, "exec -i greenroom-") || line == "list" ||
			line == "--version"
		for _, sub := range []string{"clone ", "run ", "ip ", "stop ", "delete ", "list "} {
			starts = starts || strings.HasPrefix(line, sub)
		}
		if starts || len(calls) == 0 {
			calls = append(calls, line)
		} else {
			calls[len(calls)-1] += "\n" + line
		}
	}
	return calls
}

// execsAfter is every tart exec after the first n calls, the agent's starts aside.
func execsAfter(t *testing.T, control string, n int) (execs []string, agentStarts int) {
	t.Helper()
	calls := tartCalls(t, control)
	for _, call := range calls[min(n, len(calls)):] {
		switch {
		case testsupport.IsAgentStart(call):
			agentStarts++
		case strings.HasPrefix(call, "exec "):
			execs = append(execs, call)
		}
	}
	return execs, agentStarts
}

func callCount(t *testing.T, control string) int {
	t.Helper()
	return len(tartCalls(t, control))
}

// desktopWork is n desktop operations of every kind a verifier makes: inputs, UI reads (each
// with its render check's capture and window list) and screenshots.
func desktopWork(t *testing.T, mgr *Manager, runID string, n int) {
	t.Helper()
	ctx := context.Background()
	x, y := 0.5, 0.5
	for i := range n {
		var err error
		switch i % 3 {
		case 0:
			_, err = mgr.InputAs(ctx, runID, HolderCoder, []InputAction{{Type: "click", X: &x, Y: &y}, {Type: "type", Text: "7"}})
		case 1:
			var tree UITree
			tree, err = mgr.UI(ctx, runID, HolderCoder, "", 0)
			if err == nil && (len(tree.Elements) != 1 || tree.Unrendered != "" || tree.Degraded) {
				err = fmt.Errorf("read %d: %+v", i, tree)
			}
		case 2:
			var shot Shot
			_, shot, err = mgr.Screenshot(ctx, runID)
			if err == nil && (shot.Width != 64 || shot.Scale != 1 || shot.Degraded) {
				err = fmt.Errorf("shot %d: %+v", i, shot)
			}
		}
		if err != nil {
			t.Fatalf("operation %d: %v", i, err)
		}
	}
}

// The fd proof (daemon ADR 0005, #186): with the toolkit on, 500 desktop operations and the
// frame recorder start exactly one `tart exec -i ... --agent` after boot and no other exec;
// with it off, every operation is at least one exec.
func TestTheDesktopToolkitDrivesTheDesktopThroughOneExec(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		mgr, control := agentManager(t, fastAgent(), WithFrameInterval(20*time.Millisecond))
		smallDesktop(t, control)
		mc := readyMachine(t, mgr)
		if n := testsupport.AgentStarts(t, control); n != 1 {
			t.Fatalf("boot started the agent %d times, want 1", n)
		}
		mark := callCount(t, control)
		desktopWork(t, mgr, mc.RunID, 500)
		frames, _ := ReadFrames(mc.Dir)
		execs, starts := execsAfter(t, control, mark)
		if len(execs) != 0 || starts != 0 {
			t.Fatalf("after boot: %d more agent starts and %d other execs, want none; first: %q", starts, len(execs), execs[:min(3, len(execs))])
		}
		if len(frames) == 0 {
			t.Fatal("the recorder wrote no frame over the channel")
		}
		reqs := testsupport.ControlLines(t, control, "agent-requests")
		if len(reqs) < 500 {
			t.Fatalf("the agent got %d requests, want at least 500", len(reqs))
		}
		if got := mgr.List()[0].AgentReconnects; got != 0 {
			t.Fatalf("AgentReconnects %d, want 0", got)
		}
	})
	t.Run("off", func(t *testing.T) {
		mgr, _, control := newTestManager(t)
		smallDesktop(t, control)
		mc := readyMachine(t, mgr)
		mark := callCount(t, control)
		const n = 60
		desktopWork(t, mgr, mc.RunID, n)
		execs, starts := execsAfter(t, control, mark)
		if starts != 0 || testsupport.AgentStarts(t, control) != 0 {
			t.Fatal("the agent was started with the toolkit off")
		}
		if len(execs) < n {
			t.Fatalf("%d operations made %d execs, want at least one each", n, len(execs))
		}
	})
}

func TestBootRecordsTheGuestAgentAndNeverFailsOnIt(t *testing.T) {
	mgr, _ := agentManager(t, fastAgent())
	mc := readyMachine(t, mgr)
	boot := bootStep(t, mc.Dir)
	if _, ok := boot["guestAgentSeconds"]; !ok || boot["agentError"] != nil {
		t.Fatalf("boot step %v, want guestAgentSeconds and no agentError", boot)
	}

	times := fastAgent()
	times.bootWait = 200 * time.Millisecond
	mgr, control := agentManager(t, times)
	testsupport.Flag(t, control, "fail-agent")
	mc = readyMachine(t, mgr)
	boot = bootStep(t, mc.Dir)
	if e, _ := boot["agentError"].(string); !strings.Contains(e, "VM is not running") {
		t.Fatalf("boot step %v, want agentError naming why the agent did not start", boot)
	}
	// The supervisor keeps trying, and the machine is driven by exec meanwhile.
	if err := os.Remove(filepath.Join(control, "fail-agent")); err != nil {
		t.Fatal(err)
	}
	conn, err := mgr.agentSupervisor(mustGet(t, mgr, mc.RunID)).Conn(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if conn.Gen() != 1 {
		t.Fatalf("the first connection after failed starts has gen %d, want 1", conn.Gen())
	}
}

func mustGet(t *testing.T, mgr *Manager, runID string) *Machine {
	t.Helper()
	mc, err := mgr.get(runID)
	if err != nil {
		t.Fatal(err)
	}
	return mc
}

// Killing the agent mid-request fails that call by name at once, never posts the input by
// another path, and the channel is back, one generation on, well within 10 s. Default timings.
func TestKillingTheAgentMidRequestFailsTheCallAndTheChannelComesBack(t *testing.T) {
	mgr, _, control := newTestManager(t, WithDesktopToolkit(true))
	smallDesktop(t, control)
	mc := readyMachine(t, mgr)
	sup := mgr.agentSupervisor(mustGet(t, mgr, mc.RunID))
	first, err := sup.Conn(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	mark := callCount(t, control)
	testsupport.Flag(t, control, "agent-exit")
	x, y := 0.5, 0.5
	start := time.Now()
	res, err := mgr.InputAs(context.Background(), mc.RunID, HolderCoder, []InputAction{{Type: "click", X: &x, Y: &y}})
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the lost call failed after %s, want well under 7 s", took)
	}
	if !errors.Is(err, guestagent.ErrLost) || !strings.Contains(err.Error(), "may or may not have been posted") {
		t.Fatalf("got %v, want the input to fail with ErrLost", err)
	}
	if res.Degraded {
		t.Fatal("a batch sent on the channel was reported degraded")
	}
	if err := os.Remove(filepath.Join(control, "agent-exit")); err != nil {
		t.Fatal(err)
	}
	second, err := sup.Conn(context.Background(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 10*time.Second || second.Gen() != first.Gen()+1 {
		t.Fatalf("channel back after %s with gen %d, want gen %d in under 10 s", took, second.Gen(), first.Gen()+1)
	}
	if got := mgr.List()[0].AgentReconnects; got != 1 {
		t.Fatalf("AgentReconnects %d, want 1", got)
	}
	execs, starts := execsAfter(t, control, mark)
	if starts != 1 || len(execs) != 0 {
		t.Fatalf("after the loss: %d agent starts and execs %q; the input must not be posted another way", starts, execs)
	}
	if lines := testsupport.ControlLines(t, control, "agent-input"); len(lines) != 0 {
		t.Fatalf("the dead agent posted %v", lines)
	}
	state, err := os.ReadFile(filepath.Join(mgr.Root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "agentReconnects") {
		t.Fatal("state.json carries the reconnect count; only copies may")
	}
	if _, err := mgr.InputAs(context.Background(), mc.RunID, HolderCoder, []InputAction{{Type: "click", X: &x, Y: &y}}); err != nil {
		t.Fatalf("an input on the new connection: %v", err)
	}
}

// An input the channel carried is never posted again by the exec path when the channel dies
// under it, however it dies: here the agent stops answering PINGs mid-batch.
func TestAnInputIsNeverPostedTwice(t *testing.T) {
	mgr, control := agentManager(t, fastAgent())
	smallDesktop(t, control)
	mc := readyMachine(t, mgr)
	if _, err := mgr.ScreenOf(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	mark := callCount(t, control)
	if err := os.WriteFile(filepath.Join(control, "agent-input-sleep"), []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		for len(testsupport.ControlLines(t, control, "agent-requests")) == 0 ||
			!strings.Contains(strings.Join(testsupport.ControlLines(t, control, "agent-requests"), ""), `"op":"input"`) {
			time.Sleep(5 * time.Millisecond)
		}
		testsupport.Flag(t, control, "agent-nopong")
	}()
	_, err := mgr.InputAs(context.Background(), mc.RunID, HolderCoder, []InputAction{{Type: "key", Key: "a"}})
	if !errors.Is(err, guestagent.ErrLost) {
		t.Fatalf("got %v, want ErrLost", err)
	}
	execs, _ := execsAfter(t, control, mark)
	for _, e := range execs {
		if strings.Contains(e, "--json-base64") {
			t.Fatalf("the input was posted again by exec: %q", e)
		}
	}
	inputs := 0
	for _, r := range testsupport.ControlLines(t, control, "agent-requests") {
		if strings.Contains(r, `"op":"input"`) {
			inputs++
		}
	}
	if inputs != 1 {
		t.Fatalf("the input went to the agent %d times, want once", inputs)
	}
}

// A human who takes the screen while another seat's input runs pauses the agent, which stops
// that input; giving the screen back resumes it (daemon ADR 0005 point 14).
func TestAHumanTakeMidInputPausesTheAgentAndGiveBackResumes(t *testing.T) {
	mgr, control := agentManager(t, fastAgent())
	smallDesktop(t, control)
	mc := readyMachine(t, mgr)
	if _, err := mgr.ScreenOf(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	// A coder lease short enough to lapse while its batch runs.
	if _, _, err := mgr.TakeControl(mc.RunID, HolderCoder, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var inputErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, inputErr = mgr.Input(context.Background(), mc.RunID, HolderCoder, []InputAction{{Type: "sleep", MS: 3000}})
	}()
	waitUntil(t, 2*time.Second, "the batch reached the agent", func() bool {
		return len(testsupport.ControlLines(t, control, "agent-input")) == 1
	})
	time.Sleep(200 * time.Millisecond) // the coder's lease lapses
	if _, fresh, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil || !fresh {
		t.Fatalf("the human take: fresh %v, %v", fresh, err)
	}
	wg.Wait()
	var ae *guestagent.Error
	if !errors.As(inputErr, &ae) || ae.Code != guestagent.CodePaused {
		t.Fatalf("the running input ended with %v, want the agent's paused", inputErr)
	}
	if _, _, err := mgr.ReleaseControl(mc.RunID, "human"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, "RESUME", func() bool { return len(testsupport.ControlLines(t, control, "agent-control")) >= 2 })
	got := testsupport.ControlLines(t, control, "agent-control")
	if got[0] != `pause {"holder":"human"}` || got[1] != "resume {}" {
		t.Fatalf("agent-control %q, want PAUSE for the human then RESUME", got)
	}
	// The agents' own per-call leases never pause anything.
	x, y := 0.5, 0.5
	if _, err := mgr.InputAs(context.Background(), mc.RunID, HolderVerifier, []InputAction{{Type: "click", X: &x, Y: &y}}); err != nil && !errors.Is(err, ErrStaleLook) {
		t.Fatal(err)
	}
	if n := len(testsupport.ControlLines(t, control, "agent-control")); n != 2 {
		t.Fatalf("a verifier input sent %d control frames", n-2)
	}
}

func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Once the channel has been down past the threshold, reads fall back to their one-shot exec and
// record degraded: true; an input that nothing was sent for falls back too, recorded the same way.
func TestReadsDegradeToExecOnceTheChannelHasBeenDownPastTheThreshold(t *testing.T) {
	mgr, control := agentManager(t, fastAgent())
	smallDesktop(t, control)
	mc := readyMachine(t, mgr)
	ctx := context.Background()
	if _, err := mgr.UI(ctx, mc.RunID, HolderCoder, "", 0); err != nil {
		t.Fatal(err)
	}
	testsupport.Flag(t, control, "fail-agent")
	testsupport.Flag(t, control, "agent-exit")
	if _, _, err := mgr.Screenshot(ctx, mc.RunID); !errors.Is(err, guestagent.ErrLost) {
		t.Fatalf("a screenshot the agent died on: %v, want ErrLost", err)
	}
	mark := callCount(t, control)
	start := time.Now()
	tree, err := mgr.UI(ctx, mc.RunID, HolderCoder, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 200*time.Millisecond || took > 3*time.Second {
		t.Fatalf("the read fell back after %s, want after the 300 ms threshold", took)
	}
	if !tree.Degraded || len(tree.Elements) != 1 || tree.Unrendered != "" {
		t.Fatalf("got %+v, want a degraded tree with its render check", tree)
	}
	_, shot, err := mgr.Screenshot(ctx, mc.RunID)
	if err != nil || !shot.Degraded {
		t.Fatalf("screenshot %+v, %v, want a degraded shot", shot, err)
	}
	x, y := 0.5, 0.5
	res, err := mgr.InputAs(ctx, mc.RunID, HolderCoder, []InputAction{{Type: "click", X: &x, Y: &y}})
	if err != nil || !res.Degraded {
		t.Fatalf("input %+v, %v, want a degraded input", res, err)
	}
	execs, _ := execsAfter(t, control, mark)
	joined := strings.Join(execs, "\n")
	for _, want := range []string{"--ui-base64", "--desktop", "screencapture", "--json-base64"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %s exec among %d after the channel went down", want, len(execs))
		}
	}
	steps, err := ReadSteps(mc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	degraded := map[string]bool{}
	for _, s := range steps[len(steps)-3:] {
		b, _ := json.Marshal(s.Output)
		degraded[s.Tool] = strings.Contains(string(b), `"degraded":true`)
	}
	if !degraded["machine_ui"] || !degraded["machine_screenshot"] || !degraded["machine_input"] {
		t.Fatalf("steps record degraded as %v, want all three", degraded)
	}
}

// While the agent reports a stalled screen, captures and UI reads fail at once, and work again
// once it recovers.
func TestAStalledAgentFailsLooksAtOnce(t *testing.T) {
	mgr, control := agentManager(t, fastAgent())
	smallDesktop(t, control)
	mc := readyMachine(t, mgr)
	ctx := context.Background()
	sup := mgr.agentSupervisor(mustGet(t, mgr, mc.RunID))
	if err := os.WriteFile(filepath.Join(control, "agent-stalled"), []byte("capture"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, "stalled", func() bool { _, st := sup.Stalled(); return st })
	start := time.Now()
	_, _, shotErr := mgr.Screenshot(ctx, mc.RunID)
	_, uiErr := mgr.UI(ctx, mc.RunID, HolderCoder, "", 0)
	if !errors.Is(shotErr, ErrScreenNotAnswering) || !errors.Is(uiErr, ErrScreenNotAnswering) || time.Since(start) > time.Second {
		t.Fatalf("while stalled: screenshot %v, UI %v after %s; want both to fail at once", shotErr, uiErr, time.Since(start))
	}
	if !strings.Contains(shotErr.Error(), "machine_reboot") {
		t.Fatalf("the error does not say how to recover: %v", shotErr)
	}
	if err := os.Remove(filepath.Join(control, "agent-stalled")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, "recovered", func() bool { _, st := sup.Stalled(); return !st })
	if _, _, err := mgr.Screenshot(ctx, mc.RunID); err != nil {
		t.Fatal(err)
	}
}

// A capture that takes longer than its deadline is a screen that did not answer, as on the exec
// path, and counts toward the recorder's backoff.
func TestAnAgentCaptureDeadlineIsAScreenThatDidNotAnswer(t *testing.T) {
	mgr, control := agentManager(t, fastAgent(), withLookTimes(lookTimes{capture: 200 * time.Millisecond, ui: time.Second,
		grace: 100 * time.Millisecond, cap: 5 * time.Second}))
	smallDesktop(t, control)
	mc := readyMachine(t, mgr)
	if err := os.WriteFile(filepath.Join(control, "agent-capture-sleep"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := mgr.Screenshot(context.Background(), mc.RunID)
	if !errors.Is(err, ErrScreenNotAnswering) {
		t.Fatalf("got %v, want ErrScreenNotAnswering", err)
	}
	if n := mustGet(t, mgr, mc.RunID).input.capture.timeoutStreak(); n != 1 {
		t.Fatalf("timeout streak %d, want 1", n)
	}
	cancels := strings.Join(testsupport.ControlLines(t, control, "agent-control"), "\n")
	if !strings.Contains(cancels, "cancel ") {
		t.Fatalf("no CANCEL for the abandoned capture: %q", cancels)
	}
}

// The capture-approval check goes over the channel as the sh op; only a stale record's rewrite,
// which kills replayd, stays an exec.
func TestTheCaptureApprovalCheckGoesOverTheChannel(t *testing.T) {
	mgr, control := agentManager(t, fastAgent())
	smallDesktop(t, control)
	mc := readyMachine(t, mgr)
	machine := mustGet(t, mgr, mc.RunID)
	due := func() {
		machine.input.approval.mu.Lock()
		machine.input.approval.checked = time.Time{}
		machine.input.approval.mu.Unlock()
	}
	due()
	mark := callCount(t, control)
	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if execs, _ := execsAfter(t, control, mark); len(execs) != 0 {
		t.Fatalf("a current record made execs %q", execs)
	}
	var sh []string
	for _, r := range testsupport.ControlLines(t, control, "agent-requests") {
		if strings.Contains(r, `"op":"sh"`) {
			sh = append(sh, r)
		}
	}
	if len(sh) != 1 || !strings.Contains(sh[0], "ScreenCaptureApprovals") || !strings.Contains(sh[0], `"args":["check"]`) {
		t.Fatalf("sh requests %q, want one approval check", sh)
	}

	due()
	testsupport.Flag(t, control, "capture-approval-stale")
	if _, _, err := mgr.Screenshot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	execs, _ := execsAfter(t, control, mark)
	if len(execs) != 1 || !strings.Contains(execs[0], "ScreenCaptureApprovals") || !strings.HasSuffix(execs[0], "sh write") {
		t.Fatalf("a stale record made execs %q, want one rewrite", execs)
	}
}

func TestAgentCallRefusesAnOpTheAgentDoesNotOffer(t *testing.T) {
	mgr, control := agentManager(t, fastAgent())
	mc := readyMachine(t, mgr)
	_, conn, err := mgr.agentCall(context.Background(), mustGet(t, mgr, mc.RunID), guestagent.Request{Op: "drag"})
	if !errors.Is(err, guestagent.ErrMissingOp) || !strings.Contains(err.Error(), "machine_reboot") || conn == nil {
		t.Fatalf("got %v, %v", conn, err)
	}
	for _, r := range testsupport.ControlLines(t, control, "agent-requests") {
		if strings.Contains(r, `"drag"`) {
			t.Fatal("a refused op was sent")
		}
	}
	// A toolkit op is sent with its reader, and the connection it went on is returned.
	if err := os.WriteFile(filepath.Join(control, "agent-snapshot.json"), []byte(`{"result":{"nodes":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, conn, err := mgr.agentCall(context.Background(), mustGet(t, mgr, mc.RunID), guestagent.Request{Op: "snapshot", Reader: HolderVerifier})
	if err != nil || string(resp.Result) != `{"nodes":[]}` || conn.Gen() != 1 {
		t.Fatalf("got %s on gen %d, %v", resp.Result, conn.Gen(), err)
	}
	// With the toolkit off there is no agent to call.
	off, _, _ := newTestManager(t)
	offMc := readyMachine(t, off)
	if _, _, err := off.agentCall(context.Background(), mustGet(t, off, offMc.RunID), guestagent.Request{Op: "snapshot"}); !errors.Is(err, guestagent.ErrUnavailable) {
		t.Fatalf("toolkit off: %v", err)
	}
}

// A reboot stops the old boot's agent and the new boot starts its own; destroy stops that one,
// and no agent process outlives the machine.
func TestRebootAndDestroyStopTheAgent(t *testing.T) {
	mgr, control := agentManager(t, fastAgent())
	mc := readyMachine(t, mgr)
	machine := mustGet(t, mgr, mc.RunID)
	old := mgr.agentSupervisor(machine)
	if _, _, err := mgr.Reboot(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if got, err := mgr.Wait(context.Background(), mc.RunID, 20*time.Second); err != nil || got.Status != Ready {
		t.Fatalf("reboot: %+v, %v", got, err)
	}
	if _, err := old.Conn(context.Background(), 0); !errors.Is(err, guestagent.ErrUnavailable) || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("the old boot's agent after the reboot: %v", err)
	}
	cur := mgr.agentSupervisor(machine)
	if cur == nil || cur == old {
		t.Fatal("the new boot has no agent of its own")
	}
	if n := testsupport.AgentStarts(t, control); n != 2 {
		t.Fatalf("%d agent starts, want 2", n)
	}
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := cur.Conn(context.Background(), 0); !errors.Is(err, guestagent.ErrUnavailable) {
		t.Fatalf("the agent after destroy: %v", err)
	}
	if starts, exits := len(testsupport.ControlLines(t, control, "agent-starts")), len(testsupport.ControlLines(t, control, "agent-exits")); starts != 2 || exits != 2 {
		t.Fatalf("%d agents started and %d exited once destroy returned", starts, exits)
	}
}
