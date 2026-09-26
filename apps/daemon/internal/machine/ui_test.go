package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// tipSplitUI is what the real helper printed for the TipSplit demo app on a
// 1024x768 guest (macOS 26.6.2): points, top-left origin.
const tipSplitUI = `{"app":{"name":"TipSplit","bundleId":"","pid":2187},"apps":["Finder","TipSplit"],
"screen":{"width":1024,"height":768},"truncated":false,"elements":[
{"role":"AXWindow","subrole":"AXStandardWindow","title":"TipSplit","depth":0,"frame":{"x":292,"y":183,"w":440,"h":373}},
{"role":"AXTextField","identifier":"bill","value":"84.00","focused":true,"depth":1,"frame":{"x":402,"y":305,"w":140,"h":24}},
{"role":"AXRadioGroup","depth":1,"frame":{"x":438,"y":347,"w":196,"h":24}},
{"role":"AXRadioButton","subrole":"AXSegment","label":"18%","selected":true,"depth":2,"frame":{"x":488,"y":347,"w":48,"h":24}},
{"role":"AXRadioButton","subrole":"AXSegment","label":"25%","depth":2,"frame":{"x":586,"y":347,"w":48,"h":24}},
{"role":"AXButton","subrole":"AXZoomButton","enabled":false,"depth":1,"frame":{"x":338,"y":183,"w":16,"h":16}}
]}`

