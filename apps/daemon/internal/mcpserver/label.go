package mcpserver

import (
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/summary"
)

// maxLabel bounds what a client can put in a manifest's name or source, in characters.
const maxLabel = 120

// runName is machine_create's name on one line, at most summary.NameWords words; "" when none
// was given.
func runName(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return clipRunes(summary.Name(name, ""), maxLabel)
}

// genericAgents are User-Agent products that name an HTTP library, not the coding agent.
var genericAgents = []string{"go-http-client", "node", "node-fetch", "undici", "axios", "python", "python-requests",
	"python-httpx", "aiohttp", "curl", "wget", "mozilla", "okhttp", "java", "dart"}

// clientName is the MCP client that sent req, as it named itself ("claude-code"), or "" when
// it did not say. The daemon's server is stateless, so an older-protocol client's initialize
// never reaches a tool call: its clientInfo is known only from a newer client's per-request
// _meta. Otherwise the HTTP User-Agent's first product names it, unless that is a library's.
// The summary words it (summary.SourceWords).
func clientName(req *mcp.CallToolRequest) string {
	if req == nil {
		return ""
	}
	if info := req.ClientInfo(); info != nil && strings.TrimSpace(info.Name) != "" {
		return clipRunes(strings.Join(strings.Fields(info.Name), " "), maxLabel)
	}
	if req.Extra == nil || req.Extra.Header == nil {
		return ""
	}
	return agentProduct(req.Extra.Header.Get("User-Agent"))
}

// agentCaller names the agent that sent req for a run's record (daemon ADR 0008): "agent
// (claude-code)", or "agent" when the client did not say which it is.
func agentCaller(req *mcp.CallToolRequest) string {
	if name := clientName(req); name != "" {
		return "agent (" + name + ")"
	}
	return "agent"
}

// agentProduct is a User-Agent's first product name ("claude-code/2.1.3 (cli)" is
// "claude-code"), or "" for an HTTP library's.
func agentProduct(ua string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(ua), " ")
	name, _, _ := strings.Cut(first, "/")
	lower := strings.ToLower(name)
	if lower == "" {
		return ""
	}
	for _, g := range genericAgents {
		if lower == g || strings.HasPrefix(lower, g+"-") {
			return ""
		}
	}
	return clipRunes(name, maxLabel)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
