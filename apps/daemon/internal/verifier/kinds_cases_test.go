package verifier

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	imagepng "image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// The two false passes of the first bench baseline (ADR 0025) that ADR 0027 is for, replayed
// with a scripted model that answers the way the baseline's verifier did. Under ADR 0024 alone
// both passes were posted; now each is refused, naming the rule, and the fail that the evidence
// supports is posted instead.

// invisibleEachPaysUI is TipSplit with the tipsplit-each-pays-invisible patch: the tree reports
// Each pays, while the screen (invisibleEachPaysShot) draws it in the window's background.
const invisibleEachPaysUI = `{"app":{"name":"TipSplit","pid":7},"apps":["TipSplit"],"screen":{"width":1024,"height":768},
"truncated":false,"elements":[
{"role":"AXWindow","title":"TipSplit","depth":0,"frame":{"x":292,"y":183,"w":440,"h":373}},
{"role":"AXTextField","identifier":"bill","value":"84.00","depth":1,"frame":{"x":402,"y":305,"w":140,"h":24}},
{"role":"AXStaticText","value":"Tip: $15.12","identifier":"tip","depth":1,"frame":{"x":402,"y":390,"w":120,"h":18}},
{"role":"AXStaticText","value":"Each pays: $49.56","identifier":"perPerson","depth":1,"frame":{"x":402,"y":420,"w":260,"h":34}}]}`

// invisibleEachPaysShot draws the window as the patch leaves it: the bill and the Tip line in
// ink, Each pays in the window's own colour.
func invisibleEachPaysShot(t *testing.T, control string) {
	t.Helper()
	bg := color.NRGBA{R: 236, G: 236, B: 236, A: 255}
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 768))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{R: 40, G: 60, B: 90, A: 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(292, 183, 732, 556), image.NewUniform(bg), image.Point{}, draw.Src)
	for _, s := range []struct {
		text string
		at   image.Point
		ink  color.Color
	}{{"84.00", image.Pt(406, 322), color.Black}, {"Tip: $15.12", image.Pt(404, 404), color.Black},
		{"Each pays: $49.56", image.Pt(404, 442), bg}} {
		d := font.Drawer{Dst: img, Src: image.NewUniform(s.ink), Face: basicfont.Face7x13, Dot: fixed.P(s.at.X, s.at.Y)}
		d.DrawString(s.text)
	}
	var buf bytes.Buffer
	if err := imagepng.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "shot.b64"), []byte(base64.StdEncoding.EncodeToString(buf.Bytes())), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tipsplit-each-pays-invisible: the baseline verifier read the tree and passed in 3 steps. Now
// the claim of visibility is a visual check that needs a screenshot, and the tree read marks
// Each pays not drawn, so neither check can pass on it.
func TestBenchCaseInvisibleEachPaysCannotPassOnTheTree(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, invisibleEachPaysUI)
	invisibleEachPaysShot(t, control)
	look := lastStep(t, mgr, runID) + 1
	shot := look + 1
	baseline := verdictOf("pass", "Each pays reads $49.56 in large text (step 2).",
		said(answer("visible", "pass", []int{look}), "Each pays: $49.56 is shown under the Tip line."),
		said(answer("total", "pass", []int{look}), "Each pays reads $49.56."))
	model := &scriptedModel{vision: "A TipSplit window with a bill of 84.00 and Tip: $15.12. Under the Tip line is empty window background.",
		replies: []string{
			toolCall("declare_checks", map[string]any{"checks": []map[string]any{
				{"id": "visible", "criterion": "Each pays is visible on screen in large text"},
				{"id": "total", "criterion": "Each pays reads $49.56 with the defaults"},
			}}),
			toolCall("machine_ui", map[string]any{}),
			baseline,
			toolCall("machine_screenshot", map[string]any{}),
			verdictOf("fail", "Each pays is in the tree (step 2) but not drawn: the screenshot shows empty background under Tip (step 3).",
				said(answer("visible", "fail", []int{look, shot}), "The screenshot shows no Each pays line."),
				said(answer("total", "fail", []int{look, shot}), "The tree says $49.56, but nothing is drawn there.")),
		}}
	v := newVerifier(t, mgr, model.start(t))
	store := openStore(t, mgr, runID)
	postTask(t, store, "I restyled TipSplit's result: Each pays is shown in large bold text under the Tip line. It is running "+
		"on screen. With the defaults it should read \"Each pays: $49.56\". Check that it is visible and correct.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 4 {
		t.Fatalf("%d progress messages, want declare, ui, the refused pass, screenshot", len(prog))
	}
	if c := prog[0].Checks; len(c) != 2 || !c[0].Is(session.CheckVisual) || !c[1].Is(session.CheckValue) {
		t.Errorf("declared = %+v, want visible upgraded to visual and total a value", c)
	}
	if !strings.Contains(prog[1].Text, `value="Each pays: $49.56" id="perPerson" [not drawn]`) ||
		strings.Contains(prog[1].Text, `"Tip: $15.12" id="tip" [not drawn]`) {
		t.Errorf("the UI read = %s, want only Each pays marked not drawn", prog[1].Text)
	}
	refused := prog[2].Text
	for _, want := range []string{"error: report_verdict refused; nothing was posted",
		`check "visible" (visual): it is a visual check, and no machine_screenshot`,
		`check "total" (rendered): machine_ui step ` + itoa(look) + ` marks [4] StaticText "Each pays: $49.56" not drawn`} {
		if !strings.Contains(refused, want) {
			t.Errorf("refusal = %q, want %q", refused, want)
		}
	}
	verdicts := messagesOfKind(store, session.Verdict)
	if len(verdicts) != 1 || verdicts[0].Verdict != "fail" || !verdicts[0].Checks[0].Is(session.CheckVisual) {
		t.Fatalf("verdicts = %+v, want only the fail, carrying the visual kind", verdicts)
	}
}

