package verifier

import (
	"context"
	"strings"
	"testing"
)

// Issue #248: a description's positions are fractions of the image or none (daemon ADR 0010).
func TestCheckPositionsKeepsFractionsAndRemovesEverythingElse(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		removed        int
	}{
		{"fractions stay", `25% segment at (0.60, 0.47), not selected`, `25% segment at (0.60, 0.47), not selected`, 0},
		{"edges stay", `corner at (0, 1) and (1.0, 0.0)`, `corner at (0, 1) and (1.0, 0.0)`, 0},
		{"a 0 to 1000 grid goes", `settings/filter icon at (180, 872), not selected`, `settings/filter icon at (position unknown), not selected`, 1},
		{"points go", `Apple menu at (22, 19)`, `Apple menu at (position unknown)`, 1},
		{"a mixed pair goes", `Tip field at (0.471, 811)`, `Tip field at (position unknown)`, 1},
		{"brackets and negatives go", `a at [240, 948], b at (-0.1, 0.5)`, `a at (position unknown), b at (position unknown)`, 2},
		{"quoted window text stays", `"Point (3, 4) is outside"`, `"Point (3, 4) is outside"`, 0},
		{"curly quoted text stays", "“Grid (12, 40)” then (12, 40)", "“Grid (12, 40)” then (position unknown)", 1},
		{"no pairs", "none", "none", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, removed := checkPositions(tc.in)
			first, _, _ := strings.Cut(got, "\n")
			if first != tc.want || removed != tc.removed {
				t.Errorf("checkPositions(%q) = %q, %d; want %q, %d", tc.in, first, removed, tc.want, tc.removed)
			}
			if said := strings.Contains(got, "take positions from machine_ui"); said != (tc.removed > 0) {
				t.Errorf("checkPositions(%q) = %q: says why positions went %v, want %v", tc.in, got, said, tc.removed > 0)
			}
		})
	}
}

// Issue #248 end to end: the describer's answer from run 20260928-144042 reaches the reasoning
// model with its out-of-screen positions removed and a line saying so; its text is untouched.
func TestADescriptionReachesTheModelWithOnlyFractions(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	model := &scriptedModel{
		vision: "1. System dialog: none\n2. Frontmost: Photos\n3. Window text:\nLibrary\n4. Controls:\n" +
			"settings/filter icon at (180, 872), not selected\ndock icon 1 at (240, 948), not selected\n" +
			"Library tab at (0.12, 0.08), selected\n5. Errors: none",
		replies: []string{
			toolCall("machine_screenshot", map[string]any{}),
			toolCall("report_verdict", map[string]any{"verdict": "inconclusive", "summary": "done"}),
		},
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Look at the screen.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	last := model.request(t, model.calls())
	for _, gone := range []string{"(180, 872)", "(240, 948)"} {
		if strings.Contains(last, gone) {
			t.Errorf("the reasoning model saw %s, a position outside the image", gone)
		}
	}
	for _, want := range []string{"settings/filter icon at (position unknown)", "Library tab at (0.12, 0.08)",
		"2 positions were not fractions of the image", "take positions from machine_ui"} {
		if !strings.Contains(last, want) {
			t.Errorf("the reasoning model never saw %q", want)
		}
	}
}
