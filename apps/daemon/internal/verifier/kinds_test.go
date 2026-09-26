package verifier

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// ADR 0027 point 2: the daemon adds every kind the criterion's words claim to the declared ones,
// ignoring case and matching whole words, and never takes one away. "within N s" sets the window,
// the stricter of it and a declared one, never under 2 s.
func TestKindsAreAddedFromTheCriterionsWords(t *testing.T) {
	value, visual, timing, both := []string{session.CheckValue}, []string{session.CheckVisual},
		[]string{session.CheckTiming}, []string{session.CheckVisual, session.CheckTiming}
	for _, c := range []struct {
		criterion string
		declared  []string
		within    float64
		kinds     []string
		window    float64
		note      string // a fragment of the note, "" for none
	}{
		{"Each pays shows $48.00", nil, 0, value, 0, ""},
		{"Each pays is Visible in large text", nil, 0, visual, 0, `its criterion says "visible"`},
		{"The total is displayed under Tip", nil, 0, visual, 0, `"displayed"`},
		{"The title is readable", nil, 0, visual, 0, "readable"},
		{"I can see the result", nil, 0, visual, 0, `"see"`},
		{"The error colour is red", nil, 0, visual, 0, "colour"},
		{"Milk appears at once after Add", nil, 0, timing, 2, `"at once"`},
		{"The result follows each edit Immediately", nil, 0, timing, 2, "immediately"},
		{"The result changes as you type", nil, 0, timing, 2, "as you type"},
		{"The list updates within 5 seconds", nil, 0, timing, 5, "within 5 seconds"},
		{"The list updates within 3 s", timing, 10, timing, 3, ""},
		{"The list updates within a second", nil, 0, timing, 2, "not 1 s"},
		{"The list updates", timing, 0.5, timing, 2, "not 0.5 s"},
		{"The list updates", nil, 4, timing, 4, ""},
		{"The list updates", visual, 0, visual, 0, ""},
		// Whole words only: "seed", "boldly" and "enlarged" claim nothing.
		{"The seed count is 3", nil, 0, value, 0, ""},
		{"Oncetotal reads 4 and it is enlarged", nil, 0, value, 0, ""},
		// Both claims, both kinds: nothing a criterion says is dropped.
		{"Each pays is shown at once", nil, 0, both, 2, `"shown"`},
		{"Each pays updates right away", visual, 0, both, 2, `"right away"`},
		{"Each pays is visible", timing, 3, both, 3, `"visible"`},
		{"The total is bold within 4 s", nil, 0, both, 4, "within 4 s"},
		{"The list updates", both, 0, both, 2, ""},
	} {
		kinds, window, note := applyKinds("c", c.criterion, c.declared, c.within)
		if !slices.Equal(kinds, c.kinds) || window != c.window {
			t.Errorf("applyKinds(%q, %q, %g) = %q within %g, want %q within %g", c.criterion, c.declared, c.within,
				kinds, window, c.kinds, c.window)
		}
		if (c.note == "") != (note == "") || !strings.Contains(note, c.note) {
			t.Errorf("applyKinds(%q, %q, %g) note = %q, want one with %q", c.criterion, c.declared, c.within, note, c.note)
		}
	}
}

// The declaration posts the applied kinds, and its result tells the model what each kind needs
// and why a kind or window changed. A single "kind" is read too.
func TestTheDeclarationCarriesTheAppliedKinds(t *testing.T) {
	checks, notes, problem := parseDeclaredChecks(`{"checks":[
		{"id":"shown","criterion":"Each pays is visible under Tip"},
		{"id":"fast","criterion":"Milk appears at once","kinds":["value"]},
		{"id":"slow","criterion":"The list saves","kind":"timing","within":1},
		{"id":"total","criterion":"Each pays reads $49.56"},
		{"id":"both","criterion":"Each pays is shown at once","kinds":["timing"]}]}`)
	if problem != "" {
		t.Fatal(problem)
	}
	want := []session.Check{
		{ID: "shown", Criterion: "Each pays is visible under Tip", Kinds: []string{session.CheckVisual}},
		{ID: "fast", Criterion: "Milk appears at once", Kinds: []string{session.CheckTiming}, Within: 2},
		{ID: "slow", Criterion: "The list saves", Kinds: []string{session.CheckTiming}, Within: 2},
		{ID: "total", Criterion: "Each pays reads $49.56", Kinds: []string{session.CheckValue}},
		{ID: "both", Criterion: "Each pays is shown at once", Kinds: []string{session.CheckVisual, session.CheckTiming}, Within: 2},
	}
	for i, w := range want {
		if c := checks[i]; c.ID != w.ID || c.Criterion != w.Criterion || !slices.Equal(c.Kinds, w.Kinds) || c.Within != w.Within {
			t.Errorf("check %d = %+v, want %+v", i, checks[i], w)
		}
	}
	result := declaredResult(checks, notes)
	for _, frag := range []string{"shown (visual: cite a machine_screenshot", "fast (timing within 2 s",
		"total (value)", `"fast" is timing: its criterion says "at once"`, `"slow" is timed within 2 s, not 1 s`,
		"both (visual: cite a machine_screenshot taken after its actions; timing within 2 s", `"both" is visual: its criterion says "shown"`} {
		if !strings.Contains(result, frag) {
			t.Errorf("declaration result = %q, want %q", result, frag)
		}
	}
}

