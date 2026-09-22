// Package verifier runs greenroom's own agent against one machine.
//
// The agent lives on the host, not in the guest, so no model credential ever
// enters a machine (ADR 0005, and docs/05-transport.md rule 1). It drives the
// machine through the same operations the MCP tools use, and everything it
// does lands in the run record beside the tool calls.
//
// The agent is an actor per run, not a function (ADR 0006). It speaks only
// through the run's conversation: a task, answer or dispute from the coder
// or a human, and any message at all from a human, starts a turn; the turn
// ends with a reply, a question or a verdict. Its model context is rebuilt
// from the conversation at the start of every turn, so the file is the
// memory and the daemon can restart mid-conversation.
//
// A turn runs whatever the machine is doing. The machine's status goes in
// front of the model at the start of the turn and again when it changes, so
// "how is the boot going" is answered while the boot is still going and a
// task sent to a dead machine gets an answer in words.
//
// Two models share the work. One reasons and calls tools. It cannot accept
// images, so a screenshot reaches it as text written by a vision model.
package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

const (
	// DefaultMaxSteps is the tool-call cap a turn gets when Config.MaxSteps
	// is left zero. main.go exposes it as -verifier-max-steps.
	DefaultMaxSteps = 40
	// DefaultBudget is the wall-clock budget a turn gets when Config.Budget
	// is left zero. main.go exposes it as -verifier-budget.
	DefaultBudget = 10 * time.Minute
	execTimeout   = 5 * time.Minute
	maxToolOutput = 6000 // characters of guest output fed back to the model
)

// The agent diagnoses. It does not repair the code under test. That boundary
// is the whole reason a separate agent is safe to run unattended: an agent
// with no knowledge of the author's intent must not edit the author's code.
const systemPrompt = `You are greenroom's verifier. You have one disposable macOS machine and you drive it with tools.

You are in a conversation with the coding agent that wrote the code under test ("coder") and possibly a human watching ("human"). They give you tasks, context and answers; you report what happened with evidence.

Rules:
- Work in small steps. Run one command, read the result, then decide.
- You diagnose failures. You do NOT fix the application source code. If the build breaks because the code is wrong, report it and stop.
- You may install tools, retry flaky steps and work around machine problems. That is infrastructure and it is yours.
- Look at the screen when the task is about what the user sees. A screenshot is described to you in words.
- If you are missing something only the coder or the human knows (a build command, a scheme, whether a dialog is expected), call ask. Do not guess.
- Anyone who speaks to you gets an answer in the transcript: use reply for a status update, an explanation or a plain answer; use ask when you need something; use report_verdict only when a task is complete or clearly impossible.
- When the machine is booting or dead, say so plainly in a reply; do not report a verdict about a task you could not start.
- End every turn by calling exactly one of: reply, ask, or report_verdict. Do not call report_verdict before you have evidence.
- A verdict must cite its evidence: the step numbers and screenshot paths it rests on.
- If a verdict of yours is disputed, re-examine the evidence with the objection in mind. Change your verdict if the objection holds and say why; restate it with the reason if it does not. Do not change your mind just because you were asked to.
- If you cannot finish, report inconclusive and say what blocked you.`

const visionPrompt = `This is the screen of a macOS machine under test. Describe what is on it for an engineer who cannot see it.
Name the frontmost application and window. Quote any visible error text or dialog exactly.
If a system dialog is covering the screen, say so first, because that is a fault of the machine and not of the application under test.
Be factual and brief. Do not guess at anything you cannot read.`

// Config names the endpoint and the two models.
type Config struct {
	BaseURL     string
	APIKey      string
	Model       string // reasons and calls tools
	VisionModel string // reads screenshots
	MaxSteps    int    // tool calls per turn
	Budget      time.Duration
}

// Verifier runs turns. One Verifier serves every run.
type Verifier struct {
	mgr *machine.Manager
	llm *nim.Client
	cfg Config
	log *slog.Logger
}

// New returns a Verifier, or an error if it has no key or no model.
func New(mgr *machine.Manager, cfg Config, log *slog.Logger) (*Verifier, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no model API key; set NVIDIA_API_KEY in .env")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("no model; set GREENROOM_VERIFIER_MODEL in .env")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	if cfg.Budget <= 0 {
		cfg.Budget = DefaultBudget
	}
	return &Verifier{mgr: mgr, llm: nim.New(cfg.BaseURL, cfg.APIKey), cfg: cfg, log: log}, nil
}

