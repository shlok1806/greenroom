package machine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

func click() []InputAction { return []InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}} }

// humanHandover is a person taking the screen and giving it back.
func humanHandover(t *testing.T, mgr *Manager, runID string) {
	t.Helper()
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if _, held, err := mgr.ReleaseControl(runID, "human"); err != nil || !held {
		t.Fatalf("ReleaseControl: held=%v err=%v", held, err)
	}
}

// batches counts the input batches that reached the guest helper.
func batches(t *testing.T, control string) int {
	t.Helper()
	return strings.Count(testsupport.Calls(t, control), "--json-base64")
}

// Issue #124: the verifier carried out a plan made on the screen as it was before a person took
// it. After someone else takes and gives back the screen, its input is refused until it looks
// again, with machine_ui or machine_screenshot; its own look, not the coder's, is what counts.
func TestAHandoverRefusesTheVerifierUntilItLooks(t *testing.T) {
	mgr, _, control := newTestManager(t)
	writeUI(t, control, tipSplitUI)
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(pngBase64(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, mgr)
	ctx := context.Background()
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); err != nil {
		t.Fatalf("InputAs before any handover: %v", err)
	}

	humanHandover(t, mgr, mc.RunID)
	before := batches(t, control)
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); !errors.Is(err, ErrStaleLook) {
		t.Fatalf("InputAs after a handover = %v, want ErrStaleLook", err)
	}
	if got := batches(t, control); got != before {
		t.Errorf("a stale input reached the guest (%d batches, want %d)", got, before)
	}
	if c, held := mgr.ControlState(mc.RunID); held {
		t.Errorf("the refused input left the lease %+v held", c)
	}
	if _, err := mgr.UI(ctx, mc.RunID, HolderCoder, "", 0); err != nil {
		t.Fatalf("UI: %v", err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); !errors.Is(err, ErrStaleLook) {
		t.Fatalf("InputAs after the coder looked = %v, want ErrStaleLook: the coder's look is not the verifier's", err)
	}
	if _, err := mgr.UI(ctx, mc.RunID, HolderVerifier, "", 0); err != nil {
		t.Fatalf("UI: %v", err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); err != nil {
		t.Fatalf("InputAs after a machine_ui read: %v", err)
	}

	humanHandover(t, mgr, mc.RunID)
	if _, _, err := mgr.Screenshot(ctx, mc.RunID); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); !errors.Is(err, ErrStaleLook) {
		t.Fatalf("InputAs after a screenshot nobody's = %v, want ErrStaleLook", err)
	}
	if _, _, err := mgr.ScreenshotAs(ctx, mc.RunID, HolderVerifier); err != nil {
		t.Fatalf("ScreenshotAs: %v", err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); err != nil {
		t.Fatalf("InputAs after the verifier's screenshot: %v", err)
	}
}

// A look while the person still holds the screen is allowed, but does not survive their Give Back.
// While they hold it the refusal is the screen being taken, not a stale look.
func TestALookWhileTheHumanHoldsTheScreenIsStaleOnceTheyGiveItBack(t *testing.T) {
	mgr, _, control := newTestManager(t)
	writeUI(t, control, tipSplitUI)
	mc := readyMachine(t, mgr)
	ctx := context.Background()
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if _, err := mgr.UI(ctx, mc.RunID, HolderVerifier, "", 0); err != nil {
		t.Fatalf("UI: %v", err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); !errors.Is(err, ErrScreenTaken) {
		t.Fatalf("InputAs while the human holds the screen = %v, want ErrScreenTaken", err)
	}
	if _, _, err := mgr.ReleaseControl(mc.RunID, "human"); err != nil {
		t.Fatalf("ReleaseControl: %v", err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); !errors.Is(err, ErrStaleLook) {
		t.Fatalf("InputAs after the give back = %v, want ErrStaleLook", err)
	}
}

// A human lease that lapsed is a handover too, whether the timer or the verifier's own take finds it.
func TestAHumanLapseMakesTheVerifierLookAgain(t *testing.T) {
	mgr, _, control := newTestManager(t)
	writeUI(t, control, tipSplitUI)
	mc := readyMachine(t, mgr)
	live, _ := mgr.get(mc.RunID)
	ctx := context.Background()
	var seen lapses
	seen.listen(mgr)
	if _, err := mgr.UI(ctx, mc.RunID, HolderVerifier, "", 0); err != nil {
		t.Fatalf("UI: %v", err)
	}
	expireLease(t, mgr, live, "human")
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); !errors.Is(err, ErrStaleLook) {
		t.Fatalf("InputAs that found the human's lease lapsed = %v, want ErrStaleLook", err)
	}
	if got := seen.all(); len(got) != 1 || got[0].Lapsed.Holder != "human" {
		t.Fatalf("lapse events %+v, want the human's lapse announced", got)
	}
}

// The verifier's own takes, renewals, releases and lapses never make its look stale, and the coder,
// which never runs inside a verifier turn (issue #82), is not refused for a handover.
func TestOnlyAnotherSeatsHandoverIsStale(t *testing.T) {
	mgr, _, control := newTestManager(t)
	writeUI(t, control, tipSplitUI)
	mc := readyMachine(t, mgr)
	live, _ := mgr.get(mc.RunID)
	ctx := context.Background()
	if _, err := mgr.UI(ctx, mc.RunID, HolderVerifier, "", 0); err != nil {
		t.Fatalf("UI: %v", err)
	}
	for range 3 {
		if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); err != nil {
			t.Fatalf("InputAs: %v", err)
		}
	}
	if _, _, err := mgr.TakeControl(mc.RunID, HolderVerifier, 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if _, err := mgr.RenewControl(mc.RunID, HolderVerifier); err != nil {
		t.Fatalf("RenewControl: %v", err)
	}
	if _, _, err := mgr.ReleaseControl(mc.RunID, HolderVerifier); err != nil {
		t.Fatalf("ReleaseControl: %v", err)
	}
	expireLease(t, mgr, live, HolderVerifier)
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); err != nil {
		t.Fatalf("InputAs after the verifier's own lease changes: %v", err)
	}

	humanHandover(t, mgr, mc.RunID)
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderCoder, click()); err != nil {
		t.Fatalf("the coder's InputAs after a handover: %v", err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, "human", click()); err != nil {
		t.Fatalf("the human's InputAs after a handover: %v", err)
	}
}

// ADR 0024: HandoverStep marks where the screen last changed hands, so a verdict's evidence can be
// required to come after it. The verifier's own leases never move it.
func TestHandoverStepMarksTheLatestChangeOfHands(t *testing.T) {
	mgr, _, control := newTestManager(t)
	writeUI(t, control, tipSplitUI)
	mc := readyMachine(t, mgr)
	ctx := context.Background()
	if got := mgr.HandoverStep(mc.RunID); got != 0 {
		t.Fatalf("HandoverStep before any handover = %d, want 0", got)
	}
	tree, err := mgr.UI(ctx, mc.RunID, HolderVerifier, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InputAs(ctx, mc.RunID, HolderVerifier, click()); err != nil {
		t.Fatal(err)
	}
	if got := mgr.HandoverStep(mc.RunID); got != 0 {
		t.Errorf("the verifier's own lease moved HandoverStep to %d", got)
	}
	humanHandover(t, mgr, mc.RunID)
	mark := mgr.HandoverStep(mc.RunID)
	if mark <= tree.Step {
		t.Errorf("HandoverStep = %d, want after the look at step %d", mark, tree.Step)
	}
	after, err := mgr.UI(ctx, mc.RunID, HolderVerifier, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if after.Step <= mark {
		t.Errorf("a look after the handover is step %d, want after %d", after.Step, mark)
	}
	if got, ok := mgr.LastUI(mc.RunID, HolderVerifier); !ok || got.Step != after.Step {
		t.Errorf("LastUI = %d %v, want the verifier's latest read %d", got.Step, ok, after.Step)
	}
	if _, ok := mgr.LastUI(mc.RunID, HolderCoder); ok {
		t.Error("LastUI returned a tree for a reader that never read one")
	}
	// ADR 0024: each step says who made it, and an effect read carries its input's effect.
	if _, err := mgr.ExecAs(ctx, mc.RunID, HolderVerifier, "true", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ExecStart(ctx, mc.RunID, "true", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	_, effect, err := mgr.UIEffect(ctx, mc.RunID, HolderVerifier, 0, after.Step,
		func(tree UITree, err error) StepEffect { return StepEffect{Kind: EffectNone, Summary: "nothing"} })
	if err != nil || effect.Of != after.Step || effect.Kind != EffectNone {
		t.Fatalf("UIEffect = %+v, %v", effect, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var steps []Step
	for {
		if steps, err = mgr.Steps(mc.RunID); err != nil {
			t.Fatal(err)
		}
		if len(steps) > 0 && steps[len(steps)-1].Tool == "machine_ui" && countTool(steps, "machine_exec") == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var by []string
	for _, st := range steps {
		if st.Seq > tree.Step-1 && (st.Tool == "machine_ui" || st.Tool == "machine_input" || st.Tool == "machine_exec") {
			by = append(by, st.Tool+":"+st.By)
		}
	}
	want := "machine_ui:verifier machine_input:verifier machine_ui:verifier machine_exec:verifier machine_exec:coder machine_ui:verifier"
	if got := strings.Join(by, " "); got != want {
		t.Errorf("steps by seat = %q, want %q", got, want)
	}
	if last := steps[len(steps)-1]; last.Effect == nil || *last.Effect != (StepEffect{Of: after.Step, Kind: EffectNone, Summary: "nothing"}) {
		t.Errorf("effect read = %+v, want the effect on its record", last)
	}
	if got := mgr.HandoverStep("no-such-run"); got != 0 {
		t.Errorf("HandoverStep of an unknown run = %d, want 0", got)
	}
}

func countTool(steps []Step, tool string) int {
	n := 0
	for _, s := range steps {
		if s.Tool == tool {
			n++
		}
	}
	return n
}
