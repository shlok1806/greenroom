package bench

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

func steps(t *testing.T, lines ...string) []machine.Step {
	t.Helper()
	out := make([]machine.Step, len(lines))
	for i, l := range lines {
		if err := json.Unmarshal([]byte(l), &out[i]); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
	}
	return out
}

// The old tools: an input's effect is on the UI read after it, a look after a look is a re-look,
// a scroll that changed nothing went to the wrong scroll area, a batch of sleeps is polling.
func TestClassifyStepsOfTheOldTools(t *testing.T) {
	got := classifySteps(steps(t,
		`{"seq":1,"tool":"machine_ui","by":"verifier"}`,
		`{"seq":2,"tool":"machine_ui","by":"verifier"}`,
		`{"seq":3,"tool":"machine_input","by":"verifier","input":{"actions":[{"type":"click","x":0.5,"y":0.5}]}}`,
		`{"seq":4,"tool":"machine_ui","by":"verifier","effect":{"of":3,"kind":"none"}}`,
		`{"seq":5,"tool":"machine_input","by":"verifier","input":{"actions":[{"type":"move","x":0.5,"y":0.8},{"type":"scroll","deltaY":200}]}}`,
		`{"seq":6,"tool":"machine_ui","by":"verifier","effect":{"of":5,"kind":"none"}}`,
		`{"seq":7,"tool":"machine_input","by":"verifier","input":{"actions":[{"type":"sleep","ms":2000}]}}`,
		`{"seq":8,"tool":"machine_screenshot","by":"verifier","error":"the guest screen is not answering"}`,
		`{"seq":9,"tool":"machine_exec","by":"verifier","input":{"command":"osascript -e 'tell app \"NavLab\" to activate'"}}`,
		`{"seq":10,"tool":"machine_exec","by":"verifier","input":{"command":"ls ~/work"}}`,
		`{"seq":11,"tool":"machine_input","by":"coder","input":{"actions":[{"type":"click","x":0.1,"y":0.1}]}}`,
		`{"seq":12,"tool":"machine_input","by":"verifier","error":"a human is driving this machine; try again in a moment"}`,
	))
	want := []string{"", WasteReLook, WasteMisaimed, "", WasteWrongScroll, "", WastePolling, WasteToolError, WasteExecUI, "", ""}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("classes\n got %q\nwant %q", got, want)
	}
}

// The toolkit: an action carries its own effect, and a refusal names its reason.
func TestClassifyStepsOfTheToolkit(t *testing.T) {
	got := classifySteps(steps(t,
		`{"seq":1,"tool":"machine_snapshot","by":"verifier"}`,
		`{"seq":2,"tool":"machine_press","by":"verifier","error":"refused: e41 is covered","output":{"refusal":{"reason":"covered"}}}`,
		`{"seq":3,"tool":"machine_press","by":"verifier","effect":{"of":3,"kind":"changed"}}`,
		`{"seq":4,"tool":"machine_type","by":"verifier","effect":{"of":4,"kind":"none"}}`,
		`{"seq":5,"tool":"machine_expect","by":"verifier"}`,
		`{"seq":6,"tool":"machine_snapshot","by":"verifier"}`,
		`{"seq":7,"tool":"machine_snapshot","by":"verifier"}`,
	))
	want := []string{"", "refused: covered", "", WasteDeadKey, "", "", WasteReLook}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("classes\n got %q\nwant %q", got, want)
	}
}

func TestNavigationStatsAddsUpAndRendersBothSets(t *testing.T) {
	logs := map[string][]machine.Step{
		"/old": steps(t,
			`{"seq":1,"tool":"machine_ui","by":"verifier"}`,
			`{"seq":2,"tool":"machine_ui","by":"verifier"}`,
			`{"seq":3,"tool":"machine_input","by":"verifier","input":{"actions":[{"type":"click","x":0.5,"y":0.5}]}}`,
			`{"seq":4,"tool":"machine_ui","by":"verifier","effect":{"of":3,"kind":"none"}}`),
		"/kit": steps(t,
			`{"seq":1,"tool":"machine_snapshot","by":"verifier"}`,
			`{"seq":2,"tool":"machine_press","by":"verifier","error":"refused","output":{"refusal":{"reason":"covered"}}}`),
	}
	read := func(dir string) ([]machine.Step, error) {
		if s, ok := logs[dir]; ok {
			return s, nil
		}
		return nil, errors.New("no such run")
	}
	old := NavigationStats([]Result{
		{Ending: EndVerdict, Steps: 4, Seconds: 120, RunDir: "/old"},
		{Ending: EndSetupError, RunDir: "/old"},
		{Ending: EndLimit, Steps: 40, RunDir: "/gone"},
	}, read)
	if old.Trials != 2 || old.Verdicts != 1 || old.Calls != 4 || old.WastedTotal() != 2 || old.Unread != 1 ||
		old.CallsP50 != 4 || old.MinP50 != 2 {
		t.Errorf("old = %+v", old)
	}
	kit := NavigationStats([]Result{{Ending: EndVerdict, Steps: 2, Seconds: 60, RunDir: "/kit", Toolkit: true}}, read)
	if kit.Wasted[WasteRefused] != 1 || kit.Wasted["refused: covered"] != 1 || kit.WastedTotal() != 1 {
		t.Errorf("kit = %+v", kit)
	}
	text := navigationSection([]string{"old tools", "toolkit"}, []NavStats{old, kit})
	for _, want := range []string{"| | old tools | toolkit |", "| Calls per verdict, p50 / p90 | 4 / 4 | 2 / 2 |",
		"| Wasted calls | 2 (50%) | 1 (50%) |", "|   - refused: covered | 0 | 1 |", "| - redundant re-look | 1 | 0 |",
		"| Trials whose steps could not be read | 1 | 0 |"} {
		if !strings.Contains(text, want) {
			t.Errorf("section lacks %q:\n%s", want, text)
		}
	}
}
