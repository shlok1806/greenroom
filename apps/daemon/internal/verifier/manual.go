package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// manualHelp is what Manual says when it is told to do something it does
// not understand, or nothing at all. It is the whole grammar, so "help" is
// also how a person learns it.
const manualHelp = `Instructions, one per line, case-insensitive first word:
  run <shell command>                       run a command on the machine
  screenshot                                capture the screen
  verdict pass|fail|inconclusive <summary>  report a verdict
  ask <question>                            ask a question
  help                                      show this text`

// Manual is a Brain with no model behind it: a person types instructions
// into the same conversation a model-driven verifier would answer, and
// Manual carries them out on the machine and posts the results back in the
// same shapes (CLAUDE.md: the manual brain is the model brain with a person
// for a model). It exists to test and demo the whole loop, the conversation,
// the companion app, the recording, with nobody paying for tokens.
type Manual struct {
	mgr *machine.Manager
	log *slog.Logger
}

// NewManual returns a Manual brain over mgr.
func NewManual(mgr *machine.Manager, log *slog.Logger) *Manual {
	return &Manual{mgr: mgr, log: log}
}

// Turn reads the last turn-starting message in store, task, answer, dispute
// or human note, and interprets its text as one instruction per line. It
// always ends by posting exactly one reply, question or verdict, like the
// model-driven verifier.
func (m *Manual) Turn(ctx context.Context, runID string, store *session.Store) (TurnResult, error) {
	started := time.Now()
	res := TurnResult{}

	target := lastTurnStarter(store.After(0))
	if target == nil {
		m.postReply(store, "(nothing to do)")
		res.Ended, res.Seconds = session.Reply, since(started)
		return res, nil
	}

	lines := instructionLines(target.Text)
	if len(lines) == 0 {
		m.postReply(store, manualHelp)
		res.Ended, res.Seconds = session.Reply, since(started)
		return res, nil
	}

	var steps []int
	var ran int
	var exitCodes []int
	var lastStdout string

linesLoop:
	for _, line := range lines {
		verb, arg := splitInstruction(line)
		res.Steps++
		switch strings.ToLower(verb) {
		case "run":
			if why := unusable(ctx, m.mgr, runID); why != "" {
				m.postReply(store, machineStatus(ctx, m.mgr, runID))
				res.Ended = session.Reply
				break linesLoop
			}
			result, err := m.mgr.Exec(ctx, runID, arg, "", execTimeout)
			ran++
			args, _ := json.Marshal(map[string]string{"command": arg})
			var text string
			if err != nil {
				text = "machine_exec " + string(args) + "\nerror: " + err.Error()
			} else {
				text = "machine_exec " + string(args) + "\n" + execResultText(result)
				exitCodes = append(exitCodes, result.ExitCode)
				lastStdout = result.Stdout
			}
			if result.Step > 0 {
				steps = append(steps, result.Step)
			}
			m.postProgress(store, text, result.Step)

		case "screenshot":
			if why := unusable(ctx, m.mgr, runID); why != "" {
				m.postReply(store, machineStatus(ctx, m.mgr, runID))
				res.Ended = session.Reply
				break linesLoop
			}
			_, path, seq, err := m.mgr.ScreenshotStep(ctx, runID)
			var text string
			if err != nil {
				text = fmt.Sprintf("machine_screenshot {}\nerror: %s", err.Error())
			} else {
				text = fmt.Sprintf("machine_screenshot {}\nstep %d\nThe image is saved at %s", seq, path)
			}
			if seq > 0 {
				steps = append(steps, seq)
			}
			m.postProgress(store, text, seq)

		case "verdict":
			word, summary := splitInstruction(arg)
			verdict := strings.ToLower(strings.TrimSpace(word))
			switch verdict {
			case "pass", "fail", "inconclusive":
			default:
				verdict = "inconclusive"
			}
			m.postVerdict(store, verdict, orElse(strings.TrimSpace(summary), "(no summary)"), evidenceOf(steps))
			res.Ended = session.Verdict
			break linesLoop

		case "ask":
			m.postQuestion(store, orElse(strings.TrimSpace(arg), "(empty question)"))
			res.Ended = session.Question
			break linesLoop

		default: // "help" and anything unrecognised
			m.postReply(store, manualHelp)
			res.Ended = session.Reply
			break linesLoop
		}
	}

	if res.Ended == "" {
		m.postReply(store, summarizeRuns(ran, exitCodes, lastStdout))
		res.Ended = session.Reply
	}
	res.Seconds = since(started)
	return res, nil
}

func (m *Manual) postReply(store *session.Store, text string) {
	appendMessage(m.log, store, session.Message{From: session.Verifier, Kind: session.Reply, Text: text})
}

func (m *Manual) postQuestion(store *session.Store, text string) {
	appendMessage(m.log, store, session.Message{From: session.Verifier, Kind: session.Question, Text: text})
}

func (m *Manual) postProgress(store *session.Store, text string, step int) {
	appendMessage(m.log, store, session.Message{From: session.Verifier, Kind: session.Progress, Text: text, Step: step})
}

func (m *Manual) postVerdict(store *session.Store, verdict, summary string, evidence []string) {
	appendMessage(m.log, store, session.Message{From: session.Verifier, Kind: session.Verdict, Verdict: verdict, Text: summary, Evidence: evidence})
}

// lastTurnStarter returns the last message in msgs that should be read as an
// instruction, or nil. It is what Manual answers, the same message an
// actor's loop decided the turn was for.
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

// splitInstruction takes one line apart into its first word and the rest.
func splitInstruction(line string) (verb, rest string) {
	line = strings.TrimSpace(line)
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i+1:])
}

// evidenceOf turns the steps a turn produced into the "step N" strings a
// verdict cites, the same shape the model-driven verifier uses.
func evidenceOf(steps []int) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = fmt.Sprintf("step %d", s)
	}
	return out
}

// summarizeRuns is the reply Manual gives when a turn ran commands but
// nobody told it to end with a verdict or a question.
func summarizeRuns(ran int, exitCodes []int, lastStdout string) string {
	codes := make([]string, len(exitCodes))
	for i, c := range exitCodes {
		codes[i] = fmt.Sprintf("%d", c)
	}
	tail := strings.TrimSpace(lastStdout)
	if len(tail) > 200 {
		tail = tail[len(tail)-200:]
	}
	return fmt.Sprintf("ran %d commands; exit codes: %s; last stdout tail: %s", ran, strings.Join(codes, ", "), tail)
}
