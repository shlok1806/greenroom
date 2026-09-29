package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// declaredOutputSchemas is every tool's output schema as a client reads it from tools/list.
func (h *harness) declaredOutputSchemas() map[string]*jsonschema.Schema {
	h.t.Helper()
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]*jsonschema.Schema{}
	for _, tool := range res.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			h.t.Fatal(err)
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(raw, &s); err != nil {
			h.t.Fatalf("%s: output schema does not parse: %v", tool.Name, err)
		}
		out[tool.Name] = &s
	}
	return out
}

// validate checks a result's structured content against schema as a strict client does.
func validate(schema *jsonschema.Schema, res *mcp.CallToolResult) error {
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return resolved.Validate(v)
}

// olderSchema is schema as a client that connected before every optional property was added
// still holds it: each object keeps only its required properties (issue #251).
func olderSchema(t *testing.T, schema *jsonschema.Schema) *jsonschema.Schema {
	t.Helper()
	s := schema.CloneSchemas()
	walkSchema(s, func(s *jsonschema.Schema) {
		for name := range s.Properties {
			if !slices.Contains(s.Required, name) {
				delete(s.Properties, name)
			}
		}
		s.PropertyOrder = nil
	})
	return s
}

// Issues #251 and #247: a client keeps the schemas it fetched when its session began, so each
// object must accept a property it does not list, at every depth (daemon ADR 0007).
func TestOutputSchemasAreOpen(t *testing.T) {
	for name, h := range map[string]*harness{"default": newHarness(t), "toolkit": newToolkitHarness(t)} {
		schemas := h.declaredOutputSchemas()
		if len(schemas) == 0 {
			t.Fatalf("%s: no tool declares an output schema", name)
		}
		for tool, s := range schemas {
			if closed := closedObjects(s); len(closed) > 0 {
				t.Errorf("%s: %s's output schema refuses unlisted properties at %v; register it with addTool", name, tool, closed)
			}
		}
	}

	// The #251 case itself: the result has a field the client's schema lacks.
	h := newHarness(t)
	sync := h.declaredOutputSchemas()["machine_sync"]
	res := &mcp.CallToolResult{StructuredContent: map[string]any{
		"dest": "work/app", "summary": "", "seconds": 0.5, "mirror": false, "strays": 1, "strayPaths": []string{"x"},
		"addedLater": true,
	}}
	if err := validate(olderSchema(t, sync), res); err != nil {
		t.Errorf("a machine_sync result with fields added since the client connected fails its check: %v", err)
	}
}

// Every tool's structured output matches the schema tools/list declares for it, and still
// matches the schema a client from before its optional fields holds. A tool with an output
// schema that this test does not call fails it, so a new tool is covered when it is added.
func TestEveryToolsOutputMatchesItsSchema(t *testing.T) {
	h := newHarness(t)
	schemas := h.declaredOutputSchemas()
	called := map[string]bool{}
	check := func(tool string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res := h.call(tool, args, nil)
		called[tool] = true
		schema, ok := schemas[tool]
		if !ok {
			return res
		}
		if res.StructuredContent == nil {
			t.Errorf("%s declares an output schema but returned no structured content", tool)
			return res
		}
		if err := validate(schema, res); err != nil {
			t.Errorf("%s: output does not match its declared schema: %v", tool, err)
		}
		if err := validate(olderSchema(t, schema), res); err != nil {
			t.Errorf("%s: output fails a client holding the schema from before its optional fields: %v", tool, err)
		}
		return res
	}

	var mc machine.Machine
	res := check("machine_create", map[string]any{"name": "Schema check"})
	decode(t, res, &mc)
	runID := mc.RunID
	for i := 0; i < 40 && mc.Status == machine.Booting; i++ {
		decode(t, check("machine_wait", map[string]any{"runId": runID, "timeoutSeconds": 5}), &mc)
	}
	if mc.Status != machine.Ready {
		t.Fatalf("machine is %s, want ready", mc.Status)
	}
	check("machine_list", nil)

	// machine_sync and machine_pull over a local rsync, with a stray so strays and strayPaths are set.
	home := guestHome(t)
	dest := filepath.Join(home, "work", "app")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stray.go"), []byte("package app"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	var synced machine.SyncResult
	decode(t, check("machine_sync", map[string]any{"runId": runID, "source": source, "dest": "work/app"}), &synced)
	if synced.Strays == nil || len(synced.StrayPaths) == 0 {
		t.Fatalf("sync result %+v, want strays and strayPaths set so the check covers them", synced)
	}
	check("machine_pull", map[string]any{"runId": runID, "source": "work/app/main.go"})

	var st machine.ExecStatus
	decode(t, check("machine_exec", map[string]any{"runId": runID, "command": "echo hi"}), &st)
	check("machine_exec_wait", map[string]any{"runId": runID, "execId": st.ExecID})
	check("machine_approve_capture", map[string]any{"runId": runID, "app": "~/work/Shot/Shot.app"})
	h.putShot()
	check("machine_screenshot", map[string]any{"runId": runID})
	h.putUI(tipSplitUI)
	check("machine_ui", map[string]any{"runId": runID})
	for _, tool := range inputTools {
		args := inputArgs(tool, runID)
		if tool == "machine_scroll" {
			args["deltaY"] = 3
		}
		check(tool, args)
	}

	var started machine.SessionStartResult
	decode(t, check("machine_session_start", map[string]any{"runId": runID}), &started)
	check("machine_session_send", map[string]any{"runId": runID, "sessionId": started.SessionID, "data": "echo hi\n"})
	check("machine_session_read", map[string]any{"runId": runID, "sessionId": started.SessionID, "waitSeconds": 1})
	check("machine_session_close", map[string]any{"runId": runID, "sessionId": started.SessionID})
	check("machine_reboot", map[string]any{"runId": runID})

	check("agent_send", map[string]any{"runId": runID, "kind": "note", "text": "schema check"})
	check("agent_wait", map[string]any{"runId": runID, "after": 0, "timeoutSeconds": 1})
	check("agent_transcript", map[string]any{"runId": runID})
	check("run_report", map[string]any{"runId": runID})
	check("run_finish", map[string]any{"runId": runID, "outcome": "unverified", "summary": "Schema check.", "destroy": false})
	check("machine_destroy", map[string]any{"runId": runID})

	var missed []string
	for tool := range schemas {
		if !called[tool] {
			missed = append(missed, tool)
		}
	}
	sort.Strings(missed)
	if len(missed) > 0 {
		t.Errorf("tools with an output schema this test never calls: %v", missed)
	}
}

func decode(t *testing.T, res *mcp.CallToolResult, out any) {
	t.Helper()
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
}
