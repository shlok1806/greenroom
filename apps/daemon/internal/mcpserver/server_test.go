package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

const defaultImage = "ghcr.io/example/default:latest"

type harness struct {
	t       *testing.T
	session *mcp.ClientSession
	control string
	root    string
	reg     *session.Registry
	mgr     *machine.Manager
}

// newHarness starts the real MCP server over HTTP with a fake tart behind it,
// and connects a real MCP client. No VM is involved.
func newHarness(t *testing.T) *harness {
	t.Helper()
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(io.Discard, nil)),
		machine.WithTartBin(bin), machine.WithReadyTimeout(10*time.Second),
		machine.WithSSHProbe(func(context.Context, string, string) error { return nil }),
		machine.WithFrameInterval(0))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// A live machine keeps the fake tart writing into its control directory, racing t.TempDir's
	// cleanup (so frames are off too). Boot settles first because Destroy does not stop it.
	t.Cleanup(func() {
		for _, mc := range mgr.List() {
			_, _ = mgr.Wait(context.Background(), mc.RunID, 15*time.Second)
			_ = mgr.Destroy(context.Background(), mc.RunID)
		}
	})
	// Tests play the verifier by appending to the store directly.
	reg := session.NewRegistry(mgr.Root, 2)
	mgr.SetMessageActivity(reg.LastMessageAt) // as main wires it
	server := New(mgr, defaultImage, reg)
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(ts.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &harness{t: t, session: session, control: control, root: root, reg: reg, mgr: mgr}
}

// call runs a tool and requires that it succeeds.
func (h *harness) call(name string, args map[string]any, out any) *mcp.CallToolResult {
	h.t.Helper()
	res := h.raw(name, args)
	if res.IsError {
		h.t.Fatalf("%s: unexpected tool error: %s", name, text(res))
	}
	if out != nil {
		data, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(data, out); err != nil {
			h.t.Fatalf("%s: decode structured content: %v", name, err)
		}
	}
	return res
}

// raw runs a tool and returns the result even when it is an error.
func (h *harness) raw(name string, args map[string]any) *mcp.CallToolResult {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := h.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("%s: transport error: %v", name, err)
	}
	return res
}

