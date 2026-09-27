package machine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #103: a run clone whose run record is gone (a test killed mid-run, a daemon crash
// between clone and record) stayed in ~/.tart holding about 30 GB. The sweep deletes exactly
// those: greenroom-<runId> clones that are stopped, older than the threshold, not a live
// machine's, and with no run directory here or in a sibling root. Nothing else is touched.
func TestTheSweepDeletesOnlyOrphanedRunClones(t *testing.T) {
	mgr, root, control := newTestManager(t)
	tartHome := t.TempDir()
	t.Setenv("TART_HOME", tartHome)
	old := time.Now().Add(-48 * time.Hour)
	const (
		orphan  = "greenroom-20260923-215022-10d218794d3d8da9" // stopped, old, no run anywhere
		young   = "greenroom-%s-00000000000000aa"              // stopped, no run, but cloned just now
		running = "greenroom-20260924-040509-00000000000000bb" // a tart run owns it
		hasRun  = "greenroom-20260924-040039-00000000000000cc" // its run directory is here
		inBench = "greenroom-20260924-050000-00000000000000dd" // its run directory is in a sibling root
		inOther = "greenroom-20260924-070000-00000000000000ff" // its run directory is under another root the caller names
		touched = "greenroom-20260924-060000-00000000000000ee" // old name, but tart touched its files just now
	)
	youngName := strings.Replace(young, "%s", time.Now().UTC().Format("20060102-150405"), 1)
	vms := []struct{ name, state string }{
		{orphan, "stopped"}, {youngName, "stopped"}, {running, "running"}, {hasRun, "stopped"}, {inBench, "stopped"},
		{touched, "stopped"}, {inOther, "stopped"},
		{"greenroom-base-v7-r2", "stopped"}, {"greenroom-lean-a", "stopped"}, {"greenroom-base", "stopped"},
		{"someone-elses-vm", "stopped"}, {"greenroom-20260923-215022-10d218794d3d8da9-copy", "stopped"},
	}
	var list []string
	for _, vm := range vms {
		list = append(list, `{"Source":"local","Name":"`+vm.name+`","State":"`+vm.state+`"}`)
		dir := filepath.Join(tartHome, "vms", vm.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if vm.name != touched {
			if err := os.Chtimes(dir, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	list = append(list, `{"Source":"oci","Name":"ghcr.io/cirruslabs/greenroom-20260101-000000-0000000000000000","State":"stopped"}`)
	if err := os.WriteFile(filepath.Join(control, "list.json"), []byte("["+strings.Join(list, ",")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(root, "runs", "20260924-040039-00000000000000cc"),
		filepath.Join(root, "bench", "runs", "20260924-050000-00000000000000dd")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "runs", "20260924-070000-00000000000000ff"), 0o755); err != nil {
		t.Fatal(err)
	}
	swept, err := mgr.SweepOrphans(context.Background(), 6*time.Hour, false, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(swept) != 1 || swept[0].Name != orphan || swept[0].Error != "" {
		t.Fatalf("swept %+v, want only %s", swept, orphan)
	}
	deleted := deletes(t, control)
	if len(deleted) != 1 || deleted[0] != orphan {
		t.Errorf("tart delete calls = %q, want only %s", deleted, orphan)
	}

	// A dry run names what it would delete and deletes nothing.
	swept, err = mgr.SweepOrphans(context.Background(), 6*time.Hour, true, other)
	if err != nil || len(swept) != 1 || swept[0].Name != orphan || !swept[0].DryRun {
		t.Errorf("dry run = %+v, %v", swept, err)
	}
	if got := deletes(t, control); len(got) != 1 {
		t.Errorf("a dry run deleted: %q", got)
	}
	// 0 turns the sweep off.
	if swept, err := mgr.SweepOrphans(context.Background(), 0, false); err != nil || swept != nil {
		t.Errorf("a sweep with no threshold = %+v, %v", swept, err)
	}
}

// A live machine's clone is never swept, whatever its run directory says.
func TestTheSweepNeverTouchesALiveMachine(t *testing.T) {
	mgr, root, control := newTestManager(t)
	t.Setenv("TART_HOME", t.TempDir())
	mc := readyMachine(t, mgr)
	if err := os.RemoveAll(filepath.Join(root, "runs", mc.RunID)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "list.json"),
		[]byte(`[{"Source":"local","Name":"`+mc.Name+`","State":"stopped"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	swept, err := mgr.SweepOrphans(context.Background(), time.Nanosecond, false)
	if err != nil || len(swept) != 0 {
		t.Errorf("swept %+v, %v; want nothing", swept, err)
	}
}

// deletes are the VM names the daemon passed to `tart delete`, in order.
func deletes(t *testing.T, control string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(testsupport.Calls(t, control), "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "delete "); ok {
			out = append(out, name)
		}
	}
	return out
}
