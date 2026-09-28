package desktop

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

const refsLook = `refs look like "e17" and come from machine_snapshot, machine_find or an action's result`

// normalizer is every argument type: decoded from a tool call, then normalized.
type normalizer interface{ Normalize() error }

// parse decodes raw into a new T and normalizes it, as a surface does.
func parse[T any, P interface {
	*T
	normalizer
}](raw string) (T, error) {
	var v T
	if err := DecodeArgs([]byte(raw), &v); err != nil {
		return v, err
	}
	return v, P(&v).Normalize()
}

// argCase is one tool call: its arguments, and the error a model reads or the arguments as the
// daemon sends them on.
type argCase[T any] struct {
	name    string
	raw     string
	want    T
	wantErr string
}

func runArgCases[T any, P interface {
	*T
	normalizer
}](t *testing.T, cases []argCase[T]) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse[T, P](tc.raw)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("no error for %s; got %+v, want error %q", tc.raw, got, tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Errorf("error:\ngot  %s\nwant %s", err, tc.wantErr)
				}
				var ae *ArgError
				if !errors.As(err, &ae) || ae.Field == "" {
					t.Errorf("the error names no field: %#v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error for %s: %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestRefValidate(t *testing.T) {
	for _, ok := range []Ref{"e0", "e1", "e17", "e20000", "e999999999"} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for ref, want := range map[Ref]string{
		"":            `ref: missing; pass a ref such as "e17" (` + refsLook + `)`,
		"17":          `ref: "17" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`,
		"E17":         `ref: "E17" is not a ref; ` + refsLook,
		"e017":        `ref: "e017" is not a ref; ` + refsLook,
		"e":           `ref: "e" is not a ref; ` + refsLook,
		"e-1":         `ref: "e-1" is not a ref; ` + refsLook,
		"e1.5":        `ref: "e1.5" is not a ref; ` + refsLook,
		" e17":        `ref: " e17" is not a ref; ` + refsLook,
		"e17\n":       `ref: "e17\n" is not a ref; ` + refsLook,
		"t1":          `ref: "t1" is not a ref; ` + refsLook,
		"e1234567890": `ref: "e1234567890" is not a ref; ` + refsLook,
		"Open run":    `ref: "Open run" is not a ref; ` + refsLook,
	} {
		err := ref.Validate()
		if err == nil || err.Error() != want {
			t.Errorf("%q:\ngot  %v\nwant %s", ref, err, want)
		}
	}
}

func TestBoundsClamp(t *testing.T) {
	for _, tc := range []struct {
		b       Bounds
		in      int
		want    int
		wantErr string
	}{
		{ActionTimeout, 0, 5000, ""},
		{ActionTimeout, 1, 1, ""},
		{ActionTimeout, 30000, 30000, ""},
		{ActionTimeout, 30001, 30000, ""},
		{ActionTimeout, math.MaxInt, 30000, ""},
		{ActionTimeout, -1, 0, "timeoutMs: -1 is negative; pass 0 or leave it out for the default of 5000, at most 30000"},
		{WaitTimeout, 0, 10000, ""},
		{WaitTimeout, 60000, 40000, ""},
		{ExpectTimeout, 0, 2000, ""},
		{ExpectTimeout, 40001, 40000, ""},
		{SnapshotLimit, 0, 250, ""},
		{SnapshotLimit, 5000, 1000, ""},
		{SnapshotLimit, -5, 0, "limit: -5 is negative; pass 0 or leave it out for the default of 250, at most 1000"},
		{FindLimit, 0, 50, ""},
		{FindLimit, 201, 200, ""},
	} {
		got, err := tc.b.Clamp(tc.in)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s %d: error %v, want %s", tc.b.Field, tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s %d: got %d, %v; want %d", tc.b.Field, tc.in, got, err, tc.want)
		}
	}
}

func TestSnapshotArgs(t *testing.T) {
	runArgCases(t, []argCase[SnapshotArgs]{
		{name: "defaults", raw: `{}`, want: SnapshotArgs{Mode: ModeInteractive, Limit: 250}},
		{name: "no arguments at all", raw: ``, want: SnapshotArgs{Mode: ModeInteractive, Limit: 250}},
		{name: "everything", raw: `{"runId":"r1","app":"TipSplit","window":"Settings","ref":"e20","mode":"all","limit":400,"fullText":["e33","e34"]}`,
			want: SnapshotArgs{App: "TipSplit", Window: "Settings", Ref: "e20", Mode: ModeAll, Limit: 400, FullText: []string{"e33", "e34"}}},
		{name: "text mode, limit clamped", raw: `{"mode":"text","limit":99999}`, want: SnapshotArgs{Mode: ModeText, Limit: 1000}},
		{name: "unknown mode", raw: `{"mode":"full"}`, wantErr: `mode: "full" is not one of "interactive", "all", "text"`},
		{name: "mode in another case", raw: `{"mode":"All"}`, wantErr: `mode: "All" is not one of "interactive", "all", "text"`},
		{name: "a bare number as a ref", raw: `{"ref":"20"}`, wantErr: `ref: "20" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "a number where a ref goes", raw: `{"ref":20}`, wantErr: `ref: must be a string, not the number`},
		{name: "a bad ref in fullText", raw: `{"fullText":["e33","33"]}`, wantErr: `fullText[1]: "33" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "negative limit", raw: `{"limit":-1}`, wantErr: `limit: -1 is negative; pass 0 or leave it out for the default of 250, at most 1000`},
		{name: "limit as a string", raw: `{"limit":"250"}`, wantErr: `limit: must be a whole number, not a string`},
		{name: "limit as a fraction", raw: `{"limit":2.5}`, wantErr: `limit: must be a whole number, not the number 2.5`},
		{name: "fullText as a string", raw: `{"fullText":"e33"}`, wantErr: `fullText: must be a list, not a string`},
	})
}