// kindSteps is a timed run for the ADR 0027 rules. Times are seconds from t0:
//
//	3 look (0), 4 screenshot (5), 5 click (10 to 10.2), 6 its effect read, changed (10.4),
//	7 look (19), 8 screenshot (20), 9 click (30 to 30.2), 10 its effect read, no change (30.5),
//	11 look (31.5), 12 look marking Each pays not drawn (40), 13 screenshot (41), 14 look (50),
//	15 click (60 to 60.2), 16 its effect read, changed (60.4), 17 screenshot (61.5), 18 screenshot (70),
//	19 look (71).
func kindSteps() []machine.Step {
	v := machine.HolderVerifier
	t0 := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	marked := machine.UITree{App: "TipSplit", Elements: []machine.UIElement{
		{ID: 1, Role: "StaticText", Value: "Tip: $8.40"},
		{ID: 2, Role: "StaticText", Value: "Each pays: $49.56", Identifier: "perPerson", Rendered: machine.RenderedBlank},
	}}
	return []machine.Step{
		{Seq: 3, Tool: "machine_ui", By: v, At: at(0), DurationMS: 500},
		{Seq: 4, Tool: "machine_screenshot", By: v, At: at(5), DurationMS: 300},
		{Seq: 5, Tool: "machine_input", By: v, At: at(10), DurationMS: 200},
		{Seq: 6, Tool: "machine_ui", By: v, At: at(10.4), DurationMS: 800, Effect: &machine.StepEffect{Of: 5, Kind: machine.EffectChanged}},
		{Seq: 7, Tool: "machine_ui", By: v, At: at(19), DurationMS: 500},
		{Seq: 8, Tool: "machine_screenshot", By: v, At: at(20), DurationMS: 300},
		{Seq: 9, Tool: "machine_input", By: v, At: at(30), DurationMS: 200},
		{Seq: 10, Tool: "machine_ui", By: v, At: at(30.5), DurationMS: 500, Effect: &machine.StepEffect{Of: 9, Kind: machine.EffectNone}},
		{Seq: 11, Tool: "machine_ui", By: v, At: at(31.5), DurationMS: 500},
		{Seq: 12, Tool: "machine_ui", By: v, At: at(40), DurationMS: 500, Output: marked},
		{Seq: 13, Tool: "machine_screenshot", By: v, At: at(41), DurationMS: 300},
		{Seq: 14, Tool: "machine_ui", By: v, At: at(50), DurationMS: 500, Output: machine.UITree{App: "TipSplit"}},
		{Seq: 15, Tool: "machine_input", By: v, At: at(60), DurationMS: 200},
		{Seq: 16, Tool: "machine_ui", By: v, At: at(60.4), DurationMS: 500, Effect: &machine.StepEffect{Of: 15, Kind: machine.EffectChanged}},
		{Seq: 17, Tool: "machine_screenshot", By: v, At: at(61.5), DurationMS: 300},
		{Seq: 18, Tool: "machine_screenshot", By: v, At: at(70), DurationMS: 300},
		{Seq: 19, Tool: "machine_ui", By: v, At: at(71), DurationMS: 500},
	}
}

// kindTranscript is a task with checks declared as given (their kinds already applied).
func kindTranscript(checks ...session.Check) []session.Message {
	return []session.Message{
		{From: session.Coder, Kind: session.Task, Text: "Check it."},
		{From: session.Verifier, Kind: session.Progress, Text: "declare_checks {}\nDeclared.", Checks: checks},
	}
}

func said(a map[string]any, observed string) map[string]any {
	a["observed"] = observed
	return a
}

