package machine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
