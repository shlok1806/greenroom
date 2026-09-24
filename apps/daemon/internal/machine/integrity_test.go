package machine

import (
	"context"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// Issue #85: a click, down, up or move without both x and y was posted at wherever the pointer
// was and reported as done. It is refused, naming what is missing, before anything is posted.
func TestPointerActionsNeedBothCoordinates(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	for _, a := range []InputAction{
		{Type: "click"},
		{Type: "down"},
		{Type: "up", X: frac(0.5)},
		{Type: "move", Y: frac(0.5)},
	} {
		_, err := mgr.InputAs(context.Background(), mc.RunID, HolderCoder, []InputAction{a})
		if err == nil || !strings.Contains(err.Error(), "needs x and y") {
			t.Errorf("%+v: err = %v, want it refused for missing x and y", a, err)
		}
	}
	if posted := postedActions(t, control); len(posted) != 0 {
		t.Fatalf("a refused batch posted %+v", posted)
	}
	// A scroll goes to the pointer by design, so it needs neither.
	if _, err := mgr.InputAs(context.Background(), mc.RunID, HolderCoder, []InputAction{{Type: "scroll", DeltaY: 10}}); err != nil {
		t.Fatalf("a scroll with no coordinates was refused: %v", err)
	}
}

// Issue #82: while the verifier is in a turn, the coding agent's input landed on the same app.
// The coder is refused with words that say what to do; the verifier and a human are not.
func TestCoderInputIsRefusedDuringAVerifierTurn(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	click := []InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}}

	mgr.SetVerifierTurn(mc.RunID, true)
	_, err := mgr.InputAs(context.Background(), mc.RunID, HolderCoder, click)
	if err == nil || !strings.Contains(err.Error(), "verifier") || !strings.Contains(err.Error(), "agent_wait") {
		t.Fatalf("coder input during a turn = %v, want a refusal naming the verifier and agent_wait", err)
	}
	if posted := postedActions(t, control); len(posted) != 0 {
		t.Fatalf("the coder's click was posted during the verifier's turn: %+v", posted)
	}
	if _, err := mgr.InputAs(context.Background(), mc.RunID, HolderVerifier, click); err != nil {
		t.Fatalf("the verifier's own input was refused: %v", err)
	}

	mgr.SetVerifierTurn(mc.RunID, false)
	if _, err := mgr.InputAs(context.Background(), mc.RunID, HolderCoder, click); err != nil {
		t.Fatalf("coder input after the turn = %v", err)
	}
}

// Issue #81: "Add period with double-space" turned "a  b" into "a. b". Boot turns the
// automatic text substitutions off with the other desktop preferences.
func TestBootTurnsOffTextSubstitutions(t *testing.T) {
	mgr, _, control := newTestManager(t)
	readyMachine(t, mgr)
	log := testsupport.Calls(t, control)
	for _, key := range []string{
		"NSAutomaticPeriodSubstitutionEnabled -bool false",
		"NSAutomaticQuoteSubstitutionEnabled -bool false",
		"NSAutomaticDashSubstitutionEnabled -bool false",
		"NSAutomaticSpellingCorrectionEnabled -bool false",
		"NSAutomaticCapitalizationEnabled -bool false",
	} {
		if !strings.Contains(log, key) {
			t.Errorf("boot never ran defaults write NSGlobalDomain %s", key)
		}
	}
}
