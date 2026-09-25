//go:build tart

// End-to-end test against a real Tart VM. Run with:
//
//	go test -tags tart -run TestEndToEnd -v -timeout 10m .
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/mcpserver"
	greenroomsession "github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

func TestEndToEnd(t *testing.T) {
	waitForAFreeSlot(t)
	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := mcpserver.New(mgr, greenroomBaseImage(), greenroomsession.NewRegistry(root, greenroomsession.DefaultMaxDisputes))
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	call := func(name string, args map[string]any, out any) *mcp.CallToolResult {
		t.Helper()
		started := time.Now()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool error: %s", name, contentText(res))
		}
		if out != nil {
			data, _ := json.Marshal(res.StructuredContent)
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("%s: decode structured content: %v", name, err)
			}
		}
		t.Logf("%s took %.1fs", name, time.Since(started).Seconds())
		return res
	}

	var created machine.Machine
	call("machine_create", nil, &created)
	if created.RunID == "" || created.Status != machine.Booting {
		t.Fatalf("bad create result: %+v", created)
	}
	// Not ctx: once a slow boot has spent it, destroy would fail at once and leave the VM
	// holding one of the host's two macOS slots for the tests after this one.
	defer func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer dcancel()
		res, err := session.CallTool(dctx, &mcp.CallToolParams{Name: "machine_destroy", Arguments: map[string]any{"runId": created.RunID}})
		switch {
		case err != nil:
			t.Errorf("machine_destroy: %v", err)
		case res.IsError:
			t.Errorf("machine_destroy: tool error: %s", contentText(res))
		}
	}()

	for created.Status == machine.Booting {
		call("machine_wait", map[string]any{"runId": created.RunID, "timeoutSeconds": 45}, &created)
	}
	if created.Status != machine.Ready || created.IP == "" {
		t.Fatalf("machine did not become ready: %+v", created)
	}
	t.Logf("machine %s at %s booted in %.1fs", created.RunID, created.IP, created.BootSeconds)

	var execOut machine.ExecResult
	call("machine_exec", map[string]any{"runId": created.RunID, "command": "sw_vers -productVersion && whoami"}, &execOut)
	if execOut.ExitCode != 0 || execOut.Stdout == "" {
		t.Fatalf("exec: %+v", execOut)
	}
	t.Logf("guest says: %q", execOut.Stdout)

	call("machine_exec", map[string]any{"runId": created.RunID, "command": "exit 3"}, &execOut)
	if execOut.ExitCode != 3 {
		t.Fatalf("expected exit 3, got %+v", execOut)
	}

	// Issue #128: the wrapper and the command reach the guest on stdin, so every
	// process listing is short and a pgrep for the command's own words finds
	// nothing but itself, which pgrep never lists.
	call("machine_exec", map[string]any{"runId": created.RunID, "command": "pgrep -fl greenroom"}, &execOut)
	t.Logf("pgrep -fl greenroom: %q", execOut.Stdout)
	if execOut.ExitCode != 0 || !strings.Contains(execOut.Stdout, "/bin/sh -s greenroom-exec 600") {
		t.Fatalf("pgrep -fl greenroom did not list the wrapper's short line: %+v", execOut)
	}
	for _, line := range strings.Split(strings.TrimSpace(execOut.Stdout), "\n") {
		if len(line) > 160 || strings.Contains(line, "pgrep") {
			t.Errorf("process line %q is long or carries the command", line)
		}
	}
	call("machine_exec", map[string]any{"runId": created.RunID, "command": "pgrep -fl some-unique-token-xyz"}, &execOut)
	if execOut.ExitCode != 1 || execOut.Stdout != "" {
		t.Fatalf("pgrep -f for the command's own words matched something: %+v", execOut)
	}
	// Still as before: the timeout kills the command with exit 124 and the output
	// so far, a background child does not hold the call, and log is /usr/bin/log.
	call("machine_exec", map[string]any{"runId": created.RunID, "command": "echo before; sleep 30", "timeoutSeconds": 2}, &execOut)
	if execOut.ExitCode != 124 || !execOut.TimedOut || execOut.Stdout != "before\n" {
		t.Fatalf("timeout: %+v", execOut)
	}
	call("machine_exec", map[string]any{"runId": created.RunID, "command": "sleep 30 & echo started; whence -w log; echo err >&2\nnosuchcmd-greenroom"}, &execOut)
	if execOut.ExitCode != 127 || execOut.Seconds > 10 || execOut.Stdout != "started\nlog: command\n" ||
		!strings.HasPrefix(execOut.Stderr, "err\n") || !strings.Contains(execOut.Stderr, ":2: command not found: nosuchcmd-greenroom") {
		t.Fatalf("background child, log and line numbers: %+v", execOut)
	}

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "node_modules", "junk"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var syncOut machine.SyncResult
	call("machine_sync", map[string]any{"runId": created.RunID, "source": src, "exclude": []string{"node_modules"}}, &syncOut)
	call("machine_exec", map[string]any{"runId": created.RunID, "command": "ls", "cwd": syncOut.Dest}, &execOut)
	if execOut.Stdout != "hello.txt\n" {
		t.Fatalf("sync: expected only hello.txt, got %q", execOut.Stdout)
	}

	res := call("machine_screenshot", map[string]any{"runId": created.RunID}, nil)
	var img *mcp.ImageContent
	for _, c := range res.Content {
		if i, ok := c.(*mcp.ImageContent); ok {
			img = i
		}
	}
	if img == nil || img.MIMEType != "image/jpeg" || len(img.Data) < 4 || img.Data[0] != 0xFF || img.Data[1] != 0xD8 {
		t.Fatalf("screenshot: no JPEG image content in result")
	}
	t.Logf("screenshot: %d bytes, %s", len(img.Data), img.MIMEType)
	// Kept with go test -artifacts, to look at the desktop after machine_exec (issue #117).
	if err := os.WriteFile(filepath.Join(t.ArtifactDir(), "after-exec.jpg"), img.Data, 0o644); err != nil {
		t.Errorf("keep the screenshot: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(root, "runs", created.RunID))
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	t.Logf("run dir: %v", names)
	if len(names) < 3 {
		t.Fatalf("expected manifest, steps and a screenshot in the run dir, got %v", names)
	}
}

func contentText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// waitForAFreeSlot blocks until fewer than two macOS guests are running, the
// host's limit. The suite shares its host with other runs and the maintainer's
// own machines, which can take the last slot between the workflow's preflight
// and a test's Create; a test waits for one like any caller told to.
func waitForAFreeSlot(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for {
		out, err := exec.Command("tart", "list", "--format", "json").Output()
		if err != nil {
			t.Fatalf("tart list: %v", err)
		}
		var vms []struct{ Name, State string }
		if err := json.Unmarshal(out, &vms); err != nil {
			t.Fatalf("tart list: %v", err)
		}
		var running []string
		for _, vm := range vms {
			if strings.EqualFold(vm.State, "running") {
				running = append(running, vm.Name)
			}
		}
		if len(running) < 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("two macOS guests have held the host for 10 minutes: %s", strings.Join(running, ", "))
		}
		t.Logf("two macOS guests are running (%s); waiting for a free slot", strings.Join(running, ", "))
		time.Sleep(15 * time.Second)
	}
}
