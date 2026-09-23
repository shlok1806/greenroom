package machine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

// ExecResult is a command's outcome plus timing. Stdout and Stderr keep at
// most ExecHeadLimit plus ExecTailLimit bytes each (issue #29); the Bytes
// fields are what the command wrote in full.
type ExecResult struct {
	ExecID          string  `json:"execId,omitempty"`
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	StdoutBytes     int64   `json:"stdoutBytes"`
	StderrBytes     int64   `json:"stderrBytes"`
	StdoutTruncated bool    `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool    `json:"stderrTruncated,omitempty"`
	ExitCode        int     `json:"exitCode"`
	TimedOut        bool    `json:"timedOut,omitempty"` // the guest killed the command at its timeout; the output is what it printed until then
	Seconds         float64 `json:"seconds"`
	Step            int     `json:"step"` // its number in steps.jsonl
}

// execTimedOutExit and execTimedOutNote are how execWrapper reports a timeout.
const (
	execTimedOutExit = 124
	execTimedOutNote = "greenroom: timed out after "
)

// execHostGrace is how much longer the host waits than the guest's own timeout,
// so the guest's watchdog, not the host, ends a command and its output comes back.
const execHostGrace = 20 * time.Second

// Exec runs command in the guest's login shell, optionally in cwd, and waits
// for it. A non-zero exit is reported in ExitCode, not as an error. At timeout
// the guest kills the command's process group (issue #28) and the result has
// TimedOut, exit 124 and the output printed so far; the host gives up only
// execHostGrace later. A caller that gives up ends the command's tart exec.
// machine_exec uses ExecStart and ExecWait instead, so no call blocks for long.
func (m *Manager) Exec(ctx context.Context, runID, command, cwd string, timeout time.Duration) (ExecResult, error) {
	j, err := m.startExec(ctx, runID, command, cwd, timeout)
	if err != nil {
		return ExecResult{}, err
	}
	select {
	case <-j.done:
		return j.res, j.err
	case <-ctx.Done():
		j.cancel()
		<-j.done
		return j.res, j.err
	}
}

// execWrapper runs a machine_exec command ($1) in a login zsh with its output
// in files, then prints them. tart exec returns only when every holder of the
// guest's stdout and stderr pipes has closed them, so a command that leaves a
// child running (`./App &`, `(cd x && ./App) &`) would hold the call until its
// timeout. Children inherit files instead, and a file never blocks anyone.
// Output that a background child writes after the shell exits is not returned.
//
// $2 is the timeout in seconds, 0 for none. The host cannot kill a guest
// process (killing tart exec leaves it running, issue #28), so the guest does:
// set -m puts zsh in its own process group, and a watchdog sends that group
// TERM at the timeout and KILL 5 s later (at once if zsh is already gone). The
// output so far is still printed, with a note and exit 124, and the files are
// removed either way. Children a command leaves running on a normal exit stay
// running. The wrapper's own stderr goes to /dev/null so the shell's job notices
// never reach the caller; the command's stderr is written to fd 3, which zsh and
// the watchdog do not inherit (a child holding it would hold the call open).
//
// The login zsh is not interactive, so it never reads /etc/zshrc, where macOS
// runs `disable log` so that log is /usr/bin/log and not zsh's builtin (issue
// #40). The wrapper does the same before the command, on its first line so the
// command's own line numbers in zsh's errors stay as they were.
const execWrapper = `exec 3>&2 2>/dev/null
d=$(mktemp -d /tmp/greenroom-exec.XXXXXX) || exit 125
set -m
/bin/zsh -lc "disable log 2>/dev/null; $1" >"$d/out" 2>"$d/err" </dev/null 3>&- &
z=$!
w=
if [ "${2:-0}" -gt 0 ]; then
  (sleep "$2"; : >"$d/timedout"; kill -TERM -"$z"; sleep 5; kill -KILL -"$z") >/dev/null 2>&1 </dev/null 3>&- &
  w=$!
fi
wait "$z"
s=$?
[ -n "$w" ] && kill -KILL -"$w"
if [ -f "$d/timedout" ]; then
  kill -KILL -"$z"
  s=124
fi
cat "$d/out"
cat "$d/err" >&3
[ -f "$d/timedout" ] && echo "` + execTimedOutNote + `$2 s; the command and its children were killed" >&3
rm -rf "$d"
exit $s`

// cdCommand is the shell command that enters a machine_exec cwd, or "" for
// none. A leading ~ means the guest home, as in a shell; anything else is
// quoted whole, so a relative path is relative to the home the shell starts in.
func cdCommand(cwd string) string {
	switch rest, tilde := homeRelative(cwd); {
	case tilde && rest == "":
		return `cd "$HOME"`
	case tilde:
		return `cd "$HOME"/` + shellQuote(rest)
	case cwd == "":
		return ""
	default:
		return "cd " + shellQuote(cwd)
	}
}

// homeRelative strips a leading "~" or "~/" from a guest path. tilde says one
// was there; rest is the path below the home, "" for the home itself. "~user"
// is not the home and is left alone.
func homeRelative(p string) (rest string, tilde bool) {
	if p == "~" {
		return "", true
	}
	if r, ok := strings.CutPrefix(p, "~/"); ok {
		return strings.TrimLeft(r, "/"), true
	}
	return p, false
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
	m.ensureCaptureApproval(ctx, mc)
	// A path per call: Screenshot and the frame recorder capture concurrently.
	var tag [8]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return nil, err
	}
	f := shellQuote("/tmp/greenroom-shot-" + hex.EncodeToString(tag[:]) + ".png")
	res, err := m.tart.Exec(ctx, mc.Name, "sh", "-c",
		"screencapture -x "+f+" && base64 -i "+f+"; s=$?; rm -f "+f+"; exit $s")
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
	s := mc.input.screen.Load()
	if s == nil || s.Width <= 0 {
		return Screen{}, false
	}
	return *s, true
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
	// Not via --rsync-path: openrsync re-splits it on spaces, so no quoting survives there.
	if _, err := execChecked(ctx, m.tart, mc.Name, "/bin/sh", "-c", "cd && mkdir -p "+shellQuote(dest)); err != nil {
		return SyncResult{}, fmt.Errorf("create %s in the guest: %w", dest, err)
	}
	sshCmd := "ssh -i " + shellQuote(m.sshKey) + " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"
	// No -z: compression costs 4x on a local VM (docs/10-build-transport.md).
	// Revisit only if sync ever crosses a real network.
	args := []string{"-a", "--stats", "-e", sshCmd}
	for _, ex := range exclude {
		args = append(args, "--exclude", ex)
	}
	// Apple's rsync (openrsync, 2.6.9) has no --protect-args, so the remote
	// shell sees the path: quote it. RSYNC_OLD_ARGS makes rsync 3.2.4+ agree.
	args = append(args, source+"/", fmt.Sprintf("%s@%s:%s/", guestUser, m.snapshot(mc).IP, shellQuote(dest)))
	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Env = append(os.Environ(), "RSYNC_OLD_ARGS=1")
	out, err := cmd.CombinedOutput()
	res := SyncResult{Dest: dest, Summary: rsyncSummary(string(out)), Seconds: time.Since(started).Seconds()}
	if err != nil {
		err = fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(string(out)))
	}
	m.emitStep(runID, mc.rec.step("machine_sync", map[string]any{"source": source, "dest": dest, "exclude": exclude}, res, err, started))
	return res, err
}

// guestDest refuses a dest that is absolute or climbs above the guest home.
// "~/x" is x in the guest home, as a shell would read it, never a directory
// named "~".
func guestDest(dest string) (string, error) {
	if rest, tilde := homeRelative(dest); tilde {
		dest = rest
		if dest == "" {
			return "", errors.New(`dest "~" is the guest home itself; name a directory inside it`)
		}
	}
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
