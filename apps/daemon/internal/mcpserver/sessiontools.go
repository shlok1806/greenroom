package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// defaultSessionReadWait usually catches a just-sent command's first output while staying well under the client timer.
const defaultSessionReadWait = 5 * time.Second

// addSessionTools exposes interactive guest sessions (machine/ptysession.go): commands that stay alive between
// calls behind a real pty, addressed by (runId, sessionId), never a guest pid.
func addSessionTools(s *mcp.Server, mgr *machine.Manager) {
	type startIn struct {
		RunID   string `json:"runId" jsonschema:"runId from machine_create"`
		Command string `json:"command,omitempty" jsonschema:"The command to run behind a pty. Defaults to an interactive login shell, which is what you want for a session you will type commands into."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_session_start",
		Description: "Start a command in the machine that stays alive between tool calls, with a real terminal " +
			"behind it. Use this instead of machine_exec when state must survive: a shell that keeps its " +
			"directory and variables, a REPL, a debugger, a build you want to watch, a server left running. " +
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
	addTool(s, &mcp.Tool{
		Name: "machine_session_send",
		Description: "Write to a session's input, exactly as given. End with a newline to run a command. Returns " +
			"as soon as the text is delivered, without waiting for the command: call machine_session_read for " +
			"the output.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, machine.SessionSendResult, error) {
		res, err := mgr.SessionSend(ctx, in.RunID, in.SessionID, in.Data)
		return nil, res, err
	})

	type readIn struct {
		RunID       string `json:"runId" jsonschema:"runId from machine_create"`
		SessionID   string `json:"sessionId" jsonschema:"sessionId from machine_session_start"`
		WaitSeconds int    `json:"waitSeconds,omitempty" jsonschema:"How long to wait for new output before answering with none. Default 5, max 50. Use a longer wait while a build runs rather than calling this in a loop."`
	}
	addTool(s, &mcp.Tool{
		Name: "machine_session_read",
		Description: "Read a session's output since your last read. Output is never returned twice: each call " +
			"continues where the last stopped, and pending says how many bytes are still waiting, so keep calling " +
			"while it is above zero. While a command runs, pending can stay at a few bytes with no new output: a " +
			"trailing carriage return or an unfinished terminal code is held until the next byte arrives, so an " +
			"empty read with a small pending means nothing more is ready yet. running says whether the command " +
			"is still going. Terminal colour and cursor codes are stripped, and the run record cites the byte " +
			"range of every read. Only the last 1 MiB of output is kept for reading: if you fall behind, the oldest bytes are lost and dropped says how many. Once running is false, exitCode " +
			"is how the command ended (0 for success), as in machine_exec; a command that exits non-zero is not " +
			"an error. error says why tart itself ended a session, and then there is no exitCode.",
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
	addTool(s, &mcp.Tool{
		Name: "machine_session_close",
		Description: "End a session and clean it up inside the machine; read anything you still want first, since " +
			"its output goes with it. Destroying the machine closes all its sessions, so use this to finish one " +
			"session while the machine carries on.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in closeIn) (*mcp.CallToolResult, machine.SessionCloseResult, error) {
		res, err := mgr.SessionClose(ctx, in.RunID, in.SessionID)
		return nil, res, err
	})
}
