// Package verifier is greenroom's own host-side agent, one actor per run, that
// answers the run's conversation by driving its machine (ADR 0005, ADR 0006).
package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

const (
	// DefaultMaxSteps is the per-turn tool-call cap when Config.MaxSteps is zero.
	DefaultMaxSteps = 40
	// DefaultBudget is the per-turn wall-clock budget when Config.Budget is zero.
	DefaultBudget = 10 * time.Minute
	execTimeout   = 5 * time.Minute
)

// The verifier diagnoses but never repairs the code under test: an agent
// without the author's intent must not edit the author's code.
const systemPrompt = `You are greenroom's verifier. You have one disposable macOS machine and you drive it with tools.

You are in a conversation with the coding agent that wrote the code under test ("coder") and possibly a human watching ("human"). They give you tasks, context and answers; you report what happened with evidence.

Constraints you are given are hard rules:
- When the coder or the human limits how you work (for example "use the UI only", "do not read the source", "do not rebuild or relaunch"), obey it for the whole task, even when breaking it looks faster. Nothing you could learn is worth a broken constraint. If you cannot finish inside the rules, call ask or report inconclusive and say which rule blocked you.
- "Use the UI only" means operate the app with machine_ui, machine_click, machine_type and machine_key, and read results from machine_ui and machine_screenshot. Do not cat, grep or open the source, and do not compute the expected answer from code.
- If the task says the app is already running or on screen, it is. Do not build it, launch it or start a second copy. If you cannot find it, say so; do not relaunch it unless you were told you may.

Operating a user interface:
- Before any click, call machine_ui. It lists the frontmost app's controls and text with their exact center as fractions of the screen. Click a control with machine_click and its element id (or its center x and y). Never estimate a position from a screenshot description when machine_ui lists the element.
- Name the app (machine_ui app) if it is not frontmost, and click its window once to bring it forward.
- To replace a text field's contents: click the field, press key a with mods [cmd] to select all, then machine_type the new text, then press tab or return so the app commits it.
- Each input's result ends with its effect: the elements that changed, "no change detected" (the input may have been lost) or "unknown". Read machine_ui again to check values; it is exact text, so use it to check numbers. Take a machine_screenshot when you need the visual evidence a verdict cites.
- Never click where machine_ui lists nothing, such as the desktop wallpaper. Use screenshot positions only for content machine_ui cannot see (a canvas, a game, a web view with no accessibility).
- If a click did not change what you expected, read machine_ui again and work out why before the next input. Click it again only when it is a control the task says changes something (see the "no change detected" rule under Rules).

Rules:
- Work in small steps. Run one command, read the result, then decide.
- You diagnose failures. You do NOT fix the application source code. If the build breaks because the code is wrong, report it and stop.
- You may install tools, retry flaky steps and work around machine problems. That is infrastructure and it is yours, unless a constraint above forbids it.
- Look at the screen when the task is about what the user sees. A screenshot is described to you in words.
- If you are missing something only the coder or the human knows (a build command, a scheme, whether a dialog is expected), call ask. Do not guess.
- Anyone who speaks to you gets an answer in the transcript: use reply for a status update, an explanation or a plain answer; use ask when you need something; use report_verdict only when a task is complete or clearly impossible.
- When the machine is booting or dead, say so plainly in a reply; do not report a verdict about a task you could not start.
- End every turn by calling exactly one of: reply, ask, or report_verdict. Do not call report_verdict before you have evidence.
- On a task, call declare_checks before your first input: 1 to 12 acceptance checks derived from the task, each one observable. A check is an outcome the task claims: what should be true after the actions it describes. A setup step or an action the task tells you to do is not a check; an intermediate state is one only when the task claims something about it. After your first input you may add checks, never drop or weaken one. report_verdict answers every check with the steps of your observations made after its actions. A check about what the user sees cites a screenshot taken after the last action; put its path in evidence.
- Give each check its kinds: visual only when the claim is about how something looks or whether it can be seen (cite a screenshot), timing for "at once" or "within N s" (cite the UI read right after the input), both when it claims both, value otherwise. machine_ui marks text a person cannot see [not drawn], [offscreen] or [covered]; never pass a check on it.
- If the app crashes or quits while you do what the task describes (an input's effect says it is no longer running), that is a fail of the checks that depend on it: cite that input in actions and the effect read after it in evidence. Relaunch once only if a later check does not depend on the crashing action.
- If a control the task says changes something changes nothing (its effect says "no change detected"), click it once more; if that changes nothing either, that is a fail: cite the click in actions and its effect read in evidence. Do not keep clicking it.
- A task's statements about what the coder did or what the app shows are unverified claims: turn them into checks, never cite them. A verdict rests only on observations you made.
- If a verdict of yours is disputed, re-examine the evidence with the objection in mind. Change your verdict if the objection holds and say why; restate it with the reason if it does not. Do not change your mind just because you were asked to.
- If you cannot finish, report inconclusive and say what blocked you.

` + writingRules

