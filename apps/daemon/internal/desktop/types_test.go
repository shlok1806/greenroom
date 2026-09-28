package desktop

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func decode[T any](t *testing.T, raw string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode %T: %v\n%s", v, err, raw)
	}
	return v
}

func ptr[T any](v T) *T { return &v }

// The ADR's node, every field set, decodes into the fields the rest of the package reads.
func TestNodeDecodesTheADRShape(t *testing.T) {
	n := decode[Node](t, `{"ref":"e41","role":"Button","subrole":"Toggle","name":"Open run","value":"on","desc":"d",
		"help":"h","id":"open-run","states":["disabled","selected"],"frame":[1,2,3,4],"vis":[1,2,3,2],"depth":2,
		"window":"e1","scroller":"e20","covered":{"by":"e70","role":"List","name":"Runs","where":"window","app":"TipSplit"},
		"offscreen":"below","clipped":"e20","scroll":{"x":null,"y":0.62,"up":true,"down":true,"left":false,"right":false},
		"chars":412,"cut":["value"],"document":"/Users/admin/Notes.txt","edited":true,"notYetKnown":[1,2]}`)
	want := Node{
		Ref: "e41", Role: "Button", Subrole: "Toggle", Name: "Open run", Value: "on", Desc: "d", Help: "h", ID: "open-run",
		States: []string{"disabled", "selected"}, Frame: Rect{1, 2, 3, 4}, Vis: Rect{1, 2, 3, 2}, Depth: 2,
		Window: "e1", Scroller: "e20", Covered: &Covered{By: "e70", Role: "List", Name: "Runs", Where: "window", App: "TipSplit"},
		Offscreen: "below", Clipped: "e20", Scroll: &ScrollPos{Y: ptr(0.62), Up: true, Down: true},
		Chars: 412, Cut: []string{"value"}, Document: "/Users/admin/Notes.txt", Edited: true,
	}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
}

// The agent leaves empty fields out: a node with only a ref decodes, and shows nothing.
func TestNodeDecodesWithEveryFieldAbsent(t *testing.T) {
	n := decode[Node](t, `{"ref":"e1"}`)
	if !reflect.DeepEqual(n, Node{Ref: "e1"}) {
		t.Errorf("got %+v", n)
	}
	if n.Visible() || n.Secret() || n.IsWindow() || n.Has(StateFocused) {
		t.Errorf("an empty node claims something: %+v", n)
	}
}

