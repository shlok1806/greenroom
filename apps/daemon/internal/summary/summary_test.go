package summary

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

const tipTask = "TipSplit, a tip calculator I just built, is running on screen. Verify it through the UI only: " +
	"set the bill to 120, choose the 20% tip, set People to 3. Each pays should read $48.00."

// notAnswering is the error a look returns on a wedged guest screen (daemon ADR 0003).
const notAnswering = "the guest screen is not answering: a screenshot got nothing back within 45s. machine_exec may still work; call machine_reboot to recover"

var tip25 = machine.UIElement{ID: 4, Role: "Button", Label: "25%", X: 0.55, Y: 0.5, W: 0.04, H: 0.03}

func TestEveryStatusHasItsGroupActionAndWords(t *testing.T) {
	type want struct {
		state     State
		group     Group
		tone      Tone
		primary   string // action id, "" for none
		secondary []string
		detail    string // a substring; "" asserts nothing
		now       string // exact; "-" means empty
		checks    string // checks.text, exact
	}
	cases := []struct {
		name string
		run  func(*builder) *builder
		want want
	}{
		{
			"a machine still booting is Starting and names its boot phase",
			func(b *builder) *builder { return b.live(machine.Booting).boot(machine.PhaseClone) },
			want{Starting, Running, ToneLive, "", nil, "", "Copying the Mac", ""},
		},
		{
			"a machine machine_reboot is restarting is Restarting, with no button",
			func(b *builder) *builder {
				return b.live(machine.Rebooting).boot(machine.PhaseStop).task(tipTask, 30).event("machine is rebooting", 300)
			},
			want{Restarting, Running, ToneLive, "", nil, "Your files are kept", "Shutting down the Mac", ""},
		},
		{
			"a ready machine nobody has asked anything is Ready and waits for the coding agent",
			func(b *builder) *builder { return b.live(machine.Ready).event("machine is ready", 40) },
			want{Ready, Running, ToneLive, ActTakeControl, nil, "", "Waiting for the coding agent", ""},
		},
		{
			"a task with no step yet is being read",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Reading the task", ""},
		},
		{
			"a plan and a click name the checks and what was clicked, in the app",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					plan(70, "Window shows Bill, Tip and People", "Tip is $24.00 for $120 at 20%", "Each pays is $48.00 for 3 people", "Each pays becomes $50.00 at 25%").
					uiRead(80, "TipSplit", tip25).click(90, 0.55, 0.5)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Clicking 25% in TipSplit", "4 checks planned"},
		},
		{
			"an open run with nothing for twenty minutes says it is idle",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					msg(session.Verifier, session.Reply, "It works.", 120).now(120 + 20*60)
			},
			want{Ready, Running, ToneLive, ActTakeControl, nil, "", "Idle for 20 minutes", ""},
		},
		{
			"a person driving the screen gives control back",
			func(b *builder) *builder {
				return b.live(machine.Ready).controlledBy("human").event("machine is ready", 40).task(tipTask, 60)
			},
			want{Checking, Running, ToneLive, ActGiveBack, nil, "", "You have control", ""},
		},
		{
			"a daemon with no verifier closes the task, so the run is Ready for the coding agent",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					event("no verifier is configured on this daemon; nobody will answer this task", 61)
			},
			want{Ready, Running, ToneLive, ActTakeControl, nil, "", "Waiting for the coding agent", ""},
		},
		{
			"a verifier stopped at its time limit is Paused and needs you to continue",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).
					msg(session.Verifier, session.Reply, "I ran out of time after 10m0s.", 660, func(m *session.Message) { m.Stop = session.StopTime })
			},
			want{Paused, NeedsYou, ToneWait, ActContinue, nil, "The verifier ran out of time. Continue lets it go on.", "-", ""},
		},
		{
			"a verifier stopped at its step limit says so",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).
					msg(session.Verifier, session.Reply, "I used 40 tool calls.", 660, func(m *session.Message) { m.Stop = session.StopSteps })
			},
			want{Paused, NeedsYou, ToneWait, ActContinue, nil, "used up its actions", "-", ""},
		},
		{
			"a Continue after the stop is checking again",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).
					msg(session.Verifier, session.Reply, "Out of time.", 660, func(m *session.Message) { m.Stop = session.StopTime }).
					msg(session.Human, session.Note, "Continue.", 700).now(710)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Reading the task", ""},
		},
		{
			"a verifier question is Paused and needs your answer",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).
					msg(session.Verifier, session.Question, "Which tip should I choose when the task names none?", 90)
			},
			want{Paused, NeedsYou, ToneWait, ActAnswer, nil, "The verifier has a question for you.", "-", ""},
		},
		{
			"looks that time out after ready are Not answering, with Restart the Mac",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					screenshot(70).step("machine_screenshot", 200, nil, nil, notAnswering).
					step("machine_ui", 260, nil, nil, notAnswering)
			},
			want{NotAnswering, NeedsYou, ToneWait, ActRestart, []string{ActKeepWaiting}, "Restart it? Your files are kept.", "-", ""},
		},
		{
			"a look that answers again ends Not answering",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					step("machine_screenshot", 200, nil, nil, notAnswering).screenshot(260)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Looking at the screen", ""},
		},
		{
			"looks that timed out before a reboot's ready do not count after it",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					step("machine_screenshot", 200, nil, nil, notAnswering).event("machine is ready", 300)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Reading the task", ""},
		},
		{
			"a machine_reboot's ready ends Not answering",
			func(b *builder) *builder {
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					step("machine_screenshot", 200, nil, nil, notAnswering).
					event("machine is rebooting (machine_reboot): its sessions, running commands and apps end; its disk stays", 250).
					event("machine rebooted and is ready", 300)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Reading the task", ""},
		},
		{
			"a reboot that failed is Stopped and keeps the files",
			func(b *builder) *builder {
				return b.live(machine.Failed).task(tipTask, 60).
					event("machine failed to reboot: tart run exited", 300)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "The Mac did not restart. Its files are kept.", "-", ""},
		},
		{
			"a machine low on files needs you while it keeps checking, and offers a plain restart",
			func(b *builder) *builder {
				return b.live(machine.Ready).lowOnFiles().event("machine is ready", 40).task(tipTask, 60)
			},
			want{Checking, NeedsYou, ToneLive, ActTakeControl, []string{ActRestart}, "", "Reading the task", ""},
		},
		{
			"a proposed fail on a live machine needs you: Accept fail, Reject beside it",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).
					verdict("fail", 266, pass("a", "Tip is $24.00", "Tip reads $24.00"), fail("b", "Each pays becomes $50.00 at 25%", "Each pays reads $10.00"))
			},
			want{Failed, NeedsYou, ToneFail, ActAccept, []string{ActReject}, "Proposed by the verifier after 3:26.", "-", "1 of 2 checks"},
		},
		{
			"a proposed pass on a live machine needs you: Accept pass",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).verdict("pass", 120, pass("a", "Tip is $24.00", "Tip reads $24.00"))
			},
			want{Passed, NeedsYou, TonePass, ActAccept, []string{ActReject}, "Proposed by the verifier after 1:00.", "-", "1 of 1 check"},
		},
		{
			"a contested verdict needs you and says only you can close it",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).verdict("fail", 100, fail("b", "Each pays is $48.00", "Each pays reads $8.00")).
					dispute(session.Coder, 110).verdict("fail", 150, fail("b", "Each pays is $48.00", "Each pays reads $8.00")).
					dispute(session.Coder, 160).verdict("fail", 200, fail("b", "Each pays is $48.00", "Each pays reads $8.00"))
			},
			want{Failed, NeedsYou, ToneFail, ActAccept, []string{ActReject}, "Only you can accept or reject it now.", "-", "1 of 1 check"},
		},
		{
			"a pass the coding agent accepted on a live machine runs on, unreviewed and uncoloured",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).verdict("pass", 120, pass("a", "Tip is $24.00", "Tip reads $24.00")).accept(session.Coder, 130)
			},
			want{Passed, Running, ToneQuiet, "", []string{ActRecheck}, "you have not reviewed it", "-", "1 of 1 check"},
		},
		{
			"a pass you accepted, machine gone, is Done and green",
			func(b *builder) *builder {
				return b.task(tipTask, 60).verdict("pass", 120, pass("a", "Tip is $24.00", "Tip reads $24.00")).accept(session.Human, 130).
					event("machine destroyed", 140).ended(140)
			},
			want{Passed, Done, TonePass, "", nil, "You accepted it.", "-", "1 of 1 check"},
		},
		{
			"a proposed verdict whose machine is gone is Done, still reviewable, not Needs you",
			func(b *builder) *builder {
				return b.task(tipTask, 60).verdict("fail", 266, fail("b", "Each pays becomes $50.00 at 25%", "Each pays reads $10.00")).
					event("machine destroyed", 300).ended(300)
			},
			want{Failed, Done, ToneFail, ActAccept, []string{ActReject}, "Proposed by the verifier", "-", "1 of 1 check"},
		},
		{
			"a finished run is Done even with its machine kept and a verdict open",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).verdict("fail", 266, fail("b", "Each pays is $48.00", "Each pays reads $8.00")).
					finish(session.OutcomeUnverified, 300)
			},
			want{Failed, Done, ToneFail, ActAccept, []string{ActReject}, "Proposed by the verifier", "-", "1 of 1 check"},
		},
		{
			"a verified finish is Done and Passed",
			func(b *builder) *builder {
				return b.task(tipTask, 60).verdict("pass", 120, pass("a", "Tip is $24.00", "Tip reads $24.00")).accept(session.Coder, 130).
					finish(session.OutcomeVerified, 140).ended(141)
			},
			want{Passed, Done, TonePass, "", nil, "you have not reviewed it", "-", "1 of 1 check"},
		},
		{
			"a finish with no verdict is Stopped and says so",
			func(b *builder) *builder {
				return b.task(tipTask, 60).finish(session.OutcomeUnverified, 140).ended(141)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "finished it without a verdict", "-", ""},
		},
		{
			"an abandoned run is Stopped",
			func(b *builder) *builder { return b.task(tipTask, 60).finish(session.OutcomeAbandoned, 140).ended(141) },
			want{Stopped, Done, ToneQuiet, "", nil, "gave it up", "-", ""},
		},
		{
			"a new task after a verdict is a re-check: Checking, not the old outcome",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).verdict("fail", 266, fail("b", "Each pays is $48.00", "Each pays reads $8.00")).
					task("I fixed it; check again.", 400).now(410)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Reading the task", ""},
		},
		{
			"a verdict you rejected gives no outcome; the run is Ready again",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).verdict("pass", 120, pass("a", "Tip is $24.00", "Tip reads $24.00")).
					dispute(session.Human, 130).msg(session.Verifier, session.Reply, "I will look again later.", 140).now(150)
			},
			want{Ready, Running, ToneLive, ActTakeControl, nil, "You rejected the verifier's pass.", "Waiting for the coding agent", ""},
		},
		{
			"a verdict with no checks has no tally and no failing check",
			func(b *builder) *builder { return b.live(machine.Ready).task(tipTask, 60).verdict("fail", 90) },
			want{Failed, NeedsYou, ToneFail, ActAccept, []string{ActReject}, "Proposed by the verifier", "-", ""},
		},
		{
			"a plan with no verdict on a run that ended is Stopped and keeps the plan's count",
			func(b *builder) *builder {
				return b.task(tipTask, 60).plan(70, "Tip is $24.00", "Each pays is $48.00").event("machine destroyed", 200).ended(200)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "The coding agent shut down the Mac.", "-", "2 checks planned"},
		},
		{
			"an inconclusive verdict offers a plain Accept",
			func(b *builder) *builder {
				return b.live(machine.Ready).task(tipTask, 60).verdict("inconclusive", 90, pass("a", "Tip is $24.00", "Tip reads $24.00"), unchecked("b", "Each pays is $48.00"))
			},
			want{Inconclusive, NeedsYou, ToneQuiet, ActAccept, []string{ActReject}, "Proposed by the verifier", "-", "1 of 2 checks passed"},
		},
		{
			"a machine that failed to boot is Stopped: the Mac did not start",
			func(b *builder) *builder {
				return b.live(machine.Failed).event("machine failed to boot: tart clone: image not found", 30)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "The Mac did not start.", "-", ""},
		},
		{
			"a machine lost under the run stopped on its own",
			func(b *builder) *builder {
				return b.task(tipTask, 60).event("machine stopped: the VM exited", 300).ended(300)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "The Mac stopped on its own.", "-", ""},
		},
		{
			"a machine you destroyed says you shut it down",
			func(b *builder) *builder {
				return b.task(tipTask, 60).event("human destroyed the machine", 300).event("machine destroyed", 301).ended(301)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "You shut down the Mac.", "-", ""},
		},
		{
			"a verifier stopped at its limit when the machine went is Stopped with why",
			func(b *builder) *builder {
				return b.task(tipTask, 60).
					msg(session.Verifier, session.Reply, "Out of time.", 660, func(m *session.Message) { m.Stop = session.StopTime }).
					event("machine destroyed", 700).ended(700)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "The verifier ran out of time before a verdict.", "-", ""},
		},
		{
			"after a daemon restart a reattached machine with an open task is still Checking",
			func(b *builder) *builder {
				// The verifier resumes an unanswered turn after a restart (verifier.Actors).
				return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60).
					uiRead(80, "TipSplit", tip25).click(90, 0.55, 0.5).now(200)
			},
			want{Checking, Running, ToneLive, ActTakeControl, nil, "", "Clicking 25% in TipSplit", ""},
		},
		{
			"a machine that vanished while the daemon was down is Stopped at its stamped end",
			func(b *builder) *builder {
				// endRun stamps destroyedAt from the last record; no "machine destroyed" event is posted.
				return b.task(tipTask, 60).uiRead(80, "TipSplit").ended(80)
			},
			want{Stopped, Done, ToneQuiet, "", nil, "", "-", ""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.run(newRun(t, "r1")).derive()
			w := c.want
			if s.State != w.state || s.Status != w.state.Word() {
				t.Fatalf("status = %s (%q), want %s", s.State, s.Status, w.state)
			}
			if s.Group != w.group {
				t.Errorf("group = %s, want %s", s.Group, w.group)
			}
			if s.Tone != w.tone {
				t.Errorf("tone = %s, want %s", s.Tone, w.tone)
			}
			gotPrimary := ""
			if s.PrimaryAction != nil {
				gotPrimary = s.PrimaryAction.ID
			}
			if gotPrimary != w.primary {
				t.Errorf("primary = %q, want %q", gotPrimary, w.primary)
			}
			var gotSecondary []string
			for _, a := range s.SecondaryActions {
				gotSecondary = append(gotSecondary, a.ID)
			}
			if !slices.Equal(gotSecondary, w.secondary) {
				t.Errorf("secondary = %v, want %v", gotSecondary, w.secondary)
			}
			if !strings.Contains(s.Detail, w.detail) {
				t.Errorf("detail = %q, want it to contain %q", s.Detail, w.detail)
			}
			wantNow := w.now
			if wantNow == "-" {
				wantNow = ""
			}
			if s.Now != wantNow {
				t.Errorf("now = %q, want %q", s.Now, wantNow)
			}
			if s.Checks.Text != w.checks {
				t.Errorf("checks = %q, want %q", s.Checks.Text, w.checks)
			}
		})
	}
}

