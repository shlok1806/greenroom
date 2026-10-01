package verifier

import (
	"slices"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Issue #263: earlier Board states are gone by verdict time, but the recorded
// UI/effect reads still prove their text/count/membership value checks.
func TestBoardIntermediateValuesUseTheirOwnUIEvidence(t *testing.T) {
	checks, _, problem := parseDeclaredChecks(`{"checks":[
 {"id":"write-doing","criterion":"Write spec belongs to Doing; headers read To Do (2), Doing (1), Done (0)","kinds":["value"]},
 {"id":"write-done","criterion":"Write spec belongs to Done; headers read Doing (0), Done (1)","kinds":["value"]},
 {"id":"build-doing","criterion":"Build UI belongs to Doing; headers read To Do (1), Doing (1)","kinds":["value"]},
 {"id":"headers","criterion":"Headers read To Do (1), Doing (1), Done (1)","kinds":["value"]}]}`)
	if problem != "" {
		t.Fatal(problem)
	}
	for _, c := range checks {
		if !slices.Equal(c.Kinds, []string{session.CheckValue}) {
			t.Fatalf("%s kinds = %v", c.ID, c.Kinds)
		}
	}
	tree := func(text ...string) machine.UITree {
		var elements []machine.UIElement
		for i, v := range text {
			elements = append(elements, machine.UIElement{ID: i + 1, Role: "StaticText", Value: v})
		}
		return machine.UITree{App: "Board", Elements: elements}
	}
	steps := []machine.Step{
		{Seq: 28, Tool: "machine_input", By: machine.HolderVerifier},
		{Seq: 29, Tool: "machine_ui", By: machine.HolderVerifier, Output: tree("To Do (2)", "Doing (1)", "Done (0)", "Doing: Write spec"), Effect: &machine.StepEffect{Of: 28, Kind: machine.EffectChanged}},
		{Seq: 31, Tool: "machine_input", By: machine.HolderVerifier},
		{Seq: 32, Tool: "machine_ui", By: machine.HolderVerifier, Output: tree("To Do (2)", "Doing (0)", "Done (1)", "Done: Write spec"), Effect: &machine.StepEffect{Of: 31, Kind: machine.EffectChanged}},
		{Seq: 34, Tool: "machine_input", By: machine.HolderVerifier},
		{Seq: 35, Tool: "machine_ui", By: machine.HolderVerifier, Output: tree("To Do (1)", "Doing (1)", "Done (1)", "Doing: Build UI"), Effect: &machine.StepEffect{Of: 34, Kind: machine.EffectChanged}},
		{Seq: 36, Tool: "machine_input", By: machine.HolderVerifier},
		{Seq: 37, Tool: "machine_ui", By: machine.HolderVerifier, Output: tree("To Do (1)", "Doing (1)", "Done (0)"), Effect: &machine.StepEffect{Of: 36, Kind: machine.EffectChanged}},
	}
	call := reviewArgs("pass",
		said(answer("write-doing", "pass", []int{29}, 28), "Doing: Write spec; To Do (2), Doing (1), Done (0)."),
		said(answer("write-done", "pass", []int{32}, 31), "Done: Write spec; Doing (0), Done (1)."),
		said(answer("build-doing", "pass", []int{35}, 34), "Doing: Build UI; To Do (1), Doing (1)."),
		said(answer("headers", "pass", []int{35}, 34), "To Do (1), Doing (1), Done (1)."))
	assertRule := func(name string, declared []session.Check, evidence []machine.Step, handover int, want string) {
		t.Helper()
		r := reviewVerdict(call, kindTranscript(declared...), evidence, handover)
		problems := strings.Join(r.problems, "\n")
		if want == "" {
			if problems != "" {
				t.Fatalf("%s refused: %s", name, problems)
			}
		} else if !strings.Contains(problems, want) {
			t.Fatalf("%s problems = %q, want %q", name, problems, want)
		}
	}
	assertRule("recorded intermediate values", checks, steps, 0, "")
	visual := slices.Clone(checks)
	visual[0].Kinds = []string{session.CheckVisual}
	assertRule("explicit visual stays visual", visual, steps, 0, "(visual)")
	assertRule("handover freshness", checks, steps, 30, "(freshness)")
	blank := slices.Clone(steps)
	badTree := tree("To Do (2)", "Doing (1)", "Done (0)", "Doing: Write spec")
	badTree.Elements[1].Rendered = machine.RenderedBlank
	blank[1].Output = badTree
	assertRule("not drawn count cannot pass", checks, blank, 0, "(rendered)")
	lost := slices.Clone(steps)
	lost[1].Effect = &machine.StepEffect{Of: 28, Kind: machine.EffectNone}
	assertRule("lost input cannot pass on its effect", checks, lost, 0, "(effect)")
}
