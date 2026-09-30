package verifier

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
)

// small outputs cost less to retain than to explain their omission.
const compactOutputMin = 2048

// priorTaskResult omits only large results superseded by a new task. Commands and
// decisions are kept by projectWith; the evidence ledger still reads the original files.
func priorTaskResult(result string) string {
	if len(result) < compactOutputMin {
		return result
	}
	step, _, ok := observationText(result)
	if !ok {
		return result
	}
	return fmt.Sprintf("step %d\n[Earlier task's large tool output omitted from model context. The original is retained in the run transcript; a new task needs fresh evidence.]", step)
}

// observationText recognizes only an entire, positive numeric step header.
func observationText(result string) (step int, body string, ok bool) {
	head, body, found := strings.Cut(result, "\n")
	if !found || !strings.HasPrefix(head, "step ") {
		return 0, "", false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(head, "step "))
	return n, body, err == nil && n > 0 && head == fmt.Sprintf("step %d", n)
}

// compactContext changes a request copy only. Identical outlines share their newest
// full copy, but each observation retains its original evidence step and time (ADR 0043).
func compactContext(msgs []nim.Message) []nim.Message {
	type outlineKey struct{ name, args, body string }
	pending := make(map[string]nim.ToolCall)
	calls := make(map[int]nim.ToolCall)
	for i, msg := range msgs {
		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				pending[call.ID] = call
			}
		} else if msg.Role == "tool" {
			if call, found := pending[msg.ToolCallID]; found {
				calls[i] = call
				delete(pending, msg.ToolCallID)
			}
		}
	}
	out := append([]nim.Message(nil), msgs...)
	latest := make(map[outlineKey]int)
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		call, found := calls[i]
		if msg.Role != "tool" || !found || (call.Name != "machine_ui" && call.Name != "machine_snapshot") || len(msg.Content) < compactOutputMin {
			continue
		}
		step, body, ok := observationText(msg.Content)
		if !ok {
			continue
		}
		key := outlineKey{call.Name, call.Arguments, body}
		if fullStep, exists := latest[key]; exists {
			out[i].Content = fmt.Sprintf("step %d\n[Identical %s observation: the complete outline is retained at step %d in this context. This is a separate observation; cite its original step/time and use the latest element references for input.]", step, call.Name, fullStep)
		} else {
			latest[key] = step
		}
	}
	return out
}
