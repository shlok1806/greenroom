package verifier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// ADR 0028: a crash after an action is evidence. Replays wordcount-clear-crash from the first
// simple-tier run on the ADR 0027 verifier: pressing Clear crashes WordCount. The effect read
// used to say only that Finder was now frontmost, and the review refused a visual fail resting
// on that read (no screenshot), so the verifier relaunched the app until it ran out of steps.

// wordCountUI is WordCount with a sentence typed, before Clear.
const wordCountUI = `{"app":{"name":"WordCount","pid":9},"apps":["Finder","WordCount"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXTextField","label":"Text","value":"The quick brown fox","identifier":"text","depth":0,"frame":{"x":324,"y":283,"w":360,"h":56}},
{"role":"AXButton","label":"Clear","depth":0,"frame":{"x":525,"y":353,"w":56,"h":24}},
{"role":"AXStaticText","value":"Words: 4","identifier":"words","depth":0,"frame":{"x":324,"y":437,"w":68,"h":20}}]}`

// finderUI is what the frontmost read shows once WordCount is gone: Finder, and WordCount no
// longer among the running apps.
const finderUI = `{"app":{"name":"Finder","bundleId":"com.apple.finder","pid":386},"apps":["Finder"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXMenuBar","depth":0,"frame":{"x":0,"y":0,"w":1024,"h":24}},
{"role":"AXMenuBarItem","title":"Finder","depth":1,"frame":{"x":30,"y":0,"w":50,"h":24}}]}`

const wordCountCrash = `/Users/admin/Library/Logs/DiagnosticReports/WordCount-2026-09-26-013222.ips
  "exception" : {"codes":"0x0000000000000001, 0x00000001a2b3c4d5","rawCodes":[1,7022],"type":"EXC_BREAKPOINT","signal":"SIGTRAP"},`

func TestBenchCaseClearCrashIsAGroundedFail(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, wordCountUI)
	look := lastStep(t, mgr, runID) + 1
	click, read := look+1, look+2
	model := &scriptedModel{replies: []string{
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{
			{"id": "after-clear-words", "criterion": "After Clear, Words shows 0", "kinds": []string{"visual"}},
		}}),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 2}),
		verdictOf("fail", "WordCount crashed when Clear was pressed (step 3).",
			said(answer("after-clear-words", "fail", []int{read}, click), "WordCount quit when Clear was pressed.")),
	}}
	model.onReasoning = func(n int) {
		if n == 3 { // the click: the app is gone when its effect is read
			putUI(t, control, finderUI)
			if err := os.WriteFile(filepath.Join(control, "crash-report"), []byte(wordCountCrash), 0o644); err != nil {
				t.Error(err)
			}
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, `I added a "Longest word" line to WordCount (running on screen). Check it, including after pressing Clear (Words: 0).`)
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}

	if sys := deliveredText(t, model, 1, "system"); !strings.Contains(sys, "If the app crashes or quits while you do what the task describes") {
		t.Error("the prompt does not say a crash is a fail")
	}
	// The model is told the app quit, and why, in the click's result.
	after := deliveredText(t, model, 4, "tool")
	for _, want := range []string{
		"effect: WordCount is no longer running (it quit or crashed)",
		"/Users/admin/Library/Logs/DiagnosticReports/WordCount-2026-09-26-013222.ips",
		`"type":"EXC_BREAKPOINT","signal":"SIGTRAP"`,
	} {
		if !strings.Contains(after, want) {
			t.Errorf("the click's result = %q, want %q", after, want)
		}
	}
	// The step record says quit.
	steps, err := mgr.Steps(runID)
	if err != nil {
		t.Fatal(err)
	}
	var effect *machine.StepEffect
	for _, s := range steps {
		if s.Seq == read {
			effect = s.Effect
		}
	}
	if effect == nil || effect.Kind != machine.EffectQuit || effect.Of != click {
		t.Errorf("step %d effect = %+v, want quit of step %d", read, effect, click)
	}
	// And the fail resting on it is posted, though the check is visual.
	verdicts := messagesOfKind(store, session.Verdict)
	if len(verdicts) != 1 || verdicts[0].Verdict != "fail" || verdicts[0].Checks[0].Status != session.CheckFail {
		t.Fatalf("verdicts = %+v, want the fail resting on the quit", verdicts)
	}
}

