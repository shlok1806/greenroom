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

// The run directory of a create whose clone failed is read back as a Mac that never started,
// and keeps the label its create was given (issue #286, root ADR 0049).
func TestACreateThatFailedIsReadAsAMacThatNeverStarted(t *testing.T) {
	mgr, root, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-clone")
	if _, err := mgr.CreateLabeled(context.Background(), "greenroom-lean-a", Label{Name: "TipSplit: rounding", Source: "claude-code"}); err == nil {
		t.Fatal("create succeeded with fail-clone")
	}
	entries, err := os.ReadDir(filepath.Join(root, "runs"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("runs = %v, %v; want the failed create's run", entries, err)
	}
	dir := filepath.Join(root, "runs", entries[0].Name())
	log, err := ReadStepLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f := log.StartFailure; f == nil || f.Tool != StepCreate || !strings.Contains(f.Error, "clone") {
		t.Errorf("StartFailure = %+v, want the create's clone error", f)
	}
	man, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if man.Name != "TipSplit: rounding" || man.Source != "claude-code" || man.DestroyedAt == nil {
		t.Errorf("manifest name %q source %q destroyedAt %v; want the label and an end", man.Name, man.Source, man.DestroyedAt)
	}
}

func TestOnlyAFailedCreateOrBootIsAStartFailure(t *testing.T) {
	at := time.Now()
	for _, tc := range []struct {
		name  string
		steps []Step
		want  string // the failing tool, "" for none
	}{
		{"nothing recorded", nil, ""},
		{"a ready Mac", []Step{{Tool: StepCreate}, {Tool: StepBoot}}, ""},
		{"a failed clone", []Step{{Tool: StepCreate, Error: "tart clone: exit status 2"}}, StepCreate},
		{"a failed boot", []Step{{Tool: StepCreate}, {Tool: StepBoot, Error: "timed out"}}, StepBoot},
		{"a failed reboot after a ready boot", []Step{{Tool: StepCreate}, {Tool: StepBoot}, {Tool: "machine_reboot", Error: "tart run exited"}}, ""},
		{"a failed command", []Step{{Tool: StepCreate}, {Tool: StepBoot}, {Tool: "machine_exec", Error: "tart exec failed"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.steps {
				tc.steps[i].Seq, tc.steps[i].At = i+1, at
			}
			got := StartFailureOf(tc.steps)
			if (got == nil) != (tc.want == "") || (got != nil && got.Tool != tc.want) {
				t.Errorf("StartFailureOf = %+v, want %q", got, tc.want)
			}
		})
	}
}
