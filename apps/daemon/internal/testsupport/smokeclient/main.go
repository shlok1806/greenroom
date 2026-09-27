// Command smokeclient drives a running greenroom daemon over MCP the way a
// coding agent would, and prints each tool result. It exists for manual
// end-to-end checks of the daemon's wiring and is not part of the build.
//
//	go run ./internal/testsupport/smokeclient -url http://127.0.0.1:7777/mcp
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:7777/mcp", "MCP endpoint")
	live := flag.String("live", "", "create a machine, sync this host directory into it, hand the verifier a task, and leave everything running for a person to join")
	flag.Parse()
	ctx := context.Background()

	client := mcp.NewClient(&mcp.Implementation{Name: "smoke", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: *url}, nil)
	if err != nil {
		fail("connect", err)
	}
	defer func() { _ = sess.Close() }()

	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		fail("list tools", err)
	}
	fmt.Print("tools:")
	for _, t := range tools.Tools {
		fmt.Print(" ", t.Name)
	}
	fmt.Println()

	call := func(name string, args map[string]any) map[string]any {
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			fail(name, err)
		}
		if res.IsError {
			var text string
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text += tc.Text
				}
			}
			fmt.Printf("%s -> tool error: %s\n", name, text)
			return nil
		}
		out := map[string]any{}
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
		short, _ := json.Marshal(out)
		if len(short) > 400 {
			short = append(short[:400], []byte("...")...)
		}
		fmt.Printf("%s -> %s\n", name, short)
		return out
	}

	if *live != "" {
		runLive(call, *live)
		return
	}
	created := call("machine_create", map[string]any{})
	runID, _ := created["runId"].(string)
	if runID == "" {
		fail("machine_create", fmt.Errorf("no runId"))
	}
	for i := 0; i < 20; i++ {
		st := call("machine_wait", map[string]any{"runId": runID, "timeoutSeconds": 5})
		if st["status"] == "ready" || st["status"] == "failed" {
			break
		}
	}
	call("machine_exec", map[string]any{"runId": runID, "command": "swift build"})
	call("machine_screenshot", map[string]any{"runId": runID})
	call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Build the app and tell me if it launches."})
	call("agent_wait", map[string]any{"runId": runID, "after": 0, "timeoutSeconds": 3})
	// Errors the coder is meant to read.
	call("agent_send", map[string]any{"runId": runID, "kind": "answer", "text": "Release"})
	call("agent_send", map[string]any{"runId": "nope", "kind": "note", "text": "x"})
	// Give the human (curl, outside) a moment to speak, then read everything.
	time.Sleep(4 * time.Second)
	call("agent_transcript", map[string]any{"runId": runID})
	call("machine_destroy", map[string]any{"runId": runID})
	time.Sleep(300 * time.Millisecond)
	call("agent_transcript", map[string]any{"runId": runID, "after": 3})
	call("run_report", map[string]any{"runId": runID}) // the record answers after the destroy (ADR 0034)
	fmt.Println("runId:", runID)
}

// runLive boots a real machine, syncs a project in, and gives the verifier a
// first task. It destroys nothing: the run stays up for the companion app.
func runLive(call func(string, map[string]any) map[string]any, dir string) {
	created := call("machine_create", map[string]any{})
	runID, _ := created["runId"].(string)
	if runID == "" {
		fail("machine_create", fmt.Errorf("no runId"))
	}
	for i := 0; i < 12; i++ {
		st := call("machine_wait", map[string]any{"runId": runID, "timeoutSeconds": 50})
		if st["status"] == "ready" || st["status"] == "failed" {
			break
		}
	}
	call("machine_sync", map[string]any{"runId": runID, "source": dir, "exclude": []string{".git", "node_modules", ".build", ".claude"}})
	call("agent_send", map[string]any{"runId": runID, "kind": "note", "text": "A human is watching this run from the companion app and may answer your questions. The project is synced to ~/work in the machine."})
	call("agent_send", map[string]any{"runId": runID, "kind": "task", "text": "Look around the synced project in ~/work: list it, read its README, and tell me what it is and whether the machine has the toolchain it would need to build it. Ask if anything is unclear."})
	fmt.Println("live run:", runID)
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
