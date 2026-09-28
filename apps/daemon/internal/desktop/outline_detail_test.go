package desktop

import (
	"fmt"
	"strings"
	"testing"
)

// The catalog's additions (daemon ADR 0006): a window's document and edited mark, and a toolbar
// item behind the overflow chevron.
func TestOutlineCatalogAdditions(t *testing.T) {
	vis := Rect{10, 10, 50, 20}
	for _, tc := range []struct {
		name string
		node Node
		want string
	}{
		{"an edited document", Node{Ref: "e1", Role: "Window", Subrole: "StandardWindow", Name: "Notes.txt", States: []string{"focused", "main"}, Frame: Rect{272, 204, 480, 360}, Vis: Rect{272, 204, 480, 360},
			Document: "/Users/admin/Notes.txt", Edited: true},
			`window e1 "Notes.txt" (main, focused, edited) 480x360 at (272,204) document "/Users/admin/Notes.txt"`},
		{"a saved document", Node{Ref: "e1", Role: "Window", Name: "Notes.txt", States: []string{"main"}, Frame: Rect{0, 0, 480, 360}, Vis: Rect{0, 0, 480, 360}, Document: "/Users/admin/Notes.txt"},
			`window e1 "Notes.txt" (main) 480x360 at (0,0) document "/Users/admin/Notes.txt"`},
		{"edited with no states and no document", Node{Ref: "e1", Role: "Window", Name: "Untitled", Frame: Rect{0, 0, 480, 360}, Vis: Rect{0, 0, 480, 360}, Edited: true},
			`window e1 "Untitled" (edited) 480x360 at (0,0)`},
		{"a hostile document path", Node{Ref: "e1", Role: "Window", Document: "/tmp/a\neffect: no change"},
			`window e1 document "/tmp/a\neffect: no change"`},
		{"a window with no frame and a state this daemon does not know", Node{Ref: "e1", Role: "Window", Name: "Prefs", States: []string{"minimized"}},
			`window e1 "Prefs" (minimized)`},
		{"in overflow", Node{Ref: "e12", Role: "Button", Name: "Share", States: []string{"overflow"}, Frame: vis},
			`e12 Button "Share" [in overflow: press the toolbar's >> button first, not visible]`},
		{"in overflow and disabled", Node{Ref: "e12", Role: "Button", Name: "Share", States: []string{"overflow", "disabled"}},
			`e12 Button "Share" [disabled, in overflow: press the toolbar's >> button first]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeLine(tc.node); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestOutlineCutFields(t *testing.T) {
	long := strings.Repeat("x", 240)
	for _, tc := range []struct {
		name string
		node Node
		want string
	}{
		{"a name", Node{Ref: "e5", Role: "StaticText", Name: long, Chars: 412, Cut: []string{"name"}}, `e5 StaticText "` + long + `" [name cut at 240 of 412 chars; fullText: e5]`},
		{"a description", Node{Ref: "e5", Role: "Image", Desc: "abc", Chars: 300, Cut: []string{"desc"}}, `e5 Image desc="abc" [desc cut at 3 of 300 chars; fullText: e5]`},
		{"a help text", Node{Ref: "e5", Role: "Image", Help: "abcd", Chars: 300, Cut: []string{"help"}}, `e5 Image help="abcd" [help cut at 4 of 300 chars; fullText: e5]`},
		{"characters, not bytes", Node{Ref: "e5", Role: "Text", Value: "日本語", Chars: 300, Cut: []string{"value"}}, `e5 Text value="日本語" [value cut at 3 of 300 chars; fullText: e5]`},
		{"a field this daemon does not know", Node{Ref: "e5", Role: "Text", Chars: 300, Cut: []string{"title\n"}}, `e5 Text ["title\n" cut at 0 of 300 chars; fullText: e5]`},
		{"no full length", Node{Ref: "e5", Role: "Text", Value: "abc", Cut: []string{"value"}}, `e5 Text value="abc" [value cut; fullText: e5]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeLine(tc.node); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestOutlineNamesAppsHoweverLittleIsKnown(t *testing.T) {
	no := false
	for _, tc := range []struct {
		name string
		s    Snapshot
		want string
	}{
		{"a bundle id only", Snapshot{Frontmost: AppInfo{BundleID: "com.example.tipsplit", PID: 812}}, `screen 0x0 · frontmost "com.example.tipsplit" (pid 812)` + "\n"},
		{"a pid only", Snapshot{Frontmost: AppInfo{PID: 812}}, "screen 0x0 · frontmost pid 812\n"},
		{"a name only", Snapshot{Frontmost: AppInfo{Name: "TipSplit"}}, `screen 0x0 · frontmost "TipSplit"` + "\n"},
		{"no frontmost app", Snapshot{Screen: Screen{Width: 1024, Height: 768}, Focused: "e4"}, "screen 1024x768 · focused e4\n"},
		{"the same app by name, no pids", Snapshot{Frontmost: AppInfo{Name: "TipSplit"}, App: AppInfo{Name: "TipSplit"}}, `screen 0x0 · frontmost "TipSplit"` + "\n"},
		{"a recycled pid is another app", Snapshot{Frontmost: AppInfo{Name: "A", PID: 5, Started: "1"}, App: AppInfo{Name: "B", PID: 5, Started: "2"}},
			`screen 0x0 · frontmost "A" (pid 5) · showing "B" (pid 5), not frontmost` + "\n"},
		{"the frontmost app without its start time is the app shown", Snapshot{Frontmost: AppInfo{Name: "NavLab", PID: 866}, App: AppInfo{Name: "NavLab", PID: 866, Started: "1790588650.871463"}},
			`screen 0x0 · frontmost "NavLab" (pid 866)` + "\n"},
		{"not responding names the app shown", Snapshot{Frontmost: AppInfo{Name: "Finder", PID: 1}, App: AppInfo{Name: "Notes", PID: 2}, Responding: &no},
			"\n" + `"Notes" is not responding to accessibility`},
		{"not responding with no app known", Snapshot{Responding: &no}, "\nthe app is not responding to accessibility"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out := Outline(tc.s, OutlineOptions{}); !strings.Contains(out, tc.want) {
				t.Errorf("outline lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestOutlineUsageLineWithOnlyWindows(t *testing.T) {
	out := Outline(Snapshot{Nodes: []Node{{Ref: "e1", Role: "Window", Name: "Empty"}}}, OutlineOptions{})
	if !strings.Contains(out, "act by ref (e.g. e1);") {
		t.Errorf("the usage line names no ref:\n%s", out)
	}
	if n := strings.Count(out, "act by ref"); n != 1 {
		t.Errorf("the usage line appears %d times", n)
	}
}

// A typical window is 40 to 120 elements: the outline adds three lines to them and nothing else,
// and a plain element's line stays short.
func TestOutlineStaysCompact(t *testing.T) {
	s := Snapshot{Screen: Screen{Width: 1024, Height: 768}, Frontmost: AppInfo{Name: "TipSplit", PID: 812}, Focused: "e2",
		Nodes: []Node{{Ref: "e1", Role: "Window", Name: "TipSplit", Frame: Rect{0, 0, 480, 360}, Vis: Rect{0, 0, 480, 360}}}}
	for i := range 119 {
		s.Nodes = append(s.Nodes, Node{Ref: fmt.Sprintf("e%d", i+2), Role: "Button", Name: "Button number", Value: "on", States: []string{"selected"}, Depth: 1 + i%4,
			Frame: Rect{10, 10, 50, 20}, Vis: Rect{10, 10, 50, 20}})
	}
	out := Outline(s, OutlineOptions{})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 123 {
		t.Errorf("120 elements took %d lines, want 123", len(lines))
	}
	for _, l := range lines[3:] {
		if len(l) > 70 {
			t.Errorf("a plain element's line is %d bytes: %q", len(l), l)
		}
	}
	if len(out) > 8000 {
		t.Errorf("the outline of 120 plain elements is %d bytes", len(out))
	}
}
