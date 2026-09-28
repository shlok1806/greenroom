package desktop

import (
	"encoding/json"
	"strconv"
)

// refKeys are the fields of the ops' results and error details that hold refs: a node's own
// `ref`, its `window` and `scroller`, what covers or clips it, a tree's `windows`, a snapshot's
// `focused` and `reResolved`. Nothing else is read, so a name or a value that happens to look
// like "e5" is never taken for a ref.
var refKeys = map[string]bool{
	"ref": true, "window": true, "scroller": true, "by": true, "clipped": true,
	"windows": true, "focused": true, "reResolved": true,
}

// HighestRef is the highest ref number in an op's result or error detail, 0 when it holds none.
// The daemon keeps the highest a reader has been handed on a machine, so a new agent connection
// is told to number past it and a ref never names two elements (daemon ADR 0006 point 3).
func HighestRef(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return 0
	}
	return highestIn(v, false)
}

// highestIn walks a decoded JSON value; ref says the value sits under one of refKeys.
func highestIn(v any, ref bool) int {
	best := 0
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			best = max(best, highestIn(x, refKeys[k]))
		}
	case []any:
		for _, x := range t {
			best = max(best, highestIn(x, ref))
		}
	case string:
		if ref && refPattern.MatchString(t) {
			n, _ := strconv.Atoi(t[1:])
			best = n
		}
	}
	return best
}