func TestFindArgs(t *testing.T) {
	yes, no := true, false
	runArgCases(t, []argCase[FindArgs]{
		{name: "defaults", raw: `{"text":"Details"}`, want: FindArgs{Text: "Details", IncludeOffscreen: &yes, Limit: 50}},
		{name: "everything", raw: `{"text":"/^Det/","role":"Button","app":"TipSplit","includeOffscreen":false,"limit":1000}`,
			want: FindArgs{Text: "/^Det/", Role: "Button", App: "TipSplit", IncludeOffscreen: &no, Limit: 200}},
		{name: "whitespace is text", raw: `{"text":" "}`, want: FindArgs{Text: " ", IncludeOffscreen: &yes, Limit: 50}},
		{name: "no text", raw: `{}`, wantErr: `text: missing; pass the text to find (a substring, or /regex/)`},
		{name: "an empty regex", raw: `{"text":"//"}`, wantErr: `text: the /regex/ is empty; put a pattern between the slashes, or pass plain text`},
		{name: "negative limit", raw: `{"text":"a","limit":-2}`, wantErr: `limit: -2 is negative; pass 0 or leave it out for the default of 50, at most 200`},
		{name: "includeOffscreen as a string", raw: `{"text":"a","includeOffscreen":"yes"}`, wantErr: `includeOffscreen: must be true or false, not a string`},
	})
}

