package summary

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// liveRun is a run recorded on a real machine for this package's live check (PR 211): TipSplit
// built with Each pays computed wrong, a task with four checks, a fail the verifier proposed
// after 2:47, the Mac stopping on its own 90 minutes later, a person accepting the fail, and
// the coding agent finishing the run as abandoned. testdata holds its manifest, conversation
// and steps as recorded, and its first 150 frame lines.
const liveRun = "20260928-000221-d9a2350ea69e1d91"

// checkRun is the second recorded run, which checked this package itself: the branch's daemon
// ran inside the machine serving liveRun's records, and the verifier read its summary routes
// with curl. 4 of 4 checks passed, the coding agent accepted, a person restarted the Mac from
// the API, a note ran the verifier into a limit of two actions twice, and the run finished as
// verified.
const checkRun = "20260928-035231-77e16574bcfba03a"

// liveStage is the run as the daemon held it at one moment.
type liveStage struct {
	run      string // liveRun when empty
	name     string
	now      string         // the moment, RFC 3339
	machine  machine.Status // "" when the Mac is gone
	boot     []string       // boot phases so far, while starting
	messages int            // the conversation's length then
}

var liveStages = []liveStage{
	{name: "starting", now: "2026-09-28T00:02:24Z", machine: machine.Booting,
		boot: []string{machine.PhaseClone, machine.PhaseStart, machine.PhaseAgent}},
	{name: "ready, before any task", now: "2026-09-28T00:03:10Z", machine: machine.Ready, messages: 1},
	{name: "ready, the coding agent builds the app", now: "2026-09-28T00:03:25Z", machine: machine.Ready, messages: 1},
	{name: "checking, the task just sent", now: "2026-09-28T00:03:45Z", machine: machine.Ready, messages: 2},
	{name: "checking, the verifier sets People to 3", now: "2026-09-28T00:04:40Z", machine: machine.Ready, messages: 10},
	{name: "failed, proposed, the Mac still on", now: "2026-09-28T00:06:45Z", machine: machine.Ready, messages: 13},
	{name: "failed, proposed, the Mac stopped on its own", now: "2026-09-28T01:37:00Z", messages: 14},
	{name: "failed, accepted by a person", now: "2026-09-28T01:38:13.5Z", messages: 15},
	{name: "finished as abandoned", now: "2026-09-28T01:38:20Z", messages: 16},

	{run: checkRun, name: "check run: ready, its daemon started in the Mac", now: "2026-09-28T03:53:12Z", machine: machine.Ready, messages: 1},
	{run: checkRun, name: "check run: checking, reading the summary routes", now: "2026-09-28T03:53:58Z", machine: machine.Ready, messages: 4},
	{run: checkRun, name: "check run: passed, proposed", now: "2026-09-28T03:54:16Z", machine: machine.Ready, messages: 7},
	{run: checkRun, name: "check run: passed, accepted by the coding agent", now: "2026-09-28T03:54:55Z", machine: machine.Ready, messages: 8},
	{run: checkRun, name: "check run: restarting", now: "2026-09-28T03:55:33Z", machine: machine.Rebooting,
		boot: []string{machine.PhaseStop}, messages: 10},
	{run: checkRun, name: "check run: restarted", now: "2026-09-28T03:55:58Z", machine: machine.Ready, messages: 11},
	{run: checkRun, name: "check run: checking your note", now: "2026-09-28T03:57:02Z", machine: machine.Ready, messages: 12},
	{run: checkRun, name: "check run: paused at the verifier's limit", now: "2026-09-28T03:57:08Z", machine: machine.Ready, messages: 15},
	{run: checkRun, name: "check run: continued", now: "2026-09-28T03:58:10Z", machine: machine.Ready, messages: 17},
	{run: checkRun, name: "check run: finished as verified", now: "2026-09-28T03:59:00Z", messages: 21},
}