// A quit is evidence for a fail of a check whose actions include the input that made the app
// quit, whatever its kinds; never for a pass.
func TestReviewTakesAQuitAsEvidenceForAFailOnly(t *testing.T) {
	v := machine.HolderVerifier
	t0 := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	steps := []machine.Step{
		{Seq: 3, Tool: "machine_ui", By: v, At: at(0), DurationMS: 400},
		{Seq: 4, Tool: "machine_input", By: v, At: at(5), DurationMS: 200},
		{Seq: 5, Tool: "machine_ui", By: v, At: at(5.3), DurationMS: 400,
			Effect: &machine.StepEffect{Of: 4, Kind: machine.EffectQuit, Summary: "effect: WordCount is no longer running (it quit or crashed)"}},
		{Seq: 6, Tool: "machine_input", By: v, At: at(9), DurationMS: 200}, // after a relaunch
		{Seq: 7, Tool: "machine_ui", By: v, At: at(9.3), DurationMS: 400, Effect: &machine.StepEffect{Of: 6, Kind: machine.EffectChanged}},
	}
	visual := session.Check{ID: "words", Criterion: "After Clear, Words shows 0", Kinds: []string{session.CheckVisual}}
	timing := session.Check{ID: "words", Criterion: "After Clear, Words shows 0 at once", Kinds: []string{session.CheckTiming}, Within: 2}
	cases := []struct {
		name  string
		check session.Check
		call  verdictCall
		want  string // "" for a verdict that holds
	}{
		{"a visual fail on the quit", visual, reviewArgs("fail", answer("words", "fail", []int{5}, 4)), ""},
		{"a timing fail on the quit", timing, reviewArgs("fail", answer("words", "fail", []int{5}, 4)), ""},
		{"a fail on the quit with a later action", visual, reviewArgs("fail", answer("words", "fail", []int{5}, 4, 6)), ""},
		{"a fail on the quit of an action it does not cite", visual, reviewArgs("fail", answer("words", "fail", []int{5})),
			`check "words" (quit): evidence step 5 found the app had quit after step 4: cite step 4 in actions`},
		{"a fail citing the quitting input", visual, reviewArgs("fail", answer("words", "fail", []int{4}, 4)),
			`check "words" (quit): evidence step 4 is the input; its effect read step 5 shows the app quit: cite step 5 as evidence in place of step 4`},
		{"a fail citing the quitting input, not in actions", visual, reviewArgs("fail", answer("words", "fail", []int{4})),
			"cite step 5 as evidence in place of step 4 and step 4 in actions"},
		{"a fail on another read after the quit", visual, reviewArgs("fail", answer("words", "fail", []int{7}, 4)),
			`check "words" (quit): action step 4 made the app quit, and its effect read step 5 shows it: cite step 5 as evidence`},
		{"a pass on the quit", session.Check{ID: "words", Criterion: "After Clear, Words shows 0"},
			reviewArgs("pass", answer("words", "pass", []int{5}, 4)),
			`check "words" (quit): evidence step 5 is the UI read that found the app had quit after action step 4`},
	}
	for _, c := range cases {
		r := reviewVerdict(c.call, kindTranscript(c.check), steps, 0)
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
		if strings.Contains(c.want, "(quit)") && c.call.checks[0].Status == session.CheckFail && strings.Contains(joined, "machine_screenshot") {
			t.Errorf("%s: a crashed app has nothing to screenshot, but the refusal says to take one: %s", c.name, joined)
		}
	}
}

