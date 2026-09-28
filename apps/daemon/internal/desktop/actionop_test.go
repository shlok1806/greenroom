package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheTypingPaceDefaultsTo20AndIsBounded(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    int
		wantErr string
	}{
		{raw: `{"text":"12"}`, want: 20},
		{raw: `{"text":"12","paceMs":0}`, want: 0},
		{raw: `{"text":"12","paceMs":55}`, want: 55},
		{raw: `{"text":"12","paceMs":500}`, want: 100},
		{raw: `{"text":"12","paceMs":-1}`, wantErr: "paceMs: -1 is negative; pass 0 to 100 milliseconds between characters, or leave it out for 20"},
	} {
		a, err := parse[TypeArgs](tc.raw)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s: error %v, want %q", tc.raw, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		op := a.Op()
		if op.PaceMs != tc.want {
			t.Errorf("%s: pace %d, want %d", tc.raw, op.PaceMs, tc.want)
		}
		wire, _ := json.Marshal(op)
		if !strings.Contains(string(wire), `"paceMs":`) {
			t.Errorf("%s: the op does not carry paceMs: %s", tc.raw, wire)
		}
	}
}

func TestAScrollsEffect(t *testing.T) {
	y0, y1 := 0.0, 1.0
	tree := &Tree{App: AppInfo{Name: "Navlab"}, Nodes: []Node{{Ref: "e1", Role: "Window"}}}
	moved := EffectOfScroll(ScrollResult{From: ScrollAxes{Y: &y0}, To: ScrollAxes{Y: &y1}, Before: tree, After: tree})
	if moved.Kind != EffectChanged {
		t.Errorf("a scroll that moved: %+v", moved)
	}
	still := EffectOfScroll(ScrollResult{From: ScrollAxes{Y: &y1}, To: ScrollAxes{Y: &y1}, Before: tree, After: tree})
	if still.Kind != EffectNone {
		t.Errorf("a scroll that did not move: %+v", still)
	}
	blind := EffectOfScroll(ScrollResult{From: ScrollAxes{Y: &y1}, To: ScrollAxes{Y: &y1}})
	if blind.Kind != EffectUnknown || blind.Reason == "" {
		t.Errorf("a scroll with no trees: %+v", blind)
	}
}
