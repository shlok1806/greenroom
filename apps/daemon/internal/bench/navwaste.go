package bench

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// Navigation waste (docs/21 sections 1 and 9): how the verifier spent its calls getting around
// the desktop, read from each trial's steps.jsonl. The classes follow docs/21 section 1.1's
// method, cut down to what a step record says without the transcript:
//
//   - a failed call that is not a refusal: tool error or timeout;
//   - a toolkit action the daemon refused after its checks, by reason (covered, offscreen,
//     disabled, ...): the toolkit names the navigation failure itself;
//   - an input whose effect was no change: by input type, as docs/21 does (a click is misaimed, a
//     scroll went to the wrong scroll area, a key or typed text had no effect);
//   - a look right after a look of the same tool with no input between: a re-look;
//   - an input batch of sleeps alone: waiting by polling;
//   - machine_exec running osascript, open or pkill: UI driven through the shell.
//
// A step is counted once, in the first class that fits, so the classes add up.

// Waste classes, in the order they are checked and reported.
const (
	WasteToolError    = "tool error or timeout"
	WasteRefused      = "refused by actionability"
	WasteMisaimed     = "misaimed click (no change)"
	WasteWrongScroll  = "wrong scroll area (no change)"
	WasteDeadKey      = "key or text with no effect"
	WasteNoChange     = "other input with no change"
	WasteReLook       = "redundant re-look"
	WastePolling      = "waiting by polling or sleep"
	WasteExecUI       = "UI through machine_exec"
	wasteRefusedLabel = "refused: "
)

// NavStats is one set of trials' navigation numbers.
type NavStats struct {
	Trials   int     // trials read
	Verdicts int     // of them, ending in a verdict
	CallsP50 float64 // verifier calls per verdict
	CallsP90 float64
	MinP50   float64 // minutes per verdict, from the task to the verdict
	MinP90   float64
	Calls    int            // verifier steps read over every trial
	Wasted   map[string]int // wasted steps by class; refusals also by reason under "refused: <reason>"
	Unread   int            // trials whose run directory could not be read
}

// WastedTotal is the wasted steps over every class, refusals counted once.
func (s NavStats) WastedTotal() int {
	n := 0
	for class, c := range s.Wasted {
		if !strings.HasPrefix(class, wasteRefusedLabel) {
			n += c
		}
	}
	return n
}

// NavigationStats reads each trial's steps and classifies the verifier's. readSteps is
// machine.ReadSteps in production.
func NavigationStats(results []Result, readSteps func(dir string) ([]machine.Step, error)) NavStats {
	st := NavStats{Wasted: map[string]int{}}
	var calls, minutes []float64
	for _, r := range results {
		if r.Ending == EndSetupError {
			continue
		}
		st.Trials++
		if r.Ending == EndVerdict {
			st.Verdicts++
			calls = append(calls, float64(r.Steps))
			minutes = append(minutes, r.Seconds/60)
		}
		if r.RunDir == "" {
			st.Unread++
			continue
		}
		steps, err := readSteps(r.RunDir)
		if err != nil {
			st.Unread++
			continue
		}
		for _, class := range classifySteps(steps) {
			if class == "" {
				st.Calls++
				continue
			}
			st.Calls++
			if reason, ok := strings.CutPrefix(class, wasteRefusedLabel); ok {
				st.Wasted[WasteRefused]++
				st.Wasted[wasteRefusedLabel+reason]++
				continue
			}
			st.Wasted[class]++
		}
	}
	st.CallsP50, st.CallsP90 = Percentile(calls, 50), Percentile(calls, 90)
	st.MinP50, st.MinP90 = Percentile(minutes, 50), Percentile(minutes, 90)
	return st
}

var (
	observationTools = []string{"machine_ui", "machine_screenshot", "machine_snapshot", "machine_find"}
	shellUI          = regexp.MustCompile(`\b(osascript|open\s+-a|open\s+-b|pkill|killall)\b`)
)

// classifySteps returns one class per verifier step, "" for a step that was not wasted.
func classifySteps(all []machine.Step) []string {
	var steps []machine.Step
	for _, s := range all {
		if s.By == machine.HolderVerifier {
			steps = append(steps, s)
		}
	}
	// The effect of an input, by its step: on the input itself (a toolkit action) or on the
	// UI read that followed it (the old tools, ADR 0024).
	effect := map[int]string{}
	for _, s := range steps {
		if s.Effect != nil {
			effect[s.Effect.Of] = s.Effect.Kind
		}
	}
	out := make([]string, len(steps))
	lastLook := ""
	for i, s := range steps {
		class := classifyStep(s, effect[s.Seq])
		if class == "" && slices.Contains(observationTools, s.Tool) && s.Error == "" {
			if s.Tool == lastLook && (s.Effect == nil || s.Effect.Of == s.Seq) {
				class = WasteReLook
			}
		}
		switch {
		case slices.Contains(observationTools, s.Tool) && s.Error == "":
			if s.Effect != nil && s.Effect.Of != s.Seq {
				lastLook = "" // an effect read belongs to its input
			} else {
				lastLook = s.Tool
			}
		case isInputStep(s.Tool):
			lastLook = ""
		}
		out[i] = class
	}
	return out
}

