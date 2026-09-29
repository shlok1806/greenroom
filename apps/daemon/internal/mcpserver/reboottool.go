package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// rebootDescription is machine_reboot's description: when to reach for it, what a reboot keeps
// and loses (daemon ADR 0004), and how its wait fits under the MCP first-byte limit.
const rebootDescription = "Restart the machine's macOS guest on the same disk, when its screen or guest stopped " +
	"answering: screenshots or machine_ui time out or fail with \"screen not answering\" (WindowServer hung), " +
	"or every guest call fails with \"is the Tart Guest Agent running?\" (the guest agent wedged). Not for an " +
	"app that misbehaves: quit or kill it with machine_exec instead. " +
	"Kept: the run and its record, the runId, everything on disk (~/work and every synced file, build " +
	"products, installed tools, the input helper, the ssh key, screen-capture and privacy approvals). " +
	"Lost: machine_session_* sessions, running machine_exec commands (they end with an error saying the " +
	"machine rebooted), apps and background processes, /tmp, the live screen (viewers reconnect) and the " +
	"control lease; UI element ids from earlier machine_ui reads no longer aim, so read again. " +
	"The IP address may change. " +
	"A reboot takes about as long as a boot, often more than one call may wait: this call waits at most " +
	"waitSeconds (default 45, max 50), then returns with status rebooting; call machine_wait until status " +
	"is ready. Meanwhile other machine tools fail at once with \"the machine is rebooting\". If the guest does " +
	"not come back within 5 minutes the status is failed with the reason, and the disk is kept: call " +
	"machine_reboot again, or machine_destroy. The result's step is the reboot's step in the run record."

func addRebootTool(s *mcp.Server, mgr *machine.Manager) {
	type rebootIn struct {
		RunID       string `json:"runId" jsonschema:"runId from machine_create"`
		WaitSeconds int    `json:"waitSeconds,omitempty" jsonschema:"How long this call waits for the reboot to finish before returning status rebooting. Default 45, max 50. The reboot goes on either way; machine_wait waits for the rest."`
	}
	type rebootOut struct {
		*machine.Machine
		Step int `json:"step" jsonschema:"The reboot's step in the run record, written when the reboot ends"`
	}
	addTool(s, &mcp.Tool{Name: "machine_reboot", Description: rebootDescription},
		func(ctx context.Context, _ *mcp.CallToolRequest, in rebootIn) (*mcp.CallToolResult, rebootOut, error) {
			// Nothing is posted here: main.go's lifecycle bridge announces the reboot and how it ended.
			mc, step, err := mgr.Reboot(ctx, in.RunID)
			if err != nil {
				return nil, rebootOut{}, err
			}
			if got, werr := mgr.Wait(ctx, in.RunID, waitTimeout(in.WaitSeconds)); werr == nil {
				mc = got
			}
			return nil, rebootOut{Machine: mc, Step: step}, nil
		})
}
