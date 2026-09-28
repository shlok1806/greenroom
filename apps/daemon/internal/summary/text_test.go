package summary

import (
	"strings"
	"testing"
)

// The tasks are real first tasks from runs on the dogfooding host (docs/19), trimmed.
func TestANameIsMadeFromTheTaskInFiveWordsOrFewer(t *testing.T) {
	cases := []struct{ task, want string }{
		{tipTask, "TipSplit"},
		{"TipSplit (a SwiftUI tip calculator) is running on screen. Verify it through the UI only.", "TipSplit"},
		{"The HelloGreenroom app (already running, source in ~/work/HelloGreenroom) gained a feature: a \"Your name\" text field under the heading.", "HelloGreenroom: Your name"},
		{"New feature in HelloGreenroom (running, black window): a segmented \"Accent\" picker with Blue / Green / Orange.", "HelloGreenroom: Accent"},
		{"Groceries, a shopping list app I just built, is running on screen. Add an item named Apples.", "Groceries"},
		{"Check the Greenroom Companion's new \"More\" dropdown (issue #179) in the app already open on this machine's screen.", "Greenroom Companion: More"},
		{"Please verify a Greenroom Companion fix on this machine's screen. Issue #146: the Companion's transcript is blank.", "Greenroom Companion: issue 146"},
		{"Look around the synced project in ~/work: list it, read its README, and tell me what it is.", "Look around the synced project"},
		{"verify that the build works", "verify that the build works"},
		{"", "Untitled run"},
	}
	for _, c := range cases {
		got := NameFromTask(c.task)
		if got != c.want {
			t.Errorf("NameFromTask(%.50q) = %q, want %q", c.task, got, c.want)
		}
		if n := len(strings.Fields(got)); n > NameWords {
			t.Errorf("%q has %d words", got, n)
		}
		if again := NameFromTask(c.task); again != got {
			t.Errorf("the name is not deterministic: %q then %q", got, again)
		}
	}
}

func TestTheAgentsNameWinsAndIsCutToFiveWords(t *testing.T) {
	if got := Name("  TipSplit:   split the bill ", tipTask); got != "TipSplit: split the bill" {
		t.Errorf("got %q", got)
	}
	if got := Name("one two three four five six", ""); got != "one two three four five…" {
		t.Errorf("got %q", got)
	}
	if got := Name("", tipTask); got != "TipSplit" {
		t.Errorf("an empty name falls back to the task: %q", got)
	}
}

func TestSourceWordsNameTheClient(t *testing.T) {
	cases := map[string]string{
		"claude-code": "Claude Code", "codex-mcp-client": "Codex", "cursor-vscode": "Cursor", "gemini-cli": "Gemini CLI",
		"": "", "my-own_agent": "my own agent", "a-very-long-client-name-here": "a very long…",
	}
	for in, want := range cases {
		if got := SourceWords(in); got != want {
			t.Errorf("SourceWords(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisagreementFindsTheValuesThatDiffer(t *testing.T) {
	cases := []struct{ criterion, observed, expected, saw string }{
		{"Each pays becomes $50.00 at 25%", "Each pays reads $10.00 with 25% selected", "$50.00", "$10.00"},
		{"Each pays is $48.00 for 3 people", "With Bill 120 and 3 people, Each pays reads $8.00", "$48.00", "$8.00"},
		{"Each pays reads \"$48.00\"", "The label reads \"$8.00\"", "“$48.00”", "“$8.00”"},
		{"The heading reads \"Hello, Ada, from Greenroom\"", "The heading reads \"Hello, from Greenroom\"", "“Hello, Ada, from Greenroom”", "“Hello, from Greenroom”"},
		{"Tip is 20%", "Tip shows 18%", "20%", "18%"},
		{"The window has three buttons", "Only two buttons show", "", ""},
		{"Count is 1,200", "Count is 1,200", "", ""},
	}
	for _, c := range cases {
		e, s := Disagreement(c.criterion, c.observed)
		if e != c.expected || s != c.saw {
			t.Errorf("Disagreement(%q, %q) = %q, %q; want %q, %q", c.criterion, c.observed, e, s, c.expected, c.saw)
		}
	}
}

func TestPlainDropsTheVerifiersRecordCitations(t *testing.T) {
	cases := map[string]string{
		"Run B's row reads \"No verdict\" (step 5, element 7); when opened it is blank (steps 26, 32).": "Run B's row reads \"No verdict\"; when opened it is blank.",
		"Each pays reads $10.00 (UI read 6).":                    "Each pays reads $10.00.",
		"I read it with machine_ui and then machine_frobnicate.": "I read it with a read of the screen and then a tool.",
		"It passes (for now).":                                   "It passes (for now).",
		"Run A passes.\n\n[greenroom] Reported as fail; posted as inconclusive because its evidence does not hold": "Run A passes.",
	}
	for in, want := range cases {
		if got := plain(in); got != want {
			t.Errorf("plain(%q) = %q, want %q", in, got, want)
		}
	}
}