// todoUI is TodoList with an Add field and button, and the items given.
func todoUI(field string, items ...string) string {
	var b strings.Builder
	b.WriteString(`{"app":{"name":"TodoList","pid":8},"apps":["TodoList"],"screen":{"width":1024,"height":768},"truncated":false,"elements":[` +
		`{"role":"AXTextField","label":"New item","value":"` + field + `","depth":0,"frame":{"x":380,"y":200,"w":200,"h":22}},` +
		`{"role":"AXButton","title":"Add","depth":0,"frame":{"x":590,"y":200,"w":50,"h":22}}`)
	for i, it := range items {
		b.WriteString(`,{"role":"AXCheckBox","title":"` + it + `","value":"0","depth":0,"frame":{"x":380,"y":` + itoa(240+30*i) + `,"w":120,"h":20}}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// todolist-late-add: an added item appears 8 s after Add; the task says at once. The baseline
// verifier saw the item and passed, and under ADR 0024 the late look is fresh evidence. Now "at
// once" makes the check timing: the late look cannot pass it, and the read right after Add,
// which does not show Milk, is the evidence for the fail.
func TestBenchCaseLateAddCannotPassOnALateLook(t *testing.T) {
	mgr, runID, control := ready(t)
	putUI(t, control, todoUI("Milk"))
	if err := os.WriteFile(filepath.Join(control, "exec-sleep"), []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	look := lastStep(t, mgr, runID) + 1
	add, effect, wait, late := look+1, look+2, look+3, look+4
	model := &scriptedModel{replies: []string{
		toolCall("declare_checks", map[string]any{"checks": []map[string]any{
			{"id": "milk", "criterion": "Milk appears in the list at once after Add"},
		}}),
		toolCall("machine_ui", map[string]any{}),
		toolCall("machine_click", map[string]any{"element": 2}),
		toolCall("machine_exec", map[string]any{"command": "sleep 3"}),
		toolCall("machine_ui", map[string]any{}),
		verdictOf("pass", "Milk is in the list (step 6).", answer("milk", "pass", []int{effect, late}, add)),
		verdictOf("fail", "Milk was not in the list right after Add (step 3); it showed up later (step 6).",
			said(answer("milk", "fail", []int{effect, late}, add), "The read right after Add has no Milk.")),
	}}
	// Add clears the field at once and adds the item later: in the fake, before the look after the wait.
	model.onReasoning = func(n int) {
		switch n {
		case 3:
			putUI(t, control, todoUI(""))
		case 4:
			putUI(t, control, todoUI("", "Milk"))
		}
	}
	v, err := New(mgr, Config{BaseURL: model.start(t), APIKey: "test-key", Model: "reasoner", VisionModel: "eyes",
		MaxSteps: 10, Budget: 60 * time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t, mgr, runID)
	postTask(t, store, "Adding an item in TodoList is instant: press Add and the item shows in the list right away. "+
		"It is running on screen. Add Milk and check it appears at once.")
	if _, err := v.Turn(context.Background(), runID, store); err != nil {
		t.Fatal(err)
	}

	prog := messagesOfKind(store, session.Progress)
	if len(prog) != 6 {
		t.Fatalf("%d progress messages, want declare, ui, click, exec, ui, then the refused pass", len(prog))
	}
	if c := prog[0].Checks; len(c) != 1 || !c[0].Is(session.CheckTiming) || c[0].Is(session.CheckVisual) || c[0].Within != 2 {
		t.Errorf("declared = %+v, want milk upgraded to timing within 2 s", c)
	}
	if !strings.Contains(prog[0].Text, `"milk" is timing: its criterion says "at once"`) {
		t.Errorf("declaration result = %q, want the upgrade explained", prog[0].Text)
	}
	if prog[3].Step != wait {
		t.Errorf("the wait was step %d, want %d", prog[3].Step, wait)
	}
	refused := lastProgressBefore(t, store, session.Verdict).Text
	for _, want := range []string{"error: report_verdict refused; nothing was posted",
		`check "milk" (timing): evidence step ` + itoa(late) + ` started`,
		"a later observation cannot pass a timing check"} {
		if !strings.Contains(refused, want) {
			t.Errorf("refusal = %q, want %q", refused, want)
		}
	}
	verdicts := messagesOfKind(store, session.Verdict)
	if len(verdicts) != 1 || verdicts[0].Verdict != "fail" {
		t.Fatalf("verdicts = %+v, want only the fail", verdicts)
	}
	if c := verdicts[0].Checks[0]; !c.Is(session.CheckTiming) || c.Within != 2 || c.Status != session.CheckFail {
		t.Errorf("posted check = %+v, want the timing fail with its window", c)
	}
}

// lastProgressBefore is the newest progress message before the first message of kind k.
func lastProgressBefore(t *testing.T, store *session.Store, k session.Kind) session.Message {
	t.Helper()
	var last session.Message
	for _, m := range store.After(0) {
		if m.Kind == k {
			return last
		}
		if m.Kind == session.Progress {
			last = m
		}
	}
	t.Fatalf("no %s in the conversation", k)
	return last
}
