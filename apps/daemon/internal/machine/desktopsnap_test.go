package machine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/desktop"
	"github.com/shlok1806/greenroom/apps/daemon/internal/guestagent"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// The desktop toolkit's calls (daemon ADR 0006) run the real manager on the fake tart, whose fake
// guest agent answers each toolkit op from a canned agent-<op>.json.

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

// toolkitMachine is a ready machine on a 64x48 screen with the toolkit on, whose capture shows ink
// in (4,4)-(24,14) only, and its manager and control directory.
func toolkitMachine(t *testing.T, extra ...Option) (*Manager, *Machine, string) {
	t.Helper()
	mgr, control := agentManager(t, fastAgent(), extra...)
	smallDesktop(t, control)
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.Set(x, y, color.White)
		}
	}
	for y := 6; y < 12; y++ {
		for x := 6; x < 22; x += 2 {
			img.Set(x, y, color.Black)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(base64.StdEncoding.EncodeToString(buf.Bytes())), 0o644); err != nil {
		t.Fatal(err)
	}
	return mgr, readyMachine(t, mgr), control
}

// stepsOf reads the run's steps, by number.
func stepsOf(t *testing.T, mgr *Manager, runID string) map[int]Step {
	t.Helper()
	steps, err := mgr.Steps(runID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]Step{}
	for _, s := range steps {
		out[s.Seq] = s
	}
	return out
}

// agentRequests are the fake agent's requests of op, decoded.
func agentRequests(t *testing.T, control, op string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range testsupport.ControlLines(t, control, "agent-requests") {
		var r map[string]any
		if json.Unmarshal([]byte(line), &r) == nil && r["op"] == op {
			out = append(out, r)
		}
	}
	return out
}

const snapshotJSON = `{"screen":{"width":64,"height":48},"frontmost":{"name":"Navlab","bundleId":"dev.greenroom.navlab","pid":7},
"app":{"name":"Navlab","bundleId":"dev.greenroom.navlab","pid":7},"focused":"e4",
"nodes":[{"ref":"e1","role":"Window","name":"Navlab","frame":[0,0,64,48],"vis":[0,0,64,48],"depth":0},
{"ref":"e4","role":"StaticText","name":"Total","frame":[4,4,20,10],"vis":[4,4,20,10],"depth":1,"window":"e1"},
{"ref":"e5","role":"StaticText","name":"Hidden text","frame":[30,20,20,10],"vis":[30,20,20,10],"depth":1,"window":"e1"},
{"ref":"e6","role":"TextField","name":"Password","value":"hunter2","states":["secret"],"chars":7,"frame":[4,30,20,10],"vis":[4,30,20,10],"depth":1,"window":"e1"}]}`

// A snapshot is a look and a step holding the whole structured snapshot; text the screen does not
// show is marked [not drawn], and a secure field's value never reaches the step or the outline.
func TestASnapshotIsAStepWithItsWholeTreeAndMarksTextThatIsNotDrawn(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "snapshot", snapshotJSON)
	snap, err := mgr.Snapshot(context.Background(), mc.RunID, HolderVerifier, desktop.SnapshotArgs{App: "Navlab"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Nodes) != 4 || snap.Unrendered != "" {
		t.Fatalf("snapshot %+v", snap)
	}
	byRef := map[string]desktop.Node{}
	for _, n := range snap.Nodes {
		byRef[n.Ref] = n
	}
	if byRef["e4"].Has(desktop.StateNotDrawn) || !byRef["e5"].Has(desktop.StateNotDrawn) {
		t.Errorf("ink test: e4 %v, e5 %v; want only e5 not drawn", byRef["e4"].States, byRef["e5"].States)
	}
	if !strings.Contains(snap.Text, `e5 StaticText "Hidden text" [not drawn]`) || !strings.Contains(snap.Text, "frontmost") {
		t.Errorf("outline:\n%s", snap.Text)
	}
	req := agentRequests(t, control, "snapshot")
	if len(req) != 1 || req[0]["reader"] != HolderVerifier || req[0]["input"] == true {
		t.Fatalf("requests %v, want one read as the verifier", req)
	}
	if args, _ := req[0]["args"].(map[string]any); args["app"] != "Navlab" || args["mode"] != "interactive" || args["limit"] != float64(250) {
		t.Errorf("args %v, want the defaults filled in", args)
	}
	step := stepsOf(t, mgr, mc.RunID)[snap.Step]
	if step.Tool != "machine_snapshot" || step.By != HolderVerifier || step.Error != "" {
		t.Fatalf("step %+v", step)
	}
	raw, _ := json.Marshal(step.Output)
	if !strings.Contains(string(raw), `"ref":"e5"`) || !strings.Contains(string(raw), "notDrawn") {
		t.Errorf("the step does not hold the whole snapshot: %s", raw)
	}
	data, _ := os.ReadFile(filepath.Join(mc.Dir, "steps.jsonl"))
	if bytes.Contains(data, []byte("hunter2")) || strings.Contains(snap.Text, "hunter2") {
		t.Error("a secure field's value reached the step log or the outline")
	}
	if mustGet(t, mgr, mc.RunID).staleLook(HolderVerifier) {
		t.Error("a snapshot is not counted as the verifier's look")
	}
}

