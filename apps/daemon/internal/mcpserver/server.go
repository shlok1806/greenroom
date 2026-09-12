// Package mcpserver exposes machines to agents as MCP tools.
package mcpserver

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// Version is stamped into the MCP server implementation info.
const Version = "0.0.1"

// New builds the MCP server over mgr. defaultImage is used when a caller
// does not name one.
func New(mgr *machine.Manager, defaultImage string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "greenroom", Version: Version}, &mcp.ServerOptions{
		Instructions: "greenroom gives you a disposable macOS machine. Call machine_create once, keep its runId, " +
			"then machine_sync to copy a project in, machine_exec to build and run, machine_screenshot to look at " +
			"the screen, and machine_destroy when done. Every run is recorded under ~/.greenroom/runs/<runId>.",
	})

	type createIn struct {
		Image string `json:"image,omitempty" jsonschema:"OCI image to clone. Defaults to the daemon's configured image."`
	}
	type createOut struct {
		RunID       string  `json:"runId"`
		MachineName string  `json:"machineName"`
		IP          string  `json:"ip"`
		Image       string  `json:"image"`
		BootSeconds float64 `json:"bootSeconds"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_create",
		Description: "Clone and boot a fresh macOS machine. Takes about 35 seconds. Returns the runId every other tool needs.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, createOut, error) {
		image := in.Image
		if image == "" {
			image = defaultImage
		}
		mc, boot, err := mgr.Create(ctx, image)
		if err != nil {
			return nil, createOut{}, err
		}
		return nil, createOut{RunID: mc.RunID, MachineName: mc.Name, IP: mc.IP, Image: mc.Image, BootSeconds: round(boot)}, nil
	})

	type listOut struct {
		Machines []*machine.Machine `json:"machines"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_list",
		Description: "List live machines and their runIds, for example to pick up a machine from an earlier session.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listOut, error) {
		return nil, listOut{Machines: mgr.List()}, nil
	})

	type syncIn struct {
		RunID   string   `json:"runId" jsonschema:"runId from machine_create"`
		Source  string   `json:"source" jsonschema:"Absolute path of a directory on the host to copy into the machine"`
		Dest    string   `json:"dest,omitempty" jsonschema:"Destination path in the guest, relative to the admin home. Defaults to work/<basename of source>."`
		Exclude []string `json:"exclude,omitempty" jsonschema:"rsync exclude patterns, e.g. node_modules, .git, build"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_sync",
		Description: "Copy a host directory into the machine with rsync. Fast on repeat calls; only changed files move.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in syncIn) (*mcp.CallToolResult, machine.SyncResult, error) {
		res, err := mgr.Sync(ctx, in.RunID, in.Source, in.Dest, in.Exclude)
		res.Seconds = round(res.Seconds)
		return nil, res, err
	})

	type execIn struct {
		RunID          string `json:"runId" jsonschema:"runId from machine_create"`
		Command        string `json:"command" jsonschema:"Shell command, run with zsh -lc in the guest"`
		Cwd            string `json:"cwd,omitempty" jsonschema:"Working directory in the guest, e.g. work/myapp"`
		TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"Kill the command after this many seconds. Default 600."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_exec",
		Description: "Run a shell command inside the machine and return stdout, stderr and the exit code.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, machine.ExecResult, error) {
		timeout := 10 * time.Minute
		if in.TimeoutSeconds > 0 {
			timeout = time.Duration(in.TimeoutSeconds) * time.Second
		}
		res, err := mgr.Exec(ctx, in.RunID, in.Command, in.Cwd, timeout)
		res.Seconds = round(res.Seconds)
		return nil, res, err
	})

	type screenshotIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
	}
	type screenshotOut struct {
		Path  string `json:"path"`
		Bytes int    `json:"bytes"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_screenshot",
		Description: "Capture the machine's screen. Returns the PNG image and the path where it was saved in the run directory.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in screenshotIn) (*mcp.CallToolResult, screenshotOut, error) {
		png, path, err := mgr.Screenshot(ctx, in.RunID)
		if err != nil {
			return nil, screenshotOut{}, err
		}
		out := screenshotOut{Path: path, Bytes: len(png)}
		meta, _ := json.Marshal(out)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{Data: png, MIMEType: "image/png"},
				&mcp.TextContent{Text: string(meta)},
			},
		}, out, nil
	})

	type destroyIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
	}
	type destroyOut struct {
		OK bool `json:"ok"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_destroy",
		Description: "Stop and delete the machine. The run's recording stays on disk.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destroyIn) (*mcp.CallToolResult, destroyOut, error) {
		if err := mgr.Destroy(ctx, in.RunID); err != nil {
			return nil, destroyOut{}, err
		}
		return nil, destroyOut{OK: true}, nil
	})

	return s
}

func round(f float64) float64 { return math.Round(f*100) / 100 }
