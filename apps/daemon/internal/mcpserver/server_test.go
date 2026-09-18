package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

const defaultImage = "ghcr.io/example/default:latest"

type harness struct {
	t       *testing.T
	session *mcp.ClientSession
	control string
	root    string
}

// newHarness starts the real MCP server over HTTP with a fake tart behind it,
// and connects a real MCP client. No VM is involved.
func newHarness(t *testing.T) *harness {
	t.Helper()
	bin, control := testsupport.FakeTart(t)
	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(io.Discard, nil)),
		machine.WithTartBin(bin), machine.WithReadyTimeout(10*time.Second),
		machine.WithSSHProbe(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	server := New(mgr, defaultImage)
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
	return &harness{t: t, session: session, control: control, root: root}
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

func TestServerExposesExactlySevenTools(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"machine_create": false, "machine_wait": false, "machine_list": false,
		"machine_sync": false, "machine_exec": false, "machine_screenshot": false,
		"machine_destroy": false,
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
	for _, name := range []string{"machine_wait", "machine_exec", "machine_screenshot", "machine_destroy", "machine_sync"} {
		args := map[string]any{"runId": "no-such-run"}
		if name == "machine_exec" {
			args["command"] = "echo hi"
		}
		if name == "machine_sync" {
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
	// bootSeconds is rounded to one decimal, and a fake machine boots in
	// milliseconds, so only require that it is not negative here. The manager
	// suite checks that it covers the real wait.
	if mc.Status != machine.Ready || mc.IP == "" || mc.BootSeconds < 0 {
		t.Errorf("ready machine is missing fields: %+v", mc)
	}
}

func TestWaitCapsTheTimeout(t *testing.T) {
	h := newHarness(t)
	runID := h.ready()
	// A timeout far above the cap must still return promptly for a ready
	// machine, and must not be rejected.
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

// Issue #1: a machine whose `tart run` process dies must fail in seconds with
// the reason tart printed, not after the three minute readiness timeout.
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

// Issue #2: the host allows a fixed number of macOS VMs. A create above that
// number must be refused, not started and then lost.
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

// Issue #5: machine_sync promises a dest relative to the guest home and an
// absolute source. It must enforce both.
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