func writeUI(t *testing.T, control, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, "ui.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The tree's frames must land in the same fractions machine_click posts, so
// clicking an element's center clicks the element.
func TestUIReportsElementCentersAsScreenFractions(t *testing.T) {
	mgr, _, control := newTestManager(t)
	writeUI(t, control, tipSplitUI)
	mc := readyMachine(t, mgr)

	tree, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "TipSplit", 0)
	if err != nil {
		t.Fatalf("UI: %v", err)
	}
	if tree.App != "TipSplit" || len(tree.Elements) != 6 || tree.Step == 0 {
		t.Fatalf("tree = %+v", tree)
	}
	seg := tree.Elements[4]
	// (586 + 48/2) / 1024 and (347 + 24/2) / 768.
	if seg.Label != "25%" || seg.X != 0.596 || seg.Y != 0.467 || seg.W != 0.047 || seg.H != 0.031 {
		t.Errorf("25%% segment = %+v, want center (0.596, 0.467) size 0.047x0.031", seg)
	}
	if seg.Role != "RadioButton" || seg.Subrole != "Segment" || seg.ID != 5 {
		t.Errorf("roles lose their AX prefix and ids count from 1: %+v", seg)
	}
	if !tree.Elements[3].Selected || !tree.Elements[1].Focused || !tree.Elements[5].Disabled || tree.Elements[4].Disabled {
		t.Errorf("state flags were lost: %+v", tree.Elements)
	}

	// The request travelled base64, never through a shell, and named the app.
	m := regexp.MustCompile(`--ui-base64 ([A-Za-z0-9+/=]+)`).FindStringSubmatch(testsupport.Calls(t, control))
	if m == nil {
		t.Fatal("the helper was never asked for the tree")
	}
	raw, _ := base64.StdEncoding.DecodeString(m[1])
	var req struct {
		App   string `json:"app"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &req); err != nil || req.App != "TipSplit" || req.Limit != DefaultUILimit {
		t.Errorf("request = %s (%v), want app TipSplit and the default limit", raw, err)
	}

	var found bool
	for _, s := range readSteps(t, mc.Dir) {
		if s.Tool == "machine_ui" && s.Seq == tree.Step {
			found = true
		}
	}
	if !found {
		t.Errorf("no machine_ui step %d was recorded", tree.Step)
	}
}

func TestUIOutlineIsOneIndentedLineAnElement(t *testing.T) {
	mgr, _, control := newTestManager(t)
	writeUI(t, control, tipSplitUI)
	mc := readyMachine(t, mgr)
	tree, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	out := tree.Outline()
	for _, want := range []string{
		"App: TipSplit (running: Finder, TipSplit). Screen 1024x768 points.",
		"\n[1] Window/StandardWindow \"TipSplit\" center (0.500, 0.481)",
		"\n  [2] TextField value=\"84.00\" id=\"bill\" focused center (0.461, 0.413)",
		"\n    [4] RadioButton/Segment label=\"18%\" selected center",
		"\n    [5] RadioButton/Segment label=\"25%\" center (0.596, 0.467) size 0.047x0.031\n",
		"ZoomButton disabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outline lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "[not drawn]") {
		t.Errorf("an outline with no marks explains them:\n%s", out)
	}
	// ADR 0027: each mark follows the element's state, and one legend line explains them.
	tree.Elements = []UIElement{
		{ID: 1, Role: "StaticText", Value: "Each pays: $49.56", Rendered: RenderedBlank, X: 0.5, Y: 0.5},
		{ID: 2, Role: "StaticText", Value: "Total", Selected: true, Rendered: RenderedOffscreen},
		{ID: 3, Role: "StaticText", Value: "Tip", Rendered: RenderedCovered},
	}
	out = tree.Outline()
	for _, want := range []string{
		"[not drawn]: the screen shows no text in its frame. [offscreen]: outside the screen. [covered]: under another window.",
		"\n[1] StaticText value=\"Each pays: $49.56\" [not drawn] center (0.500, 0.500)",
		"\n[2] StaticText value=\"Total\" selected [offscreen] center",
		"\n[3] StaticText value=\"Tip\" [covered] center",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outline lacks %q:\n%s", want, out)
		}
	}
}

func TestElementCenterAimsAtTheLatestRead(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, err := mgr.ElementCenter(mc.RunID, HolderCoder, 1, 0); !errors.Is(err, ErrNoUITree) {
		t.Errorf("before any read: err = %v, want ErrNoUITree", err)
	}
	writeUI(t, control, tipSplitUI)
	if _, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0); err != nil {
		t.Fatal(err)
	}
	e, err := mgr.ElementCenter(mc.RunID, HolderCoder, 5, 0)
	if err != nil || e.Label != "25%" {
		t.Errorf("element 5 = %+v, %v", e, err)
	}
	if e.Name() != `RadioButton/Segment "25%"` {
		t.Errorf("Name() = %q", e.Name())
	}
	if _, err := mgr.ElementCenter(mc.RunID, HolderCoder, 99, 0); err == nil || !strings.Contains(err.Error(), "machine_ui again") {
		t.Errorf("an unknown id: err = %v", err)
	}
}

// A failed read keeps the last good tree, and says why it failed.
func TestUIFailureIsReadable(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	writeUI(t, control, tipSplitUI)
	if _, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0); err != nil {
		t.Fatal(err)
	}
	testsupport.Flag(t, control, "input-down")
	_, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0)
	if err == nil || !strings.Contains(err.Error(), "Accessibility") {
		t.Errorf("err = %v, want the helper's message", err)
	}
	if _, err := mgr.ElementCenter(mc.RunID, HolderCoder, 5, 0); err != nil {
		t.Errorf("a failed read dropped the last good tree: %v", err)
	}
}

func TestUIClampsTheLimit(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	if _, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 5000); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`--ui-base64 ([A-Za-z0-9+/=]+)`).FindStringSubmatch(testsupport.Calls(t, control))
	raw, _ := base64.StdEncoding.DecodeString(m[1])
	if !strings.Contains(string(raw), `"limit":1000`) {
		t.Errorf("request = %s, want the limit capped at %d", raw, MaxUILimit)
	}
}

func TestUIFractionsRefuseAnEmptyScreen(t *testing.T) {
	if _, err := uiFractions(rawUITree{}); err == nil {
		t.Error("a 0x0 screen was accepted")
	}
}

// A walk stopped by the helper's own caps says so, and does not suggest a higher
// limit, which cannot help.
func TestUIOutlineSaysWhyTheWalkStopped(t *testing.T) {
	mgr, _, control := newTestManager(t)
	mc := readyMachine(t, mgr)
	for by, want := range map[string]string{
		"limit":   "raise limit",
		"visited": "a higher limit will not help",
		"depth":   "a higher limit will not help",
	} {
		writeUI(t, control, `{"app":{"name":"Big","pid":3},"apps":["Big"],"screen":{"width":1024,"height":768},`+
			`"truncated":true,"truncatedBy":"`+by+`","elements":[{"role":"AXButton","title":"OK","depth":0,"frame":{"x":10,"y":10,"w":40,"h":20}}]}`)
		tree, err := mgr.UI(context.Background(), mc.RunID, HolderCoder, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if tree.TruncatedBy != by || !strings.Contains(tree.Outline(), want) {
			t.Errorf("truncatedBy %q: tree says %q, outline:\n%s\nwant %q", by, tree.TruncatedBy, tree.Outline(), want)
		}
	}
}