// Model names the reasoning model, for reports.
func (v *Verifier) Model() string { return v.cfg.Model }

func (v *Verifier) tools() []nim.Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return []nim.Tool{
		{
			Name:        "machine_exec",
			Description: "Run a shell command in the machine with zsh -lc and return stdout, stderr and the exit code.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": str("The shell command to run."),
					"cwd":     str("Optional working directory in the guest, relative to the home directory."),
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "machine_screenshot",
			Description: "Capture the machine's screen. Returns a written description of what is on it and the path of the saved image.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name: "machine_click",
			Description: "Click the machine's screen at a position. x and y are fractions of the screen (0 to 1), not " +
				"pixels: call machine_screenshot first and reason in that picture. A human may be driving the " +
				"machine; if so this comes back as an error naming them, and the machine is unharmed.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x":      map[string]any{"type": "number", "description": "Horizontal position as a fraction of the screen, 0 (left) to 1 (right)."},
					"y":      map[string]any{"type": "number", "description": "Vertical position as a fraction of the screen, 0 (top) to 1 (bottom)."},
					"button": str("left (default), right, or middle."),
					"clicks": map[string]any{"type": "integer", "description": "2 for a double click. Default 1."},
				},
				"required": []string{"x", "y"},
			},
		},
		{
			Name: "machine_type",
			Description: "Type text into the machine, into whatever currently has keyboard focus. Click into a " +
				"field first if nothing does. A human may be driving the machine; if so this comes back as an " +
				"error naming them, and the machine is unharmed.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"text": str("The text to type, one character event at a time.")},
				"required":   []string{"text"},
			},
		},
		{
			Name: "machine_key",
			Description: "Press one key, optionally with modifiers held down, for example key f with mods [cmd] " +
				"for command-F. A human may be driving the machine; if so this comes back as an error naming " +
				"them, and the machine is unharmed.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": str("A key name: a letter, digit or punctuation character, or one of return, enter, tab, space, " +
						"delete, forwarddelete, escape, left, right, up, down, home, end, pageup, pagedown, capslock, " +
						"help, f1-f12."),
					"mods": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Modifiers held with the key: cmd, shift, alt, ctrl, fn."},
				},
				"required": []string{"key"},
			},
		},
		{
			Name: "machine_scroll",
			Description: "Scroll the machine's screen under the pointer's current position, or under x,y if given. " +
				"A human may be driving the machine; if so this comes back as an error naming them, and the " +
				"machine is unharmed.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x":      map[string]any{"type": "number", "description": "Optional fraction of the screen to move the pointer to first."},
					"y":      map[string]any{"type": "number", "description": "Optional fraction of the screen to move the pointer to first, paired with x."},
					"deltaX": map[string]any{"type": "number", "description": "Horizontal scroll amount, in points. Positive scrolls right."},
					"deltaY": map[string]any{"type": "number", "description": "Vertical scroll amount, in points. Positive scrolls down, negative scrolls up."},
				},
			},
		},
		{
			Name: "machine_input",
			Description: "Post an ordered batch of actions (move, click, down, up, scroll, type, key, sleep) in " +
				"one round trip: compose a drag out of down, move and up. machine_click, machine_type, " +
				"machine_key and machine_scroll are conveniences over this for the common single-action case. " +
				"Coordinates are fractions of the screen (0 to 1). A human may be driving the machine; if so " +
				"this comes back as an error naming them, and the machine is unharmed.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"actions": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"type":   str("One of: move, click, down, up, scroll, type, key, sleep."),
								"x":      map[string]any{"type": "number", "description": "Fraction of the screen, 0 to 1. For move, click, down and up."},
								"y":      map[string]any{"type": "number", "description": "Fraction of the screen, 0 to 1. For move, click, down and up."},
								"button": str("left (default), right, or middle. For click, down and up."),
								"clicks": map[string]any{"type": "integer", "description": "2 for a double click. For click, down and up."},
								"deltaX": map[string]any{"type": "number", "description": "For scroll."},
								"deltaY": map[string]any{"type": "number", "description": "For scroll."},
								"text":   str("For type."),
								"key":    str("For key."),
								"mods":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Modifiers held with key: cmd, shift, alt, ctrl, fn."},
								"ms":     map[string]any{"type": "integer", "description": "Milliseconds to wait. For sleep, capped at 5000."},
							},
							"required": []string{"type"},
						},
						"description": "Ordered actions to post in one batch, for example down, move, up to drag. The whole batch records as one step.",
					},
				},
				"required": []string{"actions"},
			},
		},
		{
			Name:        "reply",
			Description: "Answer whoever spoke when no verdict is called for: a status update, an explanation, or a plain answer to a question. Ends your turn.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"text": str("What you want to say, in plain words.")},
				"required":   []string{"text"},
			},
		},
		{
			Name:        "ask",
			Description: "Ask the coder or the human for something you need and cannot find out yourself. Ends your turn; you continue when someone answers.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"question": str("What you need to know, and why.")},
				"required":   []string{"question"},
			},
		},
		{
			Name:        "report_verdict",
			Description: "End the turn with a verdict. It is a proposal: the coder or a human may accept or dispute it.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"verdict": map[string]any{
						"type":        "string",
						"enum":        []string{"pass", "fail", "inconclusive"},
						"description": "pass if the task succeeded, fail if the thing under test is broken, inconclusive if you could not tell.",
					},
					"summary": str("What happened and what the evidence shows, in a few sentences."),
					"evidence": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Step numbers (as 'step 4') and screenshot paths the verdict rests on.",
					},
				},
				"required": []string{"verdict", "summary"},
			},
		},
	}
}