func TestPressArgs(t *testing.T) {
	runArgCases(t, []argCase[PressArgs]{
		{name: "a ref, defaults", raw: `{"ref":"e11"}`, want: PressArgs{Ref: "e11", Count: 1, Via: ViaPointer, TimeoutMs: 5000}},
		{name: "everything", raw: `{"ref":"e11","button":"Right","count":2,"mods":["Cmd","shift"],"via":"pointer","timeoutMs":60000}`,
			want: PressArgs{Ref: "e11", Button: "Right", Count: 2, Mods: []string{"Cmd", "shift"}, Via: ViaPointer, TimeoutMs: 30000}},
		{name: "triple click by accessibility", raw: `{"ref":"e11","count":3,"via":"ax"}`, want: PressArgs{Ref: "e11", Count: 3, Via: ViaAX, TimeoutMs: 5000}},
		{name: "a point with a reason", raw: `{"x":0.5,"y":0.25,"reason":"a canvas with no elements","force":true}`,
			want: PressArgs{X: ptr(0.5), Y: ptr(0.25), Reason: "a canvas with no elements", Force: true, Count: 1, Via: ViaPointer, TimeoutMs: 5000}},
		{name: "the point 0,0 is a point", raw: `{"x":0,"y":0,"reason":"the corner of a canvas"}`,
			want: PressArgs{X: ptr(0.0), Y: ptr(0.0), Reason: "the corner of a canvas", Count: 1, Via: ViaPointer, TimeoutMs: 5000}},
		{name: "nothing to press", raw: `{}`, wantErr: `ref: missing; pass a ref such as "e17" (` + refsLook + `)`},
		{name: "a ref and a point", raw: `{"ref":"e11","x":0.5,"y":0.5,"reason":"r"}`, wantErr: `ref: pass a ref or x and y, not both; a ref is better when the element has one`},
		{name: "half a point", raw: `{"x":0.5,"reason":"r"}`, wantErr: `x: a point needs both x and y, fractions of the screen 0 to 1`},
		{name: "a point with no reason", raw: `{"x":0.5,"y":0.5}`, wantErr: `reason: a press at a point needs a reason: say why the target has no ref (a canvas, a game); otherwise pass its ref`},
		{name: "a point with a blank reason", raw: `{"x":0.5,"y":0.5,"reason":"  "}`, wantErr: `reason: a press at a point needs a reason: say why the target has no ref (a canvas, a game); otherwise pass its ref`},
		{name: "a point by accessibility", raw: `{"x":0.5,"y":0.5,"reason":"r","via":"ax"}`, wantErr: `via: "ax" presses an element and needs its ref; a point is pressed with the pointer`},
		{name: "a bad ref", raw: `{"ref":"11"}`, wantErr: `ref: "11" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "unknown button", raw: `{"ref":"e11","button":"primary"}`, wantErr: `button: unknown button "primary"; use left, right or middle`},
		{name: "four clicks", raw: `{"ref":"e11","count":4}`, wantErr: `count: 4 is out of range; pass 1 (a click), 2 (a double click) or 3`},
		{name: "negative clicks", raw: `{"ref":"e11","count":-1}`, wantErr: `count: -1 is out of range; pass 1 (a click), 2 (a double click) or 3`},
		{name: "unknown modifier", raw: `{"ref":"e11","mods":["cmd","super"]}`, wantErr: `mods[1]: unknown modifier "super"; use cmd, shift, alt, ctrl, fn (or command, meta, option, opt, control, function)`},
		{name: "unknown via", raw: `{"ref":"e11","via":"mouse"}`, wantErr: `via: "mouse" is not one of "pointer", "ax"`},
		{name: "negative timeout", raw: `{"ref":"e11","timeoutMs":-5}`, wantErr: `timeoutMs: -5 is negative; pass 0 or leave it out for the default of 5000, at most 30000`},
		{name: "x as a string", raw: `{"x":"0.5","y":0.5,"reason":"r"}`, wantErr: `x: must be a number, not a string`},
		{name: "mods as a string", raw: `{"ref":"e11","mods":"cmd"}`, wantErr: `mods: must be a list, not a string`},
	})
}

func TestPressArgsNotANumber(t *testing.T) {
	a := PressArgs{X: ptr(math.NaN()), Y: ptr(0.5), Reason: "r"}
	if err := a.Normalize(); err == nil || err.Error() != "x: x and y must be numbers, fractions of the screen 0 to 1" {
		t.Errorf("got %v", err)
	}
	a = PressArgs{X: ptr(0.5), Y: ptr(math.Inf(1)), Reason: "r"}
	if err := a.Normalize(); err == nil {
		t.Error("an infinite y passed")
	}
}

// The op takes guest points where the tool takes fractions, and the button in lower case.
func TestPressOp(t *testing.T) {
	a, err := parse[PressArgs](`{"x":0.5,"y":0.25,"reason":"canvas","force":true,"button":"Right","count":2,"mods":["cmd"]}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a.Op(Screen{Width: 1024, Height: 768}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"point":[512,192],"reason":"canvas","force":true,"button":"right","count":2,"mods":["cmd"],"via":"pointer","timeoutMs":5000}`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	a, err = parse[PressArgs](`{"ref":"e11","via":"ax"}`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(a.Op(Screen{Width: 1024, Height: 768}))
	if got, want := string(b), `{"ref":"e11","count":1,"via":"ax","timeoutMs":5000}`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestTypeArgs(t *testing.T) {
	runArgCases(t, []argCase[TypeArgs]{
		{name: "into the focus, defaults", raw: `{"text":"120"}`, want: TypeArgs{Text: "120", Via: TypeViaUnicode, TimeoutMs: 5000}},
		{name: "everything", raw: `{"ref":"e4","text":"120","replace":true,"submit":"return","via":"keys","timeoutMs":45000}`,
			want: TypeArgs{Ref: "e4", Text: "120", Replace: true, Submit: "return", Via: TypeViaKeys, TimeoutMs: 30000}},
		{name: "submit with tab", raw: `{"text":"a","submit":"tab","via":"unicode"}`, want: TypeArgs{Text: "a", Submit: "tab", Via: TypeViaUnicode, TimeoutMs: 5000}},
		{name: "whitespace only is typed", raw: `{"text":" \n"}`, want: TypeArgs{Text: " \n", Via: TypeViaUnicode, TimeoutMs: 5000}},
		{name: "no text", raw: `{"ref":"e4"}`, wantErr: `text: missing; pass the characters to type (to clear a field, use machine_set_value with an empty value, or replace with the new text)`},
		{name: "a bad ref", raw: `{"ref":"4","text":"a"}`, wantErr: `ref: "4" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "unknown submit", raw: `{"text":"a","submit":"enter"}`, wantErr: `submit: "enter" is not one of "return", "tab"; leave it out to press nothing after typing`},
		{name: "unknown via", raw: `{"text":"a","via":"paste"}`, wantErr: `via: "paste" is not one of "unicode", "keys"; leave it out for "unicode" (exact text on any keyboard layout), or pass "keys" when the app must see real key presses`},
		{name: "negative timeout", raw: `{"text":"a","timeoutMs":-1}`, wantErr: `timeoutMs: -1 is negative; pass 0 or leave it out for the default of 5000, at most 30000`},
		{name: "text as a number", raw: `{"text":120}`, wantErr: `text: must be a string, not the number`},
		{name: "replace as a string", raw: `{"text":"a","replace":"true"}`, wantErr: `replace: must be true or false, not a string`},
	})
}

func TestSetValueArgs(t *testing.T) {
	runArgCases(t, []argCase[SetValueArgs]{
		{name: "a value", raw: `{"ref":"e30","value":"0.5"}`, want: SetValueArgs{Ref: "e30", Value: "0.5", TimeoutMs: 5000}},
		{name: "an empty value clears", raw: `{"ref":"e4","value":"","timeoutMs":31000}`, want: SetValueArgs{Ref: "e4", TimeoutMs: 30000}},
		{name: "no ref", raw: `{"value":"1"}`, wantErr: `ref: missing; pass a ref such as "e17" (` + refsLook + `)`},
		{name: "a value as a number", raw: `{"ref":"e30","value":0.5}`, wantErr: `value: must be a string, not the number`},
		{name: "negative timeout", raw: `{"ref":"e30","value":"1","timeoutMs":-1}`, wantErr: `timeoutMs: -1 is negative; pass 0 or leave it out for the default of 5000, at most 30000`},
	})
}