// Every word of the vocabulary is reachable, and nothing else is: the table above covers each.
func TestTheVocabularyIsFixed(t *testing.T) {
	want := []string{"Starting", "Ready", "Checking", "Paused", "Not answering", "Restarting", "Passed", "Failed", "Inconclusive", "Stopped"}
	var got []string
	for _, s := range States {
		got = append(got, s.Word())
	}
	if !slices.Equal(got, want) {
		t.Errorf("vocabulary = %v, want %v", got, want)
	}
	for _, g := range Groups {
		if g.Title() == string(g) {
			t.Errorf("group %s has no title", g)
		}
	}
}

func TestSinceIsWhenTheRunEnteredItsStatus(t *testing.T) {
	cases := []struct {
		name string
		run  func(*builder) *builder
		want time.Time
	}{
		{"starting: the run's start", func(b *builder) *builder { return b.live(machine.Booting) }, at(0)},
		{"checking: the task after ready", func(b *builder) *builder {
			return b.live(machine.Ready).event("machine is ready", 40).task(tipTask, 60)
		}, at(60)},
		{"paused: the stop", func(b *builder) *builder {
			return b.live(machine.Ready).task(tipTask, 60).
				msg(session.Verifier, session.Reply, "Out of time.", 660, func(m *session.Message) { m.Stop = session.StopTime })
		}, at(660)},
		{"not answering: the first look of the streak", func(b *builder) *builder {
			return b.live(machine.Ready).event("machine is ready", 40).screenshot(100).
				step("machine_ui", 200, nil, nil, notAnswering).step("machine_screenshot", 260, nil, nil, notAnswering)
		}, at(200)},
		{"failed: the verdict", func(b *builder) *builder {
			return b.live(machine.Ready).task(tipTask, 60).verdict("fail", 266, fail("b", "x", "y"))
		}, at(266)},
		{"passed and accepted: the accept", func(b *builder) *builder {
			return b.task(tipTask, 60).verdict("pass", 120, pass("a", "x", "x")).accept(session.Human, 400).ended(500)
		}, at(400)},
		{"stopped: the end", func(b *builder) *builder { return b.task(tipTask, 60).ended(500) }, at(500)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if s := c.run(newRun(t, "r1")).derive(); !s.Since.Equal(c.want) {
				t.Errorf("since = %s, want %s", s.Since, c.want)
			}
		})
	}
}

