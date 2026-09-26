package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Endings: how a trial ended. The first four come from the verifier's turn; the rest are not
// the verifier's answer and are reported on their own, never as a wrong verdict (ADR 0025).
const (
	EndVerdict    = "verdict"     // a verdict message
	EndQuestion   = "question"    // the verifier asked
	EndReply      = "reply"       // a plain reply with no verdict
	EndLimit      = "limit"       // a reply at the step cap or time budget (stop set)
	EndModelError = "model_error" // the actor gave up after its retries
	EndTimeout    = "timeout"     // the runner stopped waiting for the turn
	EndSetupError = "setup_error" // the harness could not boot, build or launch; retried on resume
)

// Result is one line of a results file: one case and trial.
type Result struct {
	Case      string   `json:"case"`
	Trial     int      `json:"trial"`
	App       string   `json:"app"`
	Kind      string   `json:"kind"`
	Family    string   `json:"family,omitempty"`
	Split     string   `json:"split"`
	Expected  string   `json:"expected"`
	MustCheck []string `json:"mustCheck,omitempty"`
	Infra     string   `json:"infra,omitempty"`

	RunID  string `json:"runId,omitempty"`
	RunDir string `json:"runDir,omitempty"`
	Image  string `json:"image,omitempty"`
	Model  string `json:"model,omitempty"`

	StartedAt time.Time `json:"startedAt"`
	Ending    string    `json:"ending"`
	Verdict   string    `json:"verdict,omitempty"` // pass, fail or inconclusive, with EndVerdict
	Text      string    `json:"text,omitempty"`    // the ending message's text, or the error
	// Checks are the verdict's checks and DeclaredChecks the latest declared list (ADR 0024), as
	// the conversation holds them; absent before that contract.
	Checks          json.RawMessage `json:"checks,omitempty"`
	DeclaredChecks  json.RawMessage `json:"declaredChecks,omitempty"`
	RefusedVerdicts int             `json:"refusedVerdicts"` // report_verdict calls the daemon sent back
	Steps           int             `json:"steps"`           // verifier tool calls in the trial
	Tokens          int             `json:"tokens"`          // model tokens (prompt and completion) in the trial
	Turns           int             `json:"turns"`           // turn attempts the actor made
	Seconds         float64         `json:"seconds"`         // from posting the task to the ending
	SetupSeconds    float64         `json:"setupSeconds"`    // boot, sync, build and launch
	Error           string          `json:"error,omitempty"`
}

// Outcome is what the verifier answered: pass, fail, inconclusive, ask, or the ending itself
// (reply, limit, model_error, timeout, setup_error).
func (r Result) Outcome() string {
	switch r.Ending {
	case EndVerdict:
		return r.Verdict
	case EndQuestion:
		return "ask"
	default:
		return r.Ending
	}
}

// Answered reports whether the verifier's turn ended on its own (a verdict, question, reply or
// limit), so the result counts in the verdict rates. Model errors, timeouts and setup errors
// are reported apart.
func (r Result) Answered() bool {
	switch r.Ending {
	case EndVerdict, EndQuestion, EndReply, EndLimit:
		return true
	}
	return false
}

// Right reports whether the verifier's answer is the expected one.
func (r Result) Right() bool {
	switch r.Expected {
	case ExpectPass, ExpectFail:
		return r.Ending == EndVerdict && r.Verdict == r.Expected
	case AskOrInconclusive:
		return r.Ending == EndQuestion || (r.Ending == EndVerdict && r.Verdict == "inconclusive")
	}
	return false
}

// key names a case and trial.
func (r Result) key() string { return fmt.Sprintf("%s#%d", r.Case, r.Trial) }

// ReadResults reads a results file. A last line with no newline is a write cut short and is
// skipped; any other bad line is an error.
func ReadResults(path string) ([]Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if i := strings.LastIndexByte(text, '\n'); i < len(text)-1 {
		text = text[:i+1] // torn last line
	}
	var out []Result
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// Latest keeps the last result of each case and trial, in first-seen order: a resumed run may
// have written a setup error and later the real result.
func Latest(results []Result) []Result {
	idx := map[string]int{}
	var out []Result
	for _, r := range results {
		if i, ok := idx[r.key()]; ok {
			out[i] = r
			continue
		}
		idx[r.key()] = len(out)
		out = append(out, r)
	}
	return out
}

// done is the set of case and trial pairs a resumed run skips: everything recorded except
// setup errors, which were the harness's failure and are tried again.
func done(results []Result) map[string]bool {
	out := map[string]bool{}
	for _, r := range Latest(results) {
		if r.Ending != EndSetupError {
			out[r.key()] = true
		}
	}
	return out
}

// resultsFile appends results as JSON lines, one write per line, flushed at once.
type resultsFile struct {
	mu sync.Mutex
	f  *os.File
}

func openResults(path string) (*resultsFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &resultsFile{f: f}, nil
}

func (w *resultsFile) write(r Result) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.f.Write(append(data, '\n')); err != nil {
		return err
	}
	return w.f.Sync()
}

func (w *resultsFile) close() error { return w.f.Close() }
