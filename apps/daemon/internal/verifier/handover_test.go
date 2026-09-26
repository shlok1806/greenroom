package verifier

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// The screen coming back, as internal/api posts it for a Give Back or a lapsed human lease.
const (
	gaveBack = "human gave the screen back after 0 actions"
	lapsed   = "human lost control of the screen after 0 actions: the lease lapsed with no input or renewal for 60 s"
)

func controlEvent(t *testing.T, store *session.Store, text, control string) {
	t.Helper()
	post(t, store, session.Message{From: session.System, Kind: session.Event, Text: text, Control: control})
}

// startActors runs the actor for runID with brain b and returns the run's store.
func startActors(t *testing.T, b Brain, mgr *machine.Manager, runID string) *session.Store {
	t.Helper()
	reg := session.NewRegistry(mgr.Root, 2)
	NewActors(b, mgr, reg)
	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// quiet waits long enough for a turn the actor should not take to have shown up.
func quiet() { time.Sleep(300 * time.Millisecond) }

// askedForTheScreen is a run whose task the verifier paused, asking for the screen.
func askedForTheScreen(t *testing.T, store *session.Store) {
	t.Helper()
	postTask(t, store, "Click 25% and report Each pays.")
	post(t, store, session.Message{From: session.Verifier, Kind: session.Question, Text: screenTakenQuestion("human", false)})
}

// Issue #124: after Give Back the verifier sat idle until someone sent a message, and nothing made
// it read the screen the person had changed. Giving back resumes the paused task on its own, and
// the turn is told to look before any input.
func TestGivingTheScreenBackResumesTheTaskAndItLooksFirst(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	look := lastStep(t, mgr, runID) + 1 // the resumed turn's machine_ui; the refused click records none
	model := &scriptedModel{replies: []string{
		declared("tip"),
		toolCall("machine_click", map[string]any{"x": 0.5, "y": 0.5}),
		toolCall("machine_ui", map[string]any{"app": "TipSplit"}),
		toolCall("machine_click", map[string]any{"element": 1}),
		verdictOf("pass", "25% is selected (step 6).", answer("tip", "pass", []int{look + 2}, look+1)),
	}}
	selectOnClick(t, model, control, 4)
	store := startActors(t, newVerifier(t, mgr, model.start(t)), mgr, runID)
	postTask(t, store, "Click 25% and report Each pays.")

	q := waitForKind(t, store, session.Question, 5*time.Second)
	if !strings.HasPrefix(q.Text, screenBackAsk) || !strings.Contains(q.Text, "Press Give Back in the Companion and I will continue") {
		t.Fatalf("question = %q, want the screen asked for", q.Text)
	}
	if _, _, err := mgr.ReleaseControl(runID, "human"); err != nil {
		t.Fatalf("ReleaseControl: %v", err)
	}
	controlEvent(t, store, gaveBack, session.ControlReturned)

	verdict := waitForKind(t, store, session.Verdict, 5*time.Second)
	if verdict.Verdict != "pass" {
		t.Fatalf("verdict = %+v, want the resumed turn's pass", verdict)
	}
	told := deliveredText(t, model, 3, "user")
	for _, want := range []string{gaveBack, "Look at the screen with machine_ui before any input"} {
		if !strings.Contains(told, want) {
			t.Errorf("the resumed turn was not told %q:\n%s", want, told)
		}
	}
	var resumed []session.Message
	for _, m := range messagesOfKind(store, session.Progress) {
		if m.Seq > q.Seq {
			resumed = append(resumed, m)
		}
	}
	if len(resumed) != 2 || !strings.HasPrefix(resumed[0].Text, "machine_ui ") ||
		!strings.Contains(resumed[1].Text, "clicked [1]") {
		t.Fatalf("resumed progress = %+v, want machine_ui then a click that landed", resumed)
	}
}

// A lapsed human lease gives the screen back too.
func TestALapsedLeaseResumesThePausedTask(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	look := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		toolCall("machine_ui", map[string]any{}),
		declared("tip"),
		verdictOf("pass", "25% is selected (step 4).", answer("tip", "pass", []int{look})),
	}}
	v := newVerifier(t, mgr, model.start(t))
	reg := session.NewRegistry(mgr.Root, 2)
	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	askedForTheScreen(t, store)
	NewActors(v, mgr, reg)
	controlEvent(t, store, lapsed, session.ControlReturned)

	if verdict := waitForKind(t, store, session.Verdict, 5*time.Second); verdict.Verdict != "pass" {
		t.Fatalf("verdict = %+v, want the resumed turn's pass", verdict)
	}
	if told := deliveredText(t, model, 1, "user"); !strings.Contains(told, lapsed) || !strings.Contains(told, returnedAdvice) {
		t.Errorf("the resumed turn was not told the screen came back:\n%s", told)
	}
}