func TestElapsedRunsToTheEndOrToNow(t *testing.T) {
	open := newRun(t, "r1").live(machine.Ready).now(258).derive()
	if open.ElapsedSeconds != 258 || open.EndedAt != nil {
		t.Errorf("open run: elapsed %d, ended %v; want 258 and nil", open.ElapsedSeconds, open.EndedAt)
	}
	done := newRun(t, "r2").task(tipTask, 1).ended(206).now(9999).derive()
	if done.ElapsedSeconds != 206 || done.EndedAt == nil || !done.EndedAt.Equal(at(206)) {
		t.Errorf("ended run: elapsed %d, ended %v; want 206 at its end", done.ElapsedSeconds, done.EndedAt)
	}
}

func TestTheFailingCheckCarriesItsValuesPictureAndMark(t *testing.T) {
	each := machine.UIElement{ID: 9, Role: "StaticText", Value: "Each pays: $10.00", X: 0.5, Y: 0.6, W: 0.2, H: 0.05}
	b := newRun(t, "r1").live(machine.Ready).task(tipTask, 60).
		uiRead(100, "TipSplit", each). // step 1
		screenshot(101).               // step 2: no input between it and the read
		verdict("fail", 266,
			pass("a", "Tip is $24.00 for $120 at 20%", "Tip reads $24.00"),
			fail("b", "Each pays becomes $50.00 at 25%", "Each pays reads $10.00 with 25% selected (steps 1, 2).", 1, 2))
	s := b.derive()
	f := s.Failing
	if f == nil {
		t.Fatal("no failing check")
	}
	if f.Text != "Each pays becomes $50.00 at 25%" || f.Expected != "$50.00" || f.Saw != "$10.00" {
		t.Errorf("failing = %q expected %q saw %q", f.Text, f.Expected, f.Saw)
	}
	if strings.Contains(f.Observed, "steps") {
		t.Errorf("observed keeps the record citation: %q", f.Observed)
	}
	if f.Picture == nil || f.Picture.Kind != "screenshot" || f.Picture.File != "002-screenshot.png" ||
		f.Picture.URL != "/api/runs/r1/artifacts/002-screenshot.png" || f.Step != 2 {
		t.Errorf("picture = %+v (step %d), want the screenshot of step 2", f.Picture, f.Step)
	}
	if f.Mark == nil || *f.Mark != (Box{X: 0.4, Y: 0.575, W: 0.2, H: 0.05}) {
		t.Errorf("mark = %+v, want the element that shows $10.00", f.Mark)
	}
	if s.Checks.Current == nil || s.Checks.Current.State != "fail" || s.Checks.Current.Text != f.Text {
		t.Errorf("current check = %+v, want the failing one", s.Checks.Current)
	}
}