// TurnResult says how a turn ended.
type TurnResult struct {
	Ended   session.Kind // reply, question or verdict, or "" when the budget ran out
	Steps   int
	Tokens  int
	Seconds float64
}

// Brain runs one turn of a run's conversation. *Verifier satisfies it by
// calling a model; Manual (manual.go) satisfies it with a person typing
// instructions instead. Actors drives whichever one a run was built with,
// so the actor, the retry logic and the transcript rules are the same for
// both (CLAUDE.md: the manual brain is the model brain with a person for a
// model).
type Brain interface {
	Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error)
}

// Turn runs the verifier once over the conversation in store: it rebuilds
// the model context from the transcript, tells the model what the machine is
// doing, loops over tool calls, posts progress as it goes, and ends by
// posting a reply, a question or a verdict. An error means the loop itself
// broke; that is also posted as an event.
func (v *Verifier) Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Budget)
	defer cancel()

	res := TurnResult{}
	seen := store.Len()
	status := machineStatus(ctx, v.mgr, runID)
	msgs := withStatus(project(store.After(0)), status)

	for step := 1; step <= v.cfg.MaxSteps; step++ {
		// Anything said while we were working goes in before the next call,
		// so a human's "ignore that dialog" reaches the model without
		// waiting for the turn to end.
		if fresh := store.After(seen); len(fresh) > 0 {
			msgs = append(msgs, projectLate(fresh)...)
			seen += len(fresh)
		}
		// A boot that finished, or a machine that died, mid-turn. Only the
		// change is worth a message; the status itself is already in context.
		if now := machineStatus(ctx, v.mgr, runID); now != status {
			status = now
			msgs = append(msgs, nim.Message{Role: "user", Content: "[machine status changed] " + now})
		}

		msg, usage, err := v.llm.Chat(ctx, v.cfg.Model, msgs, v.tools())
		res.Tokens += usage.PromptTokens + usage.CompletionTokens
		res.Seconds = since(started)
		if err != nil {
			// The turn's own budget ran out, not the caller giving up: this
			// is not a failure to retry, it is the same kind of graceful stop
			// as the step limit below, so it ends in a reply and no error.
			if ctx.Err() == context.DeadlineExceeded {
				res.Ended = session.Reply
				v.post(store, session.Message{From: session.Verifier, Kind: session.Reply,
					Text: fmt.Sprintf("This turn ran out of time after %s. Send another message and I will continue.", v.cfg.Budget)})
				return res, nil
			}
			v.post(store, session.Message{From: session.System, Kind: session.Event, Text: "verifier turn failed: " + err.Error()})
			return res, err
		}
		msgs = append(msgs, msg)

		if len(msg.ToolCalls) == 0 {
			// The model answered in prose. That is a reply, not a failure:
			// whoever spoke is owed an answer, and prose is an answer. It is
			// not a verdict, because a verdict nobody reasoned towards is
			// worse than none.
			res.Steps, res.Ended = step, session.Reply
			v.post(store, session.Message{From: session.Verifier, Kind: session.Reply, Text: orElse(strings.TrimSpace(msg.Content), "(the verifier had nothing to say)")})
			return res, nil
		}

		for _, call := range msg.ToolCalls {
			switch call.Name {
			case "report_verdict":
				verdict, summary, evidence := parseVerdict(call.Arguments)
				res.Steps, res.Ended = step, session.Verdict
				v.post(store, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: verdict, Text: summary, Evidence: evidence})
				v.log.Info("verifier verdict", "runId", runID, "verdict", verdict, "steps", step)
				return res, nil
			case "reply":
				res.Steps, res.Ended = step, session.Reply
				v.post(store, session.Message{From: session.Verifier, Kind: session.Reply, Text: parseReply(call.Arguments)})
				return res, nil
			case "ask":
				res.Steps, res.Ended = step, session.Question
				v.post(store, session.Message{From: session.Verifier, Kind: session.Question, Text: parseQuestion(call.Arguments)})
				return res, nil
			}
			result, stepNo := v.runTool(ctx, runID, call)
			// progress carries the text the model saw, keyed to the step
			// in steps.jsonl so a reviewer can go from memory to evidence.
			// seen is not advanced here: anything someone else appended
			// while the tool was running sits before this progress message,
			// and skipping past it would swallow the note. projectLate drops
			// the verifier's own messages, so re-reading them costs nothing.
			v.post(store, session.Message{From: session.Verifier, Kind: session.Progress, Text: progressText(call, result), Step: stepNo})
			msgs = append(msgs, nim.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
		res.Steps = step
	}

	// The step cap is not a verdict: nobody asked the verifier to give up, so
	// it says what happened and waits, the way an "ask" would.
	res.Ended = session.Reply
	v.post(store, session.Message{From: session.Verifier, Kind: session.Reply,
		Text: fmt.Sprintf("I used all %d tool calls of this turn without finishing. Send another message and I will continue from here.", v.cfg.MaxSteps)})
	return res, nil
}

