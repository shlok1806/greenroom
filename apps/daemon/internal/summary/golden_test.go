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

	// Not a Figma screen: a create whose image was not on the host (issue #286, root ADR 0049).
	didNotStart := newRun(t, "20260922-190455-a1b2c3d4e5f60718").named("TipSplit: round each share", "claude-code").
		step(machine.StepCreate, 0, map[string]any{"image": "greenroom-lean-a"}, nil,
			`tart clone greenroom-lean-a greenroom-20260922-190455-a1b2c3d4e5f60718: exit status 2: the specified VM "greenroom-lean-a" does not exist`).
		ended(0).now(2 * 24 * 3600)

	var out []Input
	for _, b := range []*builder{tipSplit, wordCount, unitConvert, paused, notAnsweringRun, starting, done, didNotStart} {
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
		{"tally", s.Checks.Text, "2 of 4 checks"},
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

// defaultWords are the words the run's page shows without being asked, all from the summary:
// the header (name, source, status, tally, the one sentence or now, the actions, the machine
// warning), every check as a row with the value a failed one saw, and under the picture the
// selected check's setup and its two values. What was observed, in full, is one click away.
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
	for _, row := range s.Checks.Items {
		parts = append(parts, row.Text)
		switch {
		case row.Saw == "":
		case row.State == "fail":
			parts = append(parts, "saw "+row.Saw)
		default:
			parts = append(parts, row.Saw) // a pass shows the value it read, alone
		}
	}
	if f := s.Failing; f != nil {
		parts = append(parts, f.Setup)
		if f.Expected != "" || f.Saw != "" {
			parts = append(parts, "Expected "+f.Expected+", saw "+f.Saw)
		}
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

// budget is docs/20 section 6's ceiling for the page a summary heads. A page that lists a
// verdict's checks has the verdict page's 70 whether the verdict still waits or is closed: the
// rows are the same. The live run's closed verdict, four checks in the verifier's own words,
// comes to 53 words, which the finished page's 50 does not hold; criteria as short as the
// Figma's do fit it.
func budget(s Summary) (string, int) {
	switch {
	case (s.State == Starting || s.State == Restarting) && len(s.Checks.Items) == 0:
		return "booting", 20
	case len(s.Checks.Items) > 0 && (s.State == Passed || s.State == Failed || s.State == Inconclusive):
		return "verdict", 70
	case s.Group == Done:
		return "finished", 50
	}
	return "live", 60
}

func TestEverySummaryStaysWithinTheTextBudget(t *testing.T) {
	for _, in := range figmaRuns(t) {
		withinBudget(t, Derive(in))
	}
}

// withinBudget checks a summary's words against docs/20 section 6: the run's page, a runs
// list row of six words, a name of five and check rows of eight.
func withinBudget(t *testing.T, s Summary) {
	t.Helper()
	screen, limit := budget(s)
	if n := len(defaultWords(s)); n > limit {
		t.Errorf("%s (%s): %d words by default, over the budget of %d: %q", s.Name, screen, n, limit, defaultWords(s))
	}
	if n := len(rowWords(s)); n > 6 {
		t.Errorf("%s: its row has %d words, over 6", s.Name, n)
	}
	if n := len(strings.Fields(s.Name)); n > NameWords {
		t.Errorf("%s: name of %d words", s.Name, n)
	}
	for _, row := range s.Checks.Items {
		if n := len(strings.Fields(row.Text)); n > checkWords {
			t.Errorf("%s: check row %q has %d words, over %d", s.Name, row.Text, n, checkWords)
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
	out := []string{s.Name, s.Source, s.Status, s.Detail, s.Now, s.Checks.Text, s.Machine.Status, s.Machine.Warning,
		s.Machine.Ended, s.Outcome}
	for _, row := range s.Checks.Items {
		out = append(out, row.Text, row.Saw)
	}
	if f := s.Failing; f != nil {
		out = append(out, f.Text, f.Setup, f.Expected, f.Saw, f.Observed)
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
		newRun(t, "a").live(machine.Rebooting).boot(machine.PhaseStop).build(),
		newRun(t, "b").live(machine.Ready).lowOnFiles().task(tipTask, 10).build(),
		newRun(t, "c").live(machine.Ready).task(tipTask, 10).msg(session.Verifier, session.Reply, "I used 40 tool calls (steps 1-40).", 20, stop).build(),
		newRun(t, "d").live(machine.Ready).task(tipTask, 10).msg(session.Verifier, session.Question, "Should I use machine_exec to read ~/TipSplit (step 4)?", 20).build(),
		newRun(t, "e").live(machine.Failed).event("machine failed to boot: tart clone: exit status 1", 5).build(),
		newRun(t, "f").task(tipTask, 10).verdict("pass", 20, pass("a", "x", "y")).accept(session.Coder, 21).finish(session.OutcomeVerified, 30).ended(31).build(),
		newRun(t, "g").task(tipTask, 10).event("human destroyed the machine", 20).event("machine destroyed", 21).ended(21).build(),
		newRun(t, "h").step(machine.StepCreate, 0, nil, nil, "tart clone greenroom-lean-a greenroom-h: exit status 2").ended(0).build(),
		newRun(t, "i").live(machine.Failed).step(machine.StepCreate, 0, nil, nil, "").
			step(machine.StepBoot, 180, nil, nil, "timed out waiting for the guest agent (machine_wait)").
			event("machine failed: timed out waiting for the guest agent", 180).build(),
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
