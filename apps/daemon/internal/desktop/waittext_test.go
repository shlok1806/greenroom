package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

func axes(x, y *float64) ScrollAxes { return ScrollAxes{X: x, Y: y} }

func TestScrollText(t *testing.T) {
	items := el("e20", "ScrollArea", "Items", 1)
	details := with(el("e45", "Button", "Details", 2), func(n *Node) { n.Scroller = "e20" })
	below := with(details, func(n *Node) { n.Offscreen, n.Vis = "below", Rect{} })
	w := window("e1", "TipSplit")
	before := tree(w, items, below, with(el("e46", "Row", "first", 2), func(n *Node) { n.Scroller = "e20" }))
	after := tree(w, items, details, with(el("e46", "Row", "first", 2), func(n *Node) { n.Scroller, n.Offscreen, n.Vis = "e20", "above", Rect{} }))
	loaded := tree(w, items, details, with(el("e46", "Row", "first", 2), func(n *Node) { n.Scroller, n.Offscreen, n.Vis = "e20", "above", Rect{} }),
		with(el("e47", "Row", "loaded later", 2), func(n *Node) { n.Scroller = "e20" }),
		with(el("e48", "StaticText", "Showing", 1), func(n *Node) { n.Value = "20 of 20" }))
	loadedBefore := tree(w, items, below, with(el("e46", "Row", "first", 2), func(n *Node) { n.Scroller = "e20" }),
		with(el("e48", "StaticText", "Showing", 1), func(n *Node) { n.Value = "10 of 20" }))

	for _, tc := range []struct {
		name string
		r    ScrollResult
		want string
	}{
		{"to an element", ScrollResult{Container: items, From: axes(nil, ptr(0.0)), To: axes(nil, ptr(1.0)), AtEnd: true, Steps: 6, Via: "wheel", Target: &details, Visible: true, Before: &before, After: &after},
			`scrolled e20 ScrollArea "Items" y 0% -> 100% (at the end, 6 steps, wheel); e45 Button "Details" is now visible`},
		{"part of the way, by accessibility", ScrollResult{Container: items, From: axes(nil, ptr(0.62)), To: axes(nil, ptr(0.8)), Steps: 1, Via: "axScrollToVisible"},
			`scrolled e20 ScrollArea "Items" y 62% -> 80% (1 step, AX scroll to visible)`},
		{"both axes, by the scroll bar", ScrollResult{Container: items, From: axes(ptr(0.0), ptr(0.5)), To: axes(ptr(0.25), ptr(0.0)), Via: "scrollBar"},
			`scrolled e20 ScrollArea "Items" x 0% -> 25%, y 50% -> 0% (set the scroll bar)`},
		{"only the axis that moved is said", ScrollResult{Container: items, From: axes(ptr(0.0), ptr(0.5)), To: axes(ptr(0.0), ptr(0.75)), Via: "wheel", Steps: 2},
			`scrolled e20 ScrollArea "Items" y 50% -> 75% (2 steps, wheel)`},
		{"a way this daemon does not know", ScrollResult{Container: items, From: axes(nil, ptr(0.0)), To: axes(nil, ptr(0.5)), Via: "page\nkeys"},
			`scrolled e20 ScrollArea "Items" y 0% -> 50% ("page\nkeys")`},
		{"already at the end", ScrollResult{Container: items, From: axes(nil, ptr(1.0)), To: axes(nil, ptr(1.0)), AtEnd: true, Steps: 1, Via: "wheel"},
			`e20 ScrollArea "Items" did not move from y 100% (at the end, 1 step, wheel)`},
		{"a position nobody could read", ScrollResult{Container: items, Steps: 3, Via: "wheel"},
			`e20 ScrollArea "Items" did not move (3 steps, wheel)`},
		{"the element did not come into view", ScrollResult{Container: items, From: axes(nil, ptr(0.0)), To: axes(nil, ptr(1.0)), AtEnd: true, Steps: 6, Via: "wheel", Target: &below},
			`scrolled e20 ScrollArea "Items" y 0% -> 100% (at the end, 6 steps, wheel); e45 Button "Details" is still not visible [offscreen in e20: below; scroll it into view]`},
		{"the element is in view but covered", ScrollResult{Container: items, From: axes(nil, ptr(0.0)), To: axes(nil, ptr(1.0)), Via: "wheel",
			Target: with2(details, func(n *Node) { n.Covered = &Covered{By: "e70", Role: "Toolbar", Where: "window"} })},
			`scrolled e20 ScrollArea "Items" y 0% -> 100% (wheel); e45 Button "Details" is still not visible [covered by e70 Toolbar (in this window)]`},
		// The live run's e92: inside the scroll area's view, but that part of it is under the Dock.
		{"the element is in the container's view but under the Dock", ScrollResult{Container: items, From: axes(nil, ptr(0.0)), To: axes(nil, ptr(1.0)), AtEnd: true, Via: "wheel", Steps: 3,
			Target: with2(details, func(n *Node) {
				n.Covered = &Covered{By: "e94", Role: "DockItem", Name: "System Settings", Where: "other", App: "Dock"}
			}),
			Visible: true,
			Notes:   []string{"e45 is inside e20's view, but that part of the view is under the Dock or the menu bar"}},
			`scrolled e20 ScrollArea "Items" y 0% -> 100% (at the end, 3 steps, wheel); e45 Button "Details" is still not visible [covered by e94 DockItem "System Settings" (a window of "Dock")]
note: e45 is inside e20's view, but that part of the view is under the Dock or the menu bar`},
		{"what else changed is said, what scrolled in and out is not", ScrollResult{Container: items, From: axes(nil, ptr(0.0)), To: axes(nil, ptr(1.0)), AtEnd: true, Steps: 6, Via: "wheel", Target: &details, Visible: true, Before: &loadedBefore, After: &loaded},
			`scrolled e20 ScrollArea "Items" y 0% -> 100% (at the end, 6 steps, wheel); e45 Button "Details" is now visible
effect: 2 changes in "TipSplit" besides what scrolled into and out of view
  e47 Row "loaded later" appeared
  e48 StaticText "Showing": value "10 of 20" -> "20 of 20"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScrollText(tc.r); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func with2(n Node, f func(*Node)) *Node {
	f(&n)
	return &n
}

func waitArgs(t *testing.T, raw string) WaitArgs {
	t.Helper()
	a := decode[WaitArgs](t, raw)
	if err := a.Normalize(); err != nil {
		t.Fatalf("normalize %s: %v", raw, err)
	}
	return a
}

func TestWaitText(t *testing.T) {
	result := with(el("e62", "StaticText", "Result", 1), func(n *Node) { n.Value = "42" })
	done := el("e77", "Button", "Done", 1)
	password := with(el("e7", "SecureTextField", "Password", 1), func(n *Node) { n.States, n.Chars = []string{"secret"}, 8 })
	settings := window("e90", "Settings")
	before, after := tree(window("e1", "TipSplit")), tree(window("e1", "TipSplit"), result)
	for _, tc := range []struct {
		name string
		args string
		r    WaitResult
		want string
	}{
		{"appeared, with its value and the diff", `{"target":"e62","timeoutMs":5000}`, WaitResult{Satisfied: true, ElapsedMs: 3100, Node: &result, Value: ptr("42"), Before: &before, After: &after},
			`e62 StaticText "Result" appeared after 3.1 s, value "42"
since the wait began: 1 change in "TipSplit"
  e62 StaticText "Result" value="42" appeared`},
		{"did not appear", `{"target":"e62","timeoutMs":5000}`, WaitResult{ElapsedMs: 5003, Before: &before, After: &before},
			`e62 did not appear within 5.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"did not appear in the longest wait there is", `{"target":"e62","timeoutMs":90000}`, WaitResult{ElapsedMs: 40000},
			`e62 did not appear within 40.0 s; wait again to keep waiting, or look with machine_snapshot`},
		{"by text, found", `{"target":{"text":"Done","role":"Button"}}`, WaitResult{Satisfied: true, ElapsedMs: 200, Node: &done},
			`e77 Button "Done" appeared after 0.2 s`},
		{"by text, not found", `{"target":{"text":"Done","role":"Button"}}`, WaitResult{ElapsedMs: 10000},
			`a Button with text "Done" did not appear within 10.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"by text with no role", `{"target":{"text":"Done"},"state":"disappears"}`, WaitResult{ElapsedMs: 10000, Node: &done},
			`e77 Button "Done" was still there after 10.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"disappeared", `{"target":"e77","state":"disappears"}`, WaitResult{Satisfied: true, ElapsedMs: 40},
			`e77 went away after 40 ms`},
		{"enabled", `{"target":"e77","state":"enabled"}`, WaitResult{Satisfied: true, ElapsedMs: 1500, Node: &done}, `e77 Button "Done" became enabled after 1.5 s`},
		{"not enabled", `{"target":"e77","state":"enabled","timeoutMs":2000}`, WaitResult{ElapsedMs: 2000, Node: &done},
			`e77 Button "Done" did not become enabled within 2.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"disabled", `{"target":"e77","state":"disabled"}`, WaitResult{Satisfied: true, ElapsedMs: 1500, Node: &done}, `e77 Button "Done" became disabled after 1.5 s`},
		{"not disabled", `{"target":"e77","state":"disabled","timeoutMs":2000}`, WaitResult{ElapsedMs: 2000, Node: &done},
			`e77 Button "Done" did not become disabled within 2.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"focused", `{"target":"e77","state":"focused"}`, WaitResult{Satisfied: true, ElapsedMs: 300, Node: &done}, `e77 Button "Done" got focus after 0.3 s`},
		{"not focused", `{"target":"e77","state":"focused","timeoutMs":2000}`, WaitResult{ElapsedMs: 2000, Node: &done},
			`e77 Button "Done" did not get focus within 2.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"changed", `{"target":"e62","state":"changes"}`, WaitResult{Satisfied: true, ElapsedMs: 700, Node: &result, Value: ptr("42")},
			`e62 StaticText "Result" changed after 0.7 s, value "42"`},
		{"did not change", `{"target":"e62","state":"changes","timeoutMs":2000}`, WaitResult{ElapsedMs: 2000, Node: &result, Value: ptr("42")},
			`e62 StaticText "Result" did not change within 2.0 s, value "42"; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"value matched", `{"target":"e62","state":"value","value":{"op":"equals","expected":"42"}}`, WaitResult{Satisfied: true, ElapsedMs: 900, Node: &result, Value: ptr("42")},
			`e62 StaticText "Result" value equals "42": matched after 0.9 s, value "42"`},
		{"value not matched", `{"target":"e62","state":"value","value":{"op":"contains","expected":"43"},"timeoutMs":3000}`, WaitResult{ElapsedMs: 3000, Node: &result, Value: ptr("42")},
			`e62 StaticText "Result" value contains "43": not matched within 3.0 s, value "42"; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"an empty value is a value", `{"target":"e62","state":"changes"}`, WaitResult{Satisfied: true, ElapsedMs: 700, Node: &result, Value: ptr("")},
			`e62 StaticText "Result" changed after 0.7 s, value ""`},
		{"a secret's value is its length", `{"target":"e7","state":"value","value":{"op":"equals","expected":"hunter22"}}`, WaitResult{Satisfied: true, ElapsedMs: 900, Node: &password, Value: ptr("hunter22")},
			`e7 SecureTextField "Password" value equals <secret, 8 chars>: matched after 0.9 s, value <secret, 8 chars>`},
		{"idle", `{"target":{"idle":true}}`, WaitResult{Satisfied: true, ElapsedMs: 450, Node: &done}, `the app went idle after 0.5 s`},
		{"idle, as a word", `{"target":"idle","timeoutMs":3000}`, WaitResult{ElapsedMs: 3000},
			`the app was still changing after 3.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
		{"an app going idle", `{"target":{"idle":true,"app":"TipSplit"}}`, WaitResult{Satisfied: true, ElapsedMs: 450}, `"TipSplit" went idle after 0.5 s`},
		{"an app appearing", `{"target":{"app":"TipSplit"}}`, WaitResult{Satisfied: true, ElapsedMs: 2000}, `app "TipSplit" appeared after 2.0 s`},
		{"an app quitting", `{"target":{"app":"TipSplit"},"state":"disappears"}`, WaitResult{Satisfied: true, ElapsedMs: 2000}, `app "TipSplit" went away after 2.0 s`},
		{"a window appearing", `{"target":{"window":"Settings"}}`, WaitResult{Satisfied: true, ElapsedMs: 1000, Node: &settings}, `e90 Window "Settings" appeared after 1.0 s`},
		{"a window not appearing", `{"target":{"window":"Settings","app":"TipSplit"},"timeoutMs":1000}`, WaitResult{ElapsedMs: 1000},
			`window "Settings" did not appear within 1.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := WaitText(waitArgs(t, tc.args), tc.r)
			if got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			if strings.Contains(got, "hunter") {
				t.Errorf("the text shows the secret: %s", got)
			}
		})
	}
}

func TestWaitTextCapsItsChanges(t *testing.T) {
	r := manyChanges(30)
	got := WaitText(WaitArgs{Target: WaitTarget{Idle: true}, TimeoutMs: 5000}, WaitResult{Satisfied: true, ElapsedMs: 100, Before: r.Before, After: r.After})
	lines := strings.Split(got, "\n")
	if len(lines) != 15 || lines[1] != `since the wait began: 30 changes in "TipSplit"` || lines[14] != "  and 18 more" {
		t.Errorf("got %d lines:\n%s", len(lines), got)
	}
}

// Args that were never normalized have no timeout to name; the text names what elapsed.
func TestWaitTextWithoutATimeout(t *testing.T) {
	got := WaitText(WaitArgs{Target: WaitTarget{Ref: "e5"}}, WaitResult{ElapsedMs: 4000})
	if want := `e5 did not appear within 4.0 s; wait longer (timeoutMs, at most 40000) or look with machine_snapshot`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func expectArgs(t *testing.T, raw string) ExpectArgs {
	t.Helper()
	a := decode[ExpectArgs](t, raw)
	if err := a.Normalize(); err != nil {
		t.Fatalf("normalize %s: %v", raw, err)
	}
	return a
}

func TestExpectText(t *testing.T) {
	result := with(el("e62", "StaticText", "Result", 1), func(n *Node) { n.Value = "42" })
	reset := el("e50", "Button", "Reset", 1)
	password := with(el("e7", "SecureTextField", "Password", 1), func(n *Node) { n.States, n.Chars = []string{"secret"}, 8 })
	for _, tc := range []struct {
		name string
		args string
		r    ExpectResult
		want string
	}{
		{"passed", `{"target":"e62","property":"value","op":"equals","expected":"42"}`, ExpectResult{Passed: true, Observed: json.RawMessage(`"42"`), ElapsedMs: 300, Node: &result},
			`expect e62 StaticText "Result" value equals "42": passed after 0.3 s (observed "42")`},
		{"failed", `{"target":"e62","property":"value","op":"equals","expected":"42"}`, ExpectResult{Observed: json.RawMessage(`"41"`), ElapsedMs: 2000, Node: &result},
			`expect e62 StaticText "Result" value equals "42": FAILED after 2.0 s (observed "41")`},
		{"no node in the result", `{"target":"e62","property":"value","expected":"42"}`, ExpectResult{Passed: true, Observed: json.RawMessage(`"42"`), ElapsedMs: 300},
			`expect e62 value equals "42": passed after 0.3 s (observed "42")`},
		{"the target was not found", `{"target":{"text":"Result"},"property":"value","op":"contains","expected":"4"}`, ExpectResult{ElapsedMs: 2000},
			`expect an element with text "Result" value contains "4": FAILED after 2.0 s (observed nothing: the target was not found)`},
		{"observed null", `{"target":"e62","property":"name","op":"matches","expected":"^Res"}`, ExpectResult{Observed: json.RawMessage(`null`), ElapsedMs: 2000},
			`expect e62 name matches "^Res": FAILED after 2.0 s (observed nothing: the target was not found)`},
		{"an empty observed value is a value", `{"target":"e62","property":"value","expected":"42"}`, ExpectResult{Observed: json.RawMessage(`""`), ElapsedMs: 2000, Node: &result},
			`expect e62 StaticText "Result" value equals "42": FAILED after 2.0 s (observed "")`},
		{"a flag expected true", `{"target":"e50","property":"enabled"}`, ExpectResult{Passed: true, Observed: json.RawMessage(`true`), ElapsedMs: 40, Node: &reset},
			`expect e50 Button "Reset" enabled: passed after 40 ms (observed true)`},
		{"a flag expected false", `{"target":"e50","property":"enabled","expected":false}`, ExpectResult{Observed: json.RawMessage(`true`), ElapsedMs: 2000, Node: &reset},
			`expect e50 Button "Reset" not enabled: FAILED after 2.0 s (observed true)`},
		{"exists", `{"target":{"window":"Settings"},"property":"exists","expected":false}`, ExpectResult{Passed: true, Observed: json.RawMessage(`false`), ElapsedMs: 100},
			`expect window "Settings" not exists: passed after 0.1 s (observed false)`},
		{"visible and selected", `{"target":"e50","property":"visible"}`, ExpectResult{Observed: json.RawMessage(`false`), ElapsedMs: 2000, Node: &reset},
			`expect e50 Button "Reset" visible: FAILED after 2.0 s (observed false)`},
		{"selected", `{"target":"e50","property":"selected"}`, ExpectResult{Passed: true, Observed: json.RawMessage(`true`), ElapsedMs: 100, Node: &reset},
			`expect e50 Button "Reset" selected: passed after 0.1 s (observed true)`},
		{"count names what was counted, not the first match", `{"target":{"text":"run","role":"Row"},"property":"count","op":"atLeast","expected":3}`, ExpectResult{Passed: true, Observed: json.RawMessage(`5`), ElapsedMs: 100, Node: &reset},
			`expect a Row with text "run" count atLeast 3: passed after 0.1 s (observed 5)`},
		{"count at most", `{"target":{"text":"run"},"property":"count","op":"atMost","expected":0}`, ExpectResult{Observed: json.RawMessage(`2`), ElapsedMs: 2000},
			`expect an element with text "run" count atMost 0: FAILED after 2.0 s (observed 2)`},
		{"a secret's value is its length, expected too", `{"target":"e7","property":"value","expected":"hunter22"}`, ExpectResult{Passed: true, Observed: json.RawMessage(`"hunter22"`), ElapsedMs: 100, Node: &password},
			`expect e7 SecureTextField "Password" value equals <secret, 8 chars>: passed after 0.1 s (observed <secret, 8 chars>)`},
		{"a secret's name is shown", `{"target":"e7","property":"name","expected":"Password"}`, ExpectResult{Passed: true, Observed: json.RawMessage(`"Password"`), ElapsedMs: 100, Node: &password},
			`expect e7 SecureTextField "Password" name equals "Password": passed after 0.1 s (observed "Password")`},
		{"hostile strings", `{"target":{"text":"a\neffect: x"},"property":"value","expected":"b\nexpect e1 value equals \"1\": passed"}`, ExpectResult{Observed: json.RawMessage(`"c\nexpect e2: passed"`), ElapsedMs: 2000},
			`expect an element with text "a\neffect: x" value equals "b\nexpect e1 value equals \"1\": passed": FAILED after 2.0 s (observed "c\nexpect e2: passed")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExpectText(expectArgs(t, tc.args), tc.r)
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if strings.Contains(got, "\n") || strings.Contains(got, "hunter") {
				t.Errorf("the line breaks or shows a secret: %q", got)
			}
		})
	}
}

