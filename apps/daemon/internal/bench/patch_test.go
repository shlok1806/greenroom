package bench

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const tenLines = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"

func TestApplyPatchEditsAFile(t *testing.T) {
	dir := writeTree(t, map[string]string{"src/a.txt": tenLines})
	diff := `diff --git a/src/a.txt b/src/a.txt
--- a/src/a.txt
+++ b/src/a.txt
@@ -2,3 +2,3 @@
 two
-three
+THREE
 four
@@ -8,2 +8,4 @@
 eight
 nine
+nine and a half
+nine and three quarters
`
	if err := ApplyPatch(dir, []byte(diff)); err != nil {
		t.Fatal(err)
	}
	want := "one\ntwo\nTHREE\nfour\nfive\nsix\nseven\neight\nnine\nnine and a half\nnine and three quarters\nten\n"
	if got := readFile(t, filepath.Join(dir, "src/a.txt")); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A hunk whose lines moved (the file gained lines above it) still applies, exactly.
func TestApplyPatchFindsAMovedHunk(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.txt": "zero\nzero\n" + tenLines})
	diff := "--- a/a.txt\n+++ b/a.txt\n@@ -5,1 +5,1 @@\n-five\n+FIVE\n"
	if err := ApplyPatch(dir, []byte(diff)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); !strings.Contains(got, "\nFIVE\n") || strings.Contains(got, "\nfive\n") {
		t.Errorf("the moved hunk did not apply: %q", got)
	}
}

// A context line that differs is a failure, never a fuzzy match, and nothing is written.
func TestApplyPatchRefusesAMismatchAndWritesNothing(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.txt": tenLines, "b.txt": tenLines})
	diff := "--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-one\n+ONE\n" +
		"--- a/b.txt\n+++ b/b.txt\n@@ -2,2 +2,2 @@\n two\n-THREE\n+3\n"
	err := ApplyPatch(dir, []byte(diff))
	if err == nil || !strings.Contains(err.Error(), "b.txt") || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v, want b.txt's hunk refused", err)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != tenLines {
		t.Error("a.txt was written although b.txt's hunk failed")
	}
}

func TestApplyPatchCreatesAndDeletesFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{"gone.txt": "bye\n"})
	diff := "--- /dev/null\n+++ b/new/file.txt\n@@ -0,0 +1,2 @@\n+hello\n+world\n" +
		"--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n"
	if err := ApplyPatch(dir, []byte(diff)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "new/file.txt")); got != "hello\nworld\n" {
		t.Errorf("created %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); !os.IsNotExist(err) {
		t.Errorf("gone.txt still exists: %v", err)
	}
}

func TestApplyPatchKeepsAMissingFinalNewline(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.txt": "one\ntwo"})
	diff := "--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,2 @@\n one\n-two\n\\ No newline at end of file\n+TWO\n\\ No newline at end of file\n"
	if err := ApplyPatch(dir, []byte(diff)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "one\nTWO" {
		t.Errorf("got %q, want %q", got, "one\nTWO")
	}
}

func TestApplyPatchStaysInsideTheDirectory(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.txt": "x\n"})
	// The first path component is stripped (-p1), as patch does: "b/../../escape.txt" is "../escape.txt".
	for _, target := range []string{"b/../../escape.txt", "b//etc/hosts"} {
		diff := "--- /dev/null\n+++ " + target + "\n@@ -0,0 +1 @@\n+x\n"
		if err := ApplyPatch(dir, []byte(diff)); err == nil {
			t.Errorf("a diff writing %s was applied", target)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Error("escape.txt was written outside the directory")
	}
}

func TestApplyPatchRefusesMalformedDiffs(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.txt": tenLines})
	for name, diff := range map[string]string{
		"no changes":      "just a commit message\n",
		"no +++":          "--- a/a.txt\n@@ -1 +1 @@\n-one\n+ONE\n",
		"short hunk":      "--- a/a.txt\n+++ b/a.txt\n@@ -1,3 +1,3 @@\n-one\n+ONE\n",
		"bad header":      "--- a/a.txt\n+++ b/a.txt\n@@ -x +1 @@\n-one\n+ONE\n",
		"no hunks":        "--- a/a.txt\n+++ b/a.txt\n",
		"creates present": "--- /dev/null\n+++ b/a.txt\n@@ -0,0 +1 @@\n+x\n",
	} {
		if err := ApplyPatch(dir, []byte(diff)); err == nil {
			t.Errorf("%s: applied", name)
		}
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != tenLines {
		t.Error("a refused diff changed the file")
	}
}

// ApplyPatch and patch(1) agree on every bench patch, so a patch a person checks with
// `patch -p1` is the one the runner applies.
func TestApplyPatchAgreesWithPatchOnEveryBenchPatch(t *testing.T) {
	bin, err := exec.LookPath("patch")
	if err != nil {
		t.Skip("no patch(1) on this host")
	}
	dir := repoBench(t)
	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Kind != KindMutant {
			continue
		}
		ours, theirs := t.TempDir(), t.TempDir()
		if err := PrepareApp(dir, c, ours); err != nil {
			t.Fatal(err)
		}
		if err := PrepareApp(dir, Case{App: c.App}, theirs); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "-p1", "-s", "--no-backup-if-mismatch", "-d", theirs, "-i", c.PatchPath(dir))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: patch(1): %v: %s", c.ID, err, out)
			continue
		}
		if treeText(t, ours) != treeText(t, theirs) {
			t.Errorf("%s: ApplyPatch and patch(1) disagree", c.ID)
		}
	}
}
