package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

func secretNode() *Node {
	return &Node{Ref: "e9", Role: "TextField", Name: "Password", States: []string{StateSecret}, Chars: 7}
}

// A step never records what was expected of, or read from, a secure text field (catalog I15).
func TestWaitsAndExpectationsOnASecretKeepOnlyLengths(t *testing.T) {
	wait := WaitArgs{Target: WaitTarget{Ref: "e9"}, State: WaitValue, Value: &ValueMatch{Op: OpEquals, Expected: "hunter2"}}
	value := "hunter2"
	got := wait.Redacted(WaitResult{Node: secretNode()})
	if got.Value.Expected != "<secret, 7 chars>" || wait.Value.Expected != "hunter2" {
		t.Errorf("redacted wait %+v (the caller's %+v must stay as it was)", got.Value, wait.Value)
	}
	if r := (WaitResult{Node: secretNode(), Value: &value}).Redacted(); *r.Value != "<secret, 7 chars>" {
		t.Errorf("a secure field's waited value was kept: %q", *r.Value)
	}
	plain := &Node{Ref: "e4", Role: "TextField", Value: "12"}
	if got := wait.Redacted(WaitResult{Node: plain}); got.Value.Expected != "hunter2" {
		t.Errorf("a plain field's wait was redacted: %+v", got.Value)
	}

	expect := ExpectArgs{Target: WaitTarget{Ref: "e9"}, Property: PropValue, Op: OpEquals, Expected: "hunter2"}
	res := ExpectResult{Passed: true, Observed: json.RawMessage(`"hunter2"`), Node: secretNode()}
	if got := expect.Redacted(res); got.Expected != "<secret, 7 chars>" {
		t.Errorf("redacted expect %+v", got)
	}
	if got := res.Redacted(PropValue); strings.Contains(string(got.Observed), "hunter2") || string(got.Observed) != `"<secret, 7 chars>"` {
		t.Errorf("observed %s", got.Observed)
	}
	// Whether it is enabled is no secret.
	flag := ExpectArgs{Target: WaitTarget{Ref: "e9"}, Property: PropEnabled, Op: OpEquals, Expected: true}
	if got := flag.Redacted(res); got.Expected != true {
		t.Errorf("a flag expectation was redacted: %+v", got)
	}
}
