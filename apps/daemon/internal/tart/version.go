package tart

// Which tart binary the daemon drives, and whether it is the one we tested
// against.
//
// The daemon pins a tart rather than taking whatever is on PATH. Two measured
// facts forced this. First, `brew upgrade tart` no longer works at all: the
// cirruslabs tap is abandoned at 2.32.1 and its formula does not evaluate
// against current Homebrew, and tart itself moved to github.com/openai/tart
// (ADR 0010). So a host's PATH tart is whatever happened to be installed
// before the tap died, and nothing will update it. Second, the versions are
// not interchangeable: a VM created by 2.37.0 with `clone --stacked` has an
// overlay.asif and no disk.img, and 2.32.1 cannot read it at all, reporting
// "VM is missing some of its files". A daemon that silently drives a
// different tart than the one a machine was made with produces errors that
// blame the wrong thing.
//
// What this is not: a version manager. It resolves a path, checks a string,
// and logs. It never installs anything and never refuses to start, because a
// daemon that has been up for hours against an older tart must keep working.

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PinnedVersion is the tart the daemon is tested against, and the single
// source of truth for it. See apps/daemon/CLAUDE.md for how to install it.
const PinnedVersion = "2.37.0"

// EnvVar names a tart binary explicitly, for a host that keeps it somewhere
// else. The -tart flag wins over it.
const EnvVar = "GREENROOM_TART"

// pinnedInstall is where the install instructions put the pinned version.
// Note the app bundle: the binary is inside tart.app/Contents/MacOS, not at
// the top of the directory. A var, not a const, so tests can point the
// resolver at a temporary file instead of depending on the host.
var pinnedInstall = defaultPinnedInstall()

func defaultPinnedInstall() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "tart-"+PinnedVersion, "tart.app", "Contents", "MacOS", "tart")
}

// Source says which of the three candidates a binary came from, so a log line
// can tell a person why the daemon is running the tart it is running.
type Source string

const (
	// SourceOverride is GREENROOM_TART, or the -tart flag above it.
	SourceOverride Source = "override"
	// SourcePinned is the pinned install this repo documents.
	SourcePinned Source = "pinned"
	// SourcePath is plain "tart", resolved by PATH at exec time. The last
	// resort: it may be any version, or missing.
	SourcePath Source = "path"
)

// Resolution is the chosen binary and where it came from.
type Resolution struct {
	Bin    string
	Source Source
}

// Resolve picks the tart binary to drive: an explicit override first, then
// the pinned install if it is present, then tart on PATH. It does not check
// the version; CheckVersion does that, after the process has started, because
// running a subprocess is not something a constructor should do.
func Resolve(override string) Resolution {
	if override = strings.TrimSpace(override); override != "" {
		return Resolution{Bin: override, Source: SourceOverride}
	}
	if env := strings.TrimSpace(os.Getenv(EnvVar)); env != "" {
		return Resolution{Bin: env, Source: SourceOverride}
	}
	if pinnedInstall != "" {
		if st, err := os.Stat(pinnedInstall); err == nil && !st.IsDir() {
			return Resolution{Bin: pinnedInstall, Source: SourcePinned}
		}
	}
	return Resolution{Bin: "tart", Source: SourcePath}
}

// Version asks the binary what it is. The output is a bare version string on
// its own line, for example "2.37.0".
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, c.Bin, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CheckVersion logs which tart the daemon is driving and whether it is the
// pinned one. It never returns an error and never stops the daemon: a running
// daemon on an older tart still works for everything except stacked clones,
// and refusing to start would take that away for a warning's worth of reason.
func (c *Client) CheckVersion(ctx context.Context, log *slog.Logger) {
	if log == nil {
		return
	}
	got, err := c.Version(ctx)
	if err != nil {
		// Worth a warning rather than silence: every machine operation is
		// about to fail, and this says so before the first one does.
		log.Warn("could not run tart",
			"bin", c.Bin, "pinned", PinnedVersion, "err", err)
		return
	}
	if got == PinnedVersion {
		log.Info("tart", "bin", c.Bin, "version", got, "source", string(c.source))
		return
	}
	log.Warn("tart version is not the pinned one",
		"bin", c.Bin,
		"found", got,
		"pinned", PinnedVersion,
		"source", string(c.source),
		"note", "stacked clones need "+PinnedVersion+" and a VM made by it cannot be read by older tart; see apps/daemon/CLAUDE.md to install the pinned version")
}
