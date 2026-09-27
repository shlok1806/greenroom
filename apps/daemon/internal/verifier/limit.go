package verifier

import (
	"context"
	"fmt"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// closingTimeout bounds the one model call made when a turn hits its step cap or budget (issue
// #127). At the budget the turn's own context is dead, so the call runs on a fresh one derived
// from the actor's, which still stops it when the machine is destroyed.
const closingTimeout = 90 * time.Second

// closingTools are the only tools offered to the closing call: it may end the turn, not act.
var closingTools = toolsNamed("report_verdict", "ask")

func toolsNamed(names ...string) []nim.Tool {
	out := make([]nim.Tool, 0, len(names))
	for _, name := range names {
		for _, t := range tools {
			if t.Name == name {
				out = append(out, t)
			}
		}
	}
	if len(out) != len(names) {
		panic(fmt.Sprintf("verifier: tools %v are not all defined", names))
	}
	return out
}

// closingPrompt asks for a verdict from the evidence gathered so far.
func closingPrompt(stop string) string {
	what := "steps"
	if stop == session.StopTime {
		what = "time"
	}
	return "[greenroom] You are out of " + what + " for this turn. Give a verdict now from the evidence you " +
		"already have: answer each declared check pass, fail or unchecked, citing the steps that show it. A pass " +
		"whose evidence does not hold, or a fail with no failing check whose evidence holds, is posted as " +
		"inconclusive; a fail's other answers that do not hold are posted unchecked. Call report_verdict, or ask if you are " +
		"blocked. No machine tools are available."
}

// limitReply is what the turn posts at a limit when it has no verdict to give.
func (v *Verifier) limitReply(stop string) string {
	if stop == session.StopTime {
		return fmt.Sprintf("I ran out of time after %s. Send a message and I will continue.", v.cfg.Budget)
	}
	return fmt.Sprintf("I used all %d tool calls for this turn and did not finish. Send a message and I will "+
		"continue from here.", v.cfg.MaxSteps)
}

// endAtLimit ends a turn that hit its step cap or budget (stop). A limit is not a verdict, but a
// task left open says nothing to whoever waits on it (issue #127): with a task open, one closing
// call asks the model for a verdict or a question from the evidence it has. Without one, or when
// that call fails, is cut off or answers in prose, the turn posts limitReply, marked with stop so
// clients can tell it from a plain reply.
func (v *Verifier) endAtLimit(parent context.Context, runID string, store *session.Store, msgs []nim.Message,
	stop string, screenTaken int, started time.Time, res TurnResult) (TurnResult, error) {
	if hasOpenTask(store.After(0)) {
		end, call, err := v.closingCall(parent, msgs, stop, &res)
		res.Seconds = since(started)
		if err != nil && parent.Err() != nil {
			// The actor is stopping (the machine was destroyed): say nothing.
			return res, err
		}
		if err != nil {
			v.log.Warn("verifier closing call failed", "runId", runID, "stop", stop, "err", err)
		}
		if err == nil && end.Kind != "" {
			if end.Kind == session.Verdict {
				// The same checks as any verdict; one that breaks them is posted as inconclusive
				// with the reasons, never as a pass or fail the evidence does not hold (ADR 0024). A
				// fail with an evidenced failing check stands, its bad answers unchecked (ADR 0031).
				in := parseVerdict(call.Arguments)
				records, handover := v.records(runID, &in)
				end = downgrade(in, store.After(0), records, handover)
			}
			res.Ended = v.postEnding(store, runID, end, screenTaken, res.Steps, res.Seconds)
			return res, nil
		}
	}
	res.Ended = session.Reply
	v.post(store, session.Message{From: session.Verifier, Kind: session.Reply, Text: v.limitReply(stop), Stop: stop})
	return res, nil
}

// closingCall makes the one closing model call and returns the verdict or question it gives,
// and the call that gave it, or a zero message when it gave neither.
func (v *Verifier) closingCall(parent context.Context, msgs []nim.Message, stop string, res *TurnResult) (session.Message, nim.ToolCall, error) {
	ctx, cancel := context.WithTimeout(parent, closingTimeout)
	defer cancel()
	msgs = append(msgs[:len(msgs):len(msgs)], nim.Message{Role: "user", Content: closingPrompt(stop)})
	msg, usage, err := v.llm.Chat(ctx, v.cfg.Model, msgs, closingTools)
	res.Tokens += usage.PromptTokens + usage.CompletionTokens
	if err != nil || cutOff(msg) {
		return session.Message{}, nim.ToolCall{}, err
	}
	for _, call := range msg.ToolCalls {
		if end, ok := endingMessage(call); ok && (end.Kind == session.Verdict || end.Kind == session.Question) {
			return end, call, nil
		}
	}
	return session.Message{}, nim.ToolCall{}, nil
}

// postEnding posts the reply, question or verdict that ends a turn and returns its kind. An
// inconclusive verdict after the screen was refused, while someone still holds it, becomes the
// question asking for it, so a verdict about the lease never replaces a real one (issue #97).
func (v *Verifier) postEnding(store *session.Store, runID string, end session.Message, screenTaken, steps int, seconds float64) session.Kind {
	if end.Kind == session.Verdict && end.Verdict == "inconclusive" && screenTaken > 0 {
		if holder, held := v.screenHolder(runID); held {
			v.askForScreen(store, holder)
			return session.Question
		}
	}
	v.post(store, end)
	if end.Kind == session.Verdict {
		v.log.Info("verifier verdict", "runId", runID, "verdict", end.Verdict, "steps", steps, "seconds", seconds)
	}
	return end.Kind
}
