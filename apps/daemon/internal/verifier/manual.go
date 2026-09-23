package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/nim"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// manualHelp is the whole instruction grammar.
const manualHelp = `Instructions, one per line, case-insensitive first word:
  run <shell command>                       run a command on the machine
  screenshot                                capture the screen
  ui [app]                                  list the frontmost (or named) app's controls
  click <x> <y>                             click at a fraction of the screen, 0 to 1
  click <id>                                click an element from the last ui
  type <text>                               type text into whatever has focus
  key <key> [mods]                          press a key, e.g. key a cmd shift
  scroll <dx> <dy>                          scroll by a delta, in points
  verdict pass|fail|inconclusive <summary>  report a verdict
  ask <question>                            ask a question
  help                                      show this text`

// Manual is a Brain driven by a person's typed instructions instead of a
// model. It posts the same message shapes as Verifier, so the whole loop can
// be tested and demoed without a model.
type Manual struct {
	mgr *machine.Manager
	log *slog.Logger
}

// NewManual returns a Manual brain over mgr.
func NewManual(mgr *machine.Manager, log *slog.Logger) *Manual {
	return &Manual{mgr: mgr, log: log}
}

// Turn runs the last turn-starting message's text as one instruction per
// line, and always ends by posting exactly one reply, question or verdict.
func (m *Manual) Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error) {
	started := time.Now()
	var res TurnResult
	if target := lastTurnStarter(store.After(0)); target == nil {
		m.post(store, session.Message{Kind: session.Reply, Text: "(nothing to do)"})
		res.Ended = session.Reply
	} else {
		res.Steps, res.Ended = m.follow(ctx, runID, store, instructionLines(target.Text))
	}
	res.Seconds = since(started)
	return res, nil
}

// runTally is what a turn's instructions did, for the closing reply.
type runTally struct {
	steps      []int
	ran        int
	exitCodes  []int
	lastStdout string
	failures   []string // "click failed: ...", one per instruction that did not run
}

func (m *Manual) follow(ctx context.Context, runID string, store *session.Store, lines []string) (int, session.Kind) {
	if len(lines) == 0 {
		m.post(store, session.Message{Kind: session.Reply, Text: manualHelp})
		return 0, session.Reply
	}
	var t runTally
	for i, line := range lines {
		steps := i + 1
		verb, arg := splitInstruction(line)
		switch verb = strings.ToLower(verb); verb {
		case "verdict":
			word, summary := splitInstruction(arg)
			if outcome := strings.ToLower(word); outcome != "pass" && outcome != "fail" && outcome != "inconclusive" {
				// A verdict is never guessed from a typo (issue #66).
				m.post(store, session.Message{Kind: session.Reply, Text: fmt.Sprintf(
					"not a verdict: %q is not pass, fail or inconclusive. Use: verdict pass|fail|inconclusive <summary>", word)})
				return steps, session.Reply
			}
			m.post(store, session.Message{Kind: session.Verdict, Verdict: normalVerdict(word),
				Text: orElse(strings.TrimSpace(summary), "(no summary)"), Evidence: evidenceOf(t.steps)})
			return steps, session.Verdict
		case "ask":
			m.post(store, session.Message{Kind: session.Question, Text: orElse(strings.TrimSpace(arg), "(empty question)")})
			return steps, session.Question
		case "run", "screenshot", "ui", "click", "type", "key", "scroll":
			if unusable(ctx, m.mgr, runID) != "" {
				m.post(store, session.Message{Kind: session.Reply, Text: machineStatus(ctx, m.mgr, runID)})
				return steps, session.Reply
			}
			call, result, step := m.do(ctx, runID, verb, arg, &t)
			if step > 0 {
				t.steps = append(t.steps, step)
			}
			if reason, failed := strings.CutPrefix(result, "error: "); failed {
				t.failures = append(t.failures, verb+" failed: "+reason)
			}
			m.post(store, session.Message{Kind: session.Progress, Text: progressText(call, result), Step: step})
		default: // "help" and anything unrecognised
			m.post(store, session.Message{Kind: session.Reply, Text: manualHelp})
			return steps, session.Reply
		}
	}
	m.post(store, session.Message{Kind: session.Reply, Text: summarize(t)})
	return len(lines), session.Reply
}

