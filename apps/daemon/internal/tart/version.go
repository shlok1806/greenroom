package tart

// The daemon pins a tart version because a VM made by 2.37.0 with
// `clone --stacked` cannot be read by older tart. See apps/daemon/CLAUDE.md
// and ADR 0010. Resolution only warns; it never installs or refuses to start.

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PinnedVersion is the tart the daemon is tested against.
const PinnedVersion = "2.37.0"

// EnvVar names a tart binary explicitly. The -tart flag wins over it.
const EnvVar = "GREENROOM_TART"

// pinnedInstall is a var so tests can point the resolver at a temp file.
var pinnedInstall = defaultPinnedInstall()

func defaultPinnedInstall() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "tart-"+PinnedVersion, "tart.app", "Contents", "MacOS", "tart")
}

// Source says where a resolved binary came from.
type Source string

const (
	// SourceOverride is the -tart flag or GREENROOM_TART.
	SourceOverride Source = "override"
	// SourcePinned is the documented pinned install.
	SourcePinned Source = "pinned"
	// SourcePath is plain "tart" on PATH: any version, or missing.
	SourcePath Source = "path"
)

// Resolution is the chosen binary and where it came from.
type Resolution struct {
	Bin    string
	Source Source
}

// Resolve picks the tart binary: override, then EnvVar, then the pinned
// install, then PATH. It does not check the version; CheckVersion does.
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

// Version returns the binary's `--version` output, for example "2.37.0".
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, c.Bin, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CheckVersion logs which tart the daemon drives and warns if it is not the
// pinned one or cannot run. It never fails: older tart still works for
// everything except stacked clones.
func (c *Client) CheckVersion(ctx context.Context, log *slog.Logger) {
	if log == nil {
		return
	}
	got, err := c.Version(ctx)
	if err != nil {
		log.Warn("could not run tart", "bin", c.Bin, "pinned", PinnedVersion, "err", err)
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
