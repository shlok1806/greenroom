package verifier

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

func TestScreenshotClaimText(t *testing.T) {
	for _, tt := range []struct {
		observed string
		want     []string
	}{
		{`The window shows "Run B" and “Ready”. Total is $49.56 at step 108.`, []string{"$49.56", "ready", "run b"}},
		{"The label reads `Done` and ‘Saved’.", []string{"done", "saved"}},
		{`No "Error" is visible. The text reads "Done".`, []string{"done"}},
		{`It does not show "Failed"; the label reads "Done"`, []string{"done"}},
		{`The label reads "Not found".`, []string{"not found"}},
		{`It doesn't contain "Error"`, nil},
		{`A green button appears at step 108.`, nil},
		{`No error, and the label reads "Run B".`, []string{"run b"}},
		{`It reads "Save and Close".`, []string{"save and close"}},
	} {
		t.Run(tt.observed, func(t *testing.T) {
			if got := screenshotClaimText(tt.observed); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScreenshotSupportNamesWrongStateAndPreservesRenderedGuard(t *testing.T) {
	c := session.Check{Status: session.CheckPass, Kinds: []string{session.CheckVisual}, Observed: `It reads "Run B" and $49.56`, Evidence: []int{2}}
	shot := stepFact{tool: "machine_screenshot", by: machine.HolderVerifier, description: &machine.ScreenshotDescription{Text: "Run A. Total $149.56."}}
	rules := screenshotSupportRule(c, []int{2}, map[int]stepFact{2: shot}, 0, 0)
	if len(rules) != 2 || !strings.Contains(strings.Join(rules, " "), `"run b"`) {
		t.Fatalf("rules %v", rules)
	}
	shot.description.Text = "Run B is not visible. Total $49.56."
	if got := screenshotSupportRule(c, []int{2}, map[int]stepFact{2: shot}, 0, 0); len(got) == 0 {
		t.Fatal("negated mention supported pass")
	}
	shot.description.Text = "Run B. Total $49.56."
	if got := screenshotSupportRule(c, []int{2}, map[int]stepFact{2: shot}, 0, 0); len(got) != 0 {
		t.Fatalf("supported text: %v", got)
	}
	// A matching description may quote text from behind another window: it must not waive the
	// authoritative rendered contradiction (P4 would have waived this on lexical matches).
	steps := map[int]stepFact{1: {tool: "machine_ui", by: machine.HolderVerifier, elements: []machine.UIElement{{Value: "Run B", Rendered: machine.RenderedCovered}}}, 2: shot}
	if got := checkEvidence(c, steps, 0); !strings.Contains(strings.Join(got, " "), "covered") {
		t.Fatalf("covered text allowed: %v", got)
	}
}

func TestUndescribedScreenshotNeverSupportsNewVisualPass(t *testing.T) {
	c := session.Check{Status: session.CheckPass, Kinds: []string{session.CheckVisual}, Evidence: []int{2}}
	for _, d := range []*machine.ScreenshotDescription{nil, {}, {Error: "description cut off"}} {
		steps := map[int]stepFact{2: {tool: "machine_screenshot", by: machine.HolderVerifier, description: d}}
		if got := checkEvidence(c, steps, 0); !strings.Contains(strings.Join(got, " "), "screenshot support") {
			t.Fatalf("undescribed pass allowed: %v", got)
		}
		c.Status = session.CheckFail
		if got := checkEvidence(c, steps, 0); len(got) != 0 {
			t.Fatalf("grounded absence fail blocked: %v", got)
		}
		c.Status = session.CheckPass
	}
}

func TestIssue193WrongScreenshotCannotPassAndItsDescriptionSurvivesReload(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	seq := lastStep(t, mgr, runID) + 1
	result := answer("state", "pass", []int{seq})
	result["observed"] = `The window reads "Run B".`
	model := &scriptedModel{vision: "The frontmost window is Run A. It shows an empty transcript.", replies: []string{
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{{"id": "state", "criterion": "The selected run is visible", "kinds": []string{"visual"}}}}),
		toolCall("machine_screenshot", map[string]any{}),
		verdictOf("pass", "Run B is selected.", result),
		verdictOf("inconclusive", "The screenshot shows another run.", result),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the selected run.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 3 || !strings.Contains(progress[2].Text, `do not affirm the claimed text "run b"`) {
		t.Fatalf("progress %+v", progress)
	}
	got := store.Verdict()
	if got.Verdict != "inconclusive" || got.Checks[0].Status != session.CheckUnchecked {
		t.Fatalf("wrong-state pass: %+v", got)
	}
	steps, err := machine.ReadSteps(mgr.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Seq == seq {
			if s.ScreenshotDescription == nil || s.ScreenshotDescription.Text != model.vision {
				t.Fatalf("recorded description %+v", s)
			}
			// Different display/progress wording cannot manufacture support in the persisted ledger.
			rules := checkEvidence(session.Check{Status: session.CheckPass, Kinds: []string{session.CheckVisual}, Observed: `It reads "Run B"`, Evidence: []int{seq}}, ledger(steps), 0)
			if len(rules) == 0 {
				t.Fatal("reloaded description lost the mismatch")
			}
			return
		}
	}
	t.Fatal("screenshot not found")
}

func TestScreenshotDescriptionErrorsAreRecorded(t *testing.T) {
	mgr, runID, control := ready(t)
	writeShot(t, control)
	_, shot, err := mgr.ScreenshotAs(context.Background(), runID, machine.HolderVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.RecordScreenshotDescription(runID, shot.Step, "partial fragment", errors.New("description cut off")); err != nil {
		t.Fatal(err)
	}
	steps, err := machine.ReadSteps(mgr.RunDir(runID))
	if err != nil {
		t.Fatal(err)
	}
	d := ledger(steps)[shot.Step].description
	if d == nil || d.Text != "" || d.Error != "description cut off" {
		t.Fatalf("description %+v", d)
	}
}

func TestScreenshotSupportAllowsDifferentTransientStatesAcrossCaptures(t *testing.T) {
	c := session.Check{Status: session.CheckPass, Kinds: []string{session.CheckVisual}, Observed: `"Run A" appears before switching, then "Run B" appears.`, Evidence: []int{2, 4}, Actions: []int{1, 3}}
	steps := map[int]stepFact{
		1: {tool: "machine_input", by: machine.HolderVerifier},
		3: {tool: "machine_input", by: machine.HolderVerifier},
		2: {tool: "machine_screenshot", by: machine.HolderVerifier, description: &machine.ScreenshotDescription{Text: "Run A is selected."}},
		4: {tool: "machine_screenshot", by: machine.HolderVerifier, description: &machine.ScreenshotDescription{Text: "Run B is selected."}},
	}
	if got := checkEvidence(c, steps, 0); len(got) != 0 {
		t.Fatalf("valid transient comparison refused: %v", got)
	}
}

func TestScreenshotSupportKeepsPositiveClaimsBesideNegations(t *testing.T) {
	c := session.Check{Status: session.CheckPass, Kinds: []string{session.CheckVisual}, Observed: `No error, and the label reads "Run B".`, Evidence: []int{2}}
	steps := map[int]stepFact{2: {tool: "machine_screenshot", by: machine.HolderVerifier, description: &machine.ScreenshotDescription{Text: "No errors, and Run A is selected."}}}
	if got := checkEvidence(c, steps, 0); len(got) == 0 {
		t.Fatal("positive Run B claim escaped the support guard")
	}
	steps[2] = stepFact{tool: "machine_screenshot", by: machine.HolderVerifier, description: &machine.ScreenshotDescription{Text: "No errors, and Run B is selected."}}
	if got := checkEvidence(c, steps, 0); len(got) != 0 {
		t.Fatalf("positive support beside negation lost: %v", got)
	}
	c.Criterion, c.Observed = `The label "Run C" is visible.`, "Looks right."
	if got := checkEvidence(c, steps, 0); len(got) == 0 {
		t.Fatal("vague observation bypassed named criterion")
	}
}

func TestScreenshotSupportComparesWholeSignedNumericTokens(t *testing.T) {
	for _, bad := range []string{"49.56.0", "149.56", "-$49.56", "$-49.56", "€49.56"} {
		if screenshotAffirmsText("Total is "+bad, "$49.56") {
			t.Fatalf("%s supported $49.56", bad)
		}
	}
	for _, bad := range []string{"49.56.0", "149.56", "-49.56", "-$49.56"} {
		if screenshotAffirmsText("Total is "+bad, "49.56") {
			t.Fatalf("%s supported 49.56", bad)
		}
	}
	if !screenshotAffirmsText("Total is $49.56", "$49.56") || !screenshotAffirmsText("Total is $49.56", "49.56") {
		t.Fatal("exact positive amount lost")
	}
}

func TestScreenshotToolsRecordDescriptionFailureAndSanitizedText(t *testing.T) {
	for _, args := range []string{`{}`, `{"ref":"e41"}`} {
		for _, vision := range []string{"<unk><unk>", "Run B is frontmost. Its button is at (50, 40)."} {
			t.Run(args+vision, func(t *testing.T) {
				mgr, runID, control := readyToolkit(t)
				writeShot(t, control)
				model := &scriptedModel{vision: vision}
				v := newVerifier(t, mgr, model.start(t))
				result, seq := v.machineTool(context.Background(), runID, nim.ToolCall{Name: "machine_screenshot", Arguments: args})
				steps, err := mgr.Steps(runID)
				if err != nil {
					t.Fatal(err)
				}
				d := ledger(steps)[seq].description
				if d == nil {
					t.Fatalf("no recorded description: %s", result)
				}
				if strings.Contains(vision, "<unk>") {
					if d.Error == "" || d.Text != "" || !strings.Contains(result, "could not be described") {
						t.Fatalf("failure lost: %+v / %s", d, result)
					}
				} else if d.Error != "" || !strings.Contains(d.Text, "position unknown") || strings.Contains(d.Text, "(50, 40)") {
					t.Fatalf("unsanitized description %+v", d)
				}
			})
		}
	}
}
