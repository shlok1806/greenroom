package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tarball"
)

// PullTool is the other tool connect answers itself: the daemon's own machine_pull copies
// onto the daemon's host.
const PullTool = "machine_pull"

// PullNote is appended to machine_pull's description: the meaning of dest changes.
const PullNote = " Through greenroom connect, dest is an absolute directory on this computer, and the default is " +
	"~/.greenroom/connect/runs/<runId>/NNN-pull here; the result's dest is that path on this computer."

// ScreenshotTool is forwarded, then its PNG is fetched, so the result's path opens here.
const ScreenshotTool = "machine_screenshot"

// PullArgs is machine_pull's input, as the daemon defines it.
type PullArgs struct {
	RunID   string   `json:"runId"`
	Source  string   `json:"source"`
	Dest    string   `json:"dest,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

// PullResult is machine_pull's result (machine.PullResult) with dest on this computer and a
// summary of what was unpacked.
type PullResult struct {
	Source  string  `json:"source"`
	Dest    string  `json:"dest"`
	Summary string  `json:"summary"`
	Seconds float64 `json:"seconds"`
	Step    int     `json:"step"`
}

// The pull route's headers (internal/api/pull.go): the step before the archive, and any
// failure after it.
const (
	pullStepHeader   = "Greenroom-Step"
	pullErrorTrailer = "Greenroom-Error"
)

// Limits of what connect writes to this computer; vars so tests can shrink them.
var (
	pullLimits       = tarball.Limits{Bytes: 4 << 30, Entries: 200_000} // the upload route's caps
	maxArtifactBytes = int64(1 << 30)
)

// trailerDrain is how much of an archive connect still reads after unpacking failed, to get
// to the daemon's trailer, which may say why.
const trailerDrain = 1 << 20

// Pull asks <base>/api/runs/{runId}/pull for args.Source and unpacks the gzipped tar into
// args.Dest as it arrives, with the upload route's rules (internal/tarball): no .., no
// absolute names, no symlink that leaves dest, nothing written through a link. Every exclude
// pattern applies here; the daemon prunes only literal names in the guest. An empty dest is
// <dir>/runs/<runId>/NNN-pull, NNN the step the daemon recorded the pull under.
func Pull(ctx context.Context, client *http.Client, base, dir string, args PullArgs) (PullResult, error) {
	started := time.Now()
	if !bareName(args.RunID) {
		return PullResult{}, errors.New("runId is required")
	}
	if strings.TrimSpace(args.Source) == "" {
		return PullResult{}, errors.New("source is required: a guest path relative to the home, ~/x, or absolute")
	}
	if args.Dest != "" && !filepath.IsAbs(args.Dest) {
		return PullResult{}, fmt.Errorf("dest %q must be an absolute path on this computer", args.Dest)
	}
	q := url.Values{"src": {args.Source}}
	for _, ex := range args.Exclude {
		q.Add("exclude", ex)
	}
	target := base + "/api/runs/" + url.PathEscape(args.RunID) + "/pull?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return PullResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return PullResult{}, describeTransportError(base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
		return PullResult{}, &RefusedError{Op: "pull", Status: resp.StatusCode, Message: daemonMessage(body)}
	}
	step, _ := strconv.Atoi(resp.Header.Get(pullStepHeader))
	dest := args.Dest
	if dest == "" {
		if step <= 0 {
			return PullResult{}, fmt.Errorf("the daemon did not number the pull (no %s header); pass dest", pullStepHeader)
		}
		dest = filepath.Join(dir, "runs", args.RunID, fmt.Sprintf("%03d-pull", step))
	}
	res := PullResult{Source: args.Source, Dest: filepath.Clean(dest), Step: step}
	if err := os.MkdirAll(res.Dest, 0o755); err != nil {
		return res, err
	}
	stats, err := tarball.Untar(resp.Body, res.Dest, pullLimits, ParseExcludes(args.Exclude).Covers)
	if err != nil {
		_, _ = io.CopyN(io.Discard, resp.Body, trailerDrain)
		if msg := resp.Trailer.Get(pullErrorTrailer); msg != "" {
			return res, fmt.Errorf("the daemon's pull failed: %s", msg)
		}
		return res, fmt.Errorf("unpack into %s: %w", res.Dest, err)
	}
	// The trailer arrives only once the body is read to its end, past tar's own padding.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return res, describeTransportError(base, err)
	}
	if msg := resp.Trailer.Get(pullErrorTrailer); msg != "" {
		return res, fmt.Errorf("the daemon's pull failed partway, so %s may be incomplete: %s", res.Dest, msg)
	}
	res.Summary = fmt.Sprintf("%d files, %d bytes", stats.Files, stats.Bytes)
	res.Seconds = math.Round(time.Since(started).Seconds()*100) / 100
	return res, nil
}

// pullHandler answers machine_pull by unpacking the daemon's archive here.
func pullHandler(r *Remote) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args PullArgs
		if req.Params != nil && len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError("machine_pull: bad arguments: " + err.Error()), nil
			}
		}
		res, err := Pull(ctx, r.HTTPClient(), r.cfg.URL, r.opts.Dir, args)
		if err != nil {
			r.opts.Logger.Error("machine_pull failed", "runId", args.RunID, "source", args.Source, "err", err)
			return toolError("machine_pull: " + err.Error()), nil
		}
		return structuredResult(res)
	}
}

// structuredResult is v as structured content plus one text copy, as the daemon answers.
func structuredResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var structured map[string]any
	if err := json.Unmarshal(data, &structured); err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: structured}, nil
}

// screenshotHandler forwards machine_screenshot, then downloads the PNG its result names on
// the daemon's host into <dir>/runs/<runId>/ and points path there, in the structured result
// and its text copy. The image to look at is left as it came. A failed download leaves the
// daemon's path and says so in one more text item: the picture still arrived.
func screenshotHandler(r *Remote) mcp.ToolHandler {
	fwd := forward(r, ScreenshotTool)
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := fwd(ctx, req)
		if err != nil || res == nil || res.IsError {
			return res, err
		}
		var args struct {
			RunID string `json:"runId"`
		}
		if req.Params != nil && len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &args)
		}
		if err := localizePath(ctx, r, args.RunID, res); err != nil {
			r.opts.Logger.Warn("screenshot not copied to this computer", "runId", args.RunID, "err", err)
			res.Content = append(res.Content, &mcp.TextContent{Text: "greenroom connect could not copy the " +
				"screenshot to this computer, so path is on the daemon's host: " + err.Error()})
		}
		return res, nil
	}
}

// localizePath downloads the run artifact res names in its structured path and rewrites that
// path, and the same JSON string in any text item, to the local copy.
func localizePath(ctx context.Context, r *Remote, runID string, res *mcp.CallToolResult) error {
	structured, ok := res.StructuredContent.(map[string]any)
	if !ok {
		return errors.New("the result has no structured content")
	}
	remotePath, _ := structured["path"].(string)
	name := path.Base(remotePath)
	if remotePath == "" || !bareName(name) || !bareName(runID) {
		return fmt.Errorf("cannot name a local copy of %q in run %q", remotePath, runID)
	}
	local := filepath.Join(r.opts.Dir, "runs", runID, name)
	if err := DownloadArtifact(ctx, r.HTTPClient(), r.cfg.URL, runID, name, local); err != nil {
		return err
	}
	structured["path"] = local
	oldJSON, _ := json.Marshal(remotePath)
	newJSON, _ := json.Marshal(local)
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			tc.Text = strings.ReplaceAll(tc.Text, string(oldJSON), string(newJSON))
		}
	}
	return nil
}

// DownloadArtifact saves <base>/api/runs/{runId}/artifacts/{name} to local, through a
// temporary file beside it, so an interrupted download never leaves a torn file under the name.
func DownloadArtifact(ctx context.Context, client *http.Client, base, runID, name, local string) error {
	target := base + "/api/runs/" + url.PathEscape(runID) + "/artifacts/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return describeTransportError(base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
		return &RefusedError{Op: "download", Status: resp.StatusCode, Message: daemonMessage(body)}
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(local), "."+name+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // gone once renamed
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxArtifactBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("download %s: %w", name, err)
	case n > maxArtifactBytes:
		return fmt.Errorf("download %s: larger than %d bytes", name, maxArtifactBytes)
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), local)
}

// bareName reports whether name is a single path element that cannot leave its directory,
// as the daemon's routes require of a run id or a file name.
func bareName(name string) bool {
	return name != "" && name != "." && !strings.ContainsAny(name, `/\`) && !strings.Contains(name, "..")
}