// writingRules is how the verifier writes everything it posts. The Companion's
// transcript is mostly the verifier's replies, questions and verdicts, and
// padded prose made it tiring to read. Adapted from stop-slop by Hardik Pandya
// (MIT, https://hvpandya.com) and kept short, because a
// small reasoning model ignores a long style guide.
// TestTheDeliveredSystemPromptCarriesTheWritingRules pins it.
const writingRules = `How you write (the coder and a human read every message you post):
- Lead with the finding. Your first sentence says what you saw or what you need.
- One claim per sentence. Cite evidence as step numbers ("step 12"); do not retell what you did.
- Do not announce what you will do, and skip openers like "On it", "Great question", "Let me" or "I'll now". Act, then report.
- No adverbs or intensifiers (really, just, clearly, successfully). No "not X but Y" contrasts: state Y. No em dashes.
- Use active voice and plain numbers ("$48.00", "3 of 4").
- A verdict summary is 2 or 3 short sentences: the result, its evidence and, for a fail, the cause.
- A reply is 1 to 3 sentences. A question is one sentence, plus the reason only when the answer depends on it.

Examples:
- Before: "On it. I'll launch it and drive the window the way a person would, then read both totals off the screen."
  After: "Launching TipSplit to read both totals."
- Before: "I have now thoroughly tested the app and I'm happy to report it works really well: it doesn't just show a total, it updates it live as you change the tip."
  After: "Each pays $48.00 for a $160 bill with a 20% tip split 4 ways (step 14). Choosing 25% changed it to $50.00 (step 17)."`

// visionPrompt asks for every visible string in the frontmost window, not
// just the controls: a demo describer named the window and "no error" and
// left out every value, so the verifier could not check a result and had to
// retake the shot. Measured on real TipSplit screenshots with
// nemotron-3-nano-omni: the old prompt gave every value in 2 of 9 answers,
// this one in 12 of 12. TestVisionPromptAsksForEveryVisibleString pins it.
const visionPrompt = `This is the screen of a macOS machine under test. Describe it for an engineer who cannot see it and who can only click by coordinates. Answer in these five parts, in this order.
1. System dialog: if a system dialog or alert is covering the screen, say so and quote it exactly, because that is a fault of the machine and not of the application under test. Otherwise write "none".
2. Frontmost: the frontmost application and its window title.
3. Window text: quote every piece of text visible in the frontmost window, exactly as shown, one per line, top to bottom: titles, labels, the contents of every field, button and segment labels, values, results, totals, and status or error messages. Copy numbers, currency and punctuation exactly. Never skip text because it looks unimportant, and never summarize it. Write "(empty)" for an empty field and "(unreadable)" for text you cannot read.
4. Controls: each interactive element (buttons, text fields, segmented controls and each of their segments, checkboxes, steppers, menus, links) with its label, whether it looks selected, and its approximate center as fractions of the image width and height, for example: "25% segment at (0.60, 0.47), not selected". Both numbers are between 0 and 1, never pixels; give no position for an element you cannot place.
5. Errors: quote any visible error text exactly, or write "none".
Be factual. Do not guess at anything you cannot read. Keep control rows short and omit commentary. Prioritize exact window text over control descriptions; do not repeat window text outside part 3 except for control labels and errors.`