// ADR 0027 points 1 and 4: a visual check needs a screenshot after its actions; a timing check
// needs an observation that started within its window after its last action, and a pass may not
// rest on a later one; no check passes on an element the latest read marks not drawn. Each
// refusal names the rule and the check.
func TestReviewAppliesTheKindRules(t *testing.T) {
	visual := session.Check{ID: "shown", Criterion: "Each pays is visible in large text", Kinds: []string{session.CheckVisual}}
	timing := session.Check{ID: "fast", Criterion: "The total changes at once", Kinds: []string{session.CheckTiming}, Within: 2}
	slow := session.Check{ID: "fast", Criterion: "The total changes within 10 s", Kinds: []string{session.CheckTiming}, Within: 10}
	value := session.Check{ID: "total", Criterion: "Each pays reads $49.56", Kinds: []string{session.CheckValue}}
	tip := session.Check{ID: "tip", Criterion: "Tip reads $8.40", Kinds: []string{session.CheckValue}}
	both := session.Check{ID: "both", Criterion: "The total is shown at once",
		Kinds: []string{session.CheckVisual, session.CheckTiming}, Within: 2}
	old := session.Check{ID: "total", Criterion: "Each pays reads $49.56"} // declared before ADR 0027: value
	cases := []struct {
		name  string
		check session.Check
		call  verdictCall
		want  string // "" for a verdict that holds
	}{
		{"visual on the tree alone", visual, reviewArgs("pass", answer("shown", "pass", []int{6}, 5)),
			`check "shown" (visual): it is a visual check, and no machine_screenshot after action step 5 is among its evidence`},
		{"visual with a screenshot before the action", visual, reviewArgs("pass", answer("shown", "pass", []int{4, 6}, 5)),
			`check "shown" (visual)`},
		{"visual fail on the tree alone", visual, reviewArgs("fail", answer("shown", "fail", []int{6}, 5)), `check "shown" (visual)`},
		{"visual with a screenshot", visual, reviewArgs("pass", answer("shown", "pass", []int{6, 8}, 5)), ""},
		{"visual that only looks", visual, reviewArgs("pass", answer("shown", "pass", []int{4})), ""},
		{"timing seen late", timing, reviewArgs("pass", answer("fast", "pass", []int{7}, 5)),
			`check "fast" (timing): evidence step 7 started 8.8 s after action step 5 ended, later than its 2 s`},
		{"timing seen in time and late", timing, reviewArgs("pass", answer("fast", "pass", []int{6, 7}, 5)),
			`a later observation cannot pass a timing check`},
		{"timing seen in time", timing, reviewArgs("pass", answer("fast", "pass", []int{6}, 5)), ""},
		{"timing fail seen in time", timing, reviewArgs("fail", answer("fast", "fail", []int{6}, 5)), ""},
		{"timing fail seen in time and late", timing, reviewArgs("fail", answer("fast", "fail", []int{6, 7}, 5)), ""},
		{"timing fail seen only late", timing, reviewArgs("fail", answer("fast", "fail", []int{7}, 5)),
			`check "fast" (timing): no evidence step started within 2 s after action step 5 ended (the UI read right after it is step 6)`},
		{"timing with no action", timing, reviewArgs("pass", answer("fast", "pass", []int{6})),
			`check "fast" (timing): it is a timing check (within 2 s) and cites no action`},
		{"a longer window", slow, reviewArgs("pass", answer("fast", "pass", []int{7}, 5)), ""},
		{"timing on a read that found no change", timing, reviewArgs("pass", answer("fast", "pass", []int{10}, 9)),
			`check "fast" (timing): no evidence step started within 2 s after action step 9 ended after its effect read at step 10 found no change`},
		{"timing on a look after that read", timing, reviewArgs("pass", answer("fast", "pass", []int{11}, 9)), ""},
		{"a value on text not drawn", value, reviewArgs("pass", answer("total", "pass", []int{12})),
			`check "total" (rendered): machine_ui step 12 marks [2] StaticText "Each pays: $49.56" not drawn`},
		{"a check declared before kinds", old, reviewArgs("pass", answer("total", "pass", []int{12})), `check "total" (rendered)`},
		{"a fail on text not drawn", value, reviewArgs("fail", answer("total", "fail", []int{12})), ""},
		{"a value on other text", tip, reviewArgs("pass", answer("tip", "pass", []int{12})), ""},
		{"a visual pass naming it in observed", visual,
			reviewArgs("pass", said(answer("shown", "pass", []int{13}), "Each pays shows $49.56 in large text.")),
			`check "shown" (rendered): machine_ui step 12 marks [2]`},
		{"a newer read that draws it", value, reviewArgs("pass", answer("total", "pass", []int{14})), ""},
		// A check that is visual and timing needs a screenshot after its action and an observation in time.
		{"both, on the effect read alone", both, reviewArgs("pass", answer("both", "pass", []int{16}, 15)),
			`check "both" (visual): it is a visual check, and no machine_screenshot after action step 15`},
		{"both, on a late screenshot alone", both, reviewArgs("pass", answer("both", "pass", []int{18}, 15)),
			`check "both" (timing): no evidence step started within 2 s after action step 15 ended`},
		{"both, on a screenshot in time", both, reviewArgs("pass", answer("both", "pass", []int{17}, 15)), ""},
		{"both, on the effect read and a late screenshot", both, reviewArgs("pass", answer("both", "pass", []int{16, 18}, 15)), ""},
		{"both, on a late look", both, reviewArgs("pass", answer("both", "pass", []int{16, 18, 19}, 15)),
			`check "both" (timing): evidence step 19 started`},
		{"both, a fail on the effect read alone", both, reviewArgs("fail", answer("both", "fail", []int{16}, 15)),
			`check "both" (visual)`},
		{"both, a fail seen in time and on screen", both, reviewArgs("fail", answer("both", "fail", []int{16, 18}, 15)), ""},
		{"visual only, a late screenshot", visual, reviewArgs("pass", answer("shown", "pass", []int{18}, 15)), ""},
		{"timing only, a late screenshot", timing, reviewArgs("pass", answer("fast", "pass", []int{16, 18}, 15)),
			`check "fast" (timing): evidence step 18 started`},
	}
	for _, c := range cases {
		r := reviewVerdict(c.call, kindTranscript(c.check), kindSteps(), 0)
		joined := strings.Join(r.problems, "\n")
		if c.want == "" {
			if len(r.problems) > 0 {
				t.Errorf("%s: refused: %s", c.name, joined)
			}
			continue
		}
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems = %q, want %q", c.name, joined, c.want)
		}
	}
	// The posted verdict repeats each check's kind and window.
	r := reviewVerdict(reviewArgs("pass", answer("fast", "pass", []int{6}, 5)), kindTranscript(timing), kindSteps(), 0)
	if len(r.msg.Checks) != 1 || !slices.Equal(r.msg.Checks[0].Kinds, []string{session.CheckTiming}) || r.msg.Checks[0].Within != 2 {
		t.Errorf("posted checks = %+v, want the timing kind and its window", r.msg.Checks)
	}
}

