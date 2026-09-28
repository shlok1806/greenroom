package desktop

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// What a step may record of a wait or an expectation when its element is a secure text field
// (catalog I15): lengths, never the text.

// Redacted is the wait as a step may record it: when the element it watched is a secure text
// field, the value it waited for is only its length. Any other wait comes back unchanged.
func (a WaitArgs) Redacted(r WaitResult) WaitArgs {
	if r.Node == nil || !r.Node.Secret() || a.Value == nil {
		return a
	}
	v := *a.Value
	v.Expected = SecretText(utf8.RuneCountInString(v.Expected))
	a.Value = &v
	return a
}

// Redacted is the wait's result as a step may record it: a secure field's value is never kept,
// should an agent ever send it.
func (r WaitResult) Redacted() WaitResult {
	if r.Node != nil && r.Node.Secret() && r.Value != nil {
		s := SecretText(r.Node.Chars)
		r.Value = &s
	}
	return r
}

// Redacted is the expectation as a step may record it: when its element is a secure text field,
// an expected value is only its length. Any other expectation comes back unchanged.
func (a ExpectArgs) Redacted(r ExpectResult) ExpectArgs {
	if r.Node == nil || !r.Node.Secret() || a.Property != PropValue {
		return a
	}
	if s, ok := a.Expected.(string); ok {
		a.Expected = SecretText(utf8.RuneCountInString(s))
	}
	return a
}

// Redacted is the expectation's result as a step may record it: the value observed in a secure
// text field is only its length.
func (r ExpectResult) Redacted(p ExpectProperty) ExpectResult {
	if r.Node == nil || !r.Node.Secret() || p != PropValue {
		return r
	}
	r.Observed = json.RawMessage(strconv.Quote(SecretText(r.Node.Chars)))
	return r
}
