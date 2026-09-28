package desktop

import "testing"

// A shared tool's old call goes the old way, whatever the surface; one new argument makes it the
// toolkit's (daemon ADR 0006 point 9).
func TestToolkitCallRoutesByArguments(t *testing.T) {
	for _, tc := range []struct {
		tool, args string
		want       bool
	}{
		{"machine_press", `{"ref":"e4"}`, true},
		{"machine_snapshot", `{}`, true},
		{"machine_click", `{"x":0.5,"y":0.5}`, false},
		{"machine_type", `{"runId":"r","text":"12"}`, false},
		{"machine_type", `{"text":"12","ref":"e4"}`, true},
		{"machine_type", `{"text":"12","submit":"return"}`, true},
		{"machine_type", `{"text":"12","paceMs":0}`, true},
		{"machine_type", `{"text":"12","ref":null}`, false},
		{"machine_key", `{"key":"s","mods":["cmd"]}`, false},
		{"machine_key", `{"key":"s","ref":"e4"}`, true},
		{"machine_scroll", `{"x":0.5,"y":0.5,"deltaY":200}`, false},
		{"machine_scroll", `{"ref":"e20","to":"bottom"}`, true},
		{"machine_screenshot", `{"runId":"r"}`, false},
		{"machine_screenshot", `{"ref":"e41"}`, true},
		{"machine_screenshot", `{"question":"what color is it"}`, true},
		{"machine_type", `not json`, false},
	} {
		if got := ToolkitCall(tc.tool, []byte(tc.args)); got != tc.want {
			t.Errorf("ToolkitCall(%s, %s) = %v, want %v", tc.tool, tc.args, got, tc.want)
		}
	}
}