func TestKeyArgs(t *testing.T) {
	runArgCases(t, []argCase[KeyArgs]{
		{name: "into the frontmost app", raw: `{"key":"return"}`, want: KeyArgs{Key: "return", TimeoutMs: 5000}},
		{name: "everything", raw: `{"key":"s","mods":["cmd","Shift"],"ref":"e4","timeoutMs":100}`, want: KeyArgs{Key: "s", Mods: []string{"cmd", "Shift"}, Ref: "e4", TimeoutMs: 100}},
		{name: "no key", raw: `{"mods":["cmd"]}`, wantErr: `key: missing; pass a key such as "return", "escape", "tab" or "s" (with mods ["cmd"] for cmd-S)`},
		{name: "a blank key", raw: `{"key":" "}`, wantErr: `key: missing; pass a key such as "return", "escape", "tab" or "s" (with mods ["cmd"] for cmd-S)`},
		{name: "unknown modifier", raw: `{"key":"q","mods":["comand"]}`, wantErr: `mods[0]: unknown modifier "comand"; use cmd, shift, alt, ctrl, fn (or command, meta, option, opt, control, function)`},
		{name: "a bad ref", raw: `{"key":"a","ref":"e"}`, wantErr: `ref: "e" is not a ref; ` + refsLook},
	})
}

func TestModifierNamesAreTheHelpers(t *testing.T) {
	for _, m := range []string{"cmd", "command", "meta", "shift", "alt", "option", "opt", "ctrl", "control", "fn", "function", "CMD", "Option"} {
		if err := validateMods("mods", []string{m}); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
	for _, b := range []string{"left", "right", "middle", "center", "LEFT"} {
		a := PressArgs{Ref: "e1", Button: b}
		if err := a.Normalize(); err != nil {
			t.Errorf("%s: %v", b, err)
		}
	}
}

func TestScrollArgs(t *testing.T) {
	const toHelp = `pass "top", "bottom", a ref such as "e45", {"pages": n} or {"by": points}`
	runArgCases(t, []argCase[ScrollArgs]{
		{name: "to the top", raw: `{"ref":"e20","to":"top"}`, want: ScrollArgs{Ref: "e20", To: ScrollTo{Edge: "top"}, TimeoutMs: 5000}},
		{name: "to the bottom", raw: `{"ref":"e20","to":"bottom","timeoutMs":99999}`, want: ScrollArgs{Ref: "e20", To: ScrollTo{Edge: "bottom"}, TimeoutMs: 30000}},
		{name: "to a ref as a string", raw: `{"ref":"e20","to":"e45"}`, want: ScrollArgs{Ref: "e20", To: ScrollTo{Ref: "e45"}, TimeoutMs: 5000}},
		{name: "to a ref as an object", raw: `{"ref":"e45","to":{"ref":"e45"}}`, want: ScrollArgs{Ref: "e45", To: ScrollTo{Ref: "e45"}, TimeoutMs: 5000}},
		{name: "pages down", raw: `{"ref":"e20","to":{"pages":2}}`, want: ScrollArgs{Ref: "e20", To: ScrollTo{Pages: 2}, TimeoutMs: 5000}},
		{name: "half a page up", raw: `{"ref":"e20","to":{"pages":-0.5}}`, want: ScrollArgs{Ref: "e20", To: ScrollTo{Pages: -0.5}, TimeoutMs: 5000}},
		{name: "by points", raw: `{"ref":"e20","to":{"by":-120}}`, want: ScrollArgs{Ref: "e20", To: ScrollTo{By: -120}, TimeoutMs: 5000}},
		{name: "no ref", raw: `{"to":"top"}`, wantErr: `ref: missing; pass the ref of a scroll container, or of any element inside one`},
		{name: "a bad ref", raw: `{"ref":"20","to":"top"}`, wantErr: `ref: "20" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "no destination", raw: `{"ref":"e20"}`, wantErr: `to: missing; ` + toHelp},
		{name: "an empty destination", raw: `{"ref":"e20","to":{}}`, wantErr: `to: missing; ` + toHelp},
		{name: "a null destination", raw: `{"ref":"e20","to":null}`, wantErr: `to: missing; ` + toHelp},
		{name: "an empty string", raw: `{"ref":"e20","to":""}`, wantErr: `to: missing; ` + toHelp},
		{name: "a word that is no edge and no ref", raw: `{"ref":"e20","to":"end"}`, wantErr: `to.ref: "end" is not a ref; ` + refsLook},
		{name: "an edge in another case", raw: `{"ref":"e20","to":"Top"}`, wantErr: `to.ref: "Top" is not a ref; ` + refsLook},
		{name: "a bare number as the ref", raw: `{"ref":"e20","to":{"ref":"45"}}`, wantErr: `to.ref: "45" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "a number", raw: `{"ref":"e20","to":3}`, wantErr: `to: ` + toHelp},
		{name: "a list", raw: `{"ref":"e20","to":["top"]}`, wantErr: `to: ` + toHelp},
		{name: "a key it does not know", raw: `{"ref":"e20","to":{"page":2}}`, wantErr: `to: has no key "page"; pass {"ref": "e45"}, {"pages": n} or {"by": points}`},
		{name: "two destinations", raw: `{"ref":"e20","to":{"pages":1,"by":100}}`, wantErr: `to: pass one of ref, pages or by, not several`},
		{name: "a ref and pages", raw: `{"ref":"e20","to":{"ref":"e45","pages":1}}`, wantErr: `to: pass one of ref, pages or by, not several`},
		{name: "no pages", raw: `{"ref":"e20","to":{"pages":0}}`, wantErr: `to.pages: 0 scrolls nowhere; pass a positive number to scroll down or a negative one to scroll up`},
		{name: "no points", raw: `{"ref":"e20","to":{"by":0}}`, wantErr: `to.by: 0 scrolls nowhere; pass positive points to scroll down or negative to scroll up`},
		{name: "too many pages", raw: `{"ref":"e20","to":{"pages":101}}`, wantErr: `to.pages: pass a number of pages between -100 and 100`},
		{name: "too many points", raw: `{"ref":"e20","to":{"by":-100001}}`, wantErr: `to.by: pass points between -100000 and 100000`},
		{name: "pages as a string", raw: `{"ref":"e20","to":{"pages":"2"}}`, wantErr: `to.pages: must be a number, not a string; pass {"ref": "e45"}, {"pages": n} or {"by": points}`},
	})
}

func TestScrollToGoesToTheAgentInOneForm(t *testing.T) {
	for in, want := range map[string]string{
		`"top"`:          `"top"`,
		`"bottom"`:       `"bottom"`,
		`"e45"`:          `{"ref":"e45"}`,
		`{"ref":"e45"}`:  `{"ref":"e45"}`,
		`{"pages":-1.5}`: `{"pages":-1.5}`,
		`{"by":240}`:     `{"by":240}`,
	} {
		to := decode[ScrollTo](t, in)
		if err := to.Validate(); err != nil {
			t.Errorf("%s: %v", in, err)
		}
		b, err := json.Marshal(ScrollArgs{Ref: "e20", To: to, TimeoutMs: 5000})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(b), `{"ref":"e20","to":`+want+`,"timeoutMs":5000}`; got != want {
			t.Errorf("%s:\ngot  %s\nwant %s", in, got, want)
		}
	}
}

func TestScrollToDescribe(t *testing.T) {
	for _, tc := range []struct {
		to   ScrollTo
		want string
	}{
		{ScrollTo{Edge: "top"}, "to the top"},
		{ScrollTo{Edge: "bottom"}, "to the bottom"},
		{ScrollTo{Ref: "e45"}, "e45 into view"},
		{ScrollTo{Ref: "e45\n"}, `"e45\n" into view`},
		{ScrollTo{Pages: 1}, "1 page down"},
		{ScrollTo{Pages: -2.5}, "2.5 pages up"},
		{ScrollTo{By: 120}, "120 points down"},
		{ScrollTo{By: -40}, "40 points up"},
		{ScrollTo{}, "nowhere"},
	} {
		if got := tc.to.Describe(); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.to, got, tc.want)
		}
	}
}

