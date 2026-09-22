package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

func frac(v float64) *float64 { return &v }

// postedActions decodes what the daemon actually sent the guest helper: the
// fake tart logs the whole command, and the payload rides in it as base64.
func postedActions(t *testing.T, control string) []InputAction {
	t.Helper()
	calls := testsupport.Calls(t, control)
	re := regexp.MustCompile(`--json-base64 ([A-Za-z0-9+/=]+)`)
	var last []InputAction
	for _, match := range re.FindAllStringSubmatch(calls, -1) {
		raw, err := base64.StdEncoding.DecodeString(match[1])
		if err != nil {
			t.Fatalf("payload is not base64: %v", err)
		}
		var payload struct {
			Actions []InputAction `json:"actions"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("payload is not JSON: %v: %s", err, raw)
		}
		if len(payload.Actions) > 0 {
			last = payload.Actions
		}
	}
	return last
}

func TestTakeControlAndRelease(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	if _, held := mgr.ControlState(mc.RunID); held {
		t.Fatal("a fresh machine already has a control holder")
	}

	c, fresh, err := mgr.TakeControl(mc.RunID, "human", 0)
	if err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if !fresh {
		t.Error("the first take is not reported as fresh")
	}
	if c.Holder != "human" || !c.Expires.After(time.Now()) {
		t.Errorf("lease is %+v, want a live human lease", c)
	}

	// Taking it again is a renewal, not a fresh handover.
	again, fresh, err := mgr.TakeControl(mc.RunID, "human", 0)
	if err != nil {
		t.Fatalf("second TakeControl: %v", err)
	}
	if fresh {
		t.Error("renewing a lease is reported as a fresh take")
	}
	if !again.Since.Equal(c.Since) {
		t.Errorf("a renewal moved Since from %s to %s", c.Since, again.Since)
	}

	released, held, err := mgr.ReleaseControl(mc.RunID, "human")
	if err != nil || !held {
		t.Fatalf("ReleaseControl: held=%v err=%v", held, err)
	}
	if released.Holder != "human" {
		t.Errorf("released lease is %+v", released)
	}
	if _, still := mgr.ControlState(mc.RunID); still {
		t.Error("the lease survived its release")
	}
	// Releasing again is not an error.
	if _, held, err := mgr.ReleaseControl(mc.RunID, "human"); err != nil || held {
		t.Errorf("a second release reported held=%v err=%v", held, err)
	}
}

func TestControlIsRefusedToASecondHolder(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	_, _, err := mgr.TakeControl(mc.RunID, "verifier", 0)
	if !errors.Is(err, ErrControlHeld) {
		t.Fatalf("a second holder got %v, want ErrControlHeld", err)
	}
	// And it cannot take the screen by posting either.
	_, err = mgr.Input(context.Background(), mc.RunID, "verifier", []InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}})
	if !errors.Is(err, ErrControlHeld) {
		t.Fatalf("input from a second holder got %v, want ErrControlHeld", err)
	}
}

func TestAnExpiredLeaseIsNoLease(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	if _, _, err := mgr.TakeControl(mc.RunID, "human", time.Millisecond); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if c, held := mgr.ControlState(mc.RunID); held {
		t.Fatalf("an expired lease is still reported as held: %+v", c)
	}
	// The screen is free again, so somebody else may take it.
	if _, _, err := mgr.TakeControl(mc.RunID, "verifier", 0); err != nil {
		t.Fatalf("taking an expired lease: %v", err)
	}
}

func TestInputNeedsALease(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	_, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}})
	if !errors.Is(err, ErrNoControl) {
		t.Fatalf("input without a lease got %v, want ErrNoControl", err)
	}
}

func TestInputScalesFractionsToPixels(t *testing.T) {
	mgr, _, control := newTestManager(t)
	if err := os.WriteFile(filepath.Join(control, "screen"), []byte("1600x1200"), 0o644); err != nil {
		t.Fatal(err)
	}
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}

	res, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{
		{Type: "move", X: frac(0.25), Y: frac(0.5)},
		{Type: "click", X: frac(0.25), Y: frac(0.5), Clicks: 2},
	})
	if err != nil {
		t.Fatalf("Input: %v", err)
	}
	if res.Screen != (Screen{Width: 1600, Height: 1200}) {
		t.Errorf("screen is %+v, want 1600x1200", res.Screen)
	}
	if res.Actions != 2 {
		t.Errorf("reported %d actions, want 2", res.Actions)
	}

	posted := postedActions(t, control)
	if len(posted) != 2 {
		t.Fatalf("the guest was sent %d actions, want 2", len(posted))
	}
	if *posted[0].X != 400 || *posted[0].Y != 600 {
		t.Errorf("0.25,0.5 of a 1600x1200 screen went out as %v,%v, want 400,600", *posted[0].X, *posted[0].Y)
	}
	if posted[1].Clicks != 2 {
		t.Errorf("the double click lost its count: %+v", posted[1])
	}
}

func TestInputClampsFractionsToTheScreen(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	// A drag that leaves the picture is a hand sliding off the edge, not a
	// bad request.
	if _, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{
		{Type: "move", X: frac(-0.4), Y: frac(3)},
	}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	posted := postedActions(t, control)
	if len(posted) != 1 || *posted[0].X != 0 || *posted[0].Y != 768 {
		t.Fatalf("out-of-picture coordinates went out as %+v", posted)
	}
}

func TestInputRecordsOneStepPerBatch(t *testing.T) {
	mgr, root, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	res, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{
		{Type: "key", Key: "a", Mods: []string{"cmd"}},
		{Type: "type", Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Input: %v", err)
	}

	steps, err := ReadSteps(filepath.Join(root, "runs", mc.RunID))
	if err != nil {
		t.Fatalf("ReadSteps: %v", err)
	}
	var found *Step
	for i := range steps {
		if steps[i].Tool == "machine_input" {
			if found != nil {
				t.Fatalf("one batch recorded more than one step")
			}
			found = &steps[i]
		}
	}
	if found == nil {
		t.Fatal("no machine_input step was recorded")
	}
	if found.Seq != res.Step {
		t.Errorf("Input reported step %d, the log holds %d", res.Step, found.Seq)
	}
	// The text a person typed is evidence, so it is in the record.
	line, _ := json.Marshal(found.Input)
	if !strings.Contains(string(line), "hello") {
		t.Errorf("the typed text is missing from the step: %s", line)
	}
}

func TestInputCountsActionsOnTheLease(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{
			{Type: "click", X: frac(0.1), Y: frac(0.1)},
		}); err != nil {
			t.Fatalf("Input: %v", err)
		}
	}
	c, held := mgr.ControlState(mc.RunID)
	if !held {
		t.Fatal("the lease went away while it was being used")
	}
	if c.Actions != 3 {
		t.Errorf("the lease counted %d actions, want 3", c.Actions)
	}
}

func TestInputInstallsTheHelperOnce(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{
			{Type: "click", X: frac(0.5), Y: frac(0.5)},
		}); err != nil {
			t.Fatalf("Input: %v", err)
		}
	}
	// Count the compile command; the script names swiftc more than once.
	if n := strings.Count(testsupport.Calls(t, control), "swiftc -O"); n != 1 {
		t.Errorf("the helper was compiled %d times, want 1", n)
	}
}

func TestInputSaysWhenTheMachineCannotBuildTheHelper(t *testing.T) {
	mgr, _, control := newTestManager(t)
	testsupport.Flag(t, control, "fail-input-install")
	mc := readyMachine(t, mgr)

	_, err := mgr.ScreenOf(context.Background(), mc.RunID)
	if err == nil || !strings.Contains(err.Error(), "swiftc") {
		t.Fatalf("a machine with no Swift toolchain gave %v", err)
	}
}

func TestInputSurfacesTheHelperError(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	testsupport.Flag(t, control, "input-down")

	_, err := mgr.Input(context.Background(), mc.RunID, "human", []InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}})
	if err == nil || !strings.Contains(err.Error(), "this machine refused the event") {
		t.Fatalf("a refusing guest gave %v, want the helper's own message", err)
	}
}

func TestInputRefusesAnEmptyBatch(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if _, err := mgr.Input(context.Background(), mc.RunID, "human", nil); err == nil {
		t.Fatal("an empty batch was accepted")
	}
}

// InputAs takes the lease, posts, and frees the screen before it returns.
func TestInputAsTakesPostsAndReleases(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	if _, held := mgr.ControlState(mc.RunID); held {
		t.Fatal("a fresh machine already has a control holder")
	}

	res, err := mgr.InputAs(context.Background(), mc.RunID, "verifier", []InputAction{
		{Type: "click", X: frac(0.5), Y: frac(0.5)},
	})
	if err != nil {
		t.Fatalf("InputAs: %v", err)
	}
	if res.Actions != 1 {
		t.Errorf("InputAs reported %d actions, want 1", res.Actions)
	}
	if _, held := mgr.ControlState(mc.RunID); held {
		t.Fatal("InputAs left the lease held after it returned")
	}

	if _, err := mgr.InputAs(context.Background(), mc.RunID, "human", []InputAction{
		{Type: "click", X: frac(0.5), Y: frac(0.5)},
	}); err != nil {
		t.Fatalf("InputAs from a second holder after release: %v", err)
	}
}

// A held screen yields a readable error naming the holder, not ErrControlHeld.
func TestInputAsRefusesWhileSomeoneElseHoldsTheScreen(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}

	_, err := mgr.InputAs(context.Background(), mc.RunID, "verifier", []InputAction{
		{Type: "click", X: frac(0.5), Y: frac(0.5)},
	})
	if err == nil || !strings.Contains(err.Error(), "human") {
		t.Fatalf("InputAs while a human holds the screen gave %v, want an error naming human", err)
	}
	if errors.Is(err, ErrControlHeld) {
		t.Fatal("InputAs surfaced the sentinel ErrControlHeld instead of a readable message naming the holder")
	}
	// The human's own lease must survive a refused InputAs call untouched.
	if c, held := mgr.ControlState(mc.RunID); !held || c.Holder != "human" {
		t.Fatalf("the human's lease is %+v held=%v after a refused InputAs call", c, held)
	}
}

func TestControlOnAMachineThatIsGone(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err == nil {
		t.Error("control was taken of a machine that is gone")
	}
	if _, held := mgr.ControlState(mc.RunID); held {
		t.Error("a destroyed machine reports a control holder")
	}
}

func TestControlIsAnnouncedOnTheEventStream(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)

	seen := make(chan *Machine, 4)
	stop := mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind == "control" {
			seen <- ev.Machine
		}
	})
	defer stop()

	if _, _, err := mgr.TakeControl(mc.RunID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	select {
	case got := <-seen:
		if got == nil || got.Control == nil || got.Control.Holder != "human" {
			t.Fatalf("the take event carried %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("taking control published no event")
	}

	if _, _, err := mgr.ReleaseControl(mc.RunID, "human"); err != nil {
		t.Fatalf("ReleaseControl: %v", err)
	}
	select {
	case got := <-seen:
		if got == nil || got.Control != nil {
			t.Fatalf("the release event still carries a lease: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("releasing control published no event")
	}
}

func TestPixelsLeavesCoordinatelessActionsAlone(t *testing.T) {
	a := pixels(InputAction{Type: "type", Text: "hi"}, Screen{Width: 800, Height: 600})
	if a.X != nil || a.Y != nil {
		t.Errorf("typing gained coordinates: %+v", a)
	}
	if a.Text != "hi" {
		t.Errorf("the text changed: %q", a.Text)
	}
}

func TestHelperErrorPrefersTheHelpersOwnMessage(t *testing.T) {
	if got := helperError(`{"error":"unknown key foo"}`); got != "unknown key foo" {
		t.Errorf("helperError = %q", got)
	}
	if got := helperError("  plain trouble\n"); got != "plain trouble" {
		t.Errorf("helperError = %q", got)
	}
}