func TestAMarkNeedsTheScreenUntouchedBetweenTheReadAndThePicture(t *testing.T) {
	each := machine.UIElement{ID: 9, Role: "StaticText", Value: "Each pays: $10.00", X: 0.5, Y: 0.6, W: 0.2, H: 0.05}
	s := newRun(t, "r1").live(machine.Ready).task(tipTask, 60).
		uiRead(100, "TipSplit", each). // step 1
		click(101, 0.55, 0.5).         // step 2
		screenshot(102).               // step 3
		verdict("fail", 266, fail("b", "Each pays becomes $50.00", "Each pays reads $10.00", 1, 3)).derive()
	if s.Failing.Picture == nil || s.Failing.Mark != nil {
		t.Errorf("picture %+v, mark %+v: want the picture and no mark across the click", s.Failing.Picture, s.Failing.Mark)
	}
}

func TestAFailingCheckWithOnlyAUIReadShowsTheFrameOfThatStep(t *testing.T) {
	s := newRun(t, "r1").live(machine.Ready).task(tipTask, 60).
		uiRead(100, "TipSplit").frame(95, 0).frame(102, 1).frame(110, 1).
		verdict("fail", 266, fail("b", "Each pays becomes $50.00", "Each pays reads $10.00", 1)).derive()
	p := s.Failing.Picture
	if p == nil || p.Kind != "frame" || p.File != fmtMilli(102) || s.Failing.Step != 1 {
		t.Errorf("picture = %+v, want the first frame of step 1", p)
	}
}

