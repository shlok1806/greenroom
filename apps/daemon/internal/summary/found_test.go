package summary

import (
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// What the live check of run 20260928-000221-d9a2350ea69e1d91 found, each as its own rule.

func TestDisagreementSkipsTheSetupAndHearsWhatWasWanted(t *testing.T) {
	cases := []struct{ criterion, observed, expected, saw string }{
		// The live verdict's two failed checks, as the verifier wrote them.
		{"With Bill 120, 20% tip, People 3, Each pays reads $48.00", "Each pays reads $8.00, not $48.00", "$48.00", "$8.00"},
		{"After picking 25% tip, Each pays reads $50.00", "Each pays reads $10.00, not $50.00", "$50.00", "$10.00"},
		// The observation repeats the setup before the result.
		{"Each pays is $48.00 for 3 people", "With Bill 120 and 3 people, Each pays reads $8.00", "$48.00", "$8.00"},
		// Two values of one kind in the criterion: the result, not the bill.
		{"With a bill of $120, Each pays reads $48.00", "Each pays reads $8.00", "$48.00", "$8.00"},
		{"Tip is $24.00 for $120 at 20%", "Tip reads $30.00 for $120", "$24.00", "$30.00"},
		// The observation only says what it is not.
		{"The count reads 3 items", "The count does not read 3", "3", ""},
		{"Each pays reads $48.00", "Each pays reads $8.00 instead of the expected $48.00", "$48.00", "$8.00"},
	}
	for _, c := range cases {
		e, s := Disagreement(c.criterion, c.observed)
		if e != c.expected || s != c.saw {
			t.Errorf("Disagreement(%q, %q) = %q, %q; want %q, %q", c.criterion, c.observed, e, s, c.expected, c.saw)
		}
	}
}

func TestACheckRowKeepsTheClaimNotTheSetup(t *testing.T) {
	cases := []struct{ criterion, text, setup string }{
		{"With Bill 120, 20% tip, People 3, Each pays reads $48.00", "Each pays reads $48.00", "With Bill 120, 20% tip, People 3"},
		{"After picking 25% tip, Each pays reads $50.00", "After picking 25% tip, Each pays reads $50.00", ""},
		{"When the list has three items and two are ticked, then the summary reads 2 of 3 done", "The summary reads 2 of 3 done", "When the list has three items and two are ticked"},
		{"The window shows Bill, Tip and People labels", "The window shows Bill, Tip and People labels", ""},
		{"The heading reads Hello and the button reads Tap me and the footer reads Bye", "The heading reads Hello and the button reads…", ""},
		{"With everything set, ok", "With everything set, ok", ""},
	}
	for _, c := range cases {
		text, setup := checkText(session.Check{ID: "x", Criterion: c.criterion})
		if text != c.text || setup != c.setup {
			t.Errorf("checkText(%q) = %q, %q; want %q, %q", c.criterion, text, setup, c.text, c.setup)
		}
	}
	if text, _ := checkText(session.Check{ID: "each-pays-48"}); text != "each pays 48" {
		t.Errorf("a check with no criterion reads %q, want its id in words", text)
	}
}

func TestBootWordsNeverGoBackToWaiting(t *testing.T) {
	order := []string{machine.PhaseClone, machine.PhaseStart, machine.PhaseAgent, machine.PhaseIP, machine.PhaseKey,
		machine.PhaseSettings, machine.PhaseHelper, machine.PhaseChecks, machine.PhaseSSH}
	want := []string{"Copying the Mac", "Starting the Mac", "Waiting for the Mac", "Waiting for the Mac", "Getting the Mac ready",
		"Getting the Mac ready", "Getting the Mac ready", "Getting the Mac ready", "Getting the Mac ready"}
	var phases []machine.BootPhase
	for i, p := range order {
		phases = append(phases, machine.BootPhase{Phase: p})
		if got := bootWords(phases); got != want[i] {
			t.Errorf("during %s: %q, want %q", p, got, want[i])
		}
	}
}

func TestAClickIsNamedByWhatThePersonWouldCallIt(t *testing.T) {
	stepper := []machine.UIElement{
		{ID: 11, Role: "StaticText", Value: "People: 2", X: 0.344, Y: 0.464, W: 0.056, H: 0.021},
		{ID: 12, Role: "Incrementor", Value: "2", X: 0.39, Y: 0.464, W: 0.02, H: 0.034},
		{ID: 13, Role: "Button", Subrole: "IncrementArrow", X: 0.39, Y: 0.455, W: 0.02, H: 0.017},
		{ID: 14, Role: "Button", Subrole: "DecrementArrow", X: 0.39, Y: 0.472, W: 0.02, H: 0.017},
		{ID: 4, Role: "TextField", Label: "Bill", Value: "120", X: 0.461, Y: 0.358, W: 0.137, H: 0.031},
		{ID: 30, Role: "Slider", Value: "0.5", X: 0.7, Y: 0.7, W: 0.1, H: 0.02},
	}
	cases := []struct {
		x, y float64
		want string
	}{
		{0.39, 0.455, "Clicking the up arrow in TipSplit"},
		{0.39, 0.472, "Clicking the down arrow in TipSplit"},
		{0.461, 0.358, "Clicking Bill in TipSplit"},
		{0.344, 0.464, "Clicking People: 2 in TipSplit"},
		{0.7, 0.7, "Clicking in TipSplit"}, // a control with only a value has no name
		{0.9, 0.9, "Clicking in TipSplit"},
	}
	for _, c := range cases {
		b := newRun(t, "r1").live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
			uiRead(80, "TipSplit", stepper...).click(90, c.x, c.y)
		if got := b.derive().Now; got != c.want {
			t.Errorf("a click at (%v, %v): %q, want %q", c.x, c.y, got, c.want)
		}
	}
}

func TestAFrameTakenAfterTheNextInputIsNotTheChecksPicture(t *testing.T) {
	s := newRun(t, "r1").live(machine.Ready).task(tipTask, 60).
		uiRead(100, "TipSplit"). // step 1, the evidence
		click(101, 0.5, 0.5).    // step 2
		frame(102, 2).           // the first frame at or after step 1 shows the screen after the click
		verdict("fail", 266, fail("b", "Each pays becomes $50.00", "Each pays reads $10.00", 1)).derive()
	if s.Failing == nil || s.Failing.Picture != nil || s.Failing.Step != 1 {
		t.Errorf("failing = %+v, want step 1 and no picture", s.Failing)
	}
}