// The effect read tells a quit from a change of frontmost app: the app is gone from the running
// apps. An app that was not a regular app in the read before cannot be judged, and stays a change.
func TestJudgeEffectReportsAQuit(t *testing.T) {
	before := machine.UITree{App: "WordCount", Apps: []string{"Finder", "WordCount"},
		Elements: []machine.UIElement{{ID: 1, Role: "Button", Label: "Clear"}}}
	gone := machine.UITree{App: "Finder", Apps: []string{"Finder"}, Elements: []machine.UIElement{{ID: 1, Role: "MenuBar"}}}
	switched := machine.UITree{App: "Finder", Apps: []string{"Finder", "WordCount"}, Elements: gone.Elements}
	empty := machine.UITree{App: "Finder", Apps: []string{"Finder"}} // Finder with no window lists nothing

	for _, c := range []struct {
		name  string
		after machine.UITree
		want  string
	}{{"gone", gone, machine.EffectQuit}, {"gone, nothing listed", empty, machine.EffectQuit}, {"switched", switched, machine.EffectChanged}} {
		kind, text := judgeEffect(before, true, c.after, nil)
		if kind != c.want {
			t.Errorf("%s: kind = %q (%s), want %q", c.name, kind, text, c.want)
		}
		if kind == machine.EffectQuit && !strings.Contains(text, "effect: WordCount is no longer running (it quit or crashed)") {
			t.Errorf("%s: text = %q", c.name, text)
		}
	}
	accessory := machine.UITree{App: "Helper", Apps: []string{"Finder"}, Elements: before.Elements}
	if kind, _ := judgeEffect(accessory, true, gone, nil); kind == machine.EffectQuit {
		t.Error("an app not listed as running before cannot be judged gone")
	}
}

// wordcount-clear-crash, trial 3 of the second simple-tier run: the effect read (step 9) said
// WordCount was gone, and the verifier answered the checks fail citing the click (step 8) as
// evidence. The refusal said step 8 was an input and, for the visual checks, to take a
// screenshot or answer unchecked; the model answered unchecked and the case ended inconclusive.
// Now the click's result says which step to cite, and the refusal names that step and gives no
// screenshot advice: a crashed app has nothing to screenshot.
func TestBenchCaseClearCrashCitingTheInputIsToldToCiteTheEffectRead(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, wordCountUI)
	look := lastStep(t, mgr, runID) + 1
	click, read, finder := look+1, look+2, look+3
	checks := func(evidence ...int) string {
		return verdictOf("fail", "WordCount crashed when Clear was pressed.",
			said(answer("after-clear-words", "fail", evidence, click), "WordCount quit when Clear was pressed."),
			said(answer("after-clear-chars", "fail", evidence, click), "WordCount quit when Clear was pressed."))
	}
	model := &scriptedModel{replies: []string{
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{
			{"id": "after-clear-words", "criterion": "After pressing Clear, Words shows 0", "kinds": []string{"value", "visual"}},
			{"id": "after-clear-chars", "criterion": "After pressing Clear, Characters shows 0", "kinds": []string{"value"}},
		}}),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 2}),
		toolCall("machine_ui", map[string]any{}), // Finder
		checks(click, finder),                    // cites the input and a later read, as the model did
		checks(read),
	}}
	model.onReasoning = func(n int) {
		if n == 3 {
			putUI(t, control, finderUI)
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, `I added a "Longest word" line to WordCount (running on screen). Check it, including after pressing Clear (Words: 0, Characters: 0).`)
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}

	after := deliveredText(t, model, 4, "tool")
	if want := fmt.Sprintf("To fail a check that depends on this input, cite step %d as its evidence.", read); !strings.Contains(after, want) {
		t.Errorf("the click's result = %q, want %q", after, want)
	}
	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 5 {
		t.Fatalf("%d progress messages, want declare, ui, click, ui and the refused verdict", len(prog))
	}
	refused := prog[4].Text
	want := fmt.Sprintf("evidence step %d is the input; its effect read step %d shows the app quit: cite step %d", click, read, read)
	if !strings.Contains(refused, want) || strings.Contains(refused, "machine_screenshot") {
		t.Errorf("the refusal of a fail citing the input =\n%s\nwant it to say %q and give no screenshot advice", refused, want)
	}
	verdicts := messagesOfKind(store, session.Verdict)
	if len(verdicts) != 1 || verdicts[0].Verdict != "fail" {
		t.Fatalf("verdicts = %+v, want the fail citing the effect read", verdicts)
	}
	for _, c := range verdicts[0].Checks {
		if c.Status != session.CheckFail || !slices.Equal(c.Evidence, []int{read}) {
			t.Errorf("check %q = %s on %v, want fail on step %d", c.ID, c.Status, c.Evidence, read)
		}
	}
}