func isInputStep(tool string) bool {
	switch tool {
	case "machine_input", "machine_press", "machine_type", "machine_set_value", "machine_key", "machine_scroll":
		return true
	}
	return false
}

// classifyStep is one step's class apart from re-looks, which need the steps before it.
func classifyStep(s machine.Step, effect string) string {
	if s.Error != "" {
		if reason := refusalReason(s); reason != "" {
			return wasteRefusedLabel + reason
		}
		if strings.Contains(s.Error, "driving this machine") || strings.Contains(s.Error, "changed hands") {
			return "" // the lease, not navigation
		}
		return WasteToolError
	}
	if s.Tool == "machine_exec" {
		if cmd, _ := field(s.Input, "command").(string); shellUI.MatchString(cmd) {
			return WasteExecUI
		}
		return ""
	}
	if !isInputStep(s.Tool) {
		return ""
	}
	kind := inputKind(s)
	if kind == "sleep" {
		return WastePolling
	}
	if effect != machine.EffectNone {
		return ""
	}
	switch kind {
	case "click", "press":
		return WasteMisaimed
	case "scroll":
		return WasteWrongScroll
	case "key", "type":
		return WasteDeadKey
	}
	return WasteNoChange
}

// refusalReason is the actionability reason of a refused toolkit action, or "".
func refusalReason(s machine.Step) string {
	for _, key := range []string{"refusal", "refused"} {
		if r, ok := field(s.Output, key).(map[string]any); ok {
			if reason, _ := r["reason"].(string); reason != "" {
				return reason
			}
		}
	}
	if isInputStep(s.Tool) && s.Tool != "machine_input" && strings.HasPrefix(s.Error, "refused") {
		return "other"
	}
	return ""
}

// inputKind names what an input step did: click, scroll, key, type, sleep (a batch of sleeps
// alone), press (a toolkit press), or mixed.
func inputKind(s machine.Step) string {
	switch s.Tool {
	case "machine_press":
		return "press"
	case "machine_type", "machine_set_value":
		return "type"
	case "machine_key":
		return "key"
	case "machine_scroll":
		return "scroll"
	}
	actions, _ := field(s.Input, "actions").([]any)
	kinds := map[string]bool{}
	for _, a := range actions {
		if t, ok := field(a, "type").(string); ok {
			kinds[strings.ToLower(t)] = true
		}
	}
	delete(kinds, "move")
	if len(kinds) == 1 {
		for k := range kinds {
			return k
		}
	}
	if len(kinds) == 2 && kinds["sleep"] {
		for k := range kinds {
			if k != "sleep" {
				return k
			}
		}
	}
	return "mixed"
}

// field reads key from a decoded JSON object, or nil.
func field(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

// navigationSection renders the Navigation section: calls and minutes per verdict, and the
// wasted calls by class, one column per set.
func navigationSection(heads []string, sets []NavStats) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("| | %s |\n|---|%s\n", strings.Join(heads, " | "), strings.Repeat("---|", len(heads)))
	row := func(name string, cell func(NavStats) string) {
		cells := make([]string, len(sets))
		for i, s := range sets {
			cells[i] = cell(s)
		}
		p("| %s | %s |\n", name, strings.Join(cells, " | "))
	}
	row("Trials (verdicts)", func(s NavStats) string { return fmt.Sprintf("%d (%d)", s.Trials, s.Verdicts) })
	row("Calls per verdict, p50 / p90", func(s NavStats) string { return fmt.Sprintf("%.0f / %.0f", s.CallsP50, s.CallsP90) })
	row("Minutes per verdict, p50 / p90", func(s NavStats) string { return fmt.Sprintf("%.1f / %.1f", s.MinP50, s.MinP90) })
	row("Verifier calls read", func(s NavStats) string { return fmt.Sprint(s.Calls) })
	row("Wasted calls", func(s NavStats) string {
		if s.Calls == 0 {
			return "0"
		}
		return fmt.Sprintf("%d (%.0f%%)", s.WastedTotal(), 100*float64(s.WastedTotal())/float64(s.Calls))
	})
	classes := []string{WasteToolError, WasteRefused, WasteMisaimed, WasteWrongScroll, WasteDeadKey, WasteNoChange,
		WasteReLook, WastePolling, WasteExecUI}
	var reasons []string
	for _, s := range sets {
		for class := range s.Wasted {
			if strings.HasPrefix(class, wasteRefusedLabel) && !slices.Contains(reasons, class) {
				reasons = append(reasons, class)
			}
		}
	}
	sort.Strings(reasons)
	for _, class := range classes {
		row("- "+class, func(s NavStats) string { return fmt.Sprint(s.Wasted[class]) })
		if class == WasteRefused {
			for _, r := range reasons {
				row("  - "+r, func(s NavStats) string { return fmt.Sprint(s.Wasted[r]) })
			}
		}
	}
	if slices.ContainsFunc(sets, func(s NavStats) bool { return s.Unread > 0 }) {
		row("Trials whose steps could not be read", func(s NavStats) string { return fmt.Sprint(s.Unread) })
	}
	return b.String()
}