// ready creates a machine and waits for it.
func (h *harness) ready() string {
	h.t.Helper()
	var mc machine.Machine
	h.call("machine_create", nil, &mc)
	for i := 0; i < 40 && mc.Status == machine.Booting; i++ {
		h.call("machine_wait", map[string]any{"runId": mc.RunID, "timeoutSeconds": 5}, &mc)
	}
	if mc.Status != machine.Ready {
		h.t.Fatalf("machine is %s, want ready (error %q)", mc.Status, mc.Error)
	}
	return mc.RunID
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func (h *harness) putShot() {
	h.t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	img.Set(2, 2, color.RGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		h.t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString(buf.Bytes())
	if err := os.WriteFile(filepath.Join(h.control, "shot.b64"), []byte(enc), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// --- the tool surface itself ---

func TestServerExposesExactlyItsTools(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"machine_create": false, "machine_wait": false, "machine_list": false,
		"machine_sync": false, "machine_exec": false, "machine_exec_wait": false, "machine_screenshot": false,
		"machine_destroy": false, "machine_approve_capture": false,
		"agent_send": false, "agent_wait": false, "agent_transcript": false,
		"machine_click": false, "machine_type": false, "machine_key": false,
		"machine_scroll": false, "machine_input": false, "machine_ui": false,
		"machine_session_start": false, "machine_session_send": false,
		"machine_session_read": false, "machine_session_close": false,
	}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; !ok {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		want[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no input schema", tool.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q is missing", name)
		}
	}
	if len(res.Tools) != len(want) {
		t.Errorf("the server exposes %d tools, want %d", len(res.Tools), len(want))
	}
}

func TestRequiredArgumentsAreEnforced(t *testing.T) {
	h := newHarness(t)
	for name, args := range map[string]map[string]any{
		"machine_wait":       {},
		"machine_exec":       {"runId": "x"},
		"machine_sync":       {"runId": "x"},
		"machine_screenshot": {},
		"machine_destroy":    {},
	} {
		res := h.raw(name, args)
		if !res.IsError {
			t.Errorf("%s accepted a call with missing required arguments", name)
		}
	}
}

func TestUnknownRunIdIsAReadableToolError(t *testing.T) {
	h := newHarness(t)
	for _, name := range append([]string{"machine_wait", "machine_exec", "machine_screenshot", "machine_destroy", "machine_sync"}, inputTools...) {
		args := inputArgs(name, "no-such-run")
		switch name {
		case "machine_exec":
			args["command"] = "echo hi"
		case "machine_sync":
			args["source"] = t.TempDir()
		}
		res := h.raw(name, args)
		if !res.IsError {
			t.Errorf("%s accepted an unknown runId", name)
			continue
		}
		if !strings.Contains(text(res), "no machine for run") {
			t.Errorf("%s error is not readable: %q", name, text(res))
		}
	}
}

var inputTools = []string{"machine_click", "machine_type", "machine_key", "machine_scroll", "machine_input"}

// inputArgs returns valid arguments for a tool in inputTools, or just the runId for any other.
func inputArgs(tool, runID string) map[string]any {
	args := map[string]any{"runId": runID}
	switch tool {
	case "machine_click":
		args["x"], args["y"] = 0.5, 0.5
	case "machine_type":
		args["text"] = "hi"
	case "machine_key":
		args["key"] = "a"
	case "machine_input":
		args["actions"] = []map[string]any{{"type": "key", "key": "a"}}
	}
	return args
}

// inputSteps counts the run's recorded machine_input steps.
func (h *harness) inputSteps(runID string) int {
	h.t.Helper()
	steps, err := machine.ReadSteps(h.mgr.RunDir(runID))
	if err != nil {
		h.t.Fatalf("ReadSteps: %v", err)
	}
	n := 0
	for _, s := range steps {
		if s.Tool == "machine_input" {
			n++
		}
	}
	return n
}

// A machine that never came up answers input with a readable error and posts nothing.
func TestComputerUseOnAFailedMachineIsAReadableError(t *testing.T) {
	h := newHarness(t)
	testsupport.Flag(t, h.control, "fail-run")
	var mc machine.Machine
	h.call("machine_create", nil, &mc)
	for i := 0; i < 40 && mc.Status == machine.Booting; i++ {
		h.call("machine_wait", map[string]any{"runId": mc.RunID, "timeoutSeconds": 5}, &mc)
	}
	if mc.Status != machine.Failed {
		t.Fatalf("machine is %s, want failed (error %q)", mc.Status, mc.Error)
	}

	for _, name := range inputTools {
		res := h.raw(name, inputArgs(name, mc.RunID))
		if !res.IsError {
			t.Errorf("%s accepted a call on a failed machine", name)
			continue
		}
		if !strings.Contains(text(res), "is failed") {
			t.Errorf("%s error is not readable: %q", name, text(res))
		}
	}
	if strings.Contains(testsupport.Calls(t, h.control), "greenroom-input") {
		t.Error("a batch reached the guest of a machine that never came up")
	}
}

// --- machine_create ---

func TestCreateUsesTheDefaultImage(t *testing.T) {
	h := newHarness(t)
	var mc machine.Machine
	h.call("machine_create", nil, &mc)
	if mc.Image != defaultImage {
		t.Errorf("image = %q, want the daemon default %q", mc.Image, defaultImage)
	}
	if mc.Status != machine.Booting {
		t.Errorf("status = %q, want booting", mc.Status)
	}
	if mc.RunID == "" {
		t.Error("create returned no runId")
	}
	if !strings.Contains(testsupport.Calls(t, h.control), "clone "+defaultImage) {
		t.Error("the default image never reached tart")
	}
}

func TestCreateAcceptsANamedImage(t *testing.T) {
	h := newHarness(t)
	var mc machine.Machine
	h.call("machine_create", map[string]any{"image": "ghcr.io/example/other:1"}, &mc)
	if mc.Image != "ghcr.io/example/other:1" {
		t.Errorf("image = %q, want the named image", mc.Image)
	}
}

func TestCreateReportsATartFailure(t *testing.T) {
	h := newHarness(t)
	testsupport.Flag(t, h.control, "fail-clone")
	res := h.raw("machine_create", nil)
	if !res.IsError {
		t.Fatal("create succeeded although the clone failed")
	}
	if !strings.Contains(text(res), "image not found") {
		t.Errorf("the tool error hides tart's message: %q", text(res))
	}
}

// --- machine_wait ---

func TestWaitReachesReadyAndReportsBootSeconds(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	var mc machine.Machine
	h.call("machine_wait", map[string]any{"runId": runID}, &mc)
	// A fake boot rounds to 0s; the manager suite checks the real value.
	if mc.Status != machine.Ready || mc.IP == "" || mc.BootSeconds < 0 {
		t.Errorf("ready machine is missing fields: %+v", mc)
	}
}

func TestWaitCapsTheTimeout(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	started := time.Now()
	var mc machine.Machine
	h.call("machine_wait", map[string]any{"runId": runID, "timeoutSeconds": 9999}, &mc)
	if elapsed := time.Since(started); elapsed > maxWait {
		t.Errorf("wait took %v, which is above the %v cap", elapsed, maxWait)
	}
	if mc.Status != machine.Ready {
		t.Errorf("status = %q, want ready", mc.Status)
	}
}

// --- machine_list ---

func TestListIsEmptyThenHoldsTheMachine(t *testing.T) {
	h := newHarness(t)
	var out struct {
		Machines []*machine.Machine `json:"machines"`
	}
	h.call("machine_list", nil, &out)
	if len(out.Machines) != 0 {
		t.Fatalf("a fresh daemon lists %d machines, want 0", len(out.Machines))
	}
	runID := h.ready()
	h.call("machine_list", nil, &out)
	if len(out.Machines) != 1 || out.Machines[0].RunID != runID {
		t.Fatalf("list does not hold the machine: %+v", out.Machines)
	}
	if out.Machines[0].Name == "" || out.Machines[0].Status != machine.Ready {
		t.Errorf("listed machine is incomplete: %+v", out.Machines[0])
	}
}

// An agent at the host limit reads machine_list to tell a stale run from a busy one.
func TestListSaysHowLongEachMachineHasBeenIdle(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	type row struct {
		RunID        string    `json:"runId"`
		LastActivity time.Time `json:"lastActivity"`
		IdleSeconds  *int      `json:"idleSeconds"`
	}
	var out struct {
		Machines []row `json:"machines"`
	}
	h.call("machine_list", nil, &out)
	if len(out.Machines) != 1 || out.Machines[0].IdleSeconds == nil || out.Machines[0].LastActivity.IsZero() {
		t.Fatalf("machine_list does not report idle time: %+v", out.Machines)
	}
	boot := out.Machines[0].LastActivity

	store, err := h.reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Append(session.Message{From: session.Coder, Kind: session.Note, Text: "rebuilding"})
	if err != nil {
		t.Fatal(err)
	}
	h.call("machine_list", nil, &out)
	if got := out.Machines[0].LastActivity; !got.Equal(m.At) || !got.After(boot) {
		t.Errorf("lastActivity = %v after a message at %v, want the message", got, m.At)
	}
}

// --- machine_exec ---

func TestExecReturnsStreamsAndExitCode(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	testsupport.Flag(t, h.control, "exec-exit-3")
	var res machine.ExecResult
	h.call("machine_exec", map[string]any{"runId": runID, "command": "exit 3"}, &res)
	if res.ExitCode != 3 {
		t.Errorf("exitCode = %d, want 3", res.ExitCode)
	}
	if res.Stdout == "" || res.Stderr == "" {
		t.Errorf("streams were not returned: %+v", res)
	}
}

func TestExecRoundsSecondsToTwoPlaces(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	var res machine.ExecResult
	h.call("machine_exec", map[string]any{"runId": runID, "command": "echo hi"}, &res)
	if res.Seconds != round(res.Seconds) {
		t.Errorf("seconds = %v, which is not rounded to two places", res.Seconds)
	}
}

func TestExecPassesTheWorkingDirectory(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.call("machine_exec", map[string]any{"runId": runID, "command": "pwd", "cwd": "work/app"}, nil)
	if !strings.Contains(testsupport.Calls(t, h.control), "cd 'work/app'") {
		t.Error("the cwd never reached the guest command")
	}
}

func TestExecReadsATildeCwdAsTheGuestHome(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.call("machine_exec", map[string]any{"runId": runID, "command": "pwd", "cwd": "~/work/app"}, nil)
	if !strings.Contains(testsupport.Calls(t, h.control), `cd "$HOME"/'work/app'`) {
		t.Errorf("a ~ cwd was not entered from the guest home\ncalls:\n%s", testsupport.Calls(t, h.control))
	}
}

// Issue #29: a huge output comes back as its head and tail, with its full size, not whole.
func TestExecBoundsItsOutputAndSaysHowMuchWasLeftOut(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	body := "HEAD-MARK\n" + strings.Repeat("x", 4<<20) + "\nTAIL-MARK\n"
	if err := os.WriteFile(filepath.Join(h.control, "exec-stdout"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	res := h.call("machine_exec", map[string]any{"runId": runID, "command": "cat big.log"}, &out)
	stdout, _ := out["stdout"].(string)
	if !strings.HasPrefix(stdout, "HEAD-MARK\n") || !strings.HasSuffix(stdout, "\nTAIL-MARK\n") {
		t.Errorf("stdout lost its head or tail: %.40q ... %.40q", stdout, stdout[max(0, len(stdout)-40):])
	}
	if limit := machine.ExecHeadLimit + machine.ExecTailLimit + 200; len(stdout) > limit {
		t.Errorf("stdout is %d bytes, want at most %d", len(stdout), limit)
	}
	if !strings.Contains(stdout, "bytes left out") {
		t.Error("stdout does not mark where bytes were left out")
	}
	if out["stdoutTruncated"] != true || out["stdoutBytes"] != float64(len(body)) {
		t.Errorf("stdoutTruncated %v, stdoutBytes %v; want true and %d", out["stdoutTruncated"], out["stdoutBytes"], len(body))
	}
	if n := len(text(res)); n > 2*(machine.ExecHeadLimit+machine.ExecTailLimit) {
		t.Errorf("the text content is %d bytes for a bounded result", n)
	}
	// The run record keeps the same bounded output and the full size.
	steps, err := machine.ReadSteps(filepath.Join(h.root, "runs", runID))
	if err != nil {
		t.Fatal(err)
	}
	var recorded map[string]any
	for _, s := range steps {
		if s.Tool == "machine_exec" {
			recorded, _ = s.Output.(map[string]any)
		}
	}
	if recorded == nil || recorded["stdoutBytes"] != float64(len(body)) {
		t.Errorf("recorded output %v, want stdoutBytes %d", recorded["stdoutBytes"], len(body))
	}
}

// A small output is whole and not marked truncated.
func TestExecKeepsASmallOutputWhole(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	body := strings.Repeat("line of build output\n", 1000) // 21 KB, under the limit
	if err := os.WriteFile(filepath.Join(h.control, "exec-stdout"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	h.call("machine_exec", map[string]any{"runId": runID, "command": "make"}, &out)
	if out["stdout"] != body || out["stdoutBytes"] != float64(len(body)) {
		t.Errorf("a small output did not come back whole: %d bytes, stdoutBytes %v", len(out["stdout"].(string)), out["stdoutBytes"])
	}
	if _, ok := out["stdoutTruncated"]; ok {
		t.Errorf("a whole output is marked truncated: %v", out["stdoutTruncated"])
	}
}

func TestExecDescriptionStatesItsLimitsAndTheWaitTool(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "machine_exec" {
			continue
		}
		for _, want := range []string{"first 8 KiB and last 24 KiB", "stdoutBytes", "machine_exec_wait", "execId", "max 50", "machine_session_start", "not interactive"} {
			if !strings.Contains(tool.Description, want) {
				t.Errorf("machine_exec's description does not mention %q:\n%s", want, tool.Description)
			}
		}
		return
	}
	t.Fatal("no machine_exec tool")
}

// Issue #39: a call never blocks past waitSeconds. A command still going comes back running
// with an execId and keeps running; machine_exec_wait collects the same result and step.
func TestALongExecReturnsAHandleAndIsCollectedLater(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if err := os.WriteFile(filepath.Join(h.control, "exec-sleep"), []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var first map[string]any
	h.call("machine_exec", map[string]any{"runId": runID, "command": "swift build", "waitSeconds": 1}, &first)
	if took := time.Since(started); took > 2500*time.Millisecond {
		t.Errorf("machine_exec blocked %s, want about its 1 s wait", took)
	}
	if first["running"] != true || first["execId"] == "" || first["execId"] == nil {
		t.Fatalf("a command still going came back %v, want running true with an execId", first)
	}
	if _, ok := first["exitCode"]; ok {
		t.Errorf("a running command reported an exit code: %v", first)
	}

	var done map[string]any
	for i := 0; i < 10 && (i == 0 || done["running"] == true); i++ {
		h.call("machine_exec_wait", map[string]any{"runId": runID, "execId": first["execId"], "waitSeconds": 2}, &done)
	}
	if done["running"] != false || done["exitCode"] != 0.0 || done["stdout"] != "fake stdout\n" {
		t.Fatalf("the collected result is %v, want the finished command's", done)
	}
	if done["step"] != first["step"] || done["execId"] != first["execId"] {
		t.Errorf("collected step %v execId %v, want the step %v and execId %v machine_exec returned",
			done["step"], done["execId"], first["step"], first["execId"])
	}
	if secs, _ := done["seconds"].(float64); secs < 3 {
		t.Errorf("seconds = %v, want the command's whole run of about 3 s", secs)
	}

	// Collecting again gives the same answer; an unknown id is a readable error.
	var again map[string]any
	h.call("machine_exec_wait", map[string]any{"runId": runID, "execId": first["execId"]}, &again)
	if again["exitCode"] != 0.0 || again["step"] != first["step"] {
		t.Errorf("a second collect gave %v", again)
	}
	if res := h.raw("machine_exec_wait", map[string]any{"runId": runID, "execId": "nope"}); !res.IsError || !strings.Contains(text(res), "machine_exec") {
		t.Errorf("an unknown execId gave %s, want an error that points to machine_exec", text(res))
	}
}

// Issue #47: the output recorded for machine_exec and machine_ui carries the record's own seq.
func TestExecAndUIRecordTheirOwnStepNumber(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	var exec machine.ExecResult
	h.call("machine_exec", map[string]any{"runId": runID, "command": "echo hi"}, &exec)
	var tree machine.UITree
	h.call("machine_ui", map[string]any{"runId": runID}, &tree)

	steps, err := machine.ReadSteps(filepath.Join(h.root, "runs", runID))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, s := range steps {
		if s.Tool != "machine_exec" && s.Tool != "machine_ui" {
			continue
		}
		out, _ := s.Output.(map[string]any)
		if got, _ := out["step"].(float64); int(got) != s.Seq {
			t.Errorf("%s record seq %d has output.step %v", s.Tool, s.Seq, out["step"])
		}
		seen[s.Tool] = s.Seq
	}
	if seen["machine_exec"] != exec.Step || seen["machine_ui"] != tree.Step {
		t.Errorf("recorded steps %v, want exec %d and ui %d as the results said", seen, exec.Step, tree.Step)
	}
}

// --- machine_sync ---

func TestSyncRejectsASourceThatIsNotADirectory(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	res := h.raw("machine_sync", map[string]any{"runId": runID, "source": filepath.Join(t.TempDir(), "nope")})
	if !res.IsError {
		t.Fatal("sync accepted a source that does not exist")
	}
	if !strings.Contains(text(res), "not a directory") {
		t.Errorf("error text is not helpful: %q", text(res))
	}
}

func TestSyncReportsDestAndSummary(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'Number of files: 2 (reg: 1, dir: 1)'\necho 'Total transferred file size: 3 bytes'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	source := t.TempDir()
	var res machine.SyncResult
	h.call("machine_sync", map[string]any{"runId": runID, "source": source, "exclude": []string{".git"}}, &res)
	if res.Dest != "work/"+filepath.Base(source) {
		t.Errorf("dest = %q, want the default work path", res.Dest)
	}
	if !strings.Contains(res.Summary, "Number of files: 2") {
		t.Errorf("summary = %q", res.Summary)
	}
	if res.Seconds != round(res.Seconds) {
		t.Errorf("seconds = %v, which is not rounded", res.Seconds)
	}
}

// The description is what an agent reads; it must match what guestDest accepts.
func TestSyncDescriptionMatchesTheTildeBehaviour(t *testing.T) {
	h := newHarness(t)
	tools, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var desc, dest string
	for _, tool := range tools.Tools {
		if tool.Name == "machine_sync" {
			desc = tool.Description
			raw, _ := json.Marshal(tool.InputSchema)
			dest = string(raw)
		}
	}
	for _, want := range []string{"relative to the guest home", "~/ is accepted"} {
		if !strings.Contains(desc, want) {
			t.Errorf("machine_sync description %q does not say %q", desc, want)
		}
	}
	if !strings.Contains(dest, "~/work/myapp are the same place") {
		t.Errorf("the dest schema does not say ~/ is the same as a home-relative path: %s", dest)
	}

	runID := h.ready()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var res machine.SyncResult
	h.call("machine_sync", map[string]any{"runId": runID, "source": t.TempDir(), "dest": "~/work/myapp"}, &res)
	if res.Dest != "work/myapp" {
		t.Errorf("dest ~/work/myapp synced to %q, want work/myapp as the description promises", res.Dest)
	}
}

func TestApproveCaptureTakesAGuestAppPath(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	var out struct {
		Client string `json:"client"`
		Step   int    `json:"step"`
	}
	h.call("machine_approve_capture", map[string]any{"runId": runID, "app": "~/work/Shot/Shot.app"}, &out)
	if out.Client != "file:///Users/admin/work/Shot/Shot.app/" || out.Step == 0 {
		t.Errorf("result = %+v, want the bundle URL and the step", out)
	}
	if res := h.raw("machine_approve_capture", map[string]any{"runId": runID, "app": "work/Shot/shot"}); !res.IsError {
		t.Error("a path that is not an .app bundle was accepted")
	}
}

// --- machine_screenshot ---

func TestScreenshotReturnsAJPEGAndThePNGPath(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.putShot()

	res := h.call("machine_screenshot", map[string]any{"runId": runID}, nil)

	var img *mcp.ImageContent
	var meta string
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcp.ImageContent:
			img = v
		case *mcp.TextContent:
			meta = v.Text
		}
	}
	if img == nil {
		t.Fatal("the result holds no image content")
	}
	if img.MIMEType != "image/jpeg" {
		t.Errorf("mime type = %q, want image/jpeg", img.MIMEType)
	}
	if len(img.Data) < 4 || img.Data[0] != 0xFF || img.Data[1] != 0xD8 {
		t.Error("the image content is not a JPEG")
	}
	var out struct {
		Path  string `json:"path"`
		Bytes int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(meta), &out); err != nil {
		t.Fatalf("the text content is not the metadata: %q", meta)
	}
	if out.Bytes <= 0 {
		t.Errorf("bytes = %d, want the PNG size", out.Bytes)
	}
	data, err := os.ReadFile(out.Path)
	if err != nil {
		t.Fatalf("the PNG path does not exist: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Errorf("the saved file is not a PNG: %v", err)
	}
}

func TestScreenshotFailsWhenTheGuestOutputIsNotAnImage(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	if err := os.WriteFile(filepath.Join(h.control, "shot.b64"), []byte(base64.StdEncoding.EncodeToString([]byte("not a png"))), 0o644); err != nil {
		t.Fatal(err)
	}
	res := h.raw("machine_screenshot", map[string]any{"runId": runID})
	if !res.IsError {
		t.Fatal("screenshot succeeded although the bytes are not a PNG")
	}
	if !strings.Contains(text(res), "decode") {
		t.Errorf("error text is not helpful: %q", text(res))
	}
}

// --- machine_destroy ---

func TestDestroyIsNotRepeatable(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	var out struct {
		OK bool `json:"ok"`
	}
	h.call("machine_destroy", map[string]any{"runId": runID}, &out)
	if !out.OK {
		t.Error("destroy did not report ok")
	}
	if res := h.raw("machine_destroy", map[string]any{"runId": runID}); !res.IsError {
		t.Error("a second destroy succeeded")
	}
}

// --- pure helpers ---

func TestToJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	jpg, err := toJPEG(buf.Bytes())
	if err != nil {
		t.Fatalf("toJPEG: %v", err)
	}
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		t.Error("the output is not a JPEG")
	}
	for name, in := range map[string][]byte{"empty": {}, "random bytes": []byte("not a png at all")} {
		if _, err := toJPEG(in); err == nil {
			t.Errorf("toJPEG accepted %s", name)
		}
	}
}

func TestRound(t *testing.T) {
	for in, want := range map[float64]float64{
		0: 0, 1.005: 1.0, 1.006: 1.01, 12.3456: 12.35, 0.001: 0, 99.999: 100,
	} {
		if got := round(in); got != want {
			t.Errorf("round(%v) = %v, want %v", in, got, want)
		}
	}
}

// Issue #1: a dead `tart run` fails the machine in seconds with tart's reason, not after the readiness timeout.
func TestCreateFailsFastWhenTheVMProcessDies(t *testing.T) {
	h := newHarness(t)
	testsupport.Flag(t, h.control, "fail-run")

	var mc machine.Machine
	h.call("machine_create", nil, &mc)
	started := time.Now()
	for i := 0; i < 15 && mc.Status == machine.Booting; i++ {
		h.call("machine_wait", map[string]any{"runId": mc.RunID, "timeoutSeconds": 2}, &mc)
	}
	elapsed := time.Since(started)

	if mc.Status != machine.Failed {
		t.Fatalf("status = %q after %v, want failed", mc.Status, elapsed)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the failure took %v, want seconds", elapsed)
	}
	if !strings.Contains(mc.Error, "exceeds the system limit") {
		t.Errorf("error = %q, want the message tart printed", mc.Error)
	}
}

// Issue #2: a create above the host's macOS VM limit is refused before cloning.
func TestCreateRefusesAboveTheHostLimit(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.control, "vmnames"), []byte("other-one\nother-two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := h.raw("machine_create", nil)
	if !res.IsError {
		t.Fatal("create succeeded although the host is at its machine limit")
	}
	msg := text(res)
	for _, want := range []string{"limit", "other-one", "other-two"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %q: %q", want, msg)
		}
	}
	if strings.Contains(testsupport.Calls(t, h.control), "clone ") {
		t.Error("a refused create still cloned a VM")
	}
}

// Issue #5: machine_sync requires an absolute source and a dest inside the guest home.
func TestSyncKeepsTheCallerInsideTheGuestHome(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	source := t.TempDir()

	cases := map[string]map[string]any{
		"a dest that climbs out": {"runId": runID, "source": source, "dest": "../../../tmp/escaped"},
		"an absolute dest":       {"runId": runID, "source": source, "dest": "/tmp/escaped"},
		"a dest that is only ..": {"runId": runID, "source": source, "dest": ".."},
		"a relative source":      {"runId": runID, "source": "relative/path"},
	}
	for name, args := range cases {
		res := h.raw("machine_sync", args)
		if !res.IsError {
			t.Errorf("sync accepted %s", name)
			continue
		}
		if text(res) == "" {
			t.Errorf("sync refused %s with no message", name)
		}
	}
	if strings.Contains(testsupport.Calls(t, h.control), "escaped") {
		t.Error("a refused sync still reached the guest")
	}
}

// --- computer use (ADR 0009) ---

// postedActions decodes the most recent non-empty batch sent to the guest helper, from the fake tart's call log.
func postedActions(t *testing.T, control string) []map[string]any {
	t.Helper()
	calls := testsupport.Calls(t, control)
	re := regexp.MustCompile(`--json-base64 ([A-Za-z0-9+/=]+)`)
	var last []map[string]any
	for _, match := range re.FindAllStringSubmatch(calls, -1) {
		raw, err := base64.StdEncoding.DecodeString(match[1])
		if err != nil {
			t.Fatalf("payload is not base64: %v", err)
		}
		var payload struct {
			Actions []map[string]any `json:"actions"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("payload is not JSON: %v: %s", err, raw)
		}
		if len(payload.Actions) > 0 {
			last = payload.Actions
		}
	}
	return last
}

func TestClickPostsOneActionAndRecordsOneStep(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	h.call("machine_click", map[string]any{"runId": runID, "x": 0.25, "y": 0.5}, nil)

	posted := postedActions(t, h.control)
	if len(posted) != 1 {
		t.Fatalf("the guest was sent %d actions, want 1: %+v", len(posted), posted)
	}
	if posted[0]["type"] != "click" {
		t.Errorf("action type = %v, want click", posted[0]["type"])
	}
	// Default screen is 1024x768 (fake tart): 0.25,0.5 scales to 256,384.
	if posted[0]["x"] != 256.0 || posted[0]["y"] != 384.0 {
		t.Errorf("x,y = %v,%v, want 256,384", posted[0]["x"], posted[0]["y"])
	}

	if n := h.inputSteps(runID); n != 1 {
		t.Fatalf("machine_input steps = %d, want 1", n)
	}
}

func TestKeyPressesWithModifiers(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	h.call("machine_key", map[string]any{"runId": runID, "key": "a", "mods": []string{"cmd", "shift"}}, nil)

	posted := postedActions(t, h.control)
	if len(posted) != 1 {
		t.Fatalf("the guest was sent %d actions, want 1: %+v", len(posted), posted)
	}
	if posted[0]["type"] != "key" || posted[0]["key"] != "a" {
		t.Errorf("action = %+v, want a key press of \"a\"", posted[0])
	}
	mods, _ := posted[0]["mods"].([]any)
	if len(mods) != 2 || mods[0] != "cmd" || mods[1] != "shift" {
		t.Errorf("mods = %v, want [cmd shift]", posted[0]["mods"])
	}
}

func TestScrollPostsADelta(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	h.call("machine_scroll", map[string]any{"runId": runID, "deltaX": 0, "deltaY": -120}, nil)

	posted := postedActions(t, h.control)
	if len(posted) != 1 {
		t.Fatalf("the guest was sent %d actions, want 1: %+v", len(posted), posted)
	}
	// A scroll up (negative in the tool) is a positive CGEvent wheel in the helper (issue #51).
	if posted[0]["type"] != "scroll" || posted[0]["deltaY"] != 120.0 {
		t.Errorf("action = %+v, want the helper's scroll up, deltaY 120", posted[0])
	}
}

func TestInputBatchComposesADragInOneStep(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	h.call("machine_input", map[string]any{"runId": runID, "actions": []map[string]any{
		{"type": "down", "x": 0.1, "y": 0.1},
		{"type": "move", "x": 0.9, "y": 0.1},
		{"type": "up", "x": 0.9, "y": 0.1},
	}}, nil)

	posted := postedActions(t, h.control)
	if len(posted) != 3 {
		t.Fatalf("the guest was sent %d actions, want 3: %+v", len(posted), posted)
	}
	if posted[0]["type"] != "down" || posted[1]["type"] != "move" || posted[2]["type"] != "up" {
		t.Errorf("actions = %+v, want down, move, up in order", posted)
	}

	if n := h.inputSteps(runID); n != 1 {
		t.Fatalf("machine_input steps = %d, want 1: a batch is one step, not one per action", n)
	}
}

// A human holding the screen wins: the call is a readable tool error and nothing reaches the guest.
func TestClickIsRefusedWhileAHumanHoldsTheScreen(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	if _, _, err := h.mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl(human): %v", err)
	}

	res := h.raw("machine_click", map[string]any{"runId": runID, "x": 0.5, "y": 0.5})
	if !res.IsError {
		t.Fatal("machine_click succeeded although a human holds the screen")
	}
	msg := text(res)
	if !strings.Contains(msg, "human") {
		t.Errorf("the error does not name the human holder: %q", msg)
	}

	// --json-base64 posts input; boot's own helper check runs the helper too.
	if strings.Contains(testsupport.Calls(t, h.control), "--json-base64") {
		t.Error("a refused click still reached the guest input helper")
	}

	c, held := h.mgr.ControlState(runID)
	if !held || c.Holder != "human" {
		t.Errorf("control state = %+v held=%v, want the human still holding it", c, held)
	}
}

// The verifier holds the lease per call, so a human can take it right after one returns.
func TestAHumanCanTakeTheScreenBackBetweenVerifierCalls(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	h.call("machine_click", map[string]any{"runId": runID, "x": 0.5, "y": 0.5}, nil)

	if _, _, err := h.mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("a human could not take control right after a verifier call: %v", err)
	}
	c, held := h.mgr.ControlState(runID)
	if !held || c.Holder != "human" {
		t.Errorf("control state = %+v held=%v, want the human holding it", c, held)
	}
}

func TestTypeSendsOneBatchOneStep(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	h.call("machine_type", map[string]any{"runId": runID, "text": "hello"}, nil)

	posted := postedActions(t, h.control)
	if len(posted) != 1 {
		t.Fatalf("the guest was sent %d actions, want 1: %+v", len(posted), posted)
	}
	if posted[0]["type"] != "type" || posted[0]["text"] != "hello" {
		t.Errorf("action = %+v, want a type of \"hello\"", posted[0])
	}

	if n := h.inputSteps(runID); n != 1 {
		t.Fatalf("machine_input steps = %d, want 1", n)
	}
}

// --- machine_ui (ADR 0012) ---

const tipSplitUI = `{"app":{"name":"TipSplit","pid":7},"apps":["Finder","TipSplit"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXRadioGroup","depth":0,"frame":{"x":438,"y":347,"w":196,"h":24}},
{"role":"AXRadioButton","subrole":"AXSegment","label":"25%","depth":1,"frame":{"x":586,"y":347,"w":48,"h":24}}]}`

const textEditUI = `{"app":{"name":"TextEdit","pid":9},"apps":["Finder","TextEdit"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXWindow","title":"Untitled","depth":0,"frame":{"x":100,"y":100,"w":600,"h":437}},
{"role":"AXScrollArea","depth":1,"frame":{"x":80,"y":50,"w":602,"h":437}},
{"role":"AXTextArea","depth":2,"frame":{"x":80,"y":50,"w":602,"h":437}}]}`

// Issue #35: the verifier's machine_ui must not replace the coder's tree. A coder
// click by element id resolves against the coder's own last read, and a click
// pinned to a uiStep that is not the caller's latest read is refused.
func TestAnElementClickUsesTheCallersOwnTree(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.putUI(tipSplitUI)
	var coder machine.UITree
	h.call("machine_ui", map[string]any{"runId": runID}, &coder)

	// A verifier turn aims at another app in between.
	h.putUI(textEditUI)
	if _, err := h.mgr.UI(context.Background(), runID, machine.HolderVerifier, "TextEdit", 0); err != nil {
		t.Fatal(err)
	}

	var out struct {
		Element *machine.UIElement `json:"element"`
		UIStep  int                `json:"uiStep"`
		App     string             `json:"app"`
	}
	h.call("machine_click", map[string]any{"runId": runID, "element": 2}, &out)
	if out.Element == nil || out.Element.Label != "25%" || out.App != "TipSplit" || out.UIStep != coder.Step {
		t.Fatalf("clicked %+v in %q (tree step %d), want TipSplit's 25%% segment from the coder's step %d", out.Element, out.App, out.UIStep, coder.Step)
	}
	if posted := postedActions(t, h.control); len(posted) != 1 || posted[0]["x"] != 610.0 || posted[0]["y"] != 359.0 {
		t.Errorf("posted %+v, want one click at TipSplit's 25%% segment (610,359)", posted)
	}

	// Element 3 exists only in the verifier's tree.
	if res := h.raw("machine_click", map[string]any{"runId": runID, "element": 3}); !res.IsError || strings.Contains(text(res), "TextEdit") {
		t.Errorf("element 3 resolved outside the coder's tree: %s", text(res))
	}

	// A click pinned to an older read is refused, not aimed at the newer tree.
	h.putUI(tipSplitUI)
	h.call("machine_ui", map[string]any{"runId": runID}, nil)
	res := h.raw("machine_click", map[string]any{"runId": runID, "element": 2, "uiStep": coder.Step})
	if !res.IsError || !strings.Contains(text(res), "machine_ui") {
		t.Errorf("a click pinned to a stale read: %s, want an error that says to read machine_ui again", text(res))
	}
}

func (h *harness) putUI(body string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.control, "ui.json"), []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func TestUIReturnsAnOutlineAndTheStructuredTree(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	h.putUI(tipSplitUI)

	var tree machine.UITree
	res := h.call("machine_ui", map[string]any{"runId": runID, "app": "TipSplit"}, &tree)
	if len(tree.Elements) != 2 || tree.Elements[1].X != 0.596 || tree.Elements[1].Y != 0.467 {
		t.Fatalf("structured tree = %+v, want the 25%% segment at (0.596, 0.467)", tree)
	}
	if out := text(res); !strings.Contains(out, `[2] RadioButton/Segment label="25%" center (0.596, 0.467)`) {
		t.Errorf("the outline does not give the segment's center:\n%s", out)
	}
}

// A click by element lands on the element's center, in the same points a
// click by fraction would.
func TestClickAnElementFromTheLatestUIRead(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()

	if res := h.raw("machine_click", map[string]any{"runId": runID, "element": 2}); !res.IsError || !strings.Contains(text(res), "machine_ui") {
		t.Errorf("a click by element before any read: %s, want an error naming machine_ui", text(res))
	}
	h.putUI(tipSplitUI)
	h.call("machine_ui", map[string]any{"runId": runID}, nil)

	var out struct {
		Step    int                `json:"step"`
		Element *machine.UIElement `json:"element"`
	}
	h.call("machine_click", map[string]any{"runId": runID, "element": 2}, &out)
	if out.Element == nil || out.Element.Label != "25%" || out.Step == 0 {
		t.Errorf("result = %+v, want the clicked element and its step", out)
	}
	posted := postedActions(t, h.control)
	// 0.596 x 1024 and 0.467 x 768, rounded to points: the segment's middle.
	if len(posted) != 1 || posted[0]["x"] != 610.0 || posted[0]["y"] != 359.0 {
		t.Errorf("posted %+v, want one click at 610,359", posted)
	}
	if res := h.raw("machine_click", map[string]any{"runId": runID}); !res.IsError {
		t.Error("a click with neither an element nor x and y was accepted")
	}
}

// Issue #31: machine_key with a misspelt modifier posted the bare key and reported success.
func TestAnUnknownModifierOrButtonIsAToolError(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	for tool, args := range map[string]map[string]any{
		"machine_key":   {"runId": runID, "key": "q", "mods": []string{"cmnd"}},
		"machine_click": {"runId": runID, "x": 0.9, "y": 0.9, "button": "bogus"},
		"machine_input": {"runId": runID, "actions": []map[string]any{{"type": "key", "key": "x", "mods": []string{"hyper"}}}},
	} {
		res := h.raw(tool, args)
		if !res.IsError || !strings.Contains(text(res), "unknown") || !strings.Contains(text(res), "use ") {
			t.Errorf("%s %v = %q (isError %v), want an error naming what is accepted", tool, args, text(res), res.IsError)
		}
	}
}

// Issue #42: VMs another greenroom daemon runs are named as such, and the error steers the
// agent to wait and retry, never to stop a machine this daemon did not create.
func TestHostLimitErrorNamesAnotherDaemonsMachinesAndSaysToWait(t *testing.T) {
	h := newHarness(t)
	other := "greenroom-20260923-073440-ca5f93d22cf5610a"
	if err := os.WriteFile(filepath.Join(h.control, "vmnames"), []byte(other+"\nsomeones-vm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := h.raw("machine_create", nil)
	if !res.IsError {
		t.Fatal("create succeeded although the host is at its machine limit")
	}
	msg := text(res)
	for _, want := range []string{other + " (another greenroom daemon", "someones-vm", "wait", "call machine_create again", "do not stop"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, "stop one of them") || strings.Contains(msg, "all started outside greenroom") {
		t.Errorf("the error still tells the agent to stop machines it did not create: %q", msg)
	}
}

// Issue #42: with one machine of ours at the limit, the agent may destroy its own or wait,
// and is told not to stop the other daemon's.
func TestHostLimitErrorWithOursAndAnotherDaemonsOffersDestroyOrWait(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.control, "vmnames"), []byte("greenroom-20260923-074607-27de2e9c17c3c976\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ready()
	res := h.raw("machine_create", nil)
	if !res.IsError {
		t.Fatal("create succeeded although the host is at its machine limit")
	}
	msg := text(res)
	for _, want := range []string{"machine_destroy", "runId ", "another greenroom daemon", "wait", "call machine_create again", "do not stop"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, "or stop ") {
		t.Errorf("the error tells the agent to stop machines it did not create: %q", msg)
	}
}

// Issue #50: a panic in a tool handler ran on the SDK's goroutine and killed the process.
func TestAPanickingHandlerIsOneCallsError(t *testing.T) {
	boom := recoverPanics(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		var msgs []int
		return nil, fmt.Errorf("unreachable %d", msgs[len(msgs)-1])
	})
	res, err := boom(context.Background(), "tools/call", nil)
	if res != nil || err == nil || !strings.Contains(err.Error(), "tools/call") {
		t.Fatalf("recovered call = %v, %v; want nil and an error naming the method", res, err)
	}
}
