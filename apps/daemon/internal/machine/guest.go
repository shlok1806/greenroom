package machine

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	imagepng "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// recordLimit caps how much of a command's output goes into steps.jsonl.
const recordLimit = 64 * 1024

// ExecResult is a command's outcome plus timing.
type ExecResult struct {
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	ExitCode int     `json:"exitCode"`
	Seconds  float64 `json:"seconds"`
	Step     int     `json:"step"` // its number in steps.jsonl
}

// Exec runs command in the guest's login shell, optionally in cwd. A non-zero
// exit is reported in ExitCode, not as an error.
func (m *Manager) Exec(ctx context.Context, runID, command, cwd string, timeout time.Duration) (ExecResult, error) {
	mc, err := m.get(runID)
	if err != nil {
		return ExecResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return ExecResult{}, err
	}
	started := time.Now()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	script := command
	if cwd != "" {
		script = "cd " + shellQuote(cwd) + " && " + command
	}
	res, err := m.tart.Exec(ctx, mc.Name, "/bin/zsh", "-lc", script)
	out := ExecResult{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode, Seconds: time.Since(started).Seconds()}
	out.Step = mc.rec.step("machine_exec", map[string]any{"command": command, "cwd": cwd}, truncatedForLog(out), err, started)
	m.emitStep(mc.RunID, out.Step)
	return out, err
}

func truncatedForLog(r ExecResult) ExecResult {
	r.Stdout, r.Stderr = truncateForRecord(r.Stdout), truncateForRecord(r.Stderr)
	return r
}

func truncateForRecord(s string) string {
	if len(s) > recordLimit {
		return s[:recordLimit] + "\n...[truncated]"
	}
	return s
}

// Shot is one screenshot and the geometry needed to read it. Scale is image
// pixels per guest point (2 on Retina), measured rather than assumed; input
// fractions need no scale, it is here so the evidence describes itself.
type Shot struct {
	Path   string  `json:"path"`
	Bytes  int     `json:"bytes"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale,omitempty"` // absent while the point size is unknown
	Step   int     `json:"step"`
}

// Screenshot captures the guest display as PNG, stores it in the run
// directory, and returns the bytes with their geometry.
func (m *Manager) Screenshot(ctx context.Context, runID string) (data []byte, shot Shot, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return nil, Shot{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return nil, Shot{}, err
	}
	started := time.Now()
	// Claim the number before naming the file so concurrent shots never collide.
	seq := mc.rec.begin()
	shot.Step = seq
	defer func() {
		mc.rec.complete(seq, "machine_screenshot", nil, shot, err, started)
		m.emitStep(mc.RunID, seq)
	}()
	data, err = m.captureScreen(ctx, mc)
	if err != nil {
		return nil, Shot{Step: seq}, err
	}
	path := mc.rec.artifactPath(seq, "screenshot", "png")
	if err = os.WriteFile(path, data, 0o644); err != nil {
		return nil, Shot{Step: seq}, err
	}
	shot.Path, shot.Bytes = path, len(data)
	shot.Width, shot.Height, shot.Scale = m.geometryOf(mc, data)
	return data, shot, nil
}

// captureScreen returns the guest screen as PNG bytes. Screenshot and the
// frame recorder both use it.
func (m *Manager) captureScreen(ctx context.Context, mc *Machine) ([]byte, error) {
	res, err := m.tart.Exec(ctx, mc.Name, "sh", "-c", "screencapture -x /tmp/greenroom-shot.png && base64 -i /tmp/greenroom-shot.png")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("screencapture failed: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimSpace(res.Stdout))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return png, nil
}

// geometryOf measures a PNG from its header and relates it to the guest's
// point size. An unreadable header is logged, not fatal: the picture still helps.
func (m *Manager) geometryOf(mc *Machine, data []byte) (width, height int, scale float64) {
	cfg, err := imagepng.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		m.Log.Warn("screenshot dimensions unreadable", "runId", mc.RunID, "err", err)
		return 0, 0, 0
	}
	if s, ok := cachedScreen(mc); ok && cfg.Width > 0 {
		scale = round2(float64(cfg.Width) / float64(s.Width))
	}
	return cfg.Width, cfg.Height, scale
}

// cachedScreen reports the guest's point size if the input helper already
// read it. It never installs the helper: that is a Swift compile a
// screenshot must not pay for.
func cachedScreen(mc *Machine) (Screen, bool) {
	st := mc.input
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.installed || st.screen.Width <= 0 {
		return Screen{}, false
	}
	return st.screen, true
}

// SyncResult reports what rsync did.
type SyncResult struct {
	Dest    string  `json:"dest"`
	Summary string  `json:"summary"`
	Seconds float64 `json:"seconds"`
}

// Sync copies a host directory into the guest with rsync over ssh. dest is
// relative to the guest home and defaults to GuestWorkDir/<basename>.
func (m *Manager) Sync(ctx context.Context, runID, source, dest string, exclude []string) (SyncResult, error) {
	mc, err := m.get(runID)
	if err != nil {
		return SyncResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return SyncResult{}, err
	}
	started := time.Now()
	if !filepath.IsAbs(source) {
		return SyncResult{}, fmt.Errorf("source %q must be an absolute path on the host", source)
	}
	source = filepath.Clean(source)
	if st, err := os.Stat(source); err != nil || !st.IsDir() {
		return SyncResult{}, fmt.Errorf("source %q is not a directory", source)
	}
	if dest == "" {
		dest = GuestWorkDir + "/" + filepath.Base(source)
	}
	if dest, err = guestDest(dest); err != nil {
		return SyncResult{}, err
	}
	sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR", m.sshKey)
	// No -z: compression costs 4x on a local VM (docs/10-build-transport.md).
	// Revisit only if sync ever crosses a real network.
	args := []string{"-a", "--stats", "-e", sshCmd, "--rsync-path", "mkdir -p " + shellQuote(dest) + " && rsync"}
	for _, ex := range exclude {
		args = append(args, "--exclude", ex)
	}
	args = append(args, source+"/", fmt.Sprintf("%s@%s:%s/", guestUser, m.snapshot(mc).IP, dest))
	out, err := exec.CommandContext(ctx, "rsync", args...).CombinedOutput()
	res := SyncResult{Dest: dest, Summary: rsyncSummary(string(out)), Seconds: time.Since(started).Seconds()}
	if err != nil {
		err = fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(string(out)))
	}
	m.emitStep(runID, mc.rec.step("machine_sync", map[string]any{"source": source, "dest": dest, "exclude": exclude}, res, err, started))
	return res, err
}

// guestDest refuses a dest that is absolute or climbs above the guest home.
func guestDest(dest string) (string, error) {
	if filepath.IsAbs(dest) {
		return "", fmt.Errorf("dest %q must be relative to the guest home", dest)
	}
	clean := filepath.Clean(dest)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("dest %q must stay inside the guest home", dest)
	}
	return clean, nil
}

func rsyncSummary(stats string) string {
	var keep []string
	for _, line := range strings.Split(stats, "\n") {
		if strings.HasPrefix(line, "Number of files") || strings.HasPrefix(line, "Number of regular files transferred") || strings.HasPrefix(line, "Total transferred file size") {
			keep = append(keep, strings.TrimSpace(line))
		}
	}
	return strings.Join(keep, "; ")
}
