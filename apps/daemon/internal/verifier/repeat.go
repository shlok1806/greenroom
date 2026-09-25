package verifier

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// A model that repeats a failing call spends the turn on it: one sent machine_type with no
// text about 6 times in a row (issue #125). The second identical failure gets repeatWarning,
// the third ends the turn with a question naming the call.
const (
	repeatWarnAt = 2
	repeatStopAt = 3
)

// repeatWarning is appended to the result of a call that failed the same way before in this turn.
const repeatWarning = "[greenroom] You already made this exact call and it failed the same way. Do something " +
	"different: read the screen, change the arguments, or ask."

// maxLoggedArgs caps the raw arguments logged for a refused call.
const maxLoggedArgs = 500

// repeats counts the failing tool calls of one turn by call and error. A different call leaves
// the other counts alone; a success of the same call clears its counts.
type repeats map[string]int

// record counts call's result and returns how many times this turn call failed with this exact
// error, or 0 for a success. A screen-taken refusal is neither: Turn handles it (issue #97). A
// look at the screen answers a stale-look refusal, so it clears those counts (issue #124).
func (r repeats) record(call nim.ToolCall, result string) int {
	key := callKey(call) + "\x00"
	if !strings.HasPrefix(result, "error:") {
		look := call.Name == "machine_ui" || call.Name == "machine_screenshot"
		for k := range r {
			if strings.HasPrefix(k, key) || look && strings.Contains(k, "\x00"+staleLookPrefix) {
				delete(r, k)
			}
		}
		return 0
	}
	if strings.HasPrefix(result, screenTakenPrefix) {
		return 0
	}
	r[key+result]++
	return r[key+result]
}

// callKey is the tool name and its arguments as canonical JSON.
func callKey(call nim.ToolCall) string {
	return call.Name + " " + canonicalArgs(call.Arguments)
}

// canonicalArgs re-encodes args with sorted keys and no spacing, so key order does not make two
// calls differ. Arguments that are not JSON are kept as sent, trimmed.
func canonicalArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return args
	}
	b, err := json.Marshal(v)
	if err != nil {
		return args
	}
	return string(b)
}

// guardRepeat records call's result and returns the result the model sees, which on a repeated
// failure says so, and whether this failure ends the turn. Every refused call's raw arguments
// are logged: they show what the model sent when a field it needed was missing (issue #125).
func (v *Verifier) guardRepeat(runID string, seen repeats, call nim.ToolCall, result string) (string, bool) {
	n := seen.record(call, result)
	if n == 0 {
		return result, false
	}
	v.log.Warn("verifier tool call refused", "runId", runID, "tool", call.Name,
		"arguments", clip(call.Arguments, maxLoggedArgs), "result", clip(result, maxLoggedArgs), "times", n)
	switch {
	case n >= repeatStopAt:
		return result, true
	case n >= repeatWarnAt:
		return result + "\n" + repeatWarning, false
	}
	return result, false
}

// askAboutRepeat ends the turn with a question quoting the call that kept failing (issue #125).
func (v *Verifier) askAboutRepeat(store *session.Store, call nim.ToolCall, result string) {
	v.post(store, session.Message{From: session.Verifier, Kind: session.Question,
		Text: repeatQuestion(call, result, standingVerdict(store))})
}

// repeatQuestion names the call, its arguments and its error, and what the person can do.
func repeatQuestion(call nim.ToolCall, result string, standing bool) string {
	why := clip(strings.TrimSpace(strings.TrimPrefix(result, "error:")), 300)
	q := fmt.Sprintf("%s %s failed %d times with the same error: %q. Tell me what to do instead, "+
		"or fix what blocks it, then send a message and I will continue from here.",
		call.Name, clip(canonicalArgs(call.Arguments), 200), repeatStopAt, why)
	if standing {
		q += " My last verdict still stands."
	}
	return q
}

// clip cuts s to at most n bytes, on a rune boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