// Config names the endpoint and the two models.
type Config struct {
	BaseURL     string
	APIKey      string
	Model       string // reasons and calls tools
	VisionModel string // reads screenshots; the reasoning model cannot take images
	MaxSteps    int    // tool calls per turn
	Budget      time.Duration
}

// Verifier is the model-driven Brain. One Verifier serves every run.
type Verifier struct {
	mgr *machine.Manager
	llm *nim.Client
	cfg Config
	log *slog.Logger
}

// New returns a Verifier, or an error if it has no key or no model.
func New(mgr *machine.Manager, cfg Config, log *slog.Logger) (*Verifier, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("no model API key; set NVIDIA_API_KEY in .env")
	}
	if cfg.Model == "" {
		return nil, errors.New("no model; set GREENROOM_VERIFIER_MODEL in .env")
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

// Models is what this verifier's requests name and carry, for each run's record (issue #154).
func (v *Verifier) Models() machine.Models {
	m := machine.Models{Brain: machine.BrainNIM, Model: v.cfg.Model, Vision: v.cfg.VisionModel,
		ModelOptions: nim.ChatOptions()}
	if m.Vision != "" {
		m.VisionOptions = nim.DescribeOptions(m.Vision)
	}
	return m
}

// TurnResult says how a turn ended.
type TurnResult struct {
	Ended   session.Kind // reply, question or verdict
	Steps   int
	Tokens  int
	Seconds float64
}

// Brain runs one turn of a run's conversation. Verifier and Manual implement
// it identically apart from who decides the next action (apps/daemon/CLAUDE.md).
type Brain interface {
	Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error)
}

// cutOffNudge and cutOffReply answer a step the endpoint cut off at its token limit.
const (
	cutOffNudge = "[greenroom] Your last message was cut off at the output limit before it finished, so nothing " +
		"in it was received. Answer again, shorter: call the tool you meant to call, with brief arguments " +
		"(cite a few key steps as evidence, not every step)."
	cutOffReply = "The model's output limit cut off my answers, so I reported nothing. Send a task, or a note " +
		"from a person, and I will try again, shorter. A coding agent's note does not start a turn."
)

// cutOff reports whether the endpoint stopped msg at its token limit, or msg carries a tool
// call as text: some models write one inline and, cut short, leave "<tool_call>..." prose.
func cutOff(msg nim.Message) bool {
	if len(msg.ToolCalls) > 0 && msg.FinishReason != "length" {
		return false
	}
	return msg.FinishReason == "length" || strings.Contains(msg.Content, "<tool_call>")
}

// screenBackAsk starts the question asking a person for the screen. Giving it back resumes the
// turn by itself (resumeOwed), so the question asks for nothing more (issue #124).
const screenBackAsk = "You have the screen, so I cannot click or type."

// screenTakenQuestion ends a turn whose input the seat holding the screen refused.
func screenTakenQuestion(holder string, standing bool) string {
	q := screenBackAsk + " Press Give Back in the Companion and I will continue from here."
	if holder == machine.HolderCoder {
		q = "The coding agent has the screen, so I cannot click or type. It releases the screen after each " +
			"input call. Send the task again and I will continue from here."
	}
	if standing {
		q += " My last verdict still stands."
	}
	return q
}

// screenHolder reports who holds the screen, if it is anyone but the verifier.
func (v *Verifier) screenHolder(runID string) (string, bool) {
	c, held := v.mgr.ControlState(runID)
	if !held || c.Holder == machine.HolderVerifier {
		return "", false
	}
	return c.Holder, true
}