// The rules read the step log as it is on disk too: a UI read's marks survive JSON.
func TestTheDrawnRuleReadsTheLogAsWritten(t *testing.T) {
	var steps []machine.Step
	for _, s := range kindSteps() {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var back machine.Step
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, back)
	}
	value := session.Check{ID: "total", Criterion: "Each pays reads $49.56", Kinds: []string{session.CheckValue}}
	r := reviewVerdict(reviewArgs("pass", answer("total", "pass", []int{12})), kindTranscript(value), steps, 0)
	if !strings.Contains(strings.Join(r.problems, "\n"), `check "total" (rendered)`) {
		t.Errorf("problems = %q, want the rendered rule", r.problems)
	}
}

func TestMentionsMatchesTextAndNumbers(t *testing.T) {
	e := machine.UIElement{Role: "StaticText", Value: "Each pays: $49.56"}
	for claim, want := range map[string]bool{
		"Each pays: $49.56 is shown":   true,
		"Each pays reads $49.56.":      true,
		"each pays: $49.56":            true,
		"The total is $149.56":         false,
		"Each pays is shown in bold":   false,
		"Tip reads $8.40":              false,
		"Each pays reads 49.56 dollar": true,
	} {
		if got := mentions(claim, e); got != want {
			t.Errorf("mentions(%q) = %v, want %v", claim, got, want)
		}
	}
	milk := machine.UIElement{Role: "CheckBox", Title: "Milk"}
	if !mentions("Milk and Bread are in the list", milk) || mentions("Milkshake is in the list", milk) {
		t.Error("a title must match as a whole word")
	}
}

