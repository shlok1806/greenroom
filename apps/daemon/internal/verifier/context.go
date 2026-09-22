package verifier

import (
	"context"
	"fmt"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// The model context is rebuilt from the transcript every turn, so the file is
// the memory and the daemon can restart mid-conversation.

const (
	bootingAdvice = "Tools that touch the machine will not work until it is ready; you can still talk."
	deadAdvice    = "The machine cannot be used. Answer in words and say so if asked to do anything on it."
)

// machineStatus is one line describing runID's machine. Wait with a zero
// timeout returns a snapshot at once, so it is cheap to ask every step.
func machineStatus(ctx context.Context, mgr *machine.Manager, runID string) string {
	mc, err := mgr.Wait(ctx, runID, 0)
	if err != nil || mc == nil {
		return "Machine status: gone. " + deadAdvice
	}
	line := "Machine status: " + string(mc.Status)
	if mc.IP != "" {
		line += "; ip " + mc.IP
	}
	if mc.Error != "" {
		line += "; error: " + mc.Error
	}
	line += "."
	switch mc.Status {
	case machine.Booting:
		line += " " + bootingAdvice
	case machine.Failed:
		line += " " + deadAdvice
	}
	return line
}

// unusable says why runID's machine cannot be driven right now, or "". The
// manager would refuse anyway; this makes the refusal readable.
func unusable(ctx context.Context, mgr *machine.Manager, runID string) string {
	mc, err := mgr.Wait(ctx, runID, 0)
	if err != nil || mc == nil {
		return "there is no machine for this run any more"
	}
	switch mc.Status {
	case machine.Ready:
		return ""
	case machine.Booting:
		return "the machine is still booting"
	}
	return "the machine failed to boot: " + orElse(mc.Error, "no reason recorded")
}

// withStatus inserts the machine status right after the system prompt.
func withStatus(msgs []nim.Message, status string) []nim.Message {
	if len(msgs) == 0 {
		return []nim.Message{{Role: "user", Content: status}}
	}
	out := make([]nim.Message, 0, len(msgs)+1)
	out = append(out, msgs[0], nim.Message{Role: "user", Content: status})
	return append(out, msgs[1:]...)
}

// project turns the transcript into model context: the verifier's progress
// becomes assistant tool calls with results, everyone else speaks as user.
func project(msgs []session.Message) []nim.Message {
	out := []nim.Message{{Role: "system", Content: systemPrompt}}
	for _, m := range msgs {
		switch m.Kind {
		case session.Progress:
			id := fmt.Sprintf("p%d", m.Seq)
			name, args, result := splitProgress(m.Text)
			out = append(out,
				nim.Message{Role: "assistant", ToolCalls: []nim.ToolCall{{ID: id, Name: name, Arguments: args}}},
				nim.Message{Role: "tool", ToolCallID: id, Content: result})
		case session.Reply:
			out = append(out, nim.Message{Role: "assistant", Content: m.Text})
		case session.Question:
			out = append(out, nim.Message{Role: "assistant", Content: "[I asked] " + m.Text})
		case session.Verdict:
			out = append(out, nim.Message{Role: "assistant", Content: fmt.Sprintf("[I reported verdict %s] %s", m.Verdict, m.Text)})
		default:
			out = append(out, nim.Message{Role: "user", Content: speaker(m)})
		}
	}
	return out
}

// projectLate projects messages that arrive mid-turn, skipping the verifier's
// own so they are not mistaken for new history.
func projectLate(msgs []session.Message) []nim.Message {
	var out []nim.Message
	for _, m := range msgs {
		if m.From == session.Verifier {
			continue
		}
		out = append(out, nim.Message{Role: "user", Content: "[while you were working] " + speaker(m)})
	}
	return out
}

func speaker(m session.Message) string {
	label := string(m.From)
	switch m.Kind {
	case session.Task:
		return label + " gives you a task: " + m.Text
	case session.Note:
		// A human's note is owed an answer; the coder's is background.
		if m.From == session.Human {
			return label + " says: " + m.Text
		}
		return label + " notes: " + m.Text
	case session.Answer:
		return label + " answers your question: " + m.Text
	case session.Accept:
		return label + " accepts your verdict."
	case session.Dispute:
		return label + " disputes your verdict: " + m.Text + "\nRe-examine the evidence. Run more tools if you need to, then report a verdict again and say whether it changed and why."
	case session.Event:
		return "machine event: " + m.Text
	}
	return label + ": " + m.Text
}

// progressText packs a tool call and its result into a progress message.
// splitProgress is its inverse; the format is shared by both brains.
func progressText(call nim.ToolCall, result string) string {
	return call.Name + " " + call.Arguments + "\n" + result
}

func splitProgress(text string) (name, args, result string) {
	head, rest, _ := strings.Cut(text, "\n")
	name, args, _ = strings.Cut(head, " ")
	if args == "" {
		args = "{}"
	}
	return name, args, rest
}
