// Package mcpserver exposes machines and their conversations to agents as MCP tools.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"log/slog"
	"math"
	"runtime/debug"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Version is stamped into the MCP server implementation info.
const Version = "0.0.2"

const (
	defaultWait = 45 * time.Second
	maxWait     = 50 * time.Second // under the MCP client's 60 s first-byte timer
	jpegQuality = 80
)

// Option changes what New builds.
type Option func(*options)

type options struct {
	publicHost bool
}

// ForPublicHost builds the server that answers calls from the public host (ADR 0021): its
// caller is on another computer, so no tool writes where the caller names on this host.
// machine_pull refuses a dest there and always copies into the run directory (ADR 0022).
func ForPublicHost() Option { return func(o *options) { o.publicHost = true } }

// ErrRemoteDest is machine_pull's answer to a dest from the public host.
var ErrRemoteDest = errors.New("through the tunnel, dest is chosen by greenroom connect on your computer; omit dest")

// New builds the MCP server over mgr and reg. defaultImage is used when machine_create names none.
func New(mgr *machine.Manager, defaultImage string, reg *session.Registry, opts ...Option) *mcp.Server {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "greenroom", Version: Version}, &mcp.ServerOptions{
		Instructions: "greenroom gives you a disposable macOS machine. Call machine_create once and keep its runId, " +
			"then machine_wait until status is ready. Use machine_sync to copy a project in, machine_pull to copy files out, machine_exec to build " +
			"and run (a command still going after 45 s comes back running, with an execId for machine_exec_wait), " +
			"machine_screenshot to look at the screen, machine_ui to find controls and their centers before " +
			"machine_click, and machine_destroy when done. " +
			"Every run also owns one conversation: agent_send posts into it, agent_wait blocks for what comes " +
			"back, and agent_transcript reads it. That is how you reach greenroom's verifier and how a watching " +
			"human reaches you. " +
			"Every run is recorded under ~/.greenroom/runs/<runId>.",
	})

	type createIn struct {
		Image string `json:"image,omitempty" jsonschema:"OCI image to clone. Defaults to the daemon's configured image."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_create",
		Description: "Clone and start a fresh macOS machine. Returns at once with status booting and the runId every " +
			"other tool needs. Call machine_wait next; boot takes 30 to 90 seconds. Machines run headless; " +
			"a person watches the screen live in the greenroom companion app.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, *machine.Machine, error) {
		image := in.Image
		if image == "" {
			image = defaultImage
		}
		return wrap(mgr.Create(ctx, image))
	})

	type waitIn struct {
		RunID          string `json:"runId" jsonschema:"runId from machine_create"`
		TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"How long to wait before returning the current status. Default 45, max 50."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_wait",
		Description: "Wait for a machine to finish booting. Returns its status: booting (call again), ready (ip and " +
			"bootSeconds are set), or failed (error is set). A ready machine also reports toolchain, what its image " +
			"measured when it was built (Xcode present or not and its version, whether XCTest and swift-testing packages run with " +
			"swift test and whether xcodebuild builds, swift and Command Line Tools versions; known false when the image says nothing), and " +
			"desktop, whether the screen showed anything besides the desktop and Finder at ready (clean false lists " +
			"unexpectedWindows and unexpectedApps, such as a permission prompt; greenroom never closes them). models " +
			"names who verifies the run: the brain (nim, manual or none), its reasoning model and its screenshot " +
			"describer, with the options their requests carry.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, *machine.Machine, error) {
		return wrap(mgr.Wait(ctx, in.RunID, waitTimeout(in.TimeoutSeconds)))
	})

	type listedMachine struct {
		*machine.Machine
		LastActivity time.Time `json:"lastActivity" jsonschema:"When a step or message last happened on this run. Screen frames do not count."`
		IdleSeconds  int       `json:"idleSeconds" jsonschema:"Seconds since lastActivity"`
	}
	type listOut struct {
		Machines []listedMachine `json:"machines"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_list",
		Description: "List live machines with their runIds, status and how long each has been idle (idleSeconds: no " +
			"tool step or message since lastActivity), for example to pick up a machine from an earlier session or " +
			"to tell a stale run from a busy one when the host is at its machine limit.",
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, listOut, error) {
		out := listOut{Machines: []listedMachine{}}
		for _, mc := range mgr.List() {
			steps, _ := machine.ReadStepLog(mc.Dir) // unreadable counts as no steps, as in /api/runs
			last := mgr.ActivityFrom(mc.RunID, mc.CreatedAt, steps)
			out.Machines = append(out.Machines, listedMachine{Machine: mc, LastActivity: last,
				IdleSeconds: int(max(0, time.Since(last)).Seconds())})
		}
		return nil, out, nil
	})

	type syncIn struct {
		RunID   string   `json:"runId" jsonschema:"runId from machine_create"`
		Source  string   `json:"source" jsonschema:"Absolute path of a directory on the host to copy into the machine"`
		Dest    string   `json:"dest,omitempty" jsonschema:"Guest directory, relative to the guest home: work/myapp and ~/work/myapp are the same place. Must stay inside the home: no absolute path, no .., not ~ itself. Defaults to work/<basename of source>."`
		Exclude []string `json:"exclude,omitempty" jsonschema:"rsync exclude patterns, e.g. node_modules, .git, build"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_sync",
		Description: "Copy a host directory into the machine with rsync. Fast on repeat calls; only changed files move. " +
			"dest is relative to the guest home, and a leading ~/ is accepted (it means the same). The result's dest is " +
			"the path relative to the home, which machine_exec's cwd takes as is.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in syncIn) (*mcp.CallToolResult, machine.SyncResult, error) {
		res, err := mgr.Sync(ctx, in.RunID, in.Source, in.Dest, in.Exclude)
		res.Seconds = round(res.Seconds)
		return nil, res, err
	})

	type pullIn struct {
		RunID   string   `json:"runId" jsonschema:"runId from machine_create"`
		Source  string   `json:"source" jsonschema:"Guest file or directory to copy out: relative to the guest home (work/myapp/build/report.xml), starting with ~/, or absolute (/tmp/app.log)"`
		Dest    string   `json:"dest,omitempty" jsonschema:"Absolute directory on the host to copy into, made if missing. Defaults to a new NNN-pull directory in the run's directory."`
		Exclude []string `json:"exclude,omitempty" jsonschema:"rsync exclude patterns, e.g. node_modules, .git, build"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_pull",
		Description: "Copy a file or directory out of the machine to the host with rsync: build products, logs, test " +
			"reports, an .app bundle. source is a guest path, relative to the home, with ~/, or absolute. A " +
			"directory's contents land in dest; a file lands in dest under its own name (a symlink to a file is " +
			"copied as the file). Without dest the copy goes to a new numbered directory in the run's directory, so " +
			"it is also evidence. The result's dest is the absolute path on the host where the copy is.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pullIn) (*mcp.CallToolResult, machine.PullResult, error) {
		if o.publicHost && in.Dest != "" {
			return nil, machine.PullResult{}, ErrRemoteDest
		}
		res, err := mgr.Pull(ctx, in.RunID, in.Source, in.Dest, in.Exclude)
		res.Seconds = round(res.Seconds)
		return nil, res, err
	})

	type execIn struct {
		RunID          string `json:"runId" jsonschema:"runId from machine_create"`
		Command        string `json:"command" jsonschema:"Shell command, run with zsh -lc in the guest"`
		Cwd            string `json:"cwd,omitempty" jsonschema:"Working directory in the guest: relative to the home, absolute, or starting with ~/, e.g. work/myapp"`
		TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"Kill the command and its children in the guest after this many seconds. Default 600. The result then has timedOut true, exit code 124, and the output printed until then."`
		WaitSeconds    int    `json:"waitSeconds,omitempty" jsonschema:"How long this call waits for the command before returning running true with an execId. Default 45, max 50. The command keeps running either way; this is not its timeout."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_exec",
		Description: fmt.Sprintf("Run a shell command inside the machine and return stdout, stderr and the exit code. "+
			"The shell is a login zsh that is not interactive (zsh -lc). "+
			"One call waits at most waitSeconds (default 45, max 50, under MCP clients' 60 s limit on a call): a "+
			"command still going then comes back with running true, no exitCode, and an execId, and it keeps "+
			"running; collect its result with machine_exec_wait. A command returns when its shell exits: it may "+
			"leave a process running in the background (./App &), whose later output is not returned. Output "+
			"arrives when the command ends, never while it runs; to watch a build or type into a program, use "+
			"machine_session_start. stdout and stderr each keep their first %d KiB and last %d KiB; when bytes "+
			"were left out, stdoutTruncated or stderrTruncated is true, a marker line in the text says where, "+
			"and stdoutBytes and stderrBytes give the full sizes. To see more of a big output, write it to a "+
			"file in the guest and read parts of it (grep, tail, sed -n). "+
			"Before running a project's tests, read toolchain in machine_wait's result: it says whether this "+
			"machine's image can run XCTest and swift-testing. Never delete, skip or exclude a project's existing "+
			"tests to get a green run; if the machine cannot run them, say so.",
			machine.ExecHeadLimit/1024, machine.ExecTailLimit/1024),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, machine.ExecStatus, error) {
		timeout := 10 * time.Minute
		if in.TimeoutSeconds > 0 {
			timeout = time.Duration(in.TimeoutSeconds) * time.Second
		}
		st, err := mgr.ExecStart(ctx, in.RunID, in.Command, in.Cwd, timeout)
		if err != nil {
			return nil, machine.ExecStatus{}, err
		}
		return execResult(mgr.ExecWait(ctx, in.RunID, st.ExecID, waitTimeout(in.WaitSeconds)))
	})

	type execWaitIn struct {
		RunID       string `json:"runId" jsonschema:"runId from machine_create"`
		ExecID      string `json:"execId" jsonschema:"execId from a machine_exec that returned running true"`
		WaitSeconds int    `json:"waitSeconds,omitempty" jsonschema:"How long to wait for the command to finish before answering running true again. Default 45, max 50."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_exec_wait",
		Description: "Wait for a command machine_exec started and returned with running true. Returns the same " +
			"result machine_exec would have: running true (call again), or running false with exitCode, stdout " +
			"and stderr. The command's step in the run record is the step machine_exec returned. Collecting a " +
			"result again returns it again.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in execWaitIn) (*mcp.CallToolResult, machine.ExecStatus, error) {
		return execResult(mgr.ExecWait(ctx, in.RunID, in.ExecID, waitTimeout(in.WaitSeconds)))
	})

	type runIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
	}

	type approveIn struct {
		RunID string `json:"runId" jsonschema:"runId from machine_create"`
		App   string `json:"app" jsonschema:"Guest path of the .app bundle to approve, absolute or relative to the guest home (~/ accepted), e.g. work/MyApp/MyApp.app"`
	}
	type approveOut struct {
		Client string `json:"client" jsonschema:"The bundle URL macOS keys the approval by"`
		Step   int    `json:"step"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_approve_capture",
		Description: "Pre-approve an app under test that captures the screen itself (ScreenCaptureKit, a screen " +
			"recorder), so macOS does not cover the screen with \"<App> is requesting to bypass the system private " +
			"window picker\" when it starts capturing. Call it after the app is built and before it first captures. " +
			"Not needed for machine_screenshot or the live screen: greenroom approves its own capture. The approval " +
			"is by bundle path; a bare executable outside an .app cannot be approved this way. It restarts macOS's " +
			"capture service, which ends a running live screen stream; viewers reconnect to a fresh one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in approveIn) (*mcp.CallToolResult, approveOut, error) {
		client, step, err := mgr.ApproveCapture(ctx, in.RunID, in.App)
		return nil, approveOut{Client: client, Step: step}, err
	})
	mcp.AddTool(s, &mcp.Tool{
		Name: "machine_screenshot",
		Description: "Capture the machine's screen. Returns a JPEG to look at, the path of the lossless PNG saved in " +
			"the run directory, and the image's size in pixels. scale is image pixels per desktop point: 1 on the " +
			"default image (1024x768), more on a HiDPI guest, where the image is larger than the desktop. Aim clicks as a fraction of this image " +
			"(x divided by width, y divided by height), never in pixels: machine_click takes 0 to 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, machine.Shot, error) {
		pngBytes, out, err := mgr.Screenshot(ctx, in.RunID)
		if err != nil {
			return nil, machine.Shot{}, err
		}
		jpg, err := toJPEG(pngBytes)
		if err != nil {
			return nil, machine.Shot{}, err
		}
		meta, _ := json.Marshal(out)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{Data: jpg, MIMEType: "image/jpeg"},
				&mcp.TextContent{Text: string(meta)},
			},
		}, out, nil
	})

	type destroyOut struct {
		OK bool `json:"ok"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "machine_destroy",
		Description: "Stop and delete the machine. The run's recording stays on disk.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, destroyOut, error) {
		// Nothing is posted here: main.go's lifecycle bridge announces the destroy once it has happened.
		if err := mgr.Destroy(ctx, in.RunID); err != nil {
			return nil, destroyOut{}, err
		}
		return nil, destroyOut{OK: true}, nil
	})

	addAgentTools(s, reg)
	addInputTools(s, mgr)
	addSessionTools(s, mgr)
	s.AddReceivingMiddleware(recoverPanics)
	return s
}

// recoverPanics turns a panic in any handler into that one call's error. The SDK runs handlers
// on its own goroutines, where net/http's recovery never reaches, so without this one bad
// argument ends the daemon for every run and watcher (issue #50).
func recoverPanics(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (res mcp.Result, err error) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("mcp handler panicked", "method", method, "panic", p, "stack", string(debug.Stack()))
				res, err = nil, fmt.Errorf("greenroom hit an internal error handling %s: %v", method, p)
			}
		}()
		return next(ctx, method, req)
	}
}

// wrap adapts a (machine, error) pair to a tool handler's three results.
func wrap(mc *machine.Machine, err error) (*mcp.CallToolResult, *machine.Machine, error) {
	if err != nil {
		return nil, nil, err
	}
	return nil, mc, nil
}

// execResult adapts an exec status to a tool handler's three results.
func execResult(st machine.ExecStatus, err error) (*mcp.CallToolResult, machine.ExecStatus, error) {
	if err != nil {
		return nil, machine.ExecStatus{}, err
	}
	st.Seconds = round(st.Seconds)
	return nil, st, nil
}

// waitTimeout turns a caller's timeoutSeconds into a wait: defaultWait when unset, capped at maxWait.
func waitTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultWait
	}
	return min(time.Duration(seconds)*time.Second, maxWait)
}

func toJPEG(pngBytes []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

func round(f float64) float64 { return math.Round(f*100) / 100 }