// A step record holds these types encoded again; the keys must stay the ADR's and empty fields
// stay out.
func TestNodeEncodesTheADRKeys(t *testing.T) {
	b, err := json.Marshal(Node{Ref: "e1", Role: "Window", Frame: Rect{0, 0, 10, 10}, Document: "/a", Edited: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"ref":"e1","role":"Window","frame":[0,0,10,10],"document":"/a","edited":true}`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// A secure field's value must not get past the decode, even from an agent that sends it.
func TestSecretValueIsDroppedOnDecode(t *testing.T) {
	n := decode[Node](t, `{"ref":"e7","role":"TextField","value":"hunter2","states":["secret"],"chars":7}`)
	if n.Value != "" || n.Chars != 7 {
		t.Errorf("secret node kept its value: %+v", n)
	}
	s := decode[Snapshot](t, `{"screen":{"width":1,"height":1},"nodes":[{"ref":"e7","value":"hunter2","states":["secret"]}]}`)
	if s.Nodes[0].Value != "" {
		t.Errorf("secret node in a snapshot kept its value: %+v", s.Nodes[0])
	}
	r := decode[ActionResult](t, `{"target":{"ref":"e7","states":["secret"],"value":"hunter2"},"typed":"hunter2","readBack":"hunter22",
		"after":{"nodes":[{"ref":"e7","states":["secret"],"value":"hunter22","chars":8}]}}`)
	if r.Typed != "<secret, 7 chars>" || *r.ReadBack != "<secret, 8 chars>" || r.Target.Value != "" || r.After.Nodes[0].Value != "" {
		t.Errorf("secret result kept text: typed %q readBack %q target %+v after %+v", r.Typed, *r.ReadBack, r.Target, r.After.Nodes[0])
	}
	if b, _ := json.Marshal(r); strings.Contains(string(b), "hunter") {
		t.Errorf("the encoded result holds the secret: %s", b)
	}
}

func TestSecretResultKeepsTheAgentsOwnPlaceholders(t *testing.T) {
	r := decode[ActionResult](t, `{"secret":true,"typed":"<secret, 8 chars>","readBack":"<secret, 8 chars>","readBackOK":true}`)
	if r.Typed != "<secret, 8 chars>" || *r.ReadBack != "<secret, 8 chars>" || !r.IsSecret() {
		t.Errorf("got %+v", r)
	}
	plain := decode[ActionResult](t, `{"typed":"120","readBack":"12","readBackOK":false}`)
	if plain.Typed != "120" || *plain.ReadBack != "12" || *plain.ReadBackOK || plain.IsSecret() {
		t.Errorf("a plain result was changed: %+v", plain)
	}
}

func TestActionResultTellsAnEmptyReadBackFromNone(t *testing.T) {
	empty := decode[ActionResult](t, `{"readBack":"","readBackOK":true}`)
	if empty.ReadBack == nil || *empty.ReadBack != "" {
		t.Errorf("an empty read-back decoded as %v", empty.ReadBack)
	}
	none := decode[ActionResult](t, `{"settled":true}`)
	if none.ReadBack != nil || none.ReadBackOK != nil || none.Before != nil || none.After != nil || none.Point != nil {
		t.Errorf("absent fields decoded as present: %+v", none)
	}
}

func TestStampAcceptsNumbersAndStrings(t *testing.T) {
	for raw, want := range map[string]Stamp{
		`{"started":1727400000.5}`:       "1727400000.5",
		`{"started":"2026-09-27T10:00"}`: "2026-09-27T10:00",
		`{"started":null}`:               "",
		`{}`:                             "",
	} {
		if got := decode[AppInfo](t, raw).Started; got != want {
			t.Errorf("%s: got %q, want %q", raw, got, want)
		}
	}
	var a AppInfo
	if err := json.Unmarshal([]byte(`{"started":{"a":1}}`), &a); err == nil {
		t.Errorf("an object decoded as a start time: %+v", a)
	}
}

func TestRefusalDecodesItsCauseInEitherForm(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Refusal
	}{
		{`{"reason":"covered","by":{"ref":"e70","role":"List","name":"Runs","where":"window"},"waitedMs":5000,
			"checks":[{"check":"receivesEvents","ms":4900,"detail":{"hit":"e70"}}],"later":1}`,
			Refusal{Reason: ReasonCovered, By: &Cause{Ref: "e70", Role: "List", Name: "Runs", Where: "window"}, WaitedMs: 5000,
				Checks: []Check{{Check: "receivesEvents", Ms: 4900, Detail: json.RawMessage(`{"hit":"e70"}`)}}}},
		{`{"reason":"modal","by":"e80"}`, Refusal{Reason: ReasonModal, By: &Cause{Ref: "e80"}}},
		{`{"reason":"disabled","by":null}`, Refusal{Reason: ReasonDisabled}},
		{`{"reason":"disabled"}`, Refusal{Reason: ReasonDisabled}},
	} {
		if got := decode[Refusal](t, tc.raw); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\ngot  %+v\nwant %+v", tc.raw, got, tc.want)
		}
	}
}

func TestResultsDecodeTheADRShapes(t *testing.T) {
	sc := decode[ScrollResult](t, `{"container":{"ref":"e20","role":"ScrollArea"},"from":{"x":null,"y":0},"to":{"x":null,"y":1},
		"atEnd":true,"steps":6,"via":"wheel","target":{"ref":"e45"},"visible":true,"extra":true}`)
	if sc.Container.Ref != "e20" || sc.From.X != nil || *sc.From.Y != 0 || *sc.To.Y != 1 || !sc.AtEnd || sc.Steps != 6 || sc.Via != "wheel" || sc.Target.Ref != "e45" || !sc.Visible {
		t.Errorf("scroll: %+v", sc)
	}
	w := decode[WaitResult](t, `{"satisfied":true,"elapsedMs":3100,"node":{"ref":"e62"},"value":"42"}`)
	if !w.Satisfied || w.ElapsedMs != 3100 || w.Node.Ref != "e62" || *w.Value != "42" {
		t.Errorf("wait: %+v", w)
	}
	e := decode[ExpectResult](t, `{"passed":false,"observed":3,"elapsedMs":2000}`)
	if e.Passed || string(e.Observed) != "3" || e.ElapsedMs != 2000 || e.Node != nil {
		t.Errorf("expect: %+v", e)
	}
	c := decode[CaptureResult](t, `{"width":200,"height":100,"scale":2,"rect":[10,20,100,50]}`)
	if c != (CaptureResult{Width: 200, Height: 100, Scale: 2, Rect: Rect{10, 20, 100, 50}}) {
		t.Errorf("capture: %+v", c)
	}
	f := decode[FindResult](t, `{"matches":[{"ref":"e3"},{"ref":"e9"}],"searched":412}`)
	if len(f.Matches) != 2 || f.Searched != 412 {
		t.Errorf("find: %+v", f)
	}
	tr := decode[Tree](t, `{"nodes":[{"ref":"e1"}],"attention":[{"ref":"e80","kind":"sheet","pid":812}],"windows":["e1"],"app":{"name":"TipSplit","pid":812}}`)
	if len(tr.Nodes) != 1 || tr.Attention[0].Kind != AttentionSheet || tr.Windows[0] != "e1" || tr.App.PID != 812 {
		t.Errorf("tree: %+v", tr)
	}
}

func TestSnapshotRespondingOnlyWhenSaid(t *testing.T) {
	if decode[Snapshot](t, `{}`).NotResponding() {
		t.Error("an absent responding reads as a hung app")
	}
	if decode[Snapshot](t, `{"responding":true}`).NotResponding() {
		t.Error("responding true reads as a hung app")
	}
	if !decode[Snapshot](t, `{"responding":false}`).NotResponding() {
		t.Error("responding false does not read as a hung app")
	}
}

func TestRectAndPointMath(t *testing.T) {
	r := Rect{10, 20, 100, 50}
	if r.X() != 10 || r.Y() != 20 || r.W() != 100 || r.H() != 50 || r.Empty() || r.IsZero() {
		t.Errorf("rect accessors: %+v", r)
	}
	if got := r.Center(); got != (Point{60, 45}) {
		t.Errorf("center: %v", got)
	}
	for _, e := range []Rect{{}, {5, 5, 0, 10}, {5, 5, 10, 0}, {5, 5, -1, 10}} {
		if !e.Empty() {
			t.Errorf("%v is not empty", e)
		}
	}
	s := Screen{Width: 1024, Height: 768}
	if x, y := (Point{512, 192}).Fractions(s); x != 0.5 || y != 0.25 {
		t.Errorf("fractions: %v %v", x, y)
	}
	if x, y := (Point{512, 192}).Fractions(Screen{}); x != 0 || y != 0 {
		t.Errorf("fractions of no screen: %v %v", x, y)
	}
	for _, tc := range []struct {
		x, y float64
		want Point
	}{
		{0.5, 0.25, Point{512, 192}},
		{-1, 2, Point{0, 768}},
		{0.0004, 0.9996, Point{0, 768}},
	} {
		if got := s.Point(tc.x, tc.y); got != tc.want {
			t.Errorf("Point(%v, %v) = %v, want %v", tc.x, tc.y, got, tc.want)
		}
	}
}

func TestJSONText(t *testing.T) {
	for raw, want := range map[string]string{
		``:                       ``,
		`null`:                   ``,
		`"42"`:                   `"42"`,
		`"a\nb"`:                 `"a\nb"`,
		`3`:                      `3`,
		`true`:                   `true`,
		"{\n \"a\": [1,\n 2]\n}": `{"a":[1,2]}`,
		"{broken\n":              `"{broken"`,
	} {
		if got := jsonText(json.RawMessage(raw)); got != want {
			t.Errorf("jsonText(%q) = %s, want %s", raw, got, want)
		}
	}
}
