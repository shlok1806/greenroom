package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SyncTool is the tool connect answers itself instead of forwarding.
const SyncTool = "machine_sync"

// SyncNote is appended to machine_sync's description: the meaning of source changes.
const SyncNote = " Through greenroom connect, source is a directory on this computer; it is uploaded to the remote host first."

// Serve dials the daemon, mirrors its tools on a local MCP server and runs that server on
// t (stdio in production) until the client leaves or ctx ends.
func Serve(ctx context.Context, cfg Config, opts Options, t mcp.Transport) error {
	r, err := Dial(ctx, cfg, opts)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	srv, err := NewServer(ctx, r)
	if err != nil {
		return err
	}
	return srv.Run(ctx, t)
}

// NewServer builds the local server: every remote tool with its definition unchanged,
// forwarded as is, except machine_sync, which uploads the local source. It takes the
// daemon's name, version and instructions.
func NewServer(ctx context.Context, r *Remote) (*mcp.Server, error) {
	tools, err := r.Tools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the daemon's tools: %w", err)
	}
	impl := &mcp.Implementation{Name: "greenroom", Version: r.opts.Version}
	var instructions string
	if init := r.InitializeResult(); init != nil {
		instructions = init.Instructions
		if init.ServerInfo != nil && init.ServerInfo.Name != "" {
			info := *init.ServerInfo
			impl = &info
		}
	}
	srv := mcp.NewServer(impl, &mcp.ServerOptions{Instructions: instructions, Logger: sdkLogger(r.opts.Logger)})
	for _, t := range tools {
		tool := *t
		handler := forward(r, tool.Name)
		if tool.Name == SyncTool {
			tool.Description += SyncNote
			handler = syncHandler(r)
		}
		if err := addTool(srv, &tool, handler); err != nil {
			r.opts.Logger.Error("skipping a tool the local server refused", "tool", tool.Name, "err", err)
		}
	}
	r.opts.Logger.Info("connected", "url", r.cfg.URL, "tools", len(tools))
	return srv, nil
}

// addTool turns the SDK's panic on a malformed definition into an error, so one bad
// tool does not take the others down.
func addTool(srv *mcp.Server, tool *mcp.Tool, h mcp.ToolHandler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	srv.AddTool(tool, h)
	return nil
}

// forward sends the call's raw arguments to the daemon and returns its result unchanged.
func forward(r *Remote, name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args any
		if req.Params != nil && len(req.Params.Arguments) > 0 {
			args = req.Params.Arguments
		}
		res, err := r.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil {
			return res, nil
		}
		var werr *jsonrpc.Error
		if answered(err) && errors.As(err, &werr) {
			return nil, werr // the daemon's own protocol error, passed through
		}
		r.opts.Logger.Error("forward failed", "tool", name, "err", err)
		return toolError(fmt.Sprintf("greenroom connect could not reach the daemon at %s: %v", r.cfg.URL, err)), nil
	}
}

// syncHandler answers machine_sync by uploading the local source.
func syncHandler(r *Remote) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args SyncArgs
		if req.Params != nil && len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError("machine_sync: bad arguments: " + err.Error()), nil
			}
		}
		body, err := Upload(ctx, r.HTTPClient(), r.cfg.URL, args)
		if err != nil {
			r.opts.Logger.Error("machine_sync upload failed", "runId", args.RunID, "source", args.Source, "err", err)
			return toolError("machine_sync: " + err.Error()), nil
		}
		var structured map[string]any
		if err := json.Unmarshal(body, &structured); err != nil {
			return toolError(fmt.Sprintf("machine_sync: the daemon's answer is not a JSON object: %.200s", body)), nil
		}
		compact, _ := json.Marshal(structured)
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: string(compact)}},
			StructuredContent: structured,
		}, nil
	}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// Check is `greenroom connect -check`: /healthz, then initialize and list the tools. On
// success it prints "ok: <url> (<n> tools)" to w.
func Check(ctx context.Context, cfg Config, opts Options, w io.Writer) error {
	if err := healthz(ctx, cfg, opts); err != nil {
		return err
	}
	r, err := Dial(ctx, cfg, opts)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	tools, err := r.Tools(ctx)
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	_, err = fmt.Fprintf(w, "ok: %s (%d tools)\n", cfg.URL, len(tools))
	return err
}

// healthz asks <url>/healthz with the token and names what went wrong.
func healthz(ctx context.Context, cfg Config, opts Options) error {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	client := &http.Client{Transport: &authTransport{base: http.DefaultTransport, token: cfg.Token,
		userAgent: "greenroom-connect/" + opts.Version}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return describeTransportError(cfg.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(daemonMessage(body))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrTokenRejected
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("forbidden (HTTP 403): the daemon refused this host; check that the url matches its -public-host: %.200s", msg)
	case resp.StatusCode >= 500:
		return fmt.Errorf("HTTP %d from %s/healthz: is the daemon and its tunnel running? %.200s", resp.StatusCode, cfg.URL, msg)
	}
	return fmt.Errorf("HTTP %d from %s/healthz: %.200s", resp.StatusCode, cfg.URL, msg)
}