func TestSurfaceValuesDecodeLikeJSON(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want ScrollTo
	}{
		{"top", ScrollTo{Edge: "top"}},
		{"e45", ScrollTo{Ref: "e45"}},
		{map[string]any{"ref": "e45"}, ScrollTo{Ref: "e45"}},
		{map[string]any{"pages": 2.0}, ScrollTo{Pages: 2}},
		{map[string]any{"by": -120}, ScrollTo{By: -120}},
		{json.RawMessage(`{"by": 5}`), ScrollTo{By: 5}},
		{[]byte(`"bottom"`), ScrollTo{Edge: "bottom"}},
	} {
		got, err := ScrollToFrom(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ScrollToFrom(%#v) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []any{nil, "", 3, map[string]any{}, map[string]any{"page": 1}, json.RawMessage(``), json.RawMessage(`{"pages":`), "17"} {
		_, err := ScrollToFrom(in)
		var ae *ArgError
		if !errors.As(err, &ae) {
			t.Errorf("ScrollToFrom(%#v): error %v, want an ArgError", in, err)
		}
	}

	for _, tc := range []struct {
		in   any
		want WaitTarget
	}{
		{"e62", WaitTarget{Ref: "e62"}},
		{"idle", WaitTarget{Idle: true}},
		{map[string]any{"text": "Done", "role": "Button"}, WaitTarget{Text: "Done", Role: "Button"}},
		{map[string]any{"idle": true, "app": "TipSplit"}, WaitTarget{Idle: true, App: "TipSplit"}},
		{json.RawMessage(`{"window":"Settings"}`), WaitTarget{Window: "Settings"}},
	} {
		got, err := WaitTargetFrom(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("WaitTargetFrom(%#v) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []any{nil, "", "62", 62, true, map[string]any{}, map[string]any{"title": "x"}, []string{"e62"}} {
		_, err := WaitTargetFrom(in)
		var ae *ArgError
		if !errors.As(err, &ae) {
			t.Errorf("WaitTargetFrom(%#v): error %v, want an ArgError", in, err)
		}
	}
}

const targetsAre = `pass a ref such as "e62", {"text": "Done", "role": "Button"}, {"app": "TipSplit"}, {"window": "Settings"} or {"idle": true}`

func TestWaitArgs(t *testing.T) {
	runArgCases(t, []argCase[WaitArgs]{
		{name: "a ref, defaults", raw: `{"target":"e62"}`, want: WaitArgs{Target: WaitTarget{Ref: "e62"}, State: WaitAppears, TimeoutMs: 10000}},
		{name: "a ref as an object", raw: `{"target":{"ref":"e62"},"state":"disappears","timeoutMs":60000}`, want: WaitArgs{Target: WaitTarget{Ref: "e62"}, State: WaitDisappears, TimeoutMs: 40000}},
		{name: "text and role in an app", raw: `{"target":{"text":"Done","role":"Button","app":"TipSplit"},"state":"enabled"}`,
			want: WaitArgs{Target: WaitTarget{Text: "Done", Role: "Button", App: "TipSplit"}, State: WaitEnabled, TimeoutMs: 10000}},
		{name: "an app", raw: `{"target":{"app":"TipSplit"}}`, want: WaitArgs{Target: WaitTarget{App: "TipSplit"}, State: WaitAppears, TimeoutMs: 10000}},
		{name: "a window of an app", raw: `{"target":{"window":"Settings","app":"TipSplit"},"state":"disappears"}`,
			want: WaitArgs{Target: WaitTarget{Window: "Settings", App: "TipSplit"}, State: WaitDisappears, TimeoutMs: 10000}},
		{name: "idle", raw: `{"target":{"idle":true}}`, want: WaitArgs{Target: WaitTarget{Idle: true}, TimeoutMs: 10000}},
		{name: "idle as a word", raw: `{"target":"idle"}`, want: WaitArgs{Target: WaitTarget{Idle: true}, TimeoutMs: 10000}},
		{name: "idle with the state that means it", raw: `{"target":{"idle":true,"app":"TipSplit"},"state":"changes"}`, want: WaitArgs{Target: WaitTarget{Idle: true, App: "TipSplit"}, TimeoutMs: 10000}},
		{name: "disabled", raw: `{"target":"e62","state":"disabled"}`, want: WaitArgs{Target: WaitTarget{Ref: "e62"}, State: WaitDisabled, TimeoutMs: 10000}},
		{name: "focused", raw: `{"target":"e62","state":"focused"}`, want: WaitArgs{Target: WaitTarget{Ref: "e62"}, State: WaitFocused, TimeoutMs: 10000}},
		{name: "changes", raw: `{"target":"e62","state":"changes"}`, want: WaitArgs{Target: WaitTarget{Ref: "e62"}, State: WaitChanges, TimeoutMs: 10000}},
		{name: "a value", raw: `{"target":"e62","state":"value","value":{"op":"matches","expected":"^\\$2"}}`,
			want: WaitArgs{Target: WaitTarget{Ref: "e62"}, State: WaitValue, Value: &ValueMatch{Op: OpMatches, Expected: `^\$2`}, TimeoutMs: 10000}},
		{name: "a value equal to nothing", raw: `{"target":"e62","state":"value","value":{"op":"equals","expected":""}}`,
			want: WaitArgs{Target: WaitTarget{Ref: "e62"}, State: WaitValue, Value: &ValueMatch{Op: OpEquals}, TimeoutMs: 10000}},

		{name: "no target", raw: `{}`, wantErr: `target: missing; ` + targetsAre},
		{name: "an empty target", raw: `{"target":{}}`, wantErr: `target: missing; ` + targetsAre},
		{name: "an empty string", raw: `{"target":""}`, wantErr: `target: missing; ` + targetsAre},
		{name: "a null target", raw: `{"target":null}`, wantErr: `target: missing; ` + targetsAre},
		{name: "idle false names nothing", raw: `{"target":{"idle":false}}`, wantErr: `target: missing; ` + targetsAre},
		{name: "a number", raw: `{"target":62}`, wantErr: `target: ` + targetsAre},
		{name: "a bare number as a ref", raw: `{"target":"62"}`, wantErr: `target.ref: "62" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "a label where a ref goes", raw: `{"target":"Done"}`, wantErr: `target.ref: "Done" is not a ref; ` + refsLook},
		{name: "a key it does not know", raw: `{"target":{"title":"Settings"}}`, wantErr: `target: has no key "title"; ` + targetsAre},
		{name: "two things", raw: `{"target":{"ref":"e62","text":"Done"}}`, wantErr: `target: names more than one thing; pass one of ref, text, window or idle (app may scope text, window or idle)`},
		{name: "idle and a window", raw: `{"target":{"idle":true,"window":"Settings"}}`, wantErr: `target: names more than one thing; pass one of ref, text, window or idle (app may scope text, window or idle)`},
		{name: "a ref in an app", raw: `{"target":{"ref":"e62","app":"TipSplit"}}`, wantErr: `target.app: a ref already names one element; leave app out`},
		{name: "a role with no text", raw: `{"target":{"role":"Button","app":"TipSplit"}}`, wantErr: `target.role: role narrows a text search; pass text with it`},
		{name: "unknown state", raw: `{"target":"e62","state":"visible"}`, wantErr: `state: "visible" is not one of "appears", "disappears", "enabled", "disabled", "focused", "changes", "value"`},
		{name: "idle with a state", raw: `{"target":{"idle":true},"state":"appears"}`, wantErr: `state: an idle wait has no state; leave state out`},
		{name: "state value with no value", raw: `{"target":"e62","state":"value"}`, wantErr: `value: state "value" needs value: {"op": "equals", "expected": "42"} (op equals, contains or matches)`},
		{name: "a value with another state", raw: `{"target":"e62","state":"appears","value":{"op":"equals","expected":"42"}}`, wantErr: `value: value is only used with state "value"; set state to "value" or leave value out`},
		{name: "a value with no state", raw: `{"target":"e62","value":{"op":"equals","expected":"42"}}`, wantErr: `value: value is only used with state "value"; set state to "value" or leave value out`},
		{name: "unknown value op", raw: `{"target":"e62","state":"value","value":{"op":"atLeast","expected":"42"}}`, wantErr: `value.op: "atLeast" is not one of "equals", "contains", "matches"`},
		{name: "no value op", raw: `{"target":"e62","state":"value","value":{"expected":"42"}}`, wantErr: `value.op: "" is not one of "equals", "contains", "matches"`},
		{name: "an empty pattern", raw: `{"target":"e62","state":"value","value":{"op":"matches","expected":""}}`, wantErr: `value.expected: an empty pattern matches anything; pass the pattern to match`},
		{name: "negative timeout", raw: `{"target":"e62","timeoutMs":-1}`, wantErr: `timeoutMs: -1 is negative; pass 0 or leave it out for the default of 10000, at most 40000`},
		{name: "a value op as a number", raw: `{"target":"e62","state":"value","value":{"op":1,"expected":"42"}}`, wantErr: `value.op: must be a string, not the number`},
		{name: "a value as a string", raw: `{"target":"e62","state":"value","value":"42"}`, wantErr: `value: must be an object, not a string`},
		{name: "idle as a string", raw: `{"target":{"idle":"yes"}}`, wantErr: `target.idle: must be true or false, not a string; ` + targetsAre},
	})
}

func TestWaitTargetKindAndDescribe(t *testing.T) {
	for _, tc := range []struct {
		t        WaitTarget
		kind     string
		describe string
	}{
		{WaitTarget{Ref: "e62"}, TargetRef, "e62"},
		{WaitTarget{Text: "Done"}, TargetText, `an element with text "Done"`},
		{WaitTarget{Text: "Done\nx", Role: "Button", App: "TipSplit"}, TargetText, `a Button with text "Done\nx"`},
		{WaitTarget{Window: "Settings", App: "TipSplit"}, TargetWindow, `window "Settings"`},
		{WaitTarget{App: "TipSplit"}, TargetApp, `app "TipSplit"`},
		{WaitTarget{Idle: true}, TargetIdle, "the app"},
		{WaitTarget{Idle: true, App: "TipSplit"}, TargetIdle, `"TipSplit"`},
		{WaitTarget{}, "", "the target"},
	} {
		if got := tc.t.Kind(); got != tc.kind {
			t.Errorf("%+v: kind %q, want %q", tc.t, got, tc.kind)
		}
		if got := tc.t.Describe(); got != tc.describe {
			t.Errorf("%+v: described as %q, want %q", tc.t, got, tc.describe)
		}
	}
}

func TestExpectArgs(t *testing.T) {
	e62 := WaitTarget{Ref: "e62"}
	runArgCases(t, []argCase[ExpectArgs]{
		{name: "a value, defaults", raw: `{"target":"e62","property":"value","expected":"42"}`, want: ExpectArgs{Target: e62, Property: PropValue, Op: OpEquals, Expected: "42", TimeoutMs: 2000}},
		{name: "a value equal to nothing", raw: `{"target":"e62","property":"value","expected":""}`, want: ExpectArgs{Target: e62, Property: PropValue, Op: OpEquals, Expected: "", TimeoutMs: 2000}},
		{name: "a name that contains", raw: `{"target":{"text":"Total"},"property":"name","op":"contains","expected":"Tot","timeoutMs":50000}`,
			want: ExpectArgs{Target: WaitTarget{Text: "Total"}, Property: PropName, Op: OpContains, Expected: "Tot", TimeoutMs: 40000}},
		{name: "a value that matches", raw: `{"target":"e62","property":"value","op":"matches","expected":"^4"}`, want: ExpectArgs{Target: e62, Property: PropValue, Op: OpMatches, Expected: "^4", TimeoutMs: 2000}},
		{name: "a flag defaults to true", raw: `{"target":"e62","property":"enabled"}`, want: ExpectArgs{Target: e62, Property: PropEnabled, Op: OpEquals, Expected: true, TimeoutMs: 2000}},
		{name: "a flag expected false", raw: `{"target":{"window":"Settings"},"property":"exists","op":"equals","expected":false}`,
			want: ExpectArgs{Target: WaitTarget{Window: "Settings"}, Property: PropExists, Op: OpEquals, Expected: false, TimeoutMs: 2000}},
		{name: "visible", raw: `{"target":"e62","property":"visible","expected":true}`, want: ExpectArgs{Target: e62, Property: PropVisible, Op: OpEquals, Expected: true, TimeoutMs: 2000}},
		{name: "selected", raw: `{"target":"e62","property":"selected"}`, want: ExpectArgs{Target: e62, Property: PropSelected, Op: OpEquals, Expected: true, TimeoutMs: 2000}},
		{name: "a count", raw: `{"target":{"text":"run","role":"Row"},"property":"count","op":"atLeast","expected":3}`,
			want: ExpectArgs{Target: WaitTarget{Text: "run", Role: "Row"}, Property: PropCount, Op: OpAtLeast, Expected: 3, TimeoutMs: 2000}},
		{name: "a count of none", raw: `{"target":{"text":"run"},"property":"count","expected":0}`, want: ExpectArgs{Target: WaitTarget{Text: "run"}, Property: PropCount, Op: OpEquals, Expected: 0, TimeoutMs: 2000}},
		{name: "a count at most", raw: `{"target":{"text":"run"},"property":"count","op":"atMost","expected":10.0}`, want: ExpectArgs{Target: WaitTarget{Text: "run"}, Property: PropCount, Op: OpAtMost, Expected: 10, TimeoutMs: 2000}},

		{name: "no target", raw: `{"property":"value","expected":"42"}`, wantErr: `target: missing; ` + targetsAre},
		{name: "idle", raw: `{"target":"idle","property":"exists"}`, wantErr: `target: idle is for machine_wait_for; expect needs an element, a window or an app`},
		{name: "a bad ref", raw: `{"target":"62","property":"exists"}`, wantErr: `target.ref: "62" is not a ref; ` + refsLook + ` (machine_ui's element numbers are not refs)`},
		{name: "no property", raw: `{"target":"e62","expected":"42"}`, wantErr: `property: missing; pass one of "value", "name", "exists", "visible", "enabled", "selected", "count"`},
		{name: "unknown property", raw: `{"target":"e62","property":"title","expected":"42"}`, wantErr: `property: "title" is not one of "value", "name", "exists", "visible", "enabled", "selected", "count"`},
		{name: "unknown op", raw: `{"target":"e62","property":"value","op":"is","expected":"42"}`, wantErr: `op: "is" does not apply to value; use "equals", "contains", "matches"`},
		{name: "a count op on text", raw: `{"target":"e62","property":"name","op":"atLeast","expected":"4"}`, wantErr: `op: "atLeast" does not apply to name; use "equals", "contains", "matches"`},
		{name: "a text op on a count", raw: `{"target":"e62","property":"count","op":"contains","expected":4}`, wantErr: `op: "contains" does not apply to count; use "equals", "atLeast", "atMost"`},
		{name: "a text op on a flag", raw: `{"target":"e62","property":"enabled","op":"contains","expected":true}`, wantErr: `op: "contains" does not apply to enabled; use "equals"`},
		{name: "text with no expected", raw: `{"target":"e62","property":"value"}`, wantErr: `expected: value compares text; pass a string`},
		{name: "text compared with a number", raw: `{"target":"e62","property":"value","expected":42}`, wantErr: `expected: value compares text; pass a string`},
		{name: "a name compared with a bool", raw: `{"target":"e62","property":"name","expected":true}`, wantErr: `expected: name compares text; pass a string`},
		{name: "an empty pattern", raw: `{"target":"e62","property":"value","op":"matches","expected":""}`, wantErr: `expected: an empty pattern matches anything; pass the pattern to match`},
		{name: "a flag compared with a string", raw: `{"target":"e62","property":"enabled","expected":"true"}`, wantErr: `expected: enabled is true or false; pass a bool, or leave it out for true`},
		{name: "a flag compared with a number", raw: `{"target":"e62","property":"exists","expected":1}`, wantErr: `expected: exists is true or false; pass a bool, or leave it out for true`},
		{name: "a count with no expected", raw: `{"target":"e62","property":"count"}`, wantErr: `expected: count compares a whole number of elements; pass one, such as 3`},
		{name: "a count as a string", raw: `{"target":"e62","property":"count","expected":"3"}`, wantErr: `expected: count compares a whole number of elements; pass one, such as 3`},
		{name: "a count with a fraction", raw: `{"target":"e62","property":"count","expected":2.5}`, wantErr: `expected: count compares a whole number of elements; pass one, such as 3`},
		{name: "a negative count", raw: `{"target":"e62","property":"count","expected":-1}`, wantErr: `expected: count compares a whole number of elements; pass one, such as 3`},
		{name: "negative timeout", raw: `{"target":"e62","property":"exists","timeoutMs":-1}`, wantErr: `timeoutMs: -1 is negative; pass 0 or leave it out for the default of 2000, at most 40000`},
	})
}

// A count decoded with UseNumber, as some surfaces do, is a count all the same.
func TestExpectCountFromAJSONNumber(t *testing.T) {
	a := ExpectArgs{Target: WaitTarget{Text: "run"}, Property: PropCount, Expected: json.Number("3")}
	if err := a.Normalize(); err != nil || a.Expected != 3 {
		t.Errorf("got %#v, %v", a.Expected, err)
	}
	a = ExpectArgs{Target: WaitTarget{Text: "run"}, Property: PropCount, Expected: json.Number("1e400")}
	if err := a.Normalize(); err == nil {
		t.Errorf("a number that does not fit passed: %#v", a.Expected)
	}
}

func TestDecodeArgs(t *testing.T) {
	for _, tc := range []struct {
		name, raw, wantErr string
	}{
		{"not JSON", `{"ref":`, `arguments: not valid JSON (unexpected end of JSON input); send one JSON object`},
		{"a syntax error", `{ref: "e1"}`, `arguments: not valid JSON (invalid character 'r' looking for beginning of object key string); send one JSON object`},
		{"a list", `["e1"]`, `arguments: must be a JSON object, not a list`},
		{"a string", `"e1"`, `arguments: must be a JSON object, not a string`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a PressArgs
			err := DecodeArgs([]byte(tc.raw), &a)
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("got  %v\nwant %s", err, tc.wantErr)
			}
		})
	}
	var a PressArgs
	if err := DecodeArgs([]byte(`  `), &a); err != nil {
		t.Errorf("no arguments: %v", err)
	}
	if err := DecodeArgs([]byte(`null`), &a); err != nil {
		t.Errorf("null arguments: %v", err)
	}
}

// within puts a nested field under its parent and leaves other errors alone.
func TestWithin(t *testing.T) {
	if got := within("to", argErr("ref", "bad")).Error(); got != "to.ref: bad" {
		t.Errorf("got %q", got)
	}
	if got := within("to", argErr("to", "bad")).Error(); got != "to: bad" {
		t.Errorf("got %q", got)
	}
	if got := within("to", argErr("", "bad")).Error(); got != "to: bad" {
		t.Errorf("got %q", got)
	}
	plain := errors.New("plain")
	if got := within("to", plain); got != plain {
		t.Errorf("got %v", got)
	}
	if got := within("", argErr("ref", "bad")).Error(); got != "ref: bad" {
		t.Errorf("got %q", got)
	}
	if within("to", nil) != nil {
		t.Error("nil became an error")
	}
}