// do runs one machine instruction as the equivalent model tool call, so the
// progress text matches what Verifier would post.
func (m *Manual) do(ctx context.Context, runID, verb, arg string, t *runTally) (call nim.ToolCall, result string, step int) {
	switch verb {
	case "run":
		call = callOf("machine_exec", map[string]string{"command": arg})
		res, err := m.mgr.Exec(ctx, runID, arg, "", execTimeout)
		if err != nil {
			return call, "error: " + err.Error(), res.Step
		}
		t.ran++
		t.exitCodes = append(t.exitCodes, res.ExitCode)
		t.lastStdout = res.Stdout
		return call, execResultText(res), res.Step

	case "screenshot":
		call = nim.ToolCall{Name: "machine_screenshot", Arguments: "{}"}
		_, shot, err := m.mgr.Screenshot(ctx, runID)
		if err != nil {
			return call, "error: " + err.Error(), shot.Step
		}
		return call, fmt.Sprintf("step %d\n%s\nThe image is saved at %s", shot.Step, shotGeometry(shot), shot.Path), shot.Step

	case "ui":
		call = callOf("machine_ui", map[string]string{"app": arg})
		result, step = uiResult(ctx, m.mgr, runID, arg)
		return call, result, step

	case "click":
		if id, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil {
			call = callOf("machine_click", map[string]int{"element": id})
			result, step = click(ctx, m.mgr, runID, id, nil, nil, "", 0)
			return call, result, step
		}
		x, y, err := parseTwoFloats(arg)
		call = callOf("machine_click", map[string]float64{"x": x, "y": y})
		if err != nil {
			return call, "error: click needs two numbers, x and y, 0 to 1, or an element id from ui", 0
		}
		result, step = click(ctx, m.mgr, runID, 0, &x, &y, "", 0)
		return call, result, step

	case "type":
		call = callOf("machine_type", map[string]string{"text": arg})
		if arg == "" {
			return call, "error: machine_type needs text", 0
		}
		result, step = postInput(ctx, m.mgr, runID, fmt.Sprintf("typed %q", arg),
			machine.InputAction{Type: "type", Text: arg})
		return call, result, step

	case "key":
		fields := strings.Fields(arg)
		if len(fields) == 0 {
			return nim.ToolCall{Name: "machine_key", Arguments: "{}"}, "error: key needs a key name, e.g. key a cmd shift", 0
		}
		key, mods := fields[0], fields[1:]
		call = callOf("machine_key", map[string]any{"key": key, "mods": mods})
		result, step = postInput(ctx, m.mgr, runID, "pressed "+keyLabel(key, mods),
			machine.InputAction{Type: "key", Key: key, Mods: mods})
		return call, result, step

	default: // scroll
		dx, dy, err := parseTwoFloats(arg)
		call = callOf("machine_scroll", map[string]float64{"deltaX": dx, "deltaY": dy})
		if err != nil {
			return call, "error: scroll needs two numbers, dx and dy", 0
		}
		result, step = postInput(ctx, m.mgr, runID, fmt.Sprintf("scrolled (deltaX %.0f, deltaY %.0f)", dx, dy),
			machine.InputAction{Type: "scroll", DeltaX: dx, DeltaY: dy})
		return call, result, step
	}
}

func (m *Manual) post(store *session.Store, msg session.Message) {
	msg.From = session.Verifier
	appendMessage(m.log, store, msg)
}

func callOf(name string, args any) nim.ToolCall {
	b, _ := json.Marshal(args)
	return nim.ToolCall{Name: name, Arguments: string(b)}
}

// lastTurnStarter returns the last turn-starting message in msgs, or nil.
func lastTurnStarter(msgs []session.Message) *session.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].StartsTurn() {
			return &msgs[i]
		}
	}
	return nil
}

// instructionLines splits text into non-empty, trimmed lines.
func instructionLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// splitInstruction splits a line into its first word and the trimmed rest.
func splitInstruction(line string) (verb, rest string) {
	line = strings.TrimSpace(line)
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i+1:])
}

// parseTwoFloats reads exactly two whitespace-separated numbers.
func parseTwoFloats(arg string) (a, b float64, err error) {
	fields := strings.Fields(arg)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("want two numbers, got %q", arg)
	}
	if a, err = strconv.ParseFloat(fields[0], 64); err != nil {
		return 0, 0, err
	}
	if b, err = strconv.ParseFloat(fields[1], 64); err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

// evidenceOf formats steps as the "step N" strings a verdict cites.
func evidenceOf(steps []int) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = fmt.Sprintf("step %d", s)
	}
	return out
}

// summarize is the reply to a turn that did not end in a verdict or question: what failed, in the
// person's words, then what the commands did, e.g. "ran 1 command; exit code 0; last stdout: hi".
func summarize(t runTally) string {
	parts := append([]string{}, t.failures...)
	if t.ran > 0 {
		codes := make([]string, len(t.exitCodes))
		for i, c := range t.exitCodes {
			codes[i] = strconv.Itoa(c)
		}
		part := fmt.Sprintf("ran %d %s; %s %s", t.ran, plural(t.ran, "command"), plural(len(codes), "exit code"), strings.Join(codes, ", "))
		if tail := stdoutTail(t.lastStdout); tail != "" {
			part += "; last stdout: " + tail
		}
		parts = append(parts, part)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "; ")
	}
	if len(t.steps) == 0 {
		return "done"
	}
	steps := make([]string, len(t.steps))
	for i, s := range t.steps {
		steps[i] = strconv.Itoa(s)
	}
	return fmt.Sprintf("done (%s %s)", plural(len(steps), "step"), strings.Join(steps, ", "))
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// stdoutTail is the last 200 bytes of out, trimmed, never splitting a rune.
func stdoutTail(out string) string {
	tail := strings.TrimSpace(out)
	if len(tail) > 200 {
		i := len(tail) - 200
		for i < len(tail) && !utf8.RuneStart(tail[i]) {
			i++
		}
		tail = tail[i:]
	}
	return tail
}