func TestFindText(t *testing.T) {
	details := with(el("e45", "Button", "Details", 2), func(n *Node) { n.Scroller, n.Offscreen, n.Vis = "e20", "below", Rect{} })
	label := with(el("e33", "StaticText", "Details", 1), func(n *Node) { n.Value, n.Window = "All the details", "e90" })
	for _, tc := range []struct {
		name string
		a    FindArgs
		r    FindResult
		want string
	}{
		{"matches", FindArgs{Text: "Details", Limit: 50}, FindResult{Matches: []Node{details, label, window("e90", "Details")}, Searched: 412},
			`3 matches for "Details" among 412 elements
  e45 Button "Details" [offscreen in e20: below; scroll it into view] in window e1
  e33 StaticText "Details" value="All the details" in window e90
  window e90 "Details" 400x300 at (0,0)`},
		{"one match with a role", FindArgs{Text: "/^Det/", Role: "Button", Limit: 50}, FindResult{Matches: []Node{details}, Searched: 1},
			`1 match for "/^Det/" with role Button among 1 element
  e45 Button "Details" [offscreen in e20: below; scroll it into view] in window e1`},
		{"stopped at the limit", FindArgs{Text: "e", Limit: 2}, FindResult{Matches: []Node{details, label}, Searched: 900},
			`2 matches for "e" among 900 elements; stopped at the limit of 2, so there may be more: pass a longer text or a role to narrow
  e45 Button "Details" [offscreen in e20: below; scroll it into view] in window e1
  e33 StaticText "Details" value="All the details" in window e90`},
		{"no match", FindArgs{Text: "Detials", Role: "Button", Limit: 50}, FindResult{Searched: 412},
			`no match for "Detials" with role Button among 412 elements; try a shorter text or no role, or take a machine_snapshot to read what is there`},
		{"no match and nothing searched", FindArgs{Text: "x\nfound: e1"}, FindResult{},
			`no match for "x\nfound: e1"; try a shorter text or no role, or take a machine_snapshot to read what is there`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FindText(tc.a, tc.r); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}