// Nothing to pick up, nothing starts: no task open, or a task waiting on the answer to another question.
func TestGivingTheScreenBackWithNothingPausedStartsNoTurn(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, *session.Store){
		"verdict given": func(t *testing.T, store *session.Store) {
			postTask(t, store, "Check the split.")
			post(t, store, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: "pass", Text: "Each pays $48.00 (step 3)."})
		},
		"no task": func(t *testing.T, store *session.Store) {
			post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "hello"})
			post(t, store, session.Message{From: session.Verifier, Kind: session.Reply, Text: "Hello."})
		},
		"another question": func(t *testing.T, store *session.Store) {
			postTask(t, store, "Build it.")
			post(t, store, session.Message{From: session.Verifier, Kind: session.Question, Text: "Which scheme should I build?"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			mgr, runID, _ := ready(t)
			model := &scriptedModel{}
			v := newVerifier(t, mgr, model.start(t))
			reg := session.NewRegistry(mgr.Root, 2)
			store, err := reg.Get(runID)
			if err != nil {
				t.Fatal(err)
			}
			setup(t, store)
			NewActors(v, mgr, reg)
			before := store.Len()
			controlEvent(t, store, gaveBack, session.ControlReturned)
			quiet()
			if model.calls() != 0 || store.Len() != before+1 {
				t.Fatalf("a turn ran: %d model calls, transcript %+v", model.calls(), store.After(before))
			}
		})
	}
}

// Giving back and a message sent right after it make one turn, whether the message lands before
// the resumed turn starts or while it runs.
func TestGivingBackAndANoteMakeOneTurn(t *testing.T) {
	for name, midTurn := range map[string]bool{"together": false, "mid-turn": true} {
		t.Run(name, func(t *testing.T) {
			noRetryWaits(t)
			mgr, runID, control := ready(t)
			putUI(t, control, segmentUI)
			look := lastStep(t, mgr, runID) + 1
			model := &scriptedModel{replies: []string{
				toolCall("machine_ui", map[string]any{}),
				declared("tip"),
				verdictOf("pass", "25% is selected (step 4).", answer("tip", "pass", []int{look})),
			}}
			v := newVerifier(t, mgr, model.start(t))
			reg := session.NewRegistry(mgr.Root, 2)
			store, err := reg.Get(runID)
			if err != nil {
				t.Fatal(err)
			}
			askedForTheScreen(t, store)
			note := session.Message{From: session.Human, Kind: session.Note, Text: "Go ahead, it is yours."}
			if midTurn {
				model.onReasoning = func(n int) {
					if n == 1 {
						if _, err := store.Append(note); err != nil {
							t.Errorf("append: %v", err)
						}
					}
				}
			}
			NewActors(v, mgr, reg)
			controlEvent(t, store, gaveBack, session.ControlReturned)
			if !midTurn {
				post(t, store, note)
			}

			waitForKind(t, store, session.Verdict, 5*time.Second)
			quiet()
			if n := model.calls(); n != 3 {
				t.Errorf("the model was called %d times, want 3: one turn", n)
			}
			if n := len(messagesOfKind(store, session.Verdict)); n != 1 {
				t.Errorf("%d verdicts, want 1", n)
			}
			if eventSaying(store, "retrying") || eventSaying(store, "gave up") {
				t.Errorf("a second turn ran and failed: %+v", store.After(0))
			}
		})
	}
}

// The manual brain does not resume: its person types the next instruction, and running the last
// one again would click twice.
func TestManualDoesNotResumeOnGiveBack(t *testing.T) {
	mgr, runID, control := ready(t)
	reg := session.NewRegistry(mgr.Root, 2)
	store, err := reg.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	askedForTheScreen(t, store)
	NewActors(NewManual(mgr, testLog()), mgr, reg)
	before := store.Len()
	controlEvent(t, store, gaveBack, session.ControlReturned)
	quiet()
	if store.Len() != before+1 || strings.Contains(testsupport.Calls(t, control), "--json-base64") {
		t.Fatalf("the manual brain acted on a give back: %+v", store.After(before))
	}
}