// liveInput rebuilds what the daemon held at the stage: every record dated up to its moment.
func liveInput(t *testing.T, st liveStage) Input {
	t.Helper()
	run := st.run
	if run == "" {
		run = liveRun
	}
	dir := filepath.Join("testdata", run)
	now, err := time.Parse(time.RFC3339Nano, st.now)
	if err != nil {
		t.Fatal(err)
	}
	man, err := machine.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(dir, session.DefaultMaxDisputes)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := machine.ReadSteps(dir)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := machine.ReadFrames(dir)
	if err != nil {
		t.Fatal(err)
	}

	b := newRun(t, man.RunID).named(man.Name, man.Source)
	b.in.CreatedAt = man.CreatedAt
	b.in.Now, b.nowSet = now, true
	b.in.Messages = store.After(0)[:st.messages]
	b.in.Steps = slices.DeleteFunc(steps, func(s machine.Step) bool { return s.At.After(now) })
	b.in.Frames = slices.DeleteFunc(frames, func(f machine.Frame) bool { return f.At.After(now) })
	if n := len(b.in.Frames); n > 0 {
		b.in.LastFrame = &b.in.Frames[n-1]
	}
	if st.machine != "" {
		b.live(st.machine)
		start := man.CreatedAt
		if st.machine == machine.Rebooting {
			start = now.Add(-2 * time.Second)
		}
		for i, p := range st.boot {
			b.in.Machine.Boot = append(b.in.Machine.Boot, machine.BootPhase{Phase: p, At: start.Add(time.Duration(i) * time.Second)})
		}
	} else {
		b.in.EndedAt = man.DestroyedAt
	}
	return b.build() // the verdict and the finish, as a store reads them from those messages
}

func TestTheLiveRunMatchesItsGolden(t *testing.T) {
	type staged struct {
		Stage   string  `json:"stage"`
		Summary Summary `json:"summary"`
	}
	var all []staged
	for _, st := range liveStages {
		all = append(all, staged{st.name, Derive(liveInput(t, st))})
	}
	got, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "live.golden.json", append(got, '\n'))
}

// What the summary said at each moment is what happened on the machine.
func TestTheLiveRunReadsAsItHappened(t *testing.T) {
	type want struct {
		state   State
		group   Group
		now     string
		detail  string
		checks  string
		primary string
	}
	wants := map[string]want{
		"starting":                                     {Starting, Running, "Waiting for the Mac", "", "", ""},
		"ready, before any task":                       {Ready, Running, "Waiting for the coding agent", "", "", "Take control"},
		"ready, the coding agent builds the app":       {Ready, Running, "Building the app", "", "", "Take control"},
		"checking, the task just sent":                 {Checking, Running, "Reading the task", "", "", "Take control"},
		"checking, the verifier sets People to 3":      {Checking, Running, "Clicking the up arrow in TipSplit", "", "4 checks planned", "Take control"},
		"failed, proposed, the Mac still on":           {Failed, NeedsYou, "", "Proposed by the verifier after 2:47.", "2 of 4 checks", "Accept fail"},
		"failed, proposed, the Mac stopped on its own": {Failed, Done, "", "Proposed by the verifier after 2:47.", "2 of 4 checks", "Accept fail"},
		"failed, accepted by a person":                 {Failed, Done, "", "You accepted it.", "2 of 4 checks", ""},
		"finished as abandoned":                        {Failed, Done, "", "You accepted it.", "2 of 4 checks", ""},

		"check run: ready, its daemon started in the Mac": {Ready, Running, "Running a command", "", "", "Take control"},
		"check run: checking, reading the summary routes": {Checking, Running, "Running a command", "", "4 checks planned", "Take control"},
		"check run: passed, proposed":                     {Passed, NeedsYou, "", "Proposed by the verifier after 0:23.", "4 of 4 checks", "Accept pass"},
		"check run: passed, accepted by the coding agent": {Passed, Running, "", "The coding agent accepted it; you have not reviewed it.", "4 of 4 checks", ""},
		"check run: restarting":                           {Restarting, Running, "Shutting down the Mac", "Your files are kept.", "4 of 4 checks passed", ""},
		"check run: restarted":                            {Passed, Running, "", "The coding agent accepted it; you have not reviewed it.", "4 of 4 checks", ""},
		"check run: checking your note":                   {Checking, Running, "Reading your message", "", "4 of 4 checks passed", "Take control"},
		"check run: paused at the verifier's limit":       {Paused, NeedsYou, "", "The verifier used up its actions for this turn. Continue lets it go on.", "4 of 4 checks passed", "Continue"},
		"check run: continued":                            {Checking, Running, "Opening the app", "", "4 of 4 checks passed", "Take control"},
		"check run: finished as verified":                 {Passed, Done, "", "The coding agent accepted it; you have not reviewed it.", "4 of 4 checks", ""},
	}
	for _, st := range liveStages {
		t.Run(st.name, func(t *testing.T) {
			s := Derive(liveInput(t, st))
			w, ok := wants[st.name]
			if !ok {
				t.Fatalf("no expectation for stage %q", st.name)
			}
			primary := ""
			if s.PrimaryAction != nil {
				primary = s.PrimaryAction.Label
			}
			got := want{s.State, s.Group, s.Now, s.Detail, s.Checks.Text, primary}
			if got != w {
				t.Errorf("got  %+v\nwant %+v", got, w)
			}
			for _, text := range texts(s) {
				for _, re := range forbidden {
					if re.MatchString(text) {
						t.Errorf("%q matches forbidden %s", text, re)
					}
				}
			}
			withinBudget(t, s)
		})
	}
}