// askForScreen ends the turn with a question asking holder for the screen back (issue #97).
func (v *Verifier) askForScreen(store *session.Store, holder string) {
	v.post(store, session.Message{From: session.Verifier, Kind: session.Question, Text: screenTakenQuestion(holder, standingVerdict(store))})
}

// standingVerdict reports whether the run has a verdict a question must say still stands.
func standingVerdict(store *session.Store) bool {
	st := store.Verdict().Status
	return st == session.Proposed || st == session.Accepted
}

// openTaskNudge answers a reply that would end a turn with a task still waiting for a verdict.
const openTaskNudge = "[greenroom] Not posted: a task in this conversation is still open, and a task is " +
	"answered with a verdict. Call report_verdict for it now (or ask, if you are blocked). If more than " +
	"one task is open, one verdict covering the newest build or state answers them all; say in the summary " +
	"what it covers. Use reply only for a message that is not a task."

// droppedWithReply answers each call that shared a message with a reply sent back by openTaskNudge.
const droppedWithReply = "[greenroom] Not run: the reply in this message was sent back, so its other " +
	"calls were dropped; call them again if you still need them."

// countTaken counts the events in msgs saying a person took the screen.
func countTaken(msgs []session.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Kind == session.Event && m.Control == session.ControlTaken {
			n++
		}
	}
	return n
}

// resumeOwed reports whether the screen coming back should start a turn with nothing else to
// start one (issue #124): the verifier's last turn ended asking for the screen, or a task is open
// and that turn did not end waiting on an answer to some other question. A Verifier resumes; a
// Manual brain does not, since its person types the next instruction.
func (v *Verifier) resumeOwed(msgs []session.Message) bool {
	var last *session.Message
	for i := range msgs {
		if msgs[i].EndsTurn() {
			last = &msgs[i]
		}
	}
	if last != nil && last.Kind == session.Question {
		return strings.HasPrefix(last.Text, screenBackAsk)
	}
	return hasOpenTask(msgs)
}

// hasOpenTask reports whether a task from the coder or a human came after the latest verdict.
func hasOpenTask(msgs []session.Message) bool {
	open := false
	for _, m := range msgs {
		switch {
		case m.Kind == session.Task && m.From != session.Verifier:
			open = true
		case m.Kind == session.Verdict && m.From == session.Verifier:
			open = false
		}
	}
	return open
}

