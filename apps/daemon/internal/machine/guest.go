package machine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	imagepng "image/png"
	"io"
	"os"
	"os/exec"
	"path"
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

// execTimedOutExit and execTimedOutNote are how the exec wrapper reports a timeout.
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
	return m.ExecAs(ctx, runID, "", command, cwd, timeout)
}

// ExecAs is Exec recorded as made by seat by (HolderVerifier), so a verdict can tell its own
// observations from anyone else's (ADR 0024).
func (m *Manager) ExecAs(ctx context.Context, runID, by, command, cwd string, timeout time.Duration) (ExecResult, error) {
	j, err := m.startExec(ctx, runID, by, command, cwd, timeout)
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

// execShell is the guest command machine_exec runs, followed by the timeout in
// seconds: /bin/sh reading execScript from stdin (tart exec -i). Neither the
// wrapper nor the command is in any argv, so the guest's ps shows
// `/bin/sh -s greenroom-exec 600` and a `pgrep -f <pattern>` run through
// machine_exec cannot match its own wrapper (issue #128). "greenroom-exec" is
// only $1, a name for the listing; the timeout is $2.
var execShell = []string{"/bin/sh", "-s", "greenroom-exec"}

// execWrapperHead and execWrapperTail are the wrapper execScript puts around a
// machine_exec command. The head makes the temp dir and starts a quoted
// heredoc that writes the command to $d/cmd verbatim; the tail runs it in a
// login zsh with its output in files, then prints them. tart exec returns only
// when every holder of the guest's stdout and stderr pipes has closed them, so
// a command that leaves a child running (`./App &`, `(cd x && ./App) &`) would
// hold the call until its timeout. Children inherit files instead, and a file
// never blocks anyone. Output that a background child writes after the shell
// exits is not returned.
//
// zsh gets the command as `eval "$(<$d/cmd)"`, not as a script file, so it runs
// as `zsh -c` ran it: parsed whole before any of it runs, $0 and no positional
// parameters as before, `return` ending it. Its errors say "(eval):N:" where
// they said "zsh:N:". The argv holds only the short temp path, which mktemp
// makes from letters and digits, so it needs no quoting.
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
// Nothing but sh reads stdin: zsh and the watchdog get /dev/null.
//
// The login zsh is not interactive, so it never reads /etc/zshrc, where macOS
// runs `disable log` so that log is /usr/bin/log and not zsh's builtin (issue
// #40). The wrapper does the same before the eval, so the command's own line
// numbers in zsh's errors stay as they were.
const (
	execWrapperHead = `exec 3>&2 2>/dev/null
d=$(mktemp -d /tmp/greenroom-exec.XXXXXX) || exit 125
`
	execWrapperTail = `set -m
/bin/zsh -lc "disable log 2>/dev/null; eval \"\$(<$d/cmd)\"" >"$d/out" 2>"$d/err" </dev/null 3>&- &
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
exit $s
`
)

// execScript is the whole of what execShell reads on stdin for command: the
// wrapper with the command in a quoted heredoc, which the shell copies byte for
// byte. Its delimiter is random and checked against the command, so no line of
// a command can end the heredoc early.
func execScript(command string) string {
	for {
		delim := "GREENROOM_CMD_" + rand.Text()
		if !strings.Contains(command, delim) {
			return execWrapperHead + `cat >"$d/cmd" <<'` + delim + "'\n" + command + "\n" + delim + "\n" + execWrapperTail
		}
	}
}

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
	return m.ScreenshotAs(ctx, runID, "")
}

// ScreenshotAs is Screenshot for reader (HolderVerifier), whose inputs a look at the screen
// makes current again after a handover (issue #124). An empty reader records no look.
func (m *Manager) ScreenshotAs(ctx context.Context, runID, reader string) (data []byte, shot Shot, err error) {
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
		mc.rec.completeAs(seq, reader, nil, "machine_screenshot", nil, shot, err, started)
		m.emitStep(mc.RunID, seq)
	}()
	at := mc.input.handovers.Load()
	// A cap of its own, whatever the caller's ctx allows: an MCP client stops waiting at 60 s
	// but its request's ctx lives on (daemon ADR 0003).
	limit := m.looks().cap
	look, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	data, err = m.captureScreen(look, mc, true)
	if err = lookError(ctx, look, err, "the screenshot", limit); err != nil {
		return nil, Shot{Step: seq}, err
	}
	mc.noteLook(reader, at)
	path := mc.rec.artifactPath(seq, "screenshot", "png")
	if err = os.WriteFile(path, data, 0o644); err != nil {
		return nil, Shot{Step: seq}, err
	}
	shot.Path, shot.Bytes = path, len(data)
	shot.Width, shot.Height, shot.Scale = m.geometryOf(mc, data)
	return data, shot, nil
}

