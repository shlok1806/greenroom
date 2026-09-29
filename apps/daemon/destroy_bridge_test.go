package main

import (
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// Issue #217: the destroyed event names who asked and through what, after the "machine
// destroyed" prefix the summary and the Companion match on (daemon ADR 0008).
func TestTheDestroyedEventSaysWhoDestroyedTheMachine(t *testing.T) {
	for _, c := range []struct {
		by, via, want string
	}{
		{"", "", "machine destroyed"},
		{"agent (claude-code)", "machine_destroy", "machine destroyed by agent (claude-code) through machine_destroy"},
		{"agent", "run_finish", "machine destroyed by agent through run_finish"},
		{"human", "the companion", "machine destroyed by human through the companion"},
	} {
		ev := machine.LifecycleEvent{Kind: "destroyed", By: c.by, Via: c.via}
		if got := destroyedText(ev); got != c.want {
			t.Errorf("destroyedText(by %q, via %q) = %q, want %q", c.by, c.via, got, c.want)
		}
	}
}
