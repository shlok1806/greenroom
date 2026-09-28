package verifier

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// The verifier with -desktop-toolkit (daemon ADR 0006 point 9), on the real manager whose fake
// guest agent answers each toolkit op from a canned agent-<op>.json.

// readyToolkit is ready on a daemon run with -desktop-toolkit.
func readyToolkit(t *testing.T) (*machine.Manager, string, string) {
	t.Helper()
	bin, control := testsupport.FakeTart(t)
	mgr, err := machine.NewManager(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		machine.WithTartBin(bin), machine.WithReadyTimeout(10*time.Second), machine.WithFrameInterval(0),
		machine.WithSSHProbe(func(context.Context, string, string) error { return nil }), machine.WithDesktopToolkit(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, mc := range mgr.List() {
			_, _ = mgr.Wait(context.Background(), mc.RunID, 15*time.Second)
			_ = mgr.Destroy(context.Background(), mc.RunID)
		}
	})
	mc, err := mgr.Create(context.Background(), "img")
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 20*time.Second)
	if err != nil || got.Status != machine.Ready {
		t.Fatalf("machine not ready: %+v %v", got, err)
	}
	return mgr, mc.RunID, control
}

// can writes the fake agent's canned answer to op: a result, or {"error": ...} when body has one.
func can(t *testing.T, control, op, body string) {
	t.Helper()
	if !strings.HasPrefix(strings.TrimSpace(body), `{"error"`) {
		body = `{"result":` + body + `}`
	}
	if err := os.WriteFile(filepath.Join(control, "agent-"+op+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const toolkitSnapshot = `{"screen":{"width":1024,"height":768},"frontmost":{"name":"TipSplit","pid":7},"app":{"name":"TipSplit","pid":7},
"nodes":[{"ref":"e1","role":"Window","name":"TipSplit","frame":[0,0,400,300],"vis":[0,0,400,300]},
{"ref":"e62","role":"StaticText","name":"Each pays","value":"$48.00","depth":1,"window":"e1"}]}`

// deliveredTools is the names of the tools the model was offered in request n.
func deliveredTools(t *testing.T, model *scriptedModel, n int) []string {
	t.Helper()
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(model.request(t, n)), &req); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tool := range req.Tools {
		out = append(out, tool.Function.Name)
	}
	return out
}

func toolNames(ts []nim.Tool) []string {
	out := make([]string, len(ts))
	for i, tool := range ts {
		out[i] = tool.Name
	}
	return out
}

// Without the toolkit the verifier's tools and prompt are what they were; with it, it gets the
// toolkit's tools beside the old ones, and a prompt section on using them.
func TestTheToolkitsToolsAndPromptComeOnlyWithTheToolkit(t *testing.T) {
	if got := toolsFor(false); !slices.Equal(toolNames(got), toolNames(tools)) {
		t.Errorf("without the toolkit the tools are %v", toolNames(got))
	}
	if systemPromptFor(false) != systemPrompt {
		t.Error("without the toolkit the system prompt changed")
	}
	on := toolNames(toolsFor(true))
	for _, name := range append(slices.Clone(toolkitLooks), toolNames(tools)...) {
		if !slices.Contains(on, name) {
			t.Errorf("with the toolkit %s is missing", name)
		}
	}

	mgr, runID, _ := readyToolkit(t)
	model := &scriptedModel{replies: []string{verdictOf("inconclusive", "Nothing was checked.")}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the total.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	if got := deliveredTools(t, model, 1); !slices.Contains(got, "machine_snapshot") || !slices.Contains(got, "machine_ui") {
		t.Errorf("the model was offered %v", got)
	}
	system := deliveredText(t, model, 1, "system")
	for _, want := range []string{"With the desktop toolkit", "Start with machine_snapshot and read its attention line first",
		"machine_find", "Before any click, call machine_ui"} {
		if !strings.Contains(system, want) {
			t.Errorf("the toolkit prompt lacks %q:\n%s", want, system)
		}
	}
	if i, j := strings.Index(system, "With the desktop toolkit"), strings.Index(system, "Rules:"); i < 0 || j < i {
		t.Error("the toolkit section is not before the rules")
	}
}

// A snapshot is an observation: a check with no actions may pass on it.
func TestASnapshotIsEvidence(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "snapshot", toolkitSnapshot)
	first := lastStep(t, mgr, runID) + 1
	model := &scriptedModel{replies: []string{
		declared("total"),
		toolCall("machine_snapshot", map[string]any{"app": "TipSplit"}),
		verdictOf("pass", "Each pays shows $48.00 (step 1).", answer("total", "pass", []int{first})),
	}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "Check the total.")
	res, err := v.Turn(context.Background(), runID, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended != session.Verdict {
		t.Fatalf("ended with %s: %+v", res.Ended, lastMessage(t, store))
	}
	if got := lastMessage(t, store); got.Verdict != "pass" {
		t.Errorf("verdict %+v", got)
	}
	progress := messagesOfKind(store, session.Progress)
	if !strings.Contains(progress[len(progress)-1].Text, `e62 StaticText "Each pays" value="$48.00"`) {
		t.Errorf("the model did not read the outline: %q", progress[len(progress)-1].Text)
	}
	steps, _ := mgr.Steps(runID)
	if s := steps[len(steps)-1]; s.Tool != "machine_snapshot" || s.By != machine.HolderVerifier {
		t.Errorf("step %+v", s)
	}
}

func TestManualSnapshotAndFind(t *testing.T) {
	mgr, runID, control := readyToolkit(t)
	can(t, control, "snapshot", toolkitSnapshot)
	can(t, control, "find", `{"matches":[{"ref":"e62","role":"StaticText","name":"Each pays","value":"$48.00"}],"searched":2}`)
	store := openStore(t, mgr, runID)
	post(t, store, session.Message{From: session.Human, Kind: session.Note, Text: "snapshot TipSplit\nfind Each pays"})
	if _, err := NewManual(mgr, testLog()).Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}
	progress := messagesOfKind(store, session.Progress)
	if len(progress) != 2 || !strings.HasPrefix(progress[0].Text, `machine_snapshot {"app":"TipSplit"}`) ||
		!strings.Contains(progress[1].Text, `1 match for "Each pays"`) {
		t.Fatalf("progress %+v", progress)
	}

	// Without the toolkit the verbs say what they need.
	plain, plainRun, _ := ready(t)
	plainStore := openStore(t, plain, plainRun)
	post(t, plainStore, session.Message{From: session.Human, Kind: session.Note, Text: "snapshot"})
	if _, err := NewManual(plain, testLog()).Turn(context.Background(), plainRun, plainStore); err != nil {
		t.Fatal(err)
	}
	if last := lastMessage(t, plainStore); !strings.Contains(last.Text, "-desktop-toolkit") {
		t.Errorf("snapshot without the toolkit: %q", last.Text)
	}
}
