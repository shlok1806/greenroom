package desktop

// The wire forms of the action arguments that differ from the tool's (TypeOp), and the effect of a
// scroll, which has no before and after of one element but a container that moved.

// TypeOp is the `type` op's arguments as the agent reads them, with the pace filled in.
type TypeOp struct {
	Ref       string `json:"ref,omitempty"`
	Text      string `json:"text"`
	Replace   bool   `json:"replace,omitempty"`
	Submit    string `json:"submit,omitempty"`
	Via       string `json:"via,omitempty"`
	PaceMs    int    `json:"paceMs"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
}

// Op turns normalized arguments into the op's.
func (a TypeArgs) Op() TypeOp {
	return TypeOp{Ref: a.Ref, Text: a.Text, Replace: a.Replace, Submit: a.Submit, Via: a.Via,
		PaceMs: a.Pace(), TimeoutMs: a.TimeoutMs}
}

// EffectOfScroll is a scroll's effect for its step: changed when the container moved or anything
// else changed, none when both trees agree and nothing moved, unknown without the trees to say.
func EffectOfScroll(r ScrollResult) Effect {
	moved := axesMoved(r.From, r.To) != ""
	e := Effect{App: treeAppName(r.Before, r.After), Settled: true}
	if r.Before != nil && r.After != nil {
		e.Changes = Diff(*r.Before, *r.After)
	}
	switch {
	case moved || len(e.Changes) > 0:
		e.Kind = EffectChanged
	case r.Before == nil || r.After == nil:
		e.Kind, e.Reason = EffectUnknown, "no trees from before and after the scroll to compare"
	default:
		e.Kind = EffectNone
	}
	return e
}
