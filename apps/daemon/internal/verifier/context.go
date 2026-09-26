package verifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	mc, err := snapshot(ctx, mgr, runID)
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
	mc, err := snapshot(ctx, mgr, runID)
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

// snapshot is runID's machine now. A turn whose budget ran out must not read its machine as
// gone: the closing call at the limit still sees this status (issue #127), so the snapshot
// ignores ctx's cancellation. A zero-timeout Wait never blocks.
func snapshot(ctx context.Context, mgr *machine.Manager, runID string) (*machine.Machine, error) {
	return mgr.Wait(context.WithoutCancel(ctx), runID, 0)
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

// project turns the transcript into model context: everything the verifier
// did, progress and the reply, ask or report_verdict that ended each turn,
// becomes the assistant tool call that produced it, with its result; everyone
// else speaks as user. A past verdict must never read as assistant prose: a
// model imitates its history, and a verdict written as prose is posted as a
// reply, so a new pass would never supersede an accepted fail.
func project(msgs []session.Message) []nim.Message {
	out := []nim.Message{{Role: "system", Content: systemPrompt}}
	for _, m := range msgs {
		switch m.Kind {
		case session.Progress:
			name, args, result := splitProgress(m.Text)
			out = append(out, toolTurn(fmt.Sprintf("p%d", m.Seq), name, args, result)...)
		case session.Reply:
			out = append(out, toolTurn(fmt.Sprintf("r%d", m.Seq), "reply", jsonArgs(map[string]any{"text": m.Text}),
				fmt.Sprintf("Posted as message %d.", m.Seq))...)
		case session.Question:
			out = append(out, toolTurn(fmt.Sprintf("q%d", m.Seq), "ask", jsonArgs(map[string]any{"question": m.Text}),
				fmt.Sprintf("Posted as question %d; your turn ended until someone answers.", m.Seq))...)
		case session.Verdict:
			args := map[string]any{"verdict": m.Verdict, "summary": m.Text}
			if len(m.Checks) > 0 {
				args["checks"] = answeredChecks(m.Checks)
			}
			if len(m.Evidence) > 0 {
				args["evidence"] = m.Evidence
			}
			out = append(out, toolTurn(fmt.Sprintf("v%d", m.Seq), "report_verdict", jsonArgs(args),
				fmt.Sprintf("Posted as verdict %d, a proposal the coder or a human may accept or dispute. "+
					"A later task gets a new verdict of its own through report_verdict.", m.Seq))...)
		default:
			out = append(out, nim.Message{Role: "user", Content: speaker(m)})
		}
	}
	return out
}

// answeredChecks is a verdict's checks as report_verdict takes them: the criterion is the
// declaration's, so it is left out.
func answeredChecks(checks []session.Check) []map[string]any {
	out := make([]map[string]any, len(checks))
	for i, c := range checks {
		out[i] = map[string]any{"id": c.ID, "status": c.Status, "evidence": orEmpty(c.Evidence),
			"actions": orEmpty(c.Actions), "observed": c.Observed}
	}
	return out
}

func orEmpty(steps []int) []int {
	if steps == nil {
		return []int{}
	}
	return steps
}

// toolTurn is one assistant tool call and its result.
func toolTurn(id, name, args, result string) []nim.Message {
	return []nim.Message{
		{Role: "assistant", ToolCalls: []nim.ToolCall{{ID: id, Name: name, Arguments: args}}},
		{Role: "tool", ToolCallID: id, Content: result},
	}
}

func jsonArgs(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// proseVerdict matches prose that claims to be a verdict or a question: the
// shapes an older context taught the model ("[I reported verdict pass] ...")
// and a bare "Verdict: pass". Such prose would be posted as a reply.
var proseVerdict = regexp.MustCompile(`(?i)^\s*(\[\s*I (reported|report) (a )?verdict|\[\s*I asked\]|\**verdict\**\s*[:=-]\s*\**\s*(pass|fail|inconclusive))`)

// proseNudge is what the model is told when it writes proseVerdict.
const proseNudge = "You wrote a verdict or a question as plain text, which is posted as an ordinary reply and " +
	"records no verdict. Call report_verdict (or ask) now with the same content."

// What the model is told when a person takes the screen and when it comes back (issue #124). A
// plan made on the screen before a handover must not be carried out after it: the manager
// refuses the verifier's input until it looks again (machine.ErrStaleLook).
const (
	takenAdvice = "A person has the screen now. You may look (machine_ui, machine_screenshot) but not act: do " +
		"not click, type, press keys or scroll until they give it back. If you need to act, end the turn with ask."
	returnedAdvice = "The screen is yours again, and the person may have changed it. Look at the screen with " +
		"machine_ui before any input, then carry on from where you stopped."
)

// claimLabel follows the coder's task: it wrote the code under test, so what it says it did is a
// claim to check, never evidence (ADR 0024). A judge that sees the claim passes more often.
const claimLabel = "(The coder's statements about what it did or what the app shows are unverified claims: a " +
	"source of checks, never a reason for a verdict.)"

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
		if m.From == session.Coder {
			return label + " gives you a task: " + m.Text + "\n" + claimLabel
		}
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
		switch m.Control {
		case session.ControlTaken:
			return "machine event: " + m.Text + ". " + takenAdvice
		case session.ControlReturned:
			return "machine event: " + m.Text + ". " + returnedAdvice
		}
		return "machine event: " + m.Text
	}
	return label + ": " + m.Text
}

// progressText packs a tool call and its result into a progress message.
// splitProgress is its inverse; the format is shared by both brains.
func progressText(call nim.ToolCall, result string) string {
	return call.Name + " " + oneLine(call.Arguments) + "\n" + result
}

// oneLine keeps a call's arguments on the progress text's first line. A model that sends
// pretty-printed JSON would otherwise be projected back, next turn, as a call whose arguments
// are "{" and whose result starts with its own fields: a broken call for it to imitate.
func oneLine(args string) string {
	if !strings.ContainsAny(args, "\r\n") {
		return args
	}
	var b bytes.Buffer
	if json.Compact(&b, []byte(args)) == nil {
		return b.String()
	}
	return strings.Join(strings.Fields(args), " ")
}

func splitProgress(text string) (name, args, result string) {
	head, rest, _ := strings.Cut(text, "\n")
	name, args, _ = strings.Cut(head, " ")
	if args == "" {
		args = "{}"
	}
	return name, args, rest
}
