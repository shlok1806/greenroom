package desktop

import (
	"reflect"
	"strings"
	"testing"
)

var shown = Rect{10, 10, 50, 20}

// el is a visible element of window e1.
func el(ref, role, name string, depth int) Node {
	return Node{Ref: ref, Role: role, Name: name, Depth: depth, Window: "e1", Frame: shown, Vis: shown}
}

func with(n Node, f func(*Node)) Node {
	f(&n)
	return n
}

func window(ref, name string) Node {
	return Node{Ref: ref, Role: "Window", Name: name, Window: ref, Frame: Rect{0, 0, 400, 300}, Vis: Rect{0, 0, 400, 300}}
}

func tree(nodes ...Node) Tree {
	t := Tree{Nodes: nodes, App: AppInfo{Name: "TipSplit", PID: 812, Started: "1"}}
	for _, n := range nodes {
		if n.IsWindow() {
			t.Windows = append(t.Windows, n.Ref)
		}
	}
	return t
}

func texts(changes []Change) []string {
	var out []string
	for _, c := range changes {
		out = append(out, c.Text())
	}
	return out
}

func TestDiffOfOneElement(t *testing.T) {
	base := el("e5", "Button", "Go", 1)
	for _, tc := range []struct {
		name   string
		before Node
		after  Node
		want   []string
	}{
		{"nothing changed", base, base, nil},
		{"a frame that only moved is not a change", base, with(base, func(n *Node) { n.Frame, n.Vis = Rect{90, 90, 50, 20}, Rect{90, 90, 50, 20} }), nil},
		{"a depth that changed is not a change", base, with(base, func(n *Node) { n.Depth = 3 }), nil},
		{"name", base, with(base, func(n *Node) { n.Name = "Stop" }), []string{`e5 Button "Stop": name "Go" -> "Stop"`}},
		{"the same ref with another role", base, with(base, func(n *Node) { n.Role = "Link" }), []string{`e5 Link "Go": role Button -> Link`}},
		{"value", with(base, func(n *Node) { n.Value = "$15.12" }), with(base, func(n *Node) { n.Value = "$24.00" }),
			[]string{`e5 Button "Go": value "$15.12" -> "$24.00"`}},
		{"value set from nothing", base, with(base, func(n *Node) { n.Value = "1" }), []string{`e5 Button "Go": value "" -> "1"`}},
		{"a cut value says it is cut", with(base, func(n *Node) { n.Value = "aaa" }),
			with(base, func(n *Node) { n.Value, n.Chars, n.Cut = "bbb", 900, []string{"value"} }),
			[]string{`e5 Button "Go": value "aaa" -> "bbb" (cut; 900 chars in full; fullText: e5)`}},
		{"a value that changed past the cut", with(base, func(n *Node) { n.Value, n.Chars, n.Cut = "aaa", 800, []string{"value"} }),
			with(base, func(n *Node) { n.Value, n.Chars, n.Cut = "aaa", 900, []string{"value"} }),
			[]string{`e5 Button "Go": value changed past where it is cut (now 900 chars in full; fullText: e5)`}},
		{"a secret says only its length", with(base, func(n *Node) { n.States, n.Chars, n.Value = []string{"secret"}, 0, "" }),
			with(base, func(n *Node) { n.States, n.Chars, n.Value = []string{"secret"}, 8, "hunter22" }),
			[]string{`e5 Button "Go": now 8 chars (was 0), secret`}},
		{"a secret of the same length did not change", with(base, func(n *Node) { n.States, n.Chars, n.Value = []string{"secret"}, 8, "a" }),
			with(base, func(n *Node) { n.States, n.Chars, n.Value = []string{"secret"}, 8, "b" }), nil},
		{"every state on", base, with(base, func(n *Node) {
			n.States = []string{"zoomed", "busy", "editable", "expanded", "focused", "selected", "disabled"}
		}), []string{
			`e5 Button "Go": disabled`, `e5 Button "Go": selected`, `e5 Button "Go": focused`, `e5 Button "Go": expanded`,
			`e5 Button "Go": editable`, `e5 Button "Go": busy`, `e5 Button "Go": zoomed`,
		}},
		{"every state off", with(base, func(n *Node) {
			n.States = []string{"disabled", "selected", "focused", "expanded", "editable", "busy"}
		}), base, []string{
			`e5 Button "Go": not disabled`, `e5 Button "Go": not selected`, `e5 Button "Go": not focused`, `e5 Button "Go": not expanded`,
			`e5 Button "Go": not editable`, `e5 Button "Go": not busy`,
		}},
		{"one state swapped for another", with(base, func(n *Node) { n.States = []string{"selected"} }),
			with(base, func(n *Node) { n.States = []string{"focused"} }),
			[]string{`e5 Button "Go": not selected`, `e5 Button "Go": focused`}},
		{"states in another order are the same states", with(base, func(n *Node) { n.States = []string{"selected", "focused"} }),
			with(base, func(n *Node) { n.States = []string{"focused", "selected"} }), nil},
		{"into overflow", base, with(base, func(n *Node) { n.States = []string{"overflow"} }),
			[]string{`e5 Button "Go": now in overflow: press the toolbar's >> button first`}},
		{"out of overflow", with(base, func(n *Node) { n.States = []string{"overflow"} }), base,
			[]string{`e5 Button "Go": no longer in overflow`}},
		{"covered", base, with(base, func(n *Node) { n.Covered = &Covered{By: "e70", Role: "List", Name: "Runs", Where: "window"} }),
			[]string{`e5 Button "Go": now covered by e70 List "Runs" (in this window)`}},
		{"covered by something with no ref", base, with(base, func(n *Node) { n.Covered = &Covered{Role: "Window", Where: "other", App: "Notes"} }),
			[]string{`e5 Button "Go": now covered by a Window (a window of "Notes")`}},
		{"covered by something else", with(base, func(n *Node) { n.Covered = &Covered{By: "e70", Role: "List"} }),
			with(base, func(n *Node) { n.Covered = &Covered{By: "e71", Role: "Sheet", Where: "window"} }),
			[]string{`e5 Button "Go": now covered by e71 Sheet (in this window)`}},
		{"uncovered", with(base, func(n *Node) { n.Covered = &Covered{By: "e70"} }), base, []string{`e5 Button "Go": no longer covered`}},
		{"scrolled out of view, said once", with(base, func(n *Node) { n.Scroller = "e20" }),
			with(base, func(n *Node) { n.Scroller, n.Offscreen, n.Vis = "e20", "below", Rect{} }),
			[]string{`e5 Button "Go": now offscreen in e20: below; scroll it into view`}},
		{"scrolled into view, said once", with(base, func(n *Node) { n.Scroller, n.Offscreen, n.Vis = "e20", "above", Rect{} }),
			with(base, func(n *Node) { n.Scroller = "e20" }),
			[]string{`e5 Button "Go": now in view (was offscreen: above)`}},
		{"clipped", base, with(base, func(n *Node) { n.Clipped = "window" }), []string{`e5 Button "Go": now clipped by its window`}},
		{"clipped by something else", with(base, func(n *Node) { n.Clipped = "window" }), with(base, func(n *Node) { n.Clipped = "e20" }),
			[]string{`e5 Button "Go": now clipped by e20`}},
		{"no longer clipped", with(base, func(n *Node) { n.Clipped = "screen" }), base, []string{`e5 Button "Go": no longer clipped`}},
		{"went from showing to not", base, with(base, func(n *Node) { n.Vis = Rect{} }), []string{`e5 Button "Go": no longer visible`}},
		{"went from not showing to showing", with(base, func(n *Node) { n.Vis = Rect{} }), base, []string{`e5 Button "Go": now visible`}},
		{"several at once, in a fixed order", with(base, func(n *Node) { n.Value = "1" }),
			with(base, func(n *Node) { n.Name, n.Value, n.States, n.Clipped = "Stop", "2", []string{"disabled"}, "window" }),
			[]string{`e5 Button "Stop": name "Go" -> "Stop"`, `e5 Button "Stop": value "1" -> "2"`, `e5 Button "Stop": disabled`, `e5 Button "Stop": now clipped by its window`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := texts(Diff(tree(window("e1", "TipSplit"), tc.before), tree(window("e1", "TipSplit"), tc.after)))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestDiffOfAWindowsDocument(t *testing.T) {
	w := window("e1", "Notes.txt")
	edited := with(w, func(n *Node) { n.Edited = true })
	saved := with(w, func(n *Node) { n.Name, n.Document = "Final.txt", "/Users/admin/Final.txt" })
	for _, tc := range []struct {
		name          string
		before, after Node
		want          []string
	}{
		{"edited", w, edited, []string{`e1 Window "Notes.txt": edited`}},
		{"saved", edited, w, []string{`e1 Window "Notes.txt": not edited`}},
		{"saved under another name", with(edited, func(n *Node) { n.Document = "/Users/admin/Notes.txt" }), saved, []string{
			`e1 Window "Final.txt": name "Notes.txt" -> "Final.txt"`,
			`e1 Window "Final.txt": document "/Users/admin/Notes.txt" -> "/Users/admin/Final.txt"`,
			`e1 Window "Final.txt": not edited`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := texts(Diff(tree(tc.before), tree(tc.after))); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestDiffOfStructure(t *testing.T) {
	w := window("e1", "TipSplit")
	a, b, c := el("e2", "Button", "A", 1), el("e3", "Button", "B", 1), el("e4", "Button", "C", 1)
	group := el("e10", "Group", "Totals", 1)
	inGroup1, inGroup2 := el("e11", "StaticText", "Tip", 2), with(el("e12", "StaticText", "Total", 2), func(n *Node) { n.Value = "$24.00" })
	deep := el("e13", "Image", "Logo", 3)
	sheet := with(el("e80", "Sheet", "Save changes?", 1), func(n *Node) { n.States = []string{"focused"} })
	save, cancel := el("e81", "Button", "Save", 2), el("e82", "Button", "Cancel", 2)
	w2 := window("e90", "Settings")
	inW2 := with(el("e91", "CheckBox", "Sounds", 1), func(n *Node) { n.Window = "e90" })
	sheetItem := Attention{Ref: "e80", Kind: "sheet", Role: "Sheet", Name: "Save changes?", App: "TipSplit", PID: 812}
	alert := Attention{Ref: "e99", Kind: "window", Role: "Window", Name: "Allow access?", App: "UserNotificationCenter", PID: 300}

	withAttention := func(t Tree, items ...Attention) Tree {
		t.Attention = items
		return t
	}
	for _, tc := range []struct {
		name          string
		before, after Tree
		want          []string
	}{
		{"two empty trees", Tree{}, Tree{}, nil},
		{"reordered elements are the same elements", tree(w, a, b, c), tree(w, c, a, b), nil},
		{"an element appeared, with its value and flags", tree(w, a), tree(w, a, with(b, func(n *Node) { n.Value, n.States = "on", []string{"selected"} })),
			[]string{`e3 Button "B" value="on" appeared [selected]`}},
		{"an element went away", tree(w, a, b), tree(w, a), []string{`e3 Button "B" went away`}},
		{"a subtree appears as one line", tree(w, a), tree(w, a, group, inGroup1, inGroup2, deep, b),
			[]string{`e10 Group "Totals" appeared (with 3 elements inside)`, `e3 Button "B" appeared`}},
		{"a subtree goes away as one line", tree(w, a, group, inGroup1, b), tree(w, a, b),
			[]string{`e10 Group "Totals" went away (with 1 element inside)`}},
		{"new children of an old element are each said", tree(w, group), tree(w, group, inGroup1, inGroup2),
			[]string{`e11 StaticText "Tip" appeared`, `e12 StaticText "Total" value="$24.00" appeared`}},
		{"changes come in the after tree's order, then what went away in the before tree's", tree(w, a, b, c),
			tree(w, with(c, func(n *Node) { n.Name = "C2" }), group, with(a, func(n *Node) { n.Name = "A2" })),
			[]string{`e4 Button "C2": name "C" -> "C2"`, `e10 Group "Totals" appeared`, `e2 Button "A2": name "A" -> "A2"`, `e3 Button "B" went away`}},
		{"a sheet opening is said once, as a sheet", tree(w, a), withAttention(tree(w, a, sheet, save, cancel), sheetItem),
			[]string{`sheet e80 "Save changes?" of "TipSplit" (pid 812) opened (with 2 elements inside)`}},
		{"a sheet closing", withAttention(tree(w, a, sheet, save, cancel), sheetItem), tree(w, a),
			[]string{`sheet e80 "Save changes?" of "TipSplit" (pid 812) closed (with 2 elements inside)`}},
		{"a sheet that stays is no change", withAttention(tree(w, a, sheet), sheetItem), withAttention(tree(w, a, sheet), sheetItem), nil},
		{"another app's alert has no element here", tree(w, a), withAttention(tree(w, a), alert),
			[]string{`window e99 "Allow access?" of "UserNotificationCenter" (pid 300) opened`}},
		{"attention comes before windows and elements", withAttention(tree(w, a), alert), withAttention(tree(w, a, b, w2, inW2), sheetItem), []string{
			`sheet e80 "Save changes?" of "TipSplit" (pid 812) opened`,
			`window e99 "Allow access?" of "UserNotificationCenter" (pid 300) closed`,
			`window e90 "Settings" opened (with 1 element inside)`,
			`e3 Button "B" appeared`,
		}},
		{"a window opened", tree(w, a), tree(w, a, w2, inW2), []string{`window e90 "Settings" opened (with 1 element inside)`}},
		{"a window closed", tree(w, a, w2, inW2), tree(w, a), []string{`window e90 "Settings" closed (with 1 element inside)`}},
		{"a window listed without its element", tree(w, a), Tree{Nodes: []Node{w, a}, Windows: []string{"e1", "e90"}, App: tree().App},
			[]string{`window e90 opened`}},
		{"a window that is also an attention item is said once", tree(w, a), withAttention(tree(w, a, w2, inW2), Attention{Ref: "e90", Kind: "dialog", Name: "Settings"}),
			[]string{`dialog e90 "Settings" opened (with 1 element inside)`}},
		{"the same ref twice in one tree speaks once", tree(w, a), tree(w, a, b, b), []string{`e3 Button "B" appeared`}},
		{"the app restarted", tree(w, a), Tree{Nodes: []Node{w, a}, Windows: []string{"e1"}, App: AppInfo{Name: "TipSplit", PID: 900, Started: "2"}},
			[]string{`"TipSplit" is a new process (pid 812 -> 900); refs from before it are gone`}},
		{"a recycled pid is another process", tree(w, a), Tree{Nodes: []Node{w, a}, Windows: []string{"e1"}, App: AppInfo{Name: "TipSplit", PID: 812, Started: "2"}},
			[]string{`"TipSplit" is a new process (pid 812 -> 812); refs from before it are gone`}},
		{"a tree that does not name its app is the same app", tree(w, a), Tree{Nodes: []Node{w, a}, Windows: []string{"e1"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := texts(Diff(tc.before, tc.after)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// The structured change is what a step records and what later code reads: kinds, refs and
// fields, not only the text.
func TestDiffChangesCarryTheirFields(t *testing.T) {
	w := window("e1", "TipSplit")
	before := tree(w, with(el("e5", "RadioButton", "18%", 1), func(n *Node) { n.States = []string{"selected"} }), el("e6", "StaticText", "Tip", 1))
	after := tree(w, el("e5", "RadioButton", "18%", 1), with(el("e6", "StaticText", "Tip", 1), func(n *Node) { n.Value = "$24.00" }))
	got := Diff(before, after)
	if len(got) != 2 {
		t.Fatalf("got %d changes: %v", len(got), texts(got))
	}
	if c := got[0]; c.Kind != ChangeState || c.Ref != "e5" || c.Field != StateSelected || c.On || c.Node == nil || c.Node.Role != "RadioButton" {
		t.Errorf("state change: %+v", c)
	}
	if c := got[1]; c.Kind != ChangeValue || c.Ref != "e6" || c.From != "" || c.To != "$24.00" {
		t.Errorf("value change: %+v", c)
	}
	opened := Diff(tree(w), tree(w, window("e90", "Settings"), with(el("e91", "Button", "OK", 1), func(n *Node) { n.Window = "e90" })))
	if len(opened) != 1 || opened[0].Kind != ChangeWindowOpened || opened[0].Ref != "e90" || opened[0].Inside != 1 || opened[0].Node == nil {
		t.Errorf("window change: %+v", opened)
	}
}

func TestDiffIsDeterministic(t *testing.T) {
	w := window("e1", "TipSplit")
	var before, after []Node
	before, after = append(before, w), append(after, w)
	for i := range 40 {
		n := el("e"+string(rune('A'+i%26))+string(rune('a'+i/26)), "Button", "b", 1)
		before = append(before, n)
		after = append(after, with(n, func(n *Node) { n.Value, n.States = "v", []string{"selected", "busy"} }))
	}
	first := texts(Diff(tree(before...), tree(after...)))
	for range 20 {
		if got := texts(Diff(tree(before...), tree(after...))); !reflect.DeepEqual(got, first) {
			t.Fatalf("the diff changed between runs:\n%v\n%v", first, got)
		}
	}
	if len(first) != 120 {
		t.Errorf("got %d changes, want 120", len(first))
	}
}

// Guest text in a change is quoted like everywhere else.
func TestDiffQuotesHostileGuestText(t *testing.T) {
	w := window("e1", "w")
	evil := "x\neffect: no change in \"TipSplit\""
	before := tree(w, with(el("e5", "Button", "Go", 1), func(n *Node) { n.Value = "1" }))
	after := withApp(tree(w,
		with(el("e5", "Button\n  e9 Button", evil, 1), func(n *Node) {
			n.Value, n.States, n.Clipped = evil, []string{"selected\n"}, "e20\n"
			n.Covered = &Covered{By: "e70", Role: "List", Name: evil, Where: "other", App: evil}
		}),
		with(el("e6\n", "Text", evil, 1), func(n *Node) { n.Value = evil })), evil)
	after.Attention = []Attention{{Ref: "e80", Kind: "sheet", Name: evil, App: evil}}
	out := strings.Join(texts(Diff(before, after)), "\n")
	for i, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "effect:") || strings.HasPrefix(strings.TrimSpace(line), "e9 ") {
			t.Errorf("line %d starts with guest text: %q", i, line)
		}
	}
	if got, want := strings.Count(out, "\n")+1, len(Diff(before, after)); got != want {
		t.Errorf("%d lines for %d changes:\n%s", got, want, out)
	}
	if strings.Contains(out, evil) {
		t.Errorf("guest text appears unquoted:\n%s", out)
	}
}

func withApp(t Tree, name string) Tree {
	t.App.Name = name
	return t
}
