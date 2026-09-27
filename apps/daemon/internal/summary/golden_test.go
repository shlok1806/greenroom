package summary

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// figmaRuns are the runs docs/20's Figma screens show (section 12): TipSplit failed 2 of 4
// (m03), WordCount passed and waiting on you (m01), UnitConvert checking (m02), a verifier
// paused at its limit (m01), a screen not answering (m07a), plus a run starting and a run done.
func figmaRuns(t *testing.T) []Input {
	t.Helper()
	tipChecks := []string{
		"Window shows Bill, Tip and People",
		"Tip is $24.00 for $120 at 20%",
		"Each pays is $48.00 for 3 people",
		"Each pays becomes $50.00 at 25%",
	}
	eachPays := machine.UIElement{ID: 12, Role: "StaticText", Value: "Each pays: $10.00", X: 0.63, Y: 0.66, W: 0.23, H: 0.05}

	tipSplit := newRun(t, "20260923-044138-de31017819a86d84").named("TipSplit: split the bill", "claude-code").
		live(machine.Ready).event("machine is ready", 34).task(tipTask, 60).
		plan(70, tipChecks...).
		uiRead(100, "TipSplit", tip25, eachPays). // step 1
		screenshot(101).                          // step 2
		click(150, 0.55, 0.5).                    // step 3
		uiRead(160, "TipSplit", eachPays).        // step 4
		screenshot(161).                          // step 5
		frame(162, 5).
		verdict("fail", 266,
			pass("window", tipChecks[0], "The window shows Bill, Tip and People."),
			pass("tip", tipChecks[1], "Tip reads $24.00 (UI read 1)."),
			// Failed checks first, as the verifier lists them and m03 shows them.
			fail("each-25", tipChecks[3], "After choosing 25%, Each pays reads $10.00 (steps 4, 5).", 4, 5, 3),
			fail("each", tipChecks[2], "With Bill 120 and 3 people, Each pays reads $8.00 (step 1).", 1, 2)).
		now(266 + 12*60)

	wordCount := newRun(t, "20260923-025814-1feb83aa6cc4b6c8").named("WordCount: case buttons", "claude-code").
		live(machine.Ready).event("machine is ready", 30).
		task("WordCount has UPPERCASE and lowercase buttons that change the text. Check both.", 40).
		verdict("pass", 250,
			pass("upper", "UPPERCASE turns the text to capitals", "The text reads HELLO WORLD."),
			pass("lower", "lowercase turns the text to small letters", "The text reads hello world."),
			pass("count", "The word count stays 2", "Words: 2"),
			pass("window", "Window shows the text and both buttons", "Both buttons show.")).
		frame(251, 0).now(250 + 8*60)

	unitConvert := newRun(t, "20260923-051502-7ab01d2c55e0f913").named("UnitConvert: Temperature", "codex-mcp-client").
		live(machine.Ready).event("machine is ready", 31).
		task("UnitConvert converts temperatures. Enter 100 in Celsius and check Fahrenheit reads 212.", 40).
		plan(45, "Celsius 100 shows Fahrenheit 212", "Celsius 0 shows Fahrenheit 32", "Clearing the field clears the result", "Window shows both fields").
		uiRead(60, "UnitConvert", machine.UIElement{ID: 3, Role: "Button", Label: "Convert", X: 0.4, Y: 0.4, W: 0.1, H: 0.04}).
		click(70, 0.4, 0.4).frame(71, 2).now(72)

	paused := newRun(t, "20260923-060011-44c1d0e9a2b3f581").named("WordCount: longest word", "claude-code").
		live(machine.Ready).event("machine is ready", 30).
		task("I added a \"Longest word\" line to WordCount. Check it shows the longest word.", 40).
		plan(50, "Longest word shows the longest word", "Longest word updates as you type").
		msg(session.Verifier, session.Reply, "I ran out of time after 10m0s. Send a message and I will continue.", 640,
			func(m *session.Message) { m.Stop = session.StopTime }).
		frame(641, 0).now(700)

	notAnsweringRun := newRun(t, "20260923-070129-9f00e1a4b27c3d65").named("TodoList: summary line", "claude-code").
		live(machine.Ready).event("machine is ready", 30).
		task("TodoList shows a summary line under the list. Check it counts done items.", 40).
		screenshot(60).
		step("machine_screenshot", 200, nil, nil, notAnswering).
		step("machine_ui", 262, nil, nil, notAnswering).
		frame(61, 1).now(262)

	starting := newRun(t, "20260923-071502-0c1d2e3f40516273").named("TodoList: add item", "claude-code").
		live(machine.Booting).boot(machine.PhaseClone, machine.PhaseStart, machine.PhaseAgent).now(24)

	done := newRun(t, "20260922-221040-5566778899aabbcc").named("WordCount: keeps text", "claude-code").
		task("WordCount keeps the text after switching case. Check it.", 40).
		verdict("pass", 180, pass("keeps", "The text is kept after switching case", "The text reads the same.")).
		accept(session.Human, 200).finish(session.OutcomeVerified, 210).event("machine destroyed", 212).ended(212).
		frame(170, 0).now(2 * 24 * 3600)

	var out []Input
	for _, b := range []*builder{tipSplit, wordCount, unitConvert, paused, notAnsweringRun, starting, done} {
		out = append(out, b.build())
	}
	return out
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run go test -update to write it): %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed; run go test ./internal/summary -update and review the diff.\ngot:\n%s", path, got)
	}
}

