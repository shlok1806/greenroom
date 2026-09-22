package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// defaultSessionReadWait is how long a read waits for new output before
// answering empty. Long enough that a caller which has just sent a command
// usually gets its first output in the same call, short enough to leave room
// under the MCP client's first-byte timer.
const defaultSessionReadWait = 5 * time.Second

// addSessionTools exposes the guest's interactive sessions (ptysession.go).
//
// machine_exec is still the right tool for "run this and tell me what
// happened". These four are for the things that need the process to stay
// alive between calls: a shell whose cwd and exported variables persist, a
// REPL, a debugger, a long build watched while it runs, a server left in the
// foreground. The command sits behind a real pty, so anything that checks
// isatty() behaves the way it does for a developer at a terminal.
//
// The handle is (runId, sessionId). Neither is a guest pid, so a call
// against a machine that has been destroyed answers "no machine for run",
// which is the truth, rather than an error about a process id that means
// nothing to the caller.
func addSessionTools(s *mcp.Server, mgr *machine.Manager) {
	type startIn struct {
		RunID   string `json:"runId" jsonschema:"runId from machine_create"`
		Command string `json:"command,omitempty" jsonschema:"The command to run behind a pty. Defaults to an interactive login shell, which is what you want for a session you will type commands into."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_session_start",
		Description: "Start a command in the machine that stays alive between tool calls, with a real terminal " +
			"behind it. Use this instead of machine_exec when state has to survive: a shell that remembers its " +
			"directory and variables, a REPL, a debugger, a build you want to watch, a server you leave running. " +
			"Because there is a real tty, builds behave exactly as they do for a developer. Returns a sessionId; " +
			"send input with machine_session_send, collect output with machine_session_read, and finish with " +
			"machine_session_close.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in startIn) (*mcp.CallToolResult, machine.SessionStartResult, error) {
		res, err := mgr.SessionStart(ctx, in.RunID, in.Command)
		return nil, res, err
	})

	type sendIn struct {
		RunID     string `json:"runId" jsonschema:"runId from machine_create"`
		SessionID string `json:"sessionId" jsonschema:"sessionId from machine_session_start"`
		Data      string `json:"data" jsonschema:"Exactly what to write to the session's input. Include a trailing newline to run a command: without one the shell just holds the characters on its line, the same as typing without pressing return."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_session_send",
		Description: "Write to a session's input, exactly as given. End with a newline to actually run a command. " +
			"This returns as soon as the text is delivered and does not wait for the command: call " +
			"machine_session_read for the output.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, machine.SessionSendResult, error) {
		res, err := mgr.SessionSend(ctx, in.RunID, in.SessionID, in.Data)
		return nil, res, err
	})

	type readIn struct {
		RunID       string `json:"runId" jsonschema:"runId from machine_create"`
		SessionID   string `json:"sessionId" jsonschema:"sessionId from machine_session_start"`
		WaitSeconds int    `json:"waitSeconds,omitempty" jsonschema:"How long to wait for new output before answering with none. Default 5, max 50. Use a longer wait while a build runs rather than calling this in a loop."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_session_read",
		Description: "Read a session's output since your last read. Output is never returned twice: each call " +
			"continues where the last one stopped, and pending says how many bytes are still waiting, so keep " +
			"calling while it is above zero. running says whether the command is still going. Terminal colour " +
			"and cursor codes are stripped, and the run record cites the byte range of every read. The daemon keeps " +
			"only the last 1 MiB of output on the host and the guest stores none: if you fall behind, the oldest " +
			"bytes are gone for good and dropped says how many. error says why tart ended a session that is no " +
			"longer running; a command that exits non-zero is not an error.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, machine.SessionReadResult, error) {
		wait := defaultSessionReadWait
		if in.WaitSeconds > 0 {
			wait = time.Duration(in.WaitSeconds) * time.Second
		}
		res, err := mgr.SessionRead(ctx, in.RunID, in.SessionID, wait)
		return nil, res, err
	})

	type closeIn struct {
		RunID     string `json:"runId" jsonschema:"runId from machine_create"`
		SessionID string `json:"sessionId" jsonschema:"sessionId from machine_session_start"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_session_close",
		Description: "End a session and clean it up inside the machine. Read anything you still want first: " +
			"the output goes with it. Destroying the machine closes every session it holds, so this is for " +
			"finishing with one session while the machine carries on.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in closeIn) (*mcp.CallToolResult, machine.SessionCloseResult, error) {
		res, err := mgr.SessionClose(ctx, in.RunID, in.SessionID)
		return nil, res, err
	})
}