// A capture that fails leaves the snapshot unmarked and says why, as machine_ui does.
func TestASnapshotWhoseCaptureFailsSaysItsTextWasNotChecked(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "snapshot", snapshotJSON)
	if err := os.Remove(filepath.Join(control, "shot.b64")); err != nil {
		t.Fatal(err)
	}
	snap, err := mgr.Snapshot(context.Background(), mc.RunID, HolderCoder, desktop.SnapshotArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Unrendered == "" || !strings.Contains(snap.Text, "text not checked against the screen") {
		t.Errorf("unrendered %q, text:\n%s", snap.Unrendered, snap.Text)
	}
}

func TestAFindReturnsItsMatchesAsRefs(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "find", `{"matches":[{"ref":"e45","role":"Button","name":"Details","window":"e1","offscreen":"below","scroller":"e20"}],"searched":412}`)
	f, err := mgr.Find(context.Background(), mc.RunID, HolderCoder, desktop.FindArgs{Text: "Details"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Matches) != 1 || !strings.Contains(f.Text, `1 match for "Details" among 412 elements`) || !strings.Contains(f.Text, "e45") {
		t.Errorf("find %+v\n%s", f.FindResult, f.Text)
	}
	if s := stepsOf(t, mgr, mc.RunID)[f.Step]; s.Tool != "machine_find" || s.By != HolderCoder {
		t.Errorf("step %+v", s)
	}
}

// A bad argument is refused before anything is claimed: no step, no request, no lease.
func TestABadToolkitArgumentRecordsNothing(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	before := len(stepsOf(t, mgr, mc.RunID))
	if _, err := mgr.Snapshot(context.Background(), mc.RunID, HolderCoder, desktop.SnapshotArgs{Ref: "17"}); err == nil ||
		!strings.Contains(err.Error(), "machine_ui's element numbers are not refs") {
		t.Fatalf("got %v", err)
	}
	if _, err := mgr.Find(context.Background(), mc.RunID, HolderCoder, desktop.FindArgs{}); err == nil {
		t.Fatal("a find with no text was not refused")
	}
	if steps := stepsOf(t, mgr, mc.RunID); len(steps) != before {
		t.Errorf("recorded %v", steps)
	}
	if n := len(testsupport.ControlLines(t, control, "agent-requests")); n != 0 {
		t.Errorf("%d requests reached the agent", n)
	}
}

// A ref from an earlier connection to the agent is refused before anything is sent (daemon ADR
// 0005 point 10), and a fresh snapshot makes refs usable again.
func TestARefFromAnEarlierConnectionIsRefusedBeforeAnythingIsSent(t *testing.T) {
	mgr, mc, control := toolkitMachine(t)
	can(t, control, "snapshot", snapshotJSON)
	if _, err := mgr.Snapshot(context.Background(), mc.RunID, HolderCoder, desktop.SnapshotArgs{}); err != nil {
		t.Fatal(err)
	}
	sup := mgr.agentSupervisor(mustGet(t, mgr, mc.RunID))
	first, err := sup.Conn(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.Flag(t, control, "agent-exit")
	_, _ = mgr.Find(context.Background(), mc.RunID, HolderVerifier, desktop.FindArgs{Text: "x"}) // the agent dies on it
	if err := os.Remove(filepath.Join(control, "agent-exit")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, "a new connection", func() bool {
		c, err := sup.Conn(context.Background(), 0)
		return err == nil && c.Gen() > first.Gen()
	})
	before := len(agentRequests(t, control, "snapshot"))
	_, err = mgr.Snapshot(context.Background(), mc.RunID, HolderCoder, desktop.SnapshotArgs{Ref: "e4"})
	if err == nil || err.Error() != "e4 is from an earlier connection to the guest agent (it restarted); take a new machine_snapshot" ||
		!errors.Is(err, ErrStaleRef) {
		t.Fatalf("got %v", err)
	}
	if n := len(agentRequests(t, control, "snapshot")); n != before {
		t.Fatal("the stale ref was sent to the agent")
	}
	if _, err := mgr.Snapshot(context.Background(), mc.RunID, HolderCoder, desktop.SnapshotArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Snapshot(context.Background(), mc.RunID, HolderCoder, desktop.SnapshotArgs{Ref: "e4"}); err != nil {
		t.Fatalf("a ref from the new connection: %v", err)
	}
}

// Toolkit ops never fall back to an exec: without the agent they fail and say why.
func TestToolkitOpsNeverFallBackToExec(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mc := readyMachine(t, mgr)
	_, err := mgr.Snapshot(context.Background(), mc.RunID, HolderCoder, desktop.SnapshotArgs{})
	if !errors.Is(err, guestagent.ErrUnavailable) || !strings.Contains(err.Error(), "-desktop-toolkit") {
		t.Fatalf("got %v", err)
	}
}