func fmtMilli(sec int) string { return fmt.Sprintf("%d.jpg", at(sec).UnixMilli()) }

func TestLastFrameIsTheNewestFrame(t *testing.T) {
	s := newRun(t, "r1").live(machine.Ready).frame(10, 0).frame(20, 0).derive()
	if s.LastFrame == nil || s.LastFrame.Kind != "frame" || s.LastFrame.URL != "/api/runs/r1/frames/"+fmtMilli(20) {
		t.Errorf("last frame = %+v", s.LastFrame)
	}
	if none := newRun(t, "r2").derive(); none.LastFrame != nil {
		t.Errorf("a run with no frames has a last frame: %+v", none.LastFrame)
	}
}

func TestTheNameIsTheAgentsElseTheTasks(t *testing.T) {
	given := newRun(t, "r1").named("TipSplit: split the bill", "claude-code").task(tipTask, 10).derive()
	if given.Name != "TipSplit: split the bill" || given.Source != "Claude Code" {
		t.Errorf("name %q source %q", given.Name, given.Source)
	}
	long := newRun(t, "r2").named("TipSplit: split the bill between three people fairly", "").derive()
	if long.Name != "TipSplit: split the bill between…" {
		t.Errorf("a long name is %q, want it cut to five words", long.Name)
	}
	derived := newRun(t, "r3").task(tipTask, 10).derive()
	if derived.Name != "TipSplit" || derived.Source != "" {
		t.Errorf("derived name %q source %q", derived.Name, derived.Source)
	}
}

