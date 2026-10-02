package machine

// Output a caller reads (machine_exec, machine_exec_wait, machine_session_read, the verifier's
// own exec) never carries a long run of NUL bytes (issue #227). A file truncated under a writer
// that keeps its offset (`cmd | tee log`, then `: > log`) reads back as a hole of NULs before
// the new text, and JSON spells each one `\u0000`: 4 KiB of hole became 24 KiB of escapes in the
// caller's context. The run record keeps the bytes; only what is handed to a client or a model
// is collapsed.

import (
	"strconv"
	"strings"
)

// NULRunMin is the shortest run of NUL bytes CollapseNULs replaces. Shorter runs stay as they
// are: `find -print0` and `xargs -0` output separate names with single NULs, and binary
// headers hold short runs that are still worth seeing.
const NULRunMin = 16

// nulRunProbe is the shortest run CollapseNULs replaces, for the fast check.
var nulRunProbe = strings.Repeat("\x00", NULRunMin)

// nulMarkerPrefix and nulMarkerSuffix wrap a collapsed run's length: "[greenroom: 4096 NUL
// bytes]", in the style of the head and tail cut's "[greenroom: N bytes left out here ...]".
// A command could print the same text, so the result's NulBytes count, not the marker, is what
// says a run was collapsed.
const (
	nulMarkerPrefix = "[greenroom: "
	nulMarkerSuffix = " NUL bytes]"
)

// CollapseNULs replaces every run of at least NULRunMin NUL bytes in s with a marker naming its
// length, and returns the text and how many NUL bytes it replaced (0 when it changed nothing).
// A NUL byte is never part of a multi-byte UTF-8 character, so a cut never splits one.
func CollapseNULs(s string) (string, int64) {
	if !strings.Contains(s, nulRunProbe) {
		return s, 0
	}
	var b strings.Builder
	b.Grow(len(s))
	var collapsed int64
	for len(s) > 0 {
		i := strings.Index(s, nulRunProbe)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		n := NULRunMin
		for i+n < len(s) && s[i+n] == 0 {
			n++
		}
		b.WriteString(nulMarkerPrefix)
		b.WriteString(strconv.Itoa(n))
		b.WriteString(nulMarkerSuffix)
		collapsed += int64(n)
		s = s[i+n:]
	}
	return b.String(), collapsed
}

// forCaller is r as a caller reads it: each stream's long NUL runs collapsed and counted. The
// byte counts stay what the command wrote.
func (r ExecResult) forCaller() ExecResult {
	r.Stdout, r.StdoutNulBytes = CollapseNULs(r.Stdout)
	r.Stderr, r.StderrNulBytes = CollapseNULs(r.Stderr)
	r.StdoutNulsCollapsed, r.StderrNulsCollapsed = r.StdoutNulBytes > 0, r.StderrNulBytes > 0
	return r
}