// What the model is told about a machine it cannot drive yet, or ever.
const (
	bootingAdvice = "Tools that touch the machine will not work until it is ready; you can still talk."
	deadAdvice    = "The machine cannot be used. Answer in words and say so if asked to do anything on it."
)

// machineStatus is one line describing runID's machine on mgr. A zero
// timeout on Wait returns the current snapshot at once, so this costs
// nothing to ask before every model call. It is a free function, not a
// method, so Manual (manual.go) can say the same thing about a machine that
// the model-driven verifier would.
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

// unusable says why runID's machine cannot be touched right now, or "". The
// manager refuses the call anyway; asking first is what makes the refusal
// readable instead of a bare error from three layers down. Shared with
// Manual for the same reason as machineStatus.
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

// withStatus puts the machine's status in front of the model, right after
// the system prompt, where it frames everything the transcript then says.
func withStatus(msgs []nim.Message, status string) []nim.Message {
	if len(msgs) == 0 {
		return []nim.Message{{Role: "user", Content: status}}
	}
	out := make([]nim.Message, 0, len(msgs)+1)
	out = append(out, msgs[0], nim.Message{Role: "user", Content: status})
	return append(out, msgs[1:]...)
}

func (v *Verifier) post(store *session.Store, m session.Message) {
	appendMessage(v.log, store, m)
}

// appendMessage posts m and logs if the store refuses it. Both brains and
// the actor use it, so a message that cannot be written is always noticed
// and never a panic.
func appendMessage(log *slog.Logger, store *session.Store, m session.Message) {
	if _, err := store.Append(m); err != nil {
		log.Error("could not post to conversation", "kind", m.Kind, "err", err)
	}
}

