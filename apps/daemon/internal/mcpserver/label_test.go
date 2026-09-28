package mcpserver

import (
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

func TestCreateRecordsTheRunsNameAndClient(t *testing.T) {
	h := newHarness(t)
	var mc machine.Machine
	h.call("machine_create", map[string]any{"name": "  TipSplit:  split the bill between three people "}, &mc)
	man, err := machine.ReadManifest(h.mgr.RunDir(mc.RunID))
	if err != nil {
		t.Fatal(err)
	}
	if man.Name != "TipSplit: split the bill between…" {
		t.Errorf("name = %q, want it on one line and cut to five words", man.Name)
	}
	if man.Source != "test" {
		t.Errorf("source = %q, want the client's own name", man.Source)
	}
}

func TestCreateWithoutANameLeavesItToTheSummary(t *testing.T) {
	h := newHarness(t)
	var mc machine.Machine
	h.call("machine_create", nil, &mc)
	man, err := machine.ReadManifest(h.mgr.RunDir(mc.RunID))
	if err != nil {
		t.Fatal(err)
	}
	if man.Name != "" {
		t.Errorf("name = %q, want none", man.Name)
	}
}

func TestAgentProductNamesTheAgentNotItsHTTPLibrary(t *testing.T) {
	cases := map[string]string{
		"claude-code/2.1.3 (cli)": "claude-code",
		"codex-mcp-client/0.40":   "codex-mcp-client",
		"Go-http-client/1.1":      "",
		"node":                    "",
		"python-httpx/0.27":       "",
		"Mozilla/5.0 (Macintosh)": "",
		"":                        "",
	}
	for ua, want := range cases {
		if got := agentProduct(ua); got != want {
			t.Errorf("agentProduct(%q) = %q, want %q", ua, got, want)
		}
	}
}