func TestAFinishedRunSaysItsOutcome(t *testing.T) {
	cases := map[string]string{
		session.OutcomeVerified: "Verified", session.OutcomeUnverified: "Unverified", session.OutcomeAbandoned: "Abandoned",
	}
	for outcome, want := range cases {
		b := newRun(t, "r1").task(tipTask, 10)
		if outcome == session.OutcomeVerified {
			b = b.verdict("pass", 20, pass("a", "x", "x")).accept(session.Coder, 21)
		}
		if got := b.finish(outcome, 30).ended(31).derive().Outcome; got != want {
			t.Errorf("%s: outcome = %q, want %q", outcome, got, want)
		}
	}
	if got := newRun(t, "r2").derive().Outcome; got != "" {
		t.Errorf("an unfinished run has outcome %q", got)
	}
}

func TestTheMachineIsWorded(t *testing.T) {
	cases := []struct {
		st   machine.Status
		want string
	}{{machine.Booting, "starting"}, {machine.Ready, "on"}, {machine.Rebooting, "restarting"}, {machine.Failed, "not running"}}
	for _, c := range cases {
		if got := newRun(t, "r1").live(c.st).derive().Machine.Status; got != c.want {
			t.Errorf("%s: machine status %q, want %q", c.st, got, c.want)
		}
	}
	if got := newRun(t, "r2").derive().Machine; got.Status != "off" || got.Warning != "" {
		t.Errorf("no machine: %+v", got)
	}
	if got := newRun(t, "r3").live(machine.Ready).lowOnFiles().derive().Machine.Warning; got != LowOnFilesWarning {
		t.Errorf("warning = %q", got)
	}
}

