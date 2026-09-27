package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/shlok1806/greenroom/apps/daemon/internal/api"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// hostTransport sends every request with a given Host and bearer token, as cloudflared
// delivers tunnel traffic to the daemon: from loopback, with the public name as Host.
type hostTransport struct{ host, token string }

func (t hostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.host != "" {
		req.Host = t.host
	}
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// ADR 0022: a machine_pull that came through the public host may not name a dest, or a token
// holder could write any file of the host's user (~/.zshrc, LaunchAgents, authorized_keys).
// It lands in the run directory instead. A loopback caller is on the host and keeps its dest.
func TestRoutesKeepAPublicHostPullInsideTheRunDirectory(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("no rsync on this host")
	}
	bin, _ := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0),
		machine.WithReadyTimeout(10*time.Second), machine.WithSSHProbe(func(context.Context, string, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	const public, token = "gr.example.com", "0123456789abcdef0123456789abcdef"
	ts := httptest.NewServer(routes(mgr, session.NewRegistry(mgr.Root, 2), "img", public, token, t.TempDir(), api.Version{}, log))
	t.Cleanup(ts.Close)
	ctx := context.Background()
	mc, err := mgr.Create(ctx, "img")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Destroy(context.Background(), mc.RunID) })
	if mc, err = mgr.Wait(ctx, mc.RunID, 10*time.Second); err != nil || mc.Status != machine.Ready {
		t.Fatalf("machine not ready: %+v, %v", mc, err)
	}

	// A fake ssh runs rsync's guest side in a local shell whose HOME is the guest home.
	sshBin := t.TempDir()
	ssh := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -i|-o) shift 2 ;; *) break ;; esac; done\n" +
		"shift\ncd \"$HOME\" && exec sh -c \"$*\"\n"
	if err := os.WriteFile(filepath.Join(sshBin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", sshBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "payload"), []byte("echo pwned\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pull := func(host, tok string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
		cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp",
			HTTPClient: &http.Client{Transport: hostTransport{host, tok}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatalf("connect as %q: %v", host, err)
		}
		defer func() { _ = cs.Close() }()
		args["runId"] = mc.RunID
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "machine_pull", Arguments: args})
		if err != nil {
			t.Fatalf("machine_pull as %q: %v", host, err)
		}
		return res
	}
	text := func(res *mcp.CallToolResult) string {
		var b strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return b.String()
	}

	target := filepath.Join(t.TempDir(), "LaunchAgents")
	res := pull(public, token, map[string]any{"source": "payload", "dest": target})
	if !res.IsError || !strings.Contains(text(res), "dest is chosen by greenroom connect on your computer; omit dest") {
		t.Fatalf("public host with dest = %q, want the refusal", text(res))
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("a public-host pull wrote to the dest it named")
	}

	res = pull(public, token, map[string]any{"source": "payload"})
	if res.IsError {
		t.Fatalf("public host without dest: %q", text(res))
	}
	var out machine.PullResult
	if err := json.Unmarshal([]byte(text(res)), &out); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mc.Dir, fmt.Sprintf("%03d-pull", out.Step)); out.Dest != want {
		t.Errorf("public host dest = %s, want the run's %s", out.Dest, want)
	}
	if b, err := os.ReadFile(filepath.Join(out.Dest, "payload")); err != nil || string(b) != "echo pwned\n" {
		t.Errorf("payload in the run dir = %q, %v", b, err)
	}

	mine := filepath.Join(t.TempDir(), "mine")
	res = pull("", "", map[string]any{"source": "payload", "dest": mine})
	if res.IsError {
		t.Fatalf("loopback with dest: %q", text(res))
	}
	if _, err := os.ReadFile(filepath.Join(mine, "payload")); err != nil {
		t.Errorf("loopback pull did not land in its dest: %v", err)
	}
}
