// Package verifier is greenroom's own host-side agent, one actor per run, that
// answers the run's conversation by driving its machine (ADR 0005, ADR 0006).
package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// TurnResult says how a turn ended.
type TurnResult struct {
	Ended   session.Kind // reply, question or verdict, or "" when the budget ran out
	Steps   int
	Tokens  int
	Seconds float64
}

// Brain runs one turn of a run's conversation. Verifier and Manual implement
// it identically apart from who decides the next action (apps/daemon/CLAUDE.md).
type Brain interface {
	Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error)
}

// Turn rebuilds the model context from the transcript, loops over tool calls
// posting progress, and ends by posting a reply, question or verdict. An error
// means the loop itself broke; it is also posted as an event.
func (v *Verifier) Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Budget)
	defer cancel()

	var res TurnResult
	seen := store.Len()
	status := machineStatus(ctx, v.mgr, runID)
	msgs := withStatus(project(store.After(0)), status)

	for step := 1; step <= v.cfg.MaxSteps; step++ {
		// Feed in anything said mid-turn, and any machine status change.
		if fresh := store.After(seen); len(fresh) > 0 {
			msgs = append(msgs, projectLate(fresh)...)
			seen += len(fresh)
		}
		if now := machineStatus(ctx, v.mgr, runID); now != status {
			status = now
			msgs = append(msgs, nim.Message{Role: "user", Content: "[machine status changed] " + now})
		}

		msg, usage, err := v.llm.Chat(ctx, v.cfg.Model, msgs, tools)
		res.Tokens += usage.PromptTokens + usage.CompletionTokens
		res.Seconds = since(started)
		if err != nil {
			// The turn's own budget running out is a graceful stop, not a failure.
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
			// Prose is a reply, never a verdict nobody reasoned towards.
			res.Steps, res.Ended = step, session.Reply
			v.post(store, session.Message{From: session.Verifier, Kind: session.Reply, Text: orElse(strings.TrimSpace(msg.Content), "(the verifier had nothing to say)")})
			return res, nil
		}

		for _, call := range msg.ToolCalls {
			if end, ok := endingMessage(call); ok {
				res.Steps, res.Ended = step, end.Kind
				v.post(store, end)
				if end.Kind == session.Verdict {
					v.log.Info("verifier verdict", "runId", runID, "verdict", end.Verdict, "steps", step)
				}
				return res, nil
			}
			result, stepNo := v.runTool(ctx, runID, call)
			// seen is not advanced past our own progress: a message someone else
			// appended while the tool ran sits before it. projectLate skips ours.
			v.post(store, session.Message{From: session.Verifier, Kind: session.Progress, Text: progressText(call, result), Step: stepNo})
			msgs = append(msgs, nim.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
		res.Steps = step
	}

	// The step cap is not a verdict: say so and wait, as ask would.
	res.Ended = session.Reply
	v.post(store, session.Message{From: session.Verifier, Kind: session.Reply,
		Text: fmt.Sprintf("I used all %d tool calls of this turn without finishing. Send another message and I will continue from here.", v.cfg.MaxSteps)})
	return res, nil
}

func (v *Verifier) post(store *session.Store, m session.Message) {
	appendMessage(v.log, store, m)
}

// appendMessage posts m, logging rather than failing if the store refuses it.
func appendMessage(log *slog.Logger, store *session.Store, m session.Message) {
	if _, err := store.Append(m); err != nil {
		log.Error("could not post to conversation", "kind", m.Kind, "err", err)
	}
}

// endingMessage turns a reply, ask or report_verdict call into the message
// that ends the turn.
func endingMessage(call nim.ToolCall) (session.Message, bool) {
	m := session.Message{From: session.Verifier}
	switch call.Name {
	case "report_verdict":
		m.Kind = session.Verdict
		m.Verdict, m.Text, m.Evidence = parseVerdict(call.Arguments)
	case "reply":
		m.Kind, m.Text = session.Reply, parseReply(call.Arguments)
	case "ask":
		m.Kind, m.Text = session.Question, parseQuestion(call.Arguments)
	default:
		return m, false
	}
	return m, true
}

func parseVerdict(args string) (verdict, summary string, evidence []string) {
	var in struct {
		Verdict  string   `json:"verdict"`
		Summary  string   `json:"summary"`
		Evidence []string `json:"evidence"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	return normalVerdict(in.Verdict), orElse(strings.TrimSpace(in.Summary), "(no summary)"), in.Evidence
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
