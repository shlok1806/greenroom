package desktop

import (
	"strings"
	"testing"
)

func TestAnElementNotDrawnSaysSo(t *testing.T) {
	n := Node{Ref: "e33", Role: "StaticText", Name: "Total", Vis: Rect{0, 0, 40, 10}, States: []string{StateNotDrawn}}
	line := nodeLine(n)
	if !strings.Contains(line, "[not drawn]") || strings.Contains(line, "notDrawn") {
		t.Errorf("line %q, want the [not drawn] flag and no raw state", line)
	}
	c := Change{Kind: ChangeState, Field: StateNotDrawn, On: false}
	if got := stateChangeText(c); got != "now drawn" {
		t.Errorf("state change %q", got)
	}
}
