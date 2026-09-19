package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
	"github.com/shlok1806/greenroom/apps/daemon/internal/verifier"
)

// TestMachineVerifyReturnsAVerdict drives the whole path an agent uses:
// the MCP tool, the verifier loop, a scripted model and a fake machine.
func TestMachineVerifyReturnsAVerdict(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"report_verdict","arguments":"{\"verdict\":\"pass\",\"summary\":\"The app built and launched.\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":5}}`)
	}))
	defer model.Close()

	bin, _ := testsupport.FakeTart(t)
	mgr, err := machine.NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		machine.WithTartBin(bin), machine.WithReadyTimeout(10*time.Second),
		machine.WithSSHProbe(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	v, err := verifier.New(mgr, verifier.Config{
		BaseURL: model.URL, APIKey: "k", Model: "reasoner", MaxSteps: 4, Budget: 30 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return New(mgr, "img", v) },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tool := range tools.Tools {
		if tool.Name == "machine_verify" {
			found = true
		}
	}
	if !found {
		t.Fatal("machine_verify is missing although a verifier is configured")
	}

	var mc machine.Machine
	call := func(name string, args map[string]any, out any) {
		t.Helper()
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			var b strings.Builder
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					b.WriteString(tc.Text)
				}
			}
			t.Fatalf("%s: %s", name, b.String())
		}
		if out != nil {
			decode(t, res.StructuredContent, out)
		}
	}
	call("machine_create", nil, &mc)
	for i := 0; i < 20 && mc.Status == machine.Booting; i++ {
		call("machine_wait", map[string]any{"runId": mc.RunID, "timeoutSeconds": 5}, &mc)
	}
	if mc.Status != machine.Ready {
		t.Fatalf("machine is %s", mc.Status)
	}

	var rep verifier.Report
	call("machine_verify", map[string]any{"runId": mc.RunID, "task": "Build the app and check it launches."}, &rep)
	if rep.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass", rep.Verdict)
	}
	if rep.Summary == "" {
		t.Error("the report has no summary")
	}
	if rep.RunID != mc.RunID {
		t.Errorf("runId = %q, want %q", rep.RunID, mc.RunID)
	}
}

func decode(t *testing.T, sc any, out any) {
	t.Helper()
	data, _ := json.Marshal(sc)
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
}
