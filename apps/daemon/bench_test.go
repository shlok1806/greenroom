package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/bench"
)

// From apps/daemon, as the commands in CLAUDE.md run, the bench is the repo's bench/.
func TestFindBenchDirWalksUpToTheRepoBench(t *testing.T) {
	dir, err := findBenchDir("")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(filepath.Join("..", "..", "bench"))
	if dir != want {
		t.Errorf("findBenchDir = %s, want %s", dir, want)
	}
	if _, err := findBenchDir(t.TempDir()); err == nil {
		t.Error("a -bench without cases/ was accepted")
	}
}

// bench run refuses before touching a VM when it has no model or no matching case.
func TestBenchRunRefusesWithoutAModelOrACase(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")
	root := t.TempDir()
	err := benchRun([]string{"-root", root, "-env-file", filepath.Join(root, "none.env"), "-case", "tipsplit-wrong-computation"})
	if err == nil || !strings.Contains(err.Error(), "NVIDIA_API_KEY") {
		t.Errorf("err = %v, want the missing key named", err)
	}
	if err := benchRun([]string{"-root", root, "-case", "no-such-case"}); err == nil || !strings.Contains(err.Error(), `no case "no-such-case"`) {
		t.Errorf("err = %v, want the unknown case named", err)
	}
	if err := benchRun([]string{"-root", root, "-split", "holdout", "-kind", "nope"}); err == nil || !strings.Contains(err.Error(), "no case matches") {
		t.Errorf("err = %v, want no case matched", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("a refused run wrote into its root: %v", entries)
	}
}

func TestBenchScoreWritesTheReportNextToTheResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.jsonl")
	line, _ := json.Marshal(bench.Result{Case: "m", Trial: 1, Kind: bench.KindMutant, Split: bench.SplitDev,
		Expected: bench.ExpectFail, Family: "dead_control", Ending: bench.EndVerdict, Verdict: "pass", RunDir: "/runs/m"})
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := benchCmd([]string{"score", path}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "run.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "False pass rate (pass on a broken build): **1/1") || !strings.Contains(string(data), "`/runs/m`") {
		t.Errorf("report:\n%s", data)
	}
	if err := benchCmd([]string{"score"}); err == nil {
		t.Error("score without a results file was accepted")
	}
	if err := benchCmd([]string{"nope"}); err == nil {
		t.Error("an unknown bench command was accepted")
	}
}