func TestTheFigmaRunsMatchTheirGolden(t *testing.T) {
	var runs []Summary
	for _, in := range figmaRuns(t) {
		runs = append(runs, Derive(in))
	}
	board := NewBoard(runs, MacsFree(3, 1))
	got, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "board.golden.json", append(got, '\n'))
}

// The TipSplit run reads as the m03 screen does.
func TestTheFailedTipSplitReadsAsTheFigmaScreen(t *testing.T) {
	s := Derive(figmaRuns(t)[0])
	checks := []struct{ field, got, want string }{
		{"name", s.Name, "TipSplit: split the bill"},
		{"source", s.Source, "Claude Code"},
		{"status", s.Status, "Failed"},
		{"tally", s.Checks.Text, "2 of 4 checks failed"},
		{"detail", s.Detail, "Proposed by the verifier after 3:26."},
		{"primary", s.PrimaryAction.Label, "Accept fail"},
		{"secondary", s.SecondaryActions[0].Label, "Reject"},
		{"failing", s.Failing.Text, "Each pays becomes $50.00 at 25%"},
		{"expected", s.Failing.Expected, "$50.00"},
		{"saw", s.Failing.Saw, "$10.00"},
		{"picture", s.Failing.Picture.File, "005-screenshot.png"},
		{"group", string(s.Group), string(NeedsYou)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
}

// defaultWords are the words a UI shows for a summary without being asked: the header (name,
// source, status, tally, the one sentence or now, the actions, the machine warning) and the
// selected check with its values. Observed and pictures are one click away.
func defaultWords(s Summary) []string {
	parts := []string{s.Name, s.Status, s.Checks.Text, s.Detail, s.Now, s.Machine.Warning}
	if s.Source != "" {
		parts = append(parts, "from "+s.Source)
	}
	if s.PrimaryAction != nil {
		parts = append(parts, s.PrimaryAction.Label)
	}
	for _, a := range s.SecondaryActions {
		parts = append(parts, a.Label)
	}
	if s.Failing != nil {
		parts = append(parts, s.Failing.Text)
		if s.Failing.Expected != "" || s.Failing.Saw != "" {
			parts = append(parts, "Expected "+s.Failing.Expected+", saw "+s.Failing.Saw)
		}
	} else if s.Checks.Current != nil {
		parts = append(parts, s.Checks.Current.Text)
	}
	var words []string
	for _, p := range parts {
		words = append(words, strings.Fields(p)...)
	}
	return words
}

// rowWords are a runs list row's words: the name and, for a failure, "2 failed".
func rowWords(s Summary) []string {
	words := strings.Fields(s.Name)
	if s.Checks.Failed > 0 {
		words = append(words, strconv.Itoa(s.Checks.Failed), "failed")
	}
	return words
}

// budget is docs/20 section 6's ceiling for the screen a summary heads.
func budget(s Summary) (string, int) {
	switch {
	case s.State == Starting || s.State == Restarting:
		return "booting", 20
	case s.Group == Done:
		return "finished", 50
	case s.PrimaryAction != nil && s.PrimaryAction.ID == ActAccept:
		return "verdict waiting for you", 70
	}
	return "live", 60
}

func TestEverySummaryStaysWithinTheTextBudget(t *testing.T) {
	for _, in := range figmaRuns(t) {
		s := Derive(in)
		screen, limit := budget(s)
		// Beside a checks list (eight words a row) the header leaves the list most of the
		// budget: it takes at most half. Booting shows no checks.
		ceiling := limit / 2
		if screen == "booting" {
			ceiling = limit
		}
		if n := len(defaultWords(s)); n > ceiling {
			t.Errorf("%s (%s): %d words by default, over %d of the %d budget: %q", s.Name, screen, n, ceiling, limit, defaultWords(s))
		}
		if n := len(rowWords(s)); n > 6 {
			t.Errorf("%s: its row has %d words, over 6", s.Name, n)
		}
		if n := len(strings.Fields(s.Name)); n > NameWords {
			t.Errorf("%s: name of %d words", s.Name, n)
		}
		if s.Checks.Current != nil && len(strings.Fields(s.Checks.Current.Text)) > checkWords {
			t.Errorf("%s: check row %q over %d words", s.Name, s.Checks.Current.Text, checkWords)
		}
	}
}

// forbidden are the mechanisms and internals docs/20 section 2, problem 5 found on the default
// view. None may reach a summary's words.
var forbidden = []*regexp.Regexp{
	regexp.MustCompile(`\b(machine|agent|run|report|declare)_[a-z_]+\b`), // tool names
	regexp.MustCompile(`(?i)\btart\b`),
	regexp.MustCompile(`(?i)greenroom-(base|lean|xcode)`), // image names
	regexp.MustCompile(`(?i)\b\d+(\.\d+)?\s?ms\b`),        // timings in milliseconds
	regexp.MustCompile(`(?i)\bUI read\b`),
	regexp.MustCompile(`(?i)\bsteps? \d+`),
	regexp.MustCompile(`(?i)\belement \d+`),
	regexp.MustCompile(`(?i)\b(superseded|unreviewed|contested|verdict state|step record)\b`),
	regexp.MustCompile(`(?i)context deadline|exit status|stdout|stderr|vsock|json`),
	regexp.MustCompile(`(?i)\b(ip|pid|fd)\b`),
	regexp.MustCompile(`(?i)\[greenroom\]`),
	regexp.MustCompile(`#\d+`), // issue numbers
}

// texts are every string of a summary a person could read.
func texts(s Summary) []string {
	out := []string{s.Name, s.Source, s.Status, s.Detail, s.Now, s.Checks.Text, s.Machine.Status, s.Machine.Warning, s.Outcome}
	if s.Checks.Current != nil {
		out = append(out, s.Checks.Current.Text)
	}
	if f := s.Failing; f != nil {
		out = append(out, f.Text, f.Expected, f.Saw, f.Observed)
	}
	if s.PrimaryAction != nil {
		out = append(out, s.PrimaryAction.Label)
	}
	for _, a := range s.SecondaryActions {
		out = append(out, a.Label)
	}
	return out
}

func TestNoSummaryUsesAToolNameTimingOrInternalTerm(t *testing.T) {
	inputs := figmaRuns(t)
	// And every status of the table test, so each state's sentences are checked too.
	inputs = append(inputs, statusRuns(t)...)
	for _, in := range inputs {
		s := Derive(in)
		for _, text := range texts(s) {
			for _, re := range forbidden {
				if re.MatchString(text) {
					t.Errorf("%s (%s): %q matches forbidden %s", s.Name, s.Status, text, re)
				}
			}
			if strings.ContainsRune(text, 0x2014) { // an em dash
				t.Errorf("%s: %q has an em dash", s.Name, text)
			}
		}
	}
}

// statusRuns is one run in each status, with the most text each can carry.
func statusRuns(t *testing.T) []Input {
	t.Helper()
	stop := func(m *session.Message) { m.Stop = session.StopSteps }
	return []Input{
		newRun(t, "a").live("rebooting").boot("stop").build(),
		newRun(t, "b").live(machine.Ready).lowOnFiles().task(tipTask, 10).build(),
		newRun(t, "c").live(machine.Ready).task(tipTask, 10).msg(session.Verifier, session.Reply, "I used 40 tool calls (steps 1-40).", 20, stop).build(),
		newRun(t, "d").live(machine.Ready).task(tipTask, 10).msg(session.Verifier, session.Question, "Should I use machine_exec to read ~/TipSplit (step 4)?", 20).build(),
		newRun(t, "e").live(machine.Failed).event("machine failed to boot: tart clone: exit status 1", 5).build(),
		newRun(t, "f").task(tipTask, 10).verdict("pass", 20, pass("a", "x", "y")).accept(session.Coder, 21).finish(session.OutcomeVerified, 30).ended(31).build(),
		newRun(t, "g").task(tipTask, 10).event("human destroyed the machine", 20).event("machine destroyed", 21).ended(21).build(),
	}
}

func TestTheBoardGroupsCountsAndOrders(t *testing.T) {
	older := Summary{RunID: "old", Group: NeedsYou, Since: at(10)}
	newer := Summary{RunID: "new", Group: NeedsYou, Since: at(20)}
	done := Summary{RunID: "done", Group: Done, Since: at(5), UpdatedAt: at(99)}
	b := NewBoard([]Summary{older, done, newer}, MacsFree(3, 1))
	if len(b.Groups) != 3 || b.Groups[0].ID != NeedsYou || b.Groups[1].ID != Running || b.Groups[2].ID != Done {
		t.Fatalf("groups = %+v, want Needs you, Running, Done", b.Groups)
	}
	if b.Groups[0].Count != 2 || b.Groups[0].Runs[0].RunID != "new" || b.Groups[0].Title != "Needs you" {
		t.Errorf("needs you = %+v, want 2 runs, newest first", b.Groups[0])
	}
	if b.Groups[1].Count != 0 || b.Groups[1].Runs == nil {
		t.Errorf("running = %+v, want an empty list, not null", b.Groups[1])
	}
	if b.Macs.Text != "2 of 3 Macs free" || !b.UpdatedAt.Equal(at(99)) {
		t.Errorf("macs %+v updated %s", b.Macs, b.UpdatedAt)
	}
}

func TestMacsFree(t *testing.T) {
	cases := []struct {
		limit, inUse int
		want         Macs
	}{
		{3, 1, Macs{Free: 2, Total: 3, Text: "2 of 3 Macs free"}},
		{1, 1, Macs{Free: 0, Total: 1, Text: "0 of 1 Mac free"}},
		{2, 5, Macs{Free: 0, Total: 2, Text: "0 of 2 Macs free"}}, // foreign or failed machines over the limit
		{0, 4, Macs{}},
	}
	for _, c := range cases {
		if got := MacsFree(c.limit, c.inUse); !reflect.DeepEqual(got, c.want) {
			t.Errorf("MacsFree(%d, %d) = %+v, want %+v", c.limit, c.inUse, got, c.want)
		}
	}
}