// unitConvertSteps is the read of unitconvert-result-invisible from the first simple-tier run on
// the ADR 0027 verifier (step 6): the Value field shows 10 and is drawn; the result "10 km = 6.21
// mi" is in the tree but not drawn. Step 7 is the screenshot that shows no result.
func unitConvertSteps() []machine.Step {
	v := machine.HolderVerifier
	t0 := time.Date(2026, 9, 26, 5, 36, 0, 0, time.UTC)
	read := machine.UITree{App: "UnitConvert", Apps: []string{"Finder", "UnitConvert"}, Elements: []machine.UIElement{
		{ID: 1, Role: "Window", Subrole: "StandardWindow", Title: "UnitConvert"},
		{ID: 2, Role: "StaticText", Value: "UnitConvert"},
		{ID: 4, Role: "RadioButton", Subrole: "Segment", Label: "Length", Selected: true},
		{ID: 7, Role: "StaticText", Value: "Value"},
		{ID: 8, Role: "TextField", Label: "Value", Value: "10", Identifier: "value"},
		{ID: 9, Role: "StaticText", Value: "km"},
		{ID: 11, Role: "StaticText", Value: "Decimals: 2"},
		{ID: 15, Role: "StaticText", Value: "10 km = 6.21 mi", Identifier: "result", Rendered: machine.RenderedBlank},
	}}
	split := machine.UITree{App: "TipSplit", Elements: []machine.UIElement{
		{ID: 1, Role: "StaticText", Value: "Each pays"},
		{ID: 2, Role: "StaticText", Value: "$49.56", Rendered: machine.RenderedBlank},
	}}
	return []machine.Step{
		{Seq: 6, Tool: "machine_ui", By: v, At: t0, DurationMS: 500, Output: read},
		{Seq: 7, Tool: "machine_screenshot", By: v, At: t0.Add(5 * time.Second), DurationMS: 300},
		{Seq: 8, Tool: "machine_ui", By: v, At: t0.Add(9 * time.Second), DurationMS: 500, Output: split},
	}
}

// unitconvert-result-invisible: the verifier's fail was refused because the input check's claim
// "shows 10" was tied to the blank result through the number 10, though it rests on the drawn
// Value field. A pass on the input check holds; a pass on the result check is still refused.
func TestTheDrawnRuleFiresOnlyOnWhatTheCheckRestsOn(t *testing.T) {
	input := session.Check{ID: "input-value", Criterion: "Input field shows 10", Kinds: []string{session.CheckValue}}
	result := session.Check{ID: "result-text", Criterion: `Result shows "10 km = 6.21 mi" under the divider`,
		Kinds: []string{session.CheckValue}}
	each := session.Check{ID: "total", Criterion: "Each pays reads $49.56", Kinds: []string{session.CheckValue}}
	cases := []struct {
		name  string
		check session.Check
		call  verdictCall
		want  string // "" for a verdict that holds
	}{
		{"the input, as the run answered it", input,
			reviewArgs("pass", said(answer("input-value", "pass", []int{6}), `Value text field shows 10 (element 8 value="10")`)), ""},
		{"the input, naming its unit", input,
			reviewArgs("pass", said(answer("input-value", "pass", []int{6}), "The Value field shows 10 km.")), ""},
		{"the result", result, reviewArgs("pass", answer("result-text", "pass", []int{6, 7})),
			`check "result-text" (rendered): machine_ui step 6 marks [15] StaticText "10 km = 6.21 mi" not drawn`},
		{"the result by its numbers", input,
			reviewArgs("pass", said(answer("input-value", "pass", []int{6}), "It converts to 6.21 mi.")),
			`check "input-value" (rendered): machine_ui step 6 marks [15]`},
		// A drawn label beside a blank value does not carry the value.
		{"a drawn label and a blank value", each, reviewArgs("pass", answer("total", "pass", []int{8})),
			`check "total" (rendered): machine_ui step 8 marks [2] StaticText "$49.56" not drawn`},
	}
	for _, c := range cases {
		r := reviewVerdict(c.call, kindTranscript(c.check), unitConvertSteps(), 0)
		joined := strings.Join(r.problems, "\n")
		if c.want == "" {
			if len(r.problems) > 0 {
				t.Errorf("%s: refused: %s", c.name, joined)
			}
			continue
		}
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems = %q, want %q", c.name, joined, c.want)
		}
	}
}

// A bare integer names an element only as its whole text or beside another of its words.
func TestMentionsNeedsMoreThanABareInteger(t *testing.T) {
	result := machine.UIElement{Role: "StaticText", Value: "10 km = 6.21 mi"}
	field := machine.UIElement{Role: "TextField", Value: "10"}
	words := machine.UIElement{Role: "StaticText", Value: "Words: 12"}
	for _, c := range []struct {
		claim string
		e     machine.UIElement
		want  bool
	}{
		{"Input field shows 10", result, false},
		{"The result reads 10 km", result, true},
		{"The result reads 6.21", result, true},
		{"Input field shows 10", field, true},
		{"Input field shows $10", machine.UIElement{Role: "TextField", Value: "$10"}, true},
		{"Words shows 12", words, true},
		{"There are 12 items", words, false},
	} {
		if got := mentions(c.claim, c.e); got != c.want {
			t.Errorf("mentions(%q, %q) = %v, want %v", c.claim, c.e.Value, got, c.want)
		}
	}
}