// Issue #124: input aimed before a handover is refused until the verifier looks again, and the
// refusal says so; after a machine_ui read the same click lands.
func TestAStaleLookIsRefusedUntilTheVerifierLooks(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, segmentUI)
	if _, _, err := mgr.TakeControl(runID, "human", 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	if _, _, err := mgr.ReleaseControl(runID, "human"); err != nil {
		t.Fatalf("ReleaseControl: %v", err)
	}
	look := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("tip"),
		toolCall("machine_click", map[string]any{"x": 0.596, "y": 0.467}),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"x": 0.596, "y": 0.467}),
		verdictOf("pass", "25% is selected (step 6).", answer("tip", "pass", []int{look + 2}, look+1)),
	}}
	selectOnClick(t, model, control, 4)
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Click 25%.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	prog := messagesOfKind(store, session.Progress)[1:] // after the declaration
	if len(prog) != 3 || !strings.Contains(prog[0].Text, staleLookPrefix) || !strings.Contains(prog[2].Text, "clicked (0.596, 0.467)") {
		t.Fatalf("progress = %+v, want a stale refusal, a look, then the click", prog)
	}
	if strings.Contains(prog[2].Text, repeatWarning) {
		t.Errorf("the click after a look was warned as a repeat: %q", prog[2].Text)
	}
	steps, err := os.ReadFile(filepath.Join(mgr.RunDir(runID), "steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(steps), `"tool":"machine_input"`); n != 1 {
		t.Errorf("%d input steps, want only the click after the look", n)
	}
	if got := store.Verdict(); got.Verdict != "pass" {
		t.Errorf("verdict = %+v, want pass", got)
	}
}

// A look answers a stale-look refusal, so a stale refusal after it counts from 1 again.
func TestALookClearsStaleLookRepeats(t *testing.T) {
	r := repeats{}
	click := nim.ToolCall{Name: "machine_click", Arguments: `{"x":0.5,"y":0.5}`}
	stale := staleLookResult(machine.ErrStaleLook)
	if !strings.HasPrefix(stale, staleLookPrefix) {
		t.Fatalf("stale result %q does not start with %q", stale, staleLookPrefix)
	}
	r.record(click, stale)
	if n := r.record(click, stale); n != 2 {
		t.Fatalf("second stale refusal = %d, want 2", n)
	}
	for _, look := range []nim.ToolCall{{Name: "machine_ui", Arguments: "{}"}, {Name: "machine_screenshot", Arguments: "{}"}} {
		r.record(look, "step 9\nTipSplit")
		if n := r.record(click, stale); n != 1 {
			t.Errorf("stale refusal after %s = %d, want 1", look.Name, n)
		}
	}
	if n := r.record(click, "error: some other failure"); n != 1 {
		t.Fatalf("other failure = %d, want 1", n)
	}
	r.record(nim.ToolCall{Name: "machine_ui", Arguments: "{}"}, "step 10\nTipSplit")
	if n := r.record(click, "error: some other failure"); n != 2 {
		t.Errorf("a look cleared a failure that was not a stale look: %d, want 2", n)
	}
}

// Issue #124: a person taking the screen mid-turn is told to the model plainly: look, do not act.
func TestATakeoverMidTurnTellsTheModelItMayLookButNotAct(t *testing.T) {
	mgr, runID, _ := ready(t)
	store := openStore(t, mgr, runID)
	model := &scriptedModel{replies: []string{
		toolCall("machine_exec", map[string]any{"command": "true"}),
		toolCall("ask", map[string]any{"question": "May I have the screen back?"}),
	}}
	model.onReasoning = func(n int) {
		if n == 1 {
			if _, err := store.Append(session.Message{From: session.System, Kind: session.Event,
				Text: "human took control of the screen", Control: session.ControlTaken}); err != nil {
				t.Errorf("append: %v", err)
			}
		}
	}
	v := newVerifier(t, mgr, model.start(t))
	postTask(t, store, "Click 25%.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	told := deliveredText(t, model, 2, "user")
	if !strings.Contains(told, "[while you were working] machine event: human took control of the screen. "+takenAdvice) {
		t.Errorf("the model was not told plainly that it may look but not act:\n%s", told)
	}
}

// segmentSelectedUI is segmentUI after a click selected 25%.
const segmentSelectedUI = `{"app":{"name":"TipSplit","pid":7},"apps":["TipSplit"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXRadioButton","subrole":"AXSegment","label":"25%","selected":true,"depth":0,"frame":{"x":586,"y":347,"w":48,"h":24}}]}`

// selectOnClick makes the nth scripted reply's click select 25%: the tree changes before the
// effect check reads it, as it would on a machine.
func selectOnClick(t *testing.T, model *scriptedModel, control string, n int) {
	t.Helper()
	model.onReasoning = func(i int) {
		if i == n {
			putUI(t, control, segmentSelectedUI)
		}
	}
}