// captureScreen returns the guest screen as PNG bytes. Screenshot, the UI read's render
// check and the frame recorder use it. One capture runs per machine (captureGate): with wait
// a caller waits for an outstanding one, within ctx, then captures itself; without it (the
// recorder) it gets errCaptureBusy at once. The capture runs detached from ctx under its own
// limits, so a caller that gives up returns at once while the slot stays held until the guest
// command is over. A capture that times out is a *ScreenNotAnsweringError.
func (m *Manager) captureScreen(ctx context.Context, mc *Machine, wait bool) ([]byte, error) {
	lim := m.looks().captureLimit()
	g := &mc.input.capture
	epoch, err := g.acquire(ctx, wait, lim.guest)
	if err != nil {
		return nil, err
	}
	type result struct {
		png []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		png, timedOut, err := m.captureOnce(context.WithoutCancel(ctx), mc, lim)
		g.release(epoch, timedOut, err == nil)
		done <- result{png, err}
	}()
	select {
	case r := <-done:
		return r.png, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// captureShellScript takes the screenshot into the look watchdog's private dir and prints it
// as base64. The watchdog removes the dir, so a capture it stops leaves no file behind.
const captureShellScript = `f="$GREENROOM_LOOK_DIR/shot.png"; screencapture -x "$f" && base64 -i "$f"`

// captureOnce checks the capture approvals, then runs one guest screencapture under the look
// watchdog. ctx carries no cancel from the caller: only lim ends it.
func (m *Manager) captureOnce(ctx context.Context, mc *Machine, lim lookLimit) (png []byte, timedOut bool, err error) {
	m.ensureCaptureApproval(ctx, mc)
	res, timedOut, err := guestLook(ctx, m.tart, mc.Name, lim, "/bin/sh", "-c", captureShellScript)
	switch {
	case timedOut:
		return nil, true, &ScreenNotAnsweringError{What: "the screen capture", After: lim.guest}
	case err != nil:
		return nil, false, err
	case res.ExitCode != 0:
		return nil, false, fmt.Errorf("screencapture failed: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	png, err = base64.StdEncoding.DecodeString(strings.TrimSpace(res.Stdout))
	if err != nil {
		return nil, false, fmt.Errorf("decode screenshot: %w", err)
	}
	return png, false, nil
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

// SyncOptions are machine_sync's arguments after source (daemon ADR 0001).
type SyncOptions struct {
	// Dest is relative to the guest home; empty means GuestWorkDir/<basename of source>.
	Dest string
	// Exclude are rsync exclude patterns. An excluded path is neither copied nor deleted,
	// and is never a stray.
	Exclude []string
	// Mirror deletes what dest has and source does not (rsync --delete), inside dest only.
	Mirror bool
}

// SyncResult reports what rsync did.
type SyncResult struct {
	Dest    string  `json:"dest"`
	Summary string  `json:"summary"`
	Seconds float64 `json:"seconds"`
	Mirror  bool    `json:"mirror"`
	// Strays counts the paths in dest that source does not have and no exclude covers:
	// deleted with Mirror, still there without it. Nil when they could not be counted.
	Strays *int `json:"strays,omitempty"`
	// StrayPaths are the first maxStrayPaths of them, relative to dest; a directory ends in /.
	StrayPaths []string `json:"strayPaths,omitempty"`
}

// maxStrayPaths caps SyncResult.StrayPaths; Strays is the full count.
const maxStrayPaths = 20

// Sync copies a host directory into the guest with rsync over ssh. dest is relative to the
// guest home and defaults to GuestWorkDir/<basename>. It deletes only with opts.Mirror, and
// then only inside a dest mirrorDest and syncGuardScript allow. Either way it counts strays.
func (m *Manager) Sync(ctx context.Context, runID, source string, opts SyncOptions) (SyncResult, error) {
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
	dest := opts.Dest
	if dest == "" {
		dest = GuestWorkDir + "/" + filepath.Base(source)
	}
	if dest, err = guestDest(dest); err != nil {
		return SyncResult{}, err
	}
	if opts.Mirror {
		if err := mirrorDest(dest); err != nil {
			return SyncResult{}, err
		}
		if err := m.guardMirrorDest(ctx, mc, dest); err != nil {
			return SyncResult{}, err
		}
	} else if _, err := execChecked(ctx, m.tart, mc.Name, "/bin/sh", "-c", "cd && mkdir -p "+shellQuote(dest)); err != nil {
		// Not via --rsync-path: openrsync re-splits it on spaces, so no quoting survives there.
		return SyncResult{}, fmt.Errorf("create %s in the guest: %w", dest, err)
	}
	// No -z: compression costs 4x on a local VM (docs/10-build-transport.md).
	// Revisit only if sync ever crosses a real network.
	args := []string{"-a", "--stats", "-e", m.rsyncShell()}
	if opts.Mirror {
		// -v makes rsync print what the receiver deletes ("deleting <path>"): the strays.
		args = append(args, "--delete", "-v")
	}
	args = appendExcludes(args, opts.Exclude)
	// Apple's rsync (openrsync, 2.6.9) has no --protect-args, so the remote
	// shell sees the path: quote it. RSYNC_OLD_ARGS makes rsync 3.2.4+ agree.
	remote := fmt.Sprintf("%s@%s:%s/", guestUser, m.snapshot(mc).IP, shellQuote(dest))
	out, err := runRsync(ctx, append(args, source+"/", remote))
	res := SyncResult{Dest: dest, Summary: rsyncSummary(out), Mirror: opts.Mirror}
	switch {
	case err != nil:
		err = fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(out))
	case opts.Mirror:
		res.setStrays(deletedPaths(out))
	default:
		// A dry run with --delete lists what a mirror would delete, by rsync's own exclude rules.
		dry := appendExcludes([]string{"-a", "--dry-run", "--delete", "-v", "-e", m.rsyncShell()}, opts.Exclude)
		if dryOut, dryErr := runRsync(ctx, append(dry, source+"/", remote)); dryErr != nil {
			m.Log.Warn("machine_sync could not count strays", "runId", runID, "dest", dest,
				"err", dryErr, "output", strings.TrimSpace(dryOut))
			res.Summary = joinSummary(res.Summary, "strays in dest: unknown (the dry run to count them failed)")
		} else {
			res.setStrays(deletedPaths(dryOut))
		}
	}
	res.Seconds = time.Since(started).Seconds()
	input := map[string]any{"source": source, "dest": dest, "exclude": opts.Exclude, "mirror": opts.Mirror}
	m.emitStep(runID, mc.rec.step("machine_sync", input, res, err, started))
	return res, err
}

// guardMirrorDest makes dest in the guest and refuses it when any component is a symlink:
// rsync's receiver follows a symlinked destination, so --delete would empty its target.
func (m *Manager) guardMirrorDest(ctx context.Context, mc *Machine, dest string) error {
	res, err := m.tart.Exec(ctx, mc.Name, "/bin/sh", "-c", syncGuardScript, "greenroom-sync-guard", dest)
	switch {
	case err != nil:
		return fmt.Errorf("check %s in the guest: %w", dest, err)
	case res.ExitCode == 4:
		return fmt.Errorf("mirror: dest %q goes through a symlink in the guest (it is really %s); a mirror deletes "+
			"only in a real directory, so name that directory itself", dest, strings.TrimSpace(res.Stdout))
	case res.ExitCode != 0:
		return fmt.Errorf("create %s in the guest: exit %d: %s", dest, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// syncGuardScript makes the guest dir $1 (relative to the home) and exits 4, printing where
// it really is, when its physical path is not the home's plus $1: a component is a symlink.
// The path is an argument, never shell text.
const syncGuardScript = `cd || exit; mkdir -p -- "$1" || exit; home=$(pwd -P) || exit; ` +
	`real=$(cd -P -- "$1" && pwd -P) || exit; [ "$real" = "$home/$1" ] || { printf '%s\n' "$real"; exit 4; }`

// mirrorDest refuses a dest (already through guestDest) that a mirror must not empty: a
// top-level directory of the home, or anything under Library or a hidden top-level directory.
func mirrorDest(dest string) error {
	parts := strings.Split(dest, "/")
	if len(parts) < 2 {
		return fmt.Errorf("mirror: dest %q is a top-level directory of the guest home, and a mirror deletes what "+
			"source does not have; name a project directory at least two levels down, like work/<name> (the default)", dest)
	}
	if parts[0] == "Library" || strings.HasPrefix(parts[0], ".") {
		return fmt.Errorf("mirror: dest %q is inside ~/%s, which holds the guest's own state; mirror into a "+
			"project directory like work/<name> (the default)", dest, parts[0])
	}
	return nil
}

func appendExcludes(args, exclude []string) []string {
	for _, ex := range exclude {
		args = append(args, "--exclude", ex)
	}
	return args
}

// runRsync runs the host's rsync and returns its combined output.
func runRsync(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Env = append(os.Environ(), "RSYNC_OLD_ARGS=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// setStrays records the strays rsync listed and says them in the summary.
func (r *SyncResult) setStrays(paths []string) {
	n := len(paths)
	r.Strays = &n
	r.StrayPaths = paths[:min(n, maxStrayPaths)]
	switch {
	case n == 0:
		r.Summary = joinSummary(r.Summary, "strays in dest: 0")
	case r.Mirror:
		r.Summary = joinSummary(r.Summary, fmt.Sprintf("mirror deleted %d paths in dest that are not in source (strayPaths)", n))
	default:
		r.Summary = joinSummary(r.Summary, fmt.Sprintf("strays in dest: %d paths not in source are still there "+
			"(strayPaths); sync with mirror true to delete them", n))
	}
}

// deletedPaths are the paths of rsync -v's "deleting <path>" lines, in its order.
func deletedPaths(out string) []string {
	paths := []string{}
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "deleting "); ok && p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func joinSummary(summary, more string) string {
	if summary == "" {
		return more
	}
	return summary + "; " + more
}

// rsyncShell is the ssh command rsync reaches the guest with, for Sync and Pull.
func (m *Manager) rsyncShell() string {
	return "ssh -i " + shellQuote(m.sshKey) + " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"
}

// PullResult reports what a pull copied and where it is on the host.
type PullResult struct {
	Source  string  `json:"source"`
	Dest    string  `json:"dest"`
	Summary string  `json:"summary"`
	Seconds float64 `json:"seconds"`
	Step    int     `json:"step"`
}

// ErrNotInGuest is a pull source the guest does not have.
var ErrNotInGuest = errors.New("does not exist in the guest")

// Pull copies a guest file or directory to the host with rsync over ssh, the mirror of Sync.
// source is a guest path: relative to the home, ~/x, or absolute. A directory's contents land
// in dest; a file lands in dest under its own name (a symlink to a file is copied as the
// file). dest is an absolute host directory, made if missing. It defaults to the run
// directory's NNN-pull, so what was pulled is also evidence of the step that pulled it.
func (m *Manager) Pull(ctx context.Context, runID, source, dest string, exclude []string) (res PullResult, err error) {
	mc, err := m.get(runID)
	if err != nil {
		return PullResult{}, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return PullResult{}, err
	}
	started := time.Now()
	src, err := guestSource(source)
	if err != nil {
		return PullResult{}, err
	}
	if dest != "" && !filepath.IsAbs(dest) {
		return PullResult{}, fmt.Errorf("dest %q must be an absolute path on the host", dest)
	}
	isDir, err := m.guestIsDir(ctx, mc, source, src)
	if err != nil {
		return PullResult{}, err
	}
	// Claim the number before naming the default dest, as Screenshot does.
	seq := mc.rec.begin()
	input := map[string]any{"source": source, "dest": dest, "exclude": exclude}
	defer func() {
		res.Seconds = time.Since(started).Seconds()
		mc.rec.complete(seq, "machine_pull", input, res, err, started)
		m.emitStep(runID, seq)
	}()
	if dest == "" {
		dest = mc.rec.artifactDir(seq, "pull")
	}
	res = PullResult{Source: source, Dest: filepath.Clean(dest), Step: seq}
	if err = os.MkdirAll(res.Dest, 0o755); err != nil {
		return res, fmt.Errorf("create %s: %w", res.Dest, err)
	}
	args := []string{"-a", "--stats", "-e", m.rsyncShell()}
	remote := shellQuote(src) // quoted for the guest's shell, as in Sync
	if isDir {
		remote += "/"
	} else {
		args = append(args, "--copy-links") // a file source has nothing under it, so this only reads through a link
	}
	for _, ex := range exclude {
		args = append(args, "--exclude", ex)
	}
	args = append(args, fmt.Sprintf("%s@%s:%s", guestUser, m.snapshot(mc).IP, remote), res.Dest+"/")
	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Env = append(os.Environ(), "RSYNC_OLD_ARGS=1")
	out, err := cmd.CombinedOutput()
	res.Summary = rsyncSummary(string(out))
	if err != nil {
		err = fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return res, err
}

// PullArchive is Pull for a client on another host (ADR 0021): it writes source to the writer
// open returns as a gzipped tar made in the guest, instead of rsyncing it to a host directory.
// A directory's contents are the archive's root; a file is one member under its own name.
// open is called once the source is known to exist, with the step's number, and not at all
// when the call fails before that; an error after it means the archive is incomplete. Only
// the literal names in exclude (node_modules, .git) are left out in the guest, where tar
// reads them as rsync does; the caller applies every pattern while unpacking.
func (m *Manager) PullArchive(ctx context.Context, runID, source string, exclude []string, open func(step int) io.Writer) (err error) {
	mc, err := m.get(runID)
	if err != nil {
		return err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return err
	}
	started := time.Now()
	src, err := guestSource(source)
	if err != nil {
		return err
	}
	isDir, err := m.guestIsDir(ctx, mc, source, src)
	if err != nil {
		return err
	}
	seq := mc.rec.begin()
	var out pullArchiveOutput
	defer func() {
		out.Seconds = time.Since(started).Seconds()
		mc.rec.complete(seq, "machine_pull", map[string]any{"source": source, "exclude": exclude, "archive": true}, out, err, started)
		m.emitStep(runID, seq)
	}()
	out = pullArchiveOutput{Source: source, Step: seq}
	// COPYFILE_DISABLE and --no-mac-metadata keep macOS's tar from adding ._ AppleDouble members.
	args := []string{"czf", "-", "--no-mac-metadata", "--no-xattrs"}
	for _, ex := range exclude {
		if literalName(ex) {
			args = append(args, "--exclude", ex)
		}
	}
	if isDir {
		args = append(args, "-C", src, ".")
	} else {
		// -h reads through a link, like Pull's --copy-links; "./" keeps a name like -x a name.
		args = append(args, "-h", "-C", path.Dir(src), "./"+path.Base(src))
	}
	w := &countingWriter{w: open(seq)}
	var stderr headTail
	code, err := m.tart.ExecTo(ctx, w, &stderr, mc.Name, append([]string{"/bin/sh", "-c", pullTarScript, "greenroom-pull-tar"}, args...)...)
	out.Bytes = w.n
	if err == nil && code != 0 {
		text, _, _ := stderr.result()
		err = fmt.Errorf("tar in the guest: exit %d: %s", code, strings.TrimSpace(text))
	}
	return err
}

// pullArchiveOutput is PullArchive's step output: Bytes is the compressed archive's size.
type pullArchiveOutput struct {
	Source  string  `json:"source"`
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`
	Step    int     `json:"step"`
}

// pullProbeScript prints dir or file for the guest path $1, read from the home, and exits 3
// when nothing is there. A path is an argument, never shell text.
const pullProbeScript = `cd || exit; if [ -d "$1" ]; then echo dir; elif [ -e "$1" ]; then echo file; else exit 3; fi`

// pullTarScript runs tar from the home with the arguments it is given.
const pullTarScript = `cd || exit; COPYFILE_DISABLE=1 exec tar "$@"`

// guestIsDir reports whether the guest path src is a directory, and refuses one that is not there.
func (m *Manager) guestIsDir(ctx context.Context, mc *Machine, source, src string) (bool, error) {
	res, err := m.tart.Exec(ctx, mc.Name, "/bin/sh", "-c", pullProbeScript, "greenroom-pull-probe", src)
	switch {
	case err != nil:
		return false, err
	case res.ExitCode == 3:
		return false, fmt.Errorf("source %q %w", source, ErrNotInGuest)
	case res.ExitCode != 0:
		return false, fmt.Errorf("look up %q in the guest: exit %d: %s", source, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout) == "dir", nil
}

// guestSource reads a pull source as the guest's shell and rsync take it once in the home:
// "~" and "~/x" become "." and "x", a relative path stays relative to the home and an absolute
// one stays absolute. Unlike a sync dest, it may be anywhere in the guest.
func guestSource(source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", errors.New("source is required: a guest path relative to the home, ~/x, or absolute")
	}
	if rest, tilde := homeRelative(source); tilde {
		source = rest
		if source == "" {
			source = "."
		}
	}
	return path.Clean(source), nil
}

// literalName reports whether an exclude pattern is a plain base name, which guest tar and
// rsync match the same way: any path component with exactly that name.
func literalName(p string) bool {
	return p != "" && !strings.ContainsAny(p, `/*?[\`)
}

// countingWriter counts what passes through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// CheckDest reports whether dest would be refused by Sync, so a caller can say so before
// preparing the source. An empty dest (the default) is fine.
func CheckDest(dest string) error {
	if dest == "" {
		return nil
	}
	_, err := guestDest(dest)
	return err
}

// CheckMirrorDest reports whether a mirror into dest would be refused before it reaches the
// guest (the symlink check needs the guest, so Sync still makes it). An empty dest is the
// default work/<name>, which a mirror may use.
func CheckMirrorDest(dest string) error {
	if dest == "" {
		return nil
	}
	clean, err := guestDest(dest)
	if err != nil {
		return err
	}
	return mirrorDest(clean)
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