// Turn rebuilds the model context from the transcript, loops over tool calls
// posting progress, and ends by posting a reply, question or verdict. An error
// means the loop itself broke; it is also posted as an event unless the caller
// cancelled ctx.
func (v *Verifier) Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error) {
	started := time.Now()
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Budget)
	defer cancel()

	var res TurnResult
	seen := store.Len()
	status := machineStatus(ctx, v.mgr, runID)
	toolkit := v.mgr.DesktopToolkit()
	offered := toolsFor(toolkit)
	msgs := withStatus(projectWith(systemPromptFor(toolkit), store.After(0)), status)
	nudged := false
	taskNudged := false
	screenTaken := 0        // input refused, or the screen taken, during this turn (issues #97, #124)
	failed := repeats{}     // failing tool calls this turn, by call and error (issue #125)
	dead := &deadControls{} // clicks that changed nothing this turn, by control (ADR 0029)

	for step := 1; step <= v.cfg.MaxSteps; step++ {
		// Feed in anything said mid-turn, and any machine status change.
		if fresh := store.After(seen); len(fresh) > 0 {
			msgs = append(msgs, projectLate(fresh)...)
			seen += len(fresh)
			screenTaken += countTaken(fresh)
		}
		if now := machineStatus(ctx, v.mgr, runID); now != status {
			status = now
			msgs = append(msgs, nim.Message{Role: "user", Content: "[machine status changed] " + now})
		}

		msg, usage, err := v.llm.Chat(ctx, v.cfg.Model, msgs, offered)
		res.Tokens += usage.PromptTokens + usage.CompletionTokens
		res.Seconds = since(started)
		if err != nil {
			// The actor is stopping (the machine was destroyed): say nothing.
			if parent.Err() != nil {
				return res, err
			}
			// The turn's own budget running out is a graceful stop, not a failure.
			if ctx.Err() == context.DeadlineExceeded {
				return v.endAtLimit(parent, runID, store, msgs, session.StopTime, screenTaken, started, res)
			}
			v.post(store, session.Message{From: session.System, Kind: session.Event, Text: "verifier turn failed: " + err.Error()})
			return res, err
		}
		if cutOff(msg) {
			// Never a reply: an empty message is a budget spent thinking, and text holding a
			// half-written tool call is a verdict that did not arrive (issue #71). The fragment
			// stays out of the context; the model is told to answer again, shorter.
			res.Steps = step
			if step == v.cfg.MaxSteps {
				res.Ended = session.Reply
				v.post(store, session.Message{From: session.Verifier, Kind: session.Reply, Text: cutOffReply})
				return res, nil
			}
			msgs = append(msgs, nim.Message{Role: "user", Content: cutOffNudge})
			continue
		}
		msgs = append(msgs, msg)

		if len(msg.ToolCalls) == 0 && !nudged && step < v.cfg.MaxSteps && proseVerdict.MatchString(msg.Content) {
			// Once per turn, and only with a step left to answer it: a verdict in prose
			// would be stored as a reply, but on the last step the nudge would throw the
			// model's words away, so they are posted as the reply below instead.
			nudged = true
			msgs = append(msgs, nim.Message{Role: "user", Content: proseNudge})
			res.Steps = step
			continue
		}
		if len(msg.ToolCalls) == 0 && !taskNudged && step < v.cfg.MaxSteps && hasOpenTask(store.After(0)) {
			// A task owes a verdict (issue #89): once per turn, a reply is sent back.
			taskNudged = true
			msgs = append(msgs, nim.Message{Role: "user", Content: openTaskNudge})
			res.Steps = step
			continue
		}
		if len(msg.ToolCalls) == 0 {
			// Prose is a reply, never a verdict nobody reasoned towards.
			res.Steps, res.Ended = step, session.Reply
			v.post(store, session.Message{From: session.Verifier, Kind: session.Reply, Text: orElse(strings.TrimSpace(msg.Content), "(the verifier had nothing to say)")})
			return res, nil
		}

		for i, call := range msg.ToolCalls {
			if end, ok := endingMessage(call); ok && end.Kind == session.Reply && !taskNudged &&
				step < v.cfg.MaxSteps && hasOpenTask(store.After(0)) {
				// A task in this turn is still open, often because a message arrived mid-turn
				// and the model answered that instead (issue #89). Not posted; asked once for
				// the verdict. Every other call of this message is dropped with it, and answered.
				taskNudged = true
				msgs = append(msgs, nim.Message{Role: "tool", ToolCallID: call.ID, Content: openTaskNudge})
				for _, rest := range msg.ToolCalls[i+1:] {
					msgs = append(msgs, nim.Message{Role: "tool", ToolCallID: rest.ID, Content: droppedWithReply})
				}
				break
			}
			result, stepNo, checks := "", 0, []session.Check(nil)
			end, ending := endingMessage(call)
			if ending && end.Kind == session.Verdict {
				// The daemon checks a verdict's evidence before posting it (ADR 0024, issue #116). A
				// refused one is a failed call like any other, so it counts toward #125.
				in := parseVerdict(call.Arguments)
				records, handover := v.records(runID, &in)
				r := reviewVerdict(in, store.After(0), records, handover)
				end, ending = r.msg, len(r.problems) == 0
				if !ending {
					result = refusal(r.problems)
				}
			}
			switch {
			case ending:
				res.Steps = step
				res.Ended = v.postEnding(store, runID, end, screenTaken, step, since(started))
				return res, nil
			case result != "":
			case call.Name == "declare_checks":
				var notes []string
				checks, notes, result = parseDeclaredChecks(call.Arguments)
				if result == "" {
					result = redeclareRefusal(checks, store.After(0))
				}
				if result != "" {
					checks = nil // refused: the declared checks stand
				} else {
					result = declaredResult(checks, notes)
				}
			case inputRefusal(call, store.After(0)) != "":
				// Checks before actions (ADR 0024); counts toward #125 like any error.
				result = inputRefusal(call, store.After(0))
			default:
				result, stepNo = v.runTool(ctx, runID, call, dead)
			}
			taken := strings.HasPrefix(result, screenTakenPrefix)
			if taken {
				screenTaken++
			}
			result, stuck := v.guardRepeat(runID, failed, call, result)
			// seen is not advanced past our own progress: a message someone else
			// appended while the tool ran sits before it. projectLate skips ours.
			v.post(store, session.Message{From: session.Verifier, Kind: session.Progress, Text: progressText(call, result),
				Step: stepNo, Checks: checks})
			msgs = append(msgs, nim.Message{Role: "tool", ToolCallID: call.ID, Content: result})
			if taken {
				if holder, held := v.screenHolder(runID); held {
					// The first refusal while someone else holds the screen ends the turn: the
					// verifier yields at once (issue #124) instead of spinning (issue #97). Giving
					// the screen back resumes it (resumeOwed).
					res.Steps, res.Ended = step, session.Question
					v.askForScreen(store, holder)
					return res, nil
				}
			}
			if stuck {
				// The same call failed the same way repeatStopAt times: the person sees the blocker (issue #125).
				res.Steps, res.Ended = step, session.Question
				v.askAboutRepeat(store, call, result)
				return res, nil
			}
		}
		res.Steps = step
	}

	// The step cap is not a verdict: ask for one if a task is open, else say so and wait.
	return v.endAtLimit(parent, runID, store, msgs, session.StopSteps, screenTaken, started, res)
}