// project turns the transcript into the model's context. The verifier's own
// progress becomes assistant tool calls with their results, so the model
// remembers what it did; everyone else's messages are user turns that say
// who spoke.
func project(msgs []session.Message) []nim.Message {
	out := []nim.Message{{Role: "system", Content: systemPrompt}}
	for i, m := range msgs {
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
			out = append(out, nim.Message{Role: "user", Content: speaker(m, msgs[:i])})
		}
	}
	return out
}

// projectLate is project for messages that arrive mid-turn: only what others
// said matters, and it must not be mistaken for the model's own history.
func projectLate(msgs []session.Message) []nim.Message {
	var out []nim.Message
	for _, m := range msgs {
		if m.From == session.Verifier {
			continue
		}
		out = append(out, nim.Message{Role: "user", Content: "[while you were working] " + speaker(m, nil)})
	}
	return out
}

func speaker(m session.Message, before []session.Message) string {
	label := string(m.From)
	switch m.Kind {
	case session.Task:
		return label + " gives you a task: " + m.Text
	case session.Note:
		// A human's note is addressed to the verifier and is owed an answer;
		// the coder's is background it reads on its next turn.
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

// progressText packs a tool call and its result into one line the model can
// be reminded of later. splitProgress is its inverse.
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

// runTool executes one call and returns what the model should see, plus the
// step it recorded.
func (v *Verifier) runTool(ctx context.Context, runID string, call nim.ToolCall) (result string, step int) {
	switch call.Name {
	case "machine_exec", "machine_screenshot",
		"machine_click", "machine_type", "machine_key", "machine_scroll", "machine_input":
		// The model may try the machine before it is up. Say why in words it
		// can act on; it still costs one step, so this cannot spin.
		if why := unusable(ctx, v.mgr, runID); why != "" {
			return "error: the machine is not usable: " + why, 0
		}
	}
	switch call.Name {
	case "machine_exec":
		var in struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &in); err != nil || strings.TrimSpace(in.Command) == "" {
			return "error: machine_exec needs a command", 0
		}
		res, err := v.mgr.Exec(ctx, runID, in.Command, in.Cwd, execTimeout)
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		return execResultText(res), res.Step

	case "machine_screenshot":
		png, shot, err := v.mgr.Screenshot(ctx, runID)
		seq := shot.Step
		if err != nil {
			return "error: " + err.Error(), seq
		}
		desc, err := v.describe(ctx, png)
		if err != nil {
			// A blind verifier is still useful, so say so and continue.
			return fmt.Sprintf("step %d\nThe screenshot was saved to %s but it could not be described: %v", seq, shot.Path, err), seq
		}
		return fmt.Sprintf("step %d\n%s\nThe screen shows:\n%s\n\nThe image is saved at %s",
			seq, shotGeometry(shot), desc, shot.Path), seq

	case "machine_click":
		var in struct {
			X      float64 `json:"x"`
			Y      float64 `json:"y"`
			Button string  `json:"button"`
			Clicks int     `json:"clicks"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &in); err != nil {
			return "error: machine_click needs x and y", 0
		}
		res, err := v.mgr.InputAs(ctx, runID, verifierHolder, []machine.InputAction{
			{Type: "click", X: &in.X, Y: &in.Y, Button: in.Button, Clicks: in.Clicks},
		})
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		return fmt.Sprintf("step %d\nclicked (%.2f, %.2f)", res.Step, in.X, in.Y), res.Step

	case "machine_type":
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &in); err != nil || in.Text == "" {
			return "error: machine_type needs text", 0
		}
		res, err := v.mgr.InputAs(ctx, runID, verifierHolder, []machine.InputAction{{Type: "type", Text: in.Text}})
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		return fmt.Sprintf("step %d\ntyped %q", res.Step, in.Text), res.Step

	case "machine_key":
		var in struct {
			Key  string   `json:"key"`
			Mods []string `json:"mods"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &in); err != nil || in.Key == "" {
			return "error: machine_key needs a key", 0
		}
		res, err := v.mgr.InputAs(ctx, runID, verifierHolder, []machine.InputAction{{Type: "key", Key: in.Key, Mods: in.Mods}})
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		label := in.Key
		if len(in.Mods) > 0 {
			label = strings.Join(in.Mods, "+") + "+" + in.Key
		}
		return fmt.Sprintf("step %d\npressed %s", res.Step, label), res.Step

	case "machine_scroll":
		var in struct {
			X      *float64 `json:"x"`
			Y      *float64 `json:"y"`
			DeltaX float64  `json:"deltaX"`
			DeltaY float64  `json:"deltaY"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &in); err != nil {
			return "error: machine_scroll needs deltaX or deltaY", 0
		}
		res, err := v.mgr.InputAs(ctx, runID, verifierHolder, []machine.InputAction{
			{Type: "scroll", X: in.X, Y: in.Y, DeltaX: in.DeltaX, DeltaY: in.DeltaY},
		})
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		return fmt.Sprintf("step %d\nscrolled (deltaX %.0f, deltaY %.0f)", res.Step, in.DeltaX, in.DeltaY), res.Step

	case "machine_input":
		var in struct {
			Actions []struct {
				Type   string   `json:"type"`
				X      *float64 `json:"x"`
				Y      *float64 `json:"y"`
				Button string   `json:"button"`
				Clicks int      `json:"clicks"`
				DeltaX float64  `json:"deltaX"`
				DeltaY float64  `json:"deltaY"`
				Text   string   `json:"text"`
				Key    string   `json:"key"`
				Mods   []string `json:"mods"`
				MS     int      `json:"ms"`
			} `json:"actions"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &in); err != nil || len(in.Actions) == 0 {
			return "error: machine_input needs a non-empty actions array", 0
		}
		actions := make([]machine.InputAction, len(in.Actions))
		for i, a := range in.Actions {
			actions[i] = machine.InputAction{
				Type: a.Type, X: a.X, Y: a.Y, Button: a.Button, Clicks: a.Clicks,
				DeltaX: a.DeltaX, DeltaY: a.DeltaY, Text: a.Text, Key: a.Key, Mods: a.Mods, MS: a.MS,
			}
		}
		res, err := v.mgr.InputAs(ctx, runID, verifierHolder, actions)
		if err != nil {
			return "error: " + err.Error(), res.Step
		}
		return fmt.Sprintf("step %d\nposted %d actions", res.Step, res.Actions), res.Step

	default:
		return "error: no tool named " + call.Name, 0
	}
}

func (v *Verifier) describe(ctx context.Context, png []byte) (string, error) {
	if v.cfg.VisionModel == "" {
		return "", fmt.Errorf("no vision model configured")
	}
	jpeg, err := toJPEG(png)
	if err != nil {
		return "", err
	}
	return v.llm.Describe(ctx, v.cfg.VisionModel, jpeg, visionPrompt)
}

func parseVerdict(args string) (verdict, summary string, evidence []string) {
	var in struct {
		Verdict  string   `json:"verdict"`
		Summary  string   `json:"summary"`
		Evidence []string `json:"evidence"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	verdict = strings.ToLower(strings.TrimSpace(in.Verdict))
	switch verdict {
	case "pass", "fail", "inconclusive":
	default:
		verdict = "inconclusive"
	}
	return verdict, orElse(strings.TrimSpace(in.Summary), "(no summary)"), in.Evidence
}

func parseReply(args string) string {
	var in struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	return orElse(strings.TrimSpace(in.Text), "(the verifier had nothing to say)")
}

func parseQuestion(args string) string {
	var in struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	return orElse(strings.TrimSpace(in.Question), "(the verifier asked an empty question)")
}

// execResultText is the result format both brains feed back after running a
// command: the model reads it as a tool result, and Manual (manual.go) packs
// it into the same progress text a human reading the transcript would see
// from the model-driven verifier.
func execResultText(res machine.ExecResult) string {
	return fmt.Sprintf("step %d\nexit code %d\nstdout:\n%s\nstderr:\n%s",
		res.Step, res.ExitCode, clamp(res.Stdout), clamp(res.Stderr))
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func clamp(s string) string {
	if len(s) <= maxToolOutput {
		return s
	}
	// Keep both ends: a build failure names the error near the end, and the
	// command that produced it near the start.
	head, tail := maxToolOutput/2, maxToolOutput/2
	return s[:head] + "\n...[middle removed]...\n" + s[len(s)-tail:]
}

func since(t time.Time) float64 {
	return float64(int(time.Since(t).Seconds()*10)) / 10
}
