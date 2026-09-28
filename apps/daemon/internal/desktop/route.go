package desktop

import (
	"bytes"
	"encoding/json"
	"slices"
)

// Which calls are the toolkit's (daemon ADR 0006 point 9). machine_type, machine_key,
// machine_scroll and machine_screenshot keep their old calls beside the toolkit's: a call with only
// the arguments the tool took before goes the old way and behaves exactly as before; any new
// argument makes it a toolkit call. MCP and the verifier route by the same rule.

// legacyKeys are the arguments each shared tool took before the toolkit (runId is every MCP
// tool's). The verifier's screenshot question is the toolkit's, like the crops.
var legacyKeys = map[string]map[string]bool{
	"machine_type":       {"runId": true, "text": true},
	"machine_key":        {"runId": true, "key": true, "mods": true},
	"machine_scroll":     {"runId": true, "x": true, "y": true, "deltaX": true, "deltaY": true},
	"machine_screenshot": {"runId": true},
}

// ToolkitTools are the tools only the toolkit has.
var ToolkitTools = []string{"machine_snapshot", "machine_find", "machine_press", "machine_set_value",
	"machine_wait_for", "machine_expect"}

// ToolkitCall reports whether a call of tool with arguments raw (a JSON object) is the toolkit's:
// a tool only the toolkit has, or a shared one called with a new argument. A null argument counts
// as not given. Arguments that do not parse are the old tool's to refuse. Any other tool is not
// the toolkit's.
func ToolkitCall(tool string, raw []byte) bool {
	if slices.Contains(ToolkitTools, tool) {
		return true
	}
	legacy, shared := legacyKeys[tool]
	if !shared {
		return false
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return false
	}
	for k, v := range args {
		if !legacy[k] && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return true
		}
	}
	return false
}
