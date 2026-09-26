package bench

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ApplyPatch applies a unified diff to the tree at dir, as `patch -p1` does but strictly: every
// context and removed line must match exactly (a hunk may move, never fuzz). It edits, creates
// (--- /dev/null) and deletes (+++ /dev/null) files, and never writes outside dir. Nothing is
// written unless every file's hunks apply.
func ApplyPatch(dir string, diff []byte) error {
	files, err := parseDiff(diff)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no file changes in the diff")
	}
	type result struct {
		path    string
		content []byte
		remove  bool
	}
	var out []result
	for _, f := range files {
		rel := f.newPath
		if f.newPath == "" {
			rel = f.oldPath
		}
		path, err := inside(dir, rel)
		if err != nil {
			return err
		}
		var old []string
		if f.oldPath != "" {
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			old = splitLines(string(data))
		} else if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s: the diff creates it, but it exists", rel)
		}
		lines, err := applyHunks(old, f.hunks)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		out = append(out, result{path: path, content: []byte(strings.Join(lines, "")), remove: f.newPath == ""})
	}
	for _, r := range out {
		if r.remove {
			if err := os.Remove(r.path); err != nil {
				return err
			}
			continue
		}
		mode := os.FileMode(0o644)
		if st, err := os.Stat(r.path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(r.path, r.content, mode); err != nil {
			return err
		}
	}
	return nil
}

type fileDiff struct {
	oldPath, newPath string // "" for /dev/null
	hunks            []hunk
}

type hunk struct {
	oldStart int      // 1-based; 0 for an empty old side
	old, new []string // lines with their "\n", as the file holds them
}

// parseDiff reads the files and hunks of a unified diff. Lines before the first "--- " (a
// commit message, "diff --git") are ignored.
func parseDiff(diff []byte) ([]fileDiff, error) {
	sc := bufio.NewScanner(bytes.NewReader(diff))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var files []fileDiff
	for i := 0; i < len(lines); {
		if !strings.HasPrefix(lines[i], "--- ") {
			i++
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+++ ") {
			return nil, fmt.Errorf("line %d: --- without +++", i+1)
		}
		f := fileDiff{oldPath: diffPath(lines[i][4:]), newPath: diffPath(lines[i+1][4:])}
		if f.oldPath == "" && f.newPath == "" {
			return nil, fmt.Errorf("line %d: both sides are /dev/null", i+1)
		}
		i += 2
		for i < len(lines) && strings.HasPrefix(lines[i], "@@ ") {
			h, next, err := parseHunk(lines, i)
			if err != nil {
				return nil, err
			}
			f.hunks = append(f.hunks, h)
			i = next
		}
		if len(f.hunks) == 0 {
			return nil, fmt.Errorf("line %d: no hunks for %s", i, f.newPath)
		}
		files = append(files, f)
	}
	return files, nil
}

// diffPath strips a timestamp and the first path component (-p1); /dev/null is "".
func diffPath(s string) string {
	if tab := strings.IndexByte(s, '\t'); tab >= 0 {
		s = s[:tab]
	}
	s = strings.TrimSpace(s)
	if s == "/dev/null" {
		return ""
	}
	if _, rest, ok := strings.Cut(s, "/"); ok {
		return rest
	}
	return s
}

func parseHunk(lines []string, i int) (hunk, int, error) {
	head := lines[i]
	fields := strings.Fields(head)
	if len(fields) < 4 || fields[3] != "@@" {
		return hunk{}, 0, fmt.Errorf("line %d: bad hunk header %q", i+1, head)
	}
	oldStart, oldN, err := hunkRange(fields[1], '-')
	if err != nil {
		return hunk{}, 0, fmt.Errorf("line %d: %w", i+1, err)
	}
	_, newN, err := hunkRange(fields[2], '+')
	if err != nil {
		return hunk{}, 0, fmt.Errorf("line %d: %w", i+1, err)
	}
	h := hunk{oldStart: oldStart}
	i++
	lastSide := byte(0) // which side the previous line went to: ' ', '-' or '+'
	for (len(h.old) < oldN || len(h.new) < newN) && i < len(lines) {
		l := lines[i]
		if l == "" {
			l = " " // some tools drop the space of an empty context line
		}
		switch l[0] {
		case ' ':
			h.old = append(h.old, l[1:]+"\n")
			h.new = append(h.new, l[1:]+"\n")
		case '-':
			h.old = append(h.old, l[1:]+"\n")
		case '+':
			h.new = append(h.new, l[1:]+"\n")
		case '\\':
			noNewline(&h, lastSide)
			i++
			continue
		default:
			return hunk{}, 0, fmt.Errorf("line %d: unexpected %q inside a hunk", i+1, l)
		}
		lastSide = l[0]
		i++
	}
	if len(h.old) != oldN || len(h.new) != newN {
		return hunk{}, 0, fmt.Errorf("hunk %q has %d old and %d new lines, want %d and %d", head, len(h.old), len(h.new), oldN, newN)
	}
	// A "\ No newline" marker may follow the hunk's last line.
	if i < len(lines) && strings.HasPrefix(lines[i], `\`) {
		noNewline(&h, lastSide)
		i++
	}
	return h, i, nil
}

// noNewline drops the "\n" of the line just read, on the side(s) it belongs to.
func noNewline(h *hunk, side byte) {
	trim := func(s []string) {
		if n := len(s); n > 0 {
			s[n-1] = strings.TrimSuffix(s[n-1], "\n")
		}
	}
	switch side {
	case ' ':
		trim(h.old)
		trim(h.new)
	case '-':
		trim(h.old)
	case '+':
		trim(h.new)
	}
}

func hunkRange(s string, sign byte) (start, n int, err error) {
	if len(s) < 2 || s[0] != sign {
		return 0, 0, fmt.Errorf("bad hunk range %q", s)
	}
	a, b, hasN := strings.Cut(s[1:], ",")
	if start, err = strconv.Atoi(a); err != nil {
		return 0, 0, fmt.Errorf("bad hunk range %q", s)
	}
	n = 1
	if hasN {
		if n, err = strconv.Atoi(b); err != nil {
			return 0, 0, fmt.Errorf("bad hunk range %q", s)
		}
	}
	return start, n, nil
}

// applyHunks applies hunks in order. Each must match exactly, at its stated line shifted by
// how far earlier hunks moved, or else at the nearest exact match that does not overlap an
// earlier hunk.
func applyHunks(old []string, hunks []hunk) ([]string, error) {
	var out []string
	pos := 0   // next line of old not yet copied
	shift := 0 // how far matches landed from the stated lines
	for n, h := range hunks {
		want := h.oldStart - 1 + shift
		if len(h.old) == 0 {
			want = h.oldStart + shift // an insertion after line oldStart
		}
		at := -1
		for d := 0; at < 0 && (want-d >= pos || want+d <= len(old)); d++ {
			for _, c := range []int{want + d, want - d} {
				if c >= pos && c+len(h.old) <= len(old) && equalLines(old[c:c+len(h.old)], h.old) {
					at = c
					break
				}
			}
		}
		if at < 0 {
			return nil, fmt.Errorf("hunk %d (line %d) does not match the file", n+1, h.oldStart)
		}
		shift += at - want
		out = append(out, old[pos:at]...)
		out = append(out, h.new...)
		pos = at + len(h.old)
	}
	return append(out, old[pos:]...), nil
}

func equalLines(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitLines keeps each line's "\n"; a last line without one stays without.
func splitLines(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// inside joins rel onto dir and refuses anything that would leave it.
func inside(dir, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q leaves the app directory", rel)
	}
	return filepath.Join(dir, clean), nil
}