func (v *Verifier) post(store *session.Store, m session.Message) {
	appendMessage(v.log, store, m)
}

// appendMessage posts m, logging rather than failing if the store refuses it.
// A closed store belongs to a destroyed run, which has nobody left to tell.
func appendMessage(log *slog.Logger, store *session.Store, m session.Message) {
	if _, err := store.Append(m); err != nil && !errors.Is(err, session.ErrClosed) {
		log.Error("could not post to conversation", "kind", m.Kind, "err", err)
	}
}

// endingMessage turns a reply, ask or report_verdict call into the message
// that ends the turn.
func endingMessage(call nim.ToolCall) (session.Message, bool) {
	m := session.Message{From: session.Verifier}
	switch call.Name {
	case "report_verdict":
		// Unreviewed: Turn and endAtLimit check it against the transcript (reviewVerdict).
		in := parseVerdict(call.Arguments)
		m.Kind, m.Verdict, m.Text, m.Evidence, m.Checks = session.Verdict, in.verdict, in.summary, in.paths, in.checks
	case "reply":
		m.Kind, m.Text = session.Reply, parseReply(call.Arguments)
	case "ask":
		m.Kind, m.Text = session.Question, parseQuestion(call.Arguments)
	default:
		return m, false
	}
	return m, true
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

// normalVerdict maps anything but pass, fail or inconclusive to inconclusive.
func normalVerdict(word string) string {
	switch v := strings.ToLower(strings.TrimSpace(word)); v {
	case "pass", "fail", "inconclusive":
		return v
	}
	return "inconclusive"
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func since(t time.Time) float64 {
	return float64(int(time.Since(t).Seconds()*10)) / 10
}
