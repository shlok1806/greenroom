package machine

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCollapseNULsReplacesOnlyLongRuns(t *testing.T) {
	nul := func(n int) string { return strings.Repeat("\x00", n) }
	for _, c := range []struct {
		name, in, want string
		collapsed      int64
	}{
		{"no NULs", "plain text\n", "plain text\n", 0},
		{"empty", "", "", 0},
		{"print0 separators stay", "a\x00b\x00c\x00", "a\x00b\x00c\x00", 0},
		{"one short of the threshold", "x" + nul(NULRunMin-1) + "y", "x" + nul(NULRunMin-1) + "y", 0},
		{"at the threshold", "x" + nul(NULRunMin) + "y", fmt.Sprintf("x[greenroom: %d NUL bytes]y", NULRunMin), NULRunMin},
		{"the issue's 4 KiB hole", nul(4096) + "GET /api 200\n", "[greenroom: 4096 NUL bytes]GET /api 200\n", 4096},
		{"whole output", nul(100), "[greenroom: 100 NUL bytes]", 100},
		{"several runs, a short one kept", nul(20) + "a" + nul(3) + "b" + nul(30),
			"[greenroom: 20 NUL bytes]a" + nul(3) + "b[greenroom: 30 NUL bytes]", 50},
		{"next to multi-byte characters", "é" + nul(64) + "日本", "é[greenroom: 64 NUL bytes]日本", 64},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, n := CollapseNULs(c.in)
			if got != c.want || n != c.collapsed {
				t.Errorf("CollapseNULs = %q, %d; want %q, %d", got, n, c.want, c.collapsed)
			}
		})
	}
}

// The head and tail cut runs on the bytes the command wrote, then each kept part's runs are
// collapsed: a hole across the cut becomes two markers around the cut's own, the byte count
// stays what the command wrote, and the text stays valid UTF-8.
func TestAnExecResultCollapsesNULRunsAfterItsHeadAndTailCut(t *testing.T) {
	var h headTail
	body := "日" + strings.Repeat("\x00", 100<<10) + "end\n"
	if _, err := h.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	var r ExecResult
	r.Stdout, r.StdoutBytes, r.StdoutTruncated = h.result()
	got := r.forCaller()

	headNULs := ExecHeadLimit - len("日")
	tailNULs := ExecTailLimit - len("end\n")
	left := len(body) - ExecHeadLimit - ExecTailLimit
	want := fmt.Sprintf("日[greenroom: %d NUL bytes]\n[greenroom: %d bytes left out here; the command wrote %d]\n[greenroom: %d NUL bytes]end\n",
		headNULs, left, len(body), tailNULs)
	if got.Stdout != want {
		t.Errorf("stdout = %q\nwant     %q", got.Stdout, want)
	}
	if !got.StdoutNulsCollapsed || got.StdoutNulBytes != int64(headNULs+tailNULs) {
		t.Errorf("stdoutNulsCollapsed %v, stdoutNulBytes %d; want true, %d", got.StdoutNulsCollapsed, got.StdoutNulBytes, headNULs+tailNULs)
	}
	if !got.StdoutTruncated || got.StdoutBytes != int64(len(body)) {
		t.Errorf("stdoutTruncated %v, stdoutBytes %d; want true, %d", got.StdoutTruncated, got.StdoutBytes, len(body))
	}
	if got.StderrNulsCollapsed || got.StderrNulBytes != 0 {
		t.Errorf("an empty stderr is flagged: %v, %d", got.StderrNulsCollapsed, got.StderrNulBytes)
	}
	if !utf8.ValidString(got.Stdout) {
		t.Error("the collapsed text is not valid UTF-8")
	}
}