// The first failing check of the live verdict: the claim without its setup clause, the two
// values, the frame of the read that showed them, and where the wrong value sits on it.
func TestTheLiveRunsFailingCheck(t *testing.T) {
	s := Derive(liveInput(t, liveStages[5]))
	f := s.Failing
	if f == nil {
		t.Fatal("no failing check")
	}
	if f.Text != "Each pays reads $48.00" || f.Setup != "With Bill 120, 20% tip, People 3" {
		t.Errorf("failing text = %q after setup %q", f.Text, f.Setup)
	}
	rows := []CheckRef{
		{ID: "each-pays-48", Text: "Each pays reads $48.00", State: "fail", Saw: "$8.00"},
		{ID: "each-pays-50", Text: "After picking 25% tip, Each pays reads $50.00", State: "fail", Saw: "$10.00"},
		{ID: "tip-20", Text: "Tip reads $24.00", State: "pending"},
		{ID: "labels-present", Text: "The window shows Bill, Tip and People labels", State: "pass"},
	}
	if !slices.Equal(s.Checks.Items, rows) {
		t.Errorf("rows = %+v\nwant   %+v", s.Checks.Items, rows)
	}
	if s.Checks.Current == nil || *s.Checks.Current != rows[0] {
		t.Errorf("the current check = %+v, want the first failed row", s.Checks.Current)
	}
	if f.Expected != "$48.00" || f.Saw != "$8.00" {
		t.Errorf("expected %q saw %q, want $48.00 and $8.00", f.Expected, f.Saw)
	}
	if f.Observed != "Each pays reads $8.00, not $48.00" {
		t.Errorf("observed = %q", f.Observed)
	}
	if f.Step != 17 || f.Picture == nil || f.Picture.Kind != "frame" || f.Picture.Step != 17 {
		t.Errorf("picture = %+v of step %d, want a frame of step 17", f.Picture, f.Step)
	}
	// "Each pays: $8.00" in the read of step 17: centre (0.425, 0.636), size 0.217 by 0.043.
	if f.Mark == nil || *f.Mark != (Box{X: 0.317, Y: 0.615, W: 0.217, H: 0.043}) {
		t.Errorf("mark = %+v", f.Mark)
	}
	if s.Machine.Ended != "" {
		t.Errorf("the Mac is on, yet ended = %q", s.Machine.Ended)
	}
	if lost := Derive(liveInput(t, liveStages[6])); lost.Machine.Ended != "The Mac stopped on its own." || lost.Machine.Status != "off" {
		t.Errorf("after the Mac stopped: %+v", lost.Machine)
	}
	if done := Derive(liveInput(t, liveStages[8])); done.Outcome != "Abandoned" {
		t.Errorf("outcome = %q", done.Outcome)
	}
}

// The check run's finish: verified, the pass colour, and how its Mac went.
func TestTheCheckRunFinishedVerified(t *testing.T) {
	s := Derive(liveInput(t, liveStages[len(liveStages)-1]))
	if s.Outcome != "Verified" || s.Tone != TonePass || s.Group != Done {
		t.Errorf("outcome %q tone %s group %s", s.Outcome, s.Tone, s.Group)
	}
	if s.Machine.Status != "off" || s.Machine.Ended != "The coding agent shut down the Mac." {
		t.Errorf("machine = %+v", s.Machine)
	}
	if s.Name != "Run summary: the recorded TipSplit\u2026" || s.Source != "Claude Code" {
		t.Errorf("name %q source %q", s.Name, s.Source)
	}
	if len(s.SecondaryActions) != 0 || s.PrimaryAction != nil {
		t.Errorf("a finished run offers %+v and %+v", s.PrimaryAction, s.SecondaryActions)
	}
	// A criterion cut inside a quoted phrase ends before the phrase.
	if got := s.Checks.Items[1].Text; got != "In that summary checks.text is\u2026" {
		t.Errorf("row = %q", got)
	}
}
