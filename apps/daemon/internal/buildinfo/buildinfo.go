// Package buildinfo is the daemon's build identity (root ADR 0033): the commit it was built from,
// whether that tree had local changes, and when it was built. scripts/install.sh stamps the
// three through -ldflags -X; a build without them (go run, go test, a bare go build) reports
// none, which reads as "not an installed build", never as a guess.
package buildinfo

import "strings"

// Stamped with -X github.com/shlok1806/greenroom/apps/daemon/internal/buildinfo.<name>=<value>.
// Strings, because -X sets only strings.
var (
	commit  string // short sha
	dirty   string // "true" when the tree had local changes
	builtAt string // RFC 3339, UTC
)

// Info is what a build knows about itself.
type Info struct {
	Commit  string `json:"commit"`
	Dirty   bool   `json:"dirty"`
	BuiltAt string `json:"builtAt"`
}

// Get returns the stamped identity; every field is empty (and Dirty false) when unstamped.
func Get() Info {
	return Info{Commit: strings.TrimSpace(commit), Dirty: strings.TrimSpace(dirty) == "true", BuiltAt: strings.TrimSpace(builtAt)}
}

// Stamped reports whether the install script stamped this build.
func (i Info) Stamped() bool { return i.Commit != "" }

// String is the one-line form `greenroom version` prints after the name and version:
// "abc1234 (local changes), built 2026-09-27T10:00:00Z", or "unstamped build".
func (i Info) String() string {
	if !i.Stamped() {
		return "unstamped build"
	}
	s := i.Commit
	if i.Dirty {
		s += " (local changes)"
	}
	if i.BuiltAt != "" {
		s += ", built " + i.BuiltAt
	}
	return s
}
