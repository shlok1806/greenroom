package bench

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

// Issue #154: every result names its brain and describer, and a report over a file that mixes
// describers says so in its header and breaks the numbers down by them.
func TestTheReportNamesTheDescriberAndBreaksDownMixedModels(t *testing.T) {
	omni := &machine.Models{Brain: machine.BrainNIM, Model: "nvidia/ultra", Vision: "nvidia/omni",
		ModelOptions: nim.ChatOptions(), VisionOptions: nim.DescribeOptions("nvidia/omni")}
	muse := &machine.Models{Brain: machine.BrainNIM, Model: "nvidia/ultra", Vision: "meta/muse-glimmer-30b",
		ModelOptions: nim.ChatOptions(), VisionOptions: nim.DescribeOptions("meta/muse-glimmer-30b")}
	results := []Result{
		res("m1", 1, KindMutant, SplitDev, EndVerdict, "pass"),
		res("m1", 2, KindMutant, SplitDev, EndVerdict, "fail"),
		res("c1", 1, KindCorrect, SplitDev, EndVerdict, "pass"),
	}
	results[0].Models, results[1].Models, results[2].Models = omni, muse, muse

	// The line on disk carries the models and their request options.
	line, _ := json.Marshal(results[1])
	for _, want := range []string{`"models":{"brain":"nim","model":"nvidia/ultra","vision":"meta/muse-glimmer-30b"`,
		`"visionOptions":{"chat_template_kwargs":{"enable_thinking":false},"max_tokens":2048,"temperature":0.2}`} {
		if !strings.Contains(string(line), want) {
			t.Errorf("result line lacks %s\n%s", want, line)
		}
	}

	out := Report("/tmp/r.jsonl", results, time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC), ReportOptions{})
	museLabel := "nim nvidia/ultra, describer meta/muse-glimmer-30b {\"enable_thinking\":false}"
	for _, want := range []string{
		"- Models (brain and describer): `" + museLabel + "`, `nim nvidia/ultra, describer nvidia/omni`.",
		"## By models",
		"| Metric | `" + museLabel + "` | `nim nvidia/ultra, describer nvidia/omni` |",
		"| False pass rate, per trial | 0/1 (0.0%), upper 95.0% | 1/1 (100.0%), upper 100.0% |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q\n%s", want, out)
		}
	}

	// One describer: no breakdown.
	if out := Report("/tmp/r.jsonl", results[1:], time.Now(), ReportOptions{}); strings.Contains(out, "## By models") {
		t.Errorf("a file with one describer got a By models section\n%s", out)
	}
}