func TestNowNamesEachKindOfStepInPlainWords(t *testing.T) {
	cases := []struct {
		name string
		step func(*builder) *builder
		want string
	}{
		{"typing", func(b *builder) *builder {
			return b.uiRead(80, "TipSplit").step("machine_input", 90, map[string]any{"actions": []map[string]any{{"type": "type", "text": "120"}}}, nil, "")
		}, "Typing 120 in TipSplit"},
		{"a key with modifiers", func(b *builder) *builder {
			return b.step("machine_input", 90, map[string]any{"actions": []map[string]any{{"type": "key", "key": "q", "mods": []string{"cmd"}}}}, nil, "")
		}, "Pressing Command-Q"},
		{"a double click with no UI read", func(b *builder) *builder {
			return b.step("machine_input", 90, map[string]any{"actions": []map[string]any{{"type": "click", "x": 0.1, "y": 0.1, "clicks": 2}}}, nil, "")
		}, "Double-clicking"},
		{"a UI read", func(b *builder) *builder { return b.uiRead(90, "TipSplit") }, "Reading TipSplit"},
		{"the read that checks a click's effect names the click", func(b *builder) *builder {
			b = b.uiRead(80, "TipSplit", tip25).click(90, 0.55, 0.5).uiRead(91, "TipSplit", tip25)
			b.in.Steps[len(b.in.Steps)-1].Effect = &machine.StepEffect{Of: b.in.Steps[len(b.in.Steps)-2].Seq, Kind: machine.EffectChanged}
			return b
		}, "Clicking 25% in TipSplit"},
		{"a build", func(b *builder) *builder {
			return b.step("machine_exec", 90, map[string]any{"command": "cd ~/work/TipSplit && swift build 2>&1 | tail"}, nil, "")
		}, "Building the app"},
		{"tests", func(b *builder) *builder {
			return b.step("machine_exec", 90, map[string]any{"command": "swift test --parallel"}, nil, "")
		}, "Running tests"},
		{"any other command", func(b *builder) *builder {
			return b.step("machine_exec", 90, map[string]any{"command": "ls -la ~/work"}, nil, "")
		}, "Running a command"},
		{"a sync", func(b *builder) *builder { return b.step("machine_sync", 90, nil, nil, "") }, "Copying files to the Mac"},
		{"a build script", func(b *builder) *builder {
			return b.step("machine_exec", 90, map[string]any{"command": "cd ~/work/tipsplit && sh build.sh"}, nil, "")
		}, "Building the app"},
		{"a pull", func(b *builder) *builder { return b.step("machine_pull", 90, nil, nil, "") }, "Copying files from the Mac"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newRun(t, "r1").live(machine.Ready).event("machine is ready", 40).task(tipTask, 60)
			if got := c.step(b).now(100).derive().Now; got != c.want {
				t.Errorf("now = %q, want %q", got, c.want)
			}
		})
	}
}
