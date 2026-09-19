// Package verifier runs greenroom's own agent against one machine.
//
// The agent lives on the host, not in the guest, so no model credential ever
// enters a machine (ADR 0005, and docs/05-transport.md rule 1). It drives the
// machine through the same operations the MCP tools use, and everything it
// does lands in the run record beside the tool calls.
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
)

const (
	defaultMaxSteps = 12
	defaultBudget   = 10 * time.Minute
	execTimeout     = 5 * time.Minute
	maxToolOutput   = 6000 // characters of guest output fed back to the model
)

// The agent diagnoses. It does not repair the code under test. That boundary
// is the whole reason a separate agent is safe to run unattended: an agent
// with no knowledge of the author's intent must not edit the author's code.
const systemPrompt = `You are greenroom's verifier. You have one disposable macOS machine and you drive it with tools.

Your job is to carry out the task you are given, then report what happened with evidence.

Rules:
- Work in small steps. Run one command, read the result, then decide.
- You diagnose failures. You do NOT fix the application source code. If the build breaks because the code is wrong, report it and stop.
- You may install tools, retry flaky steps and work around machine problems. That is infrastructure and it is yours.
- Look at the screen when the task is about what the user sees. A screenshot is described to you in words.
- Call report_verdict exactly once, at the end. Do not call it before you have evidence.
- If you cannot finish, call report_verdict with inconclusive and say what blocked you.`

// Report is the answer the caller gets. It is deliberately small: the run
// directory holds the evidence, and this says what it means.
type Report struct {
	RunID    string   `json:"runId"`
	Verdict  string   `json:"verdict"` // pass, fail or inconclusive
	Summary  string   `json:"summary"`
	Steps    int      `json:"steps"`
	Evidence []string `json:"evidence,omitempty"` // artifact paths in the run directory
	Seconds  float64  `json:"seconds"`
	Model    string   `json:"model"`
	Tokens   int      `json:"tokens"`
}

// Config names the endpoint and the two models.
type Config struct {
	BaseURL     string
	APIKey      string
	Model       string // reasons and calls tools
	VisionModel string // reads screenshots
	MaxSteps    int
	Budget      time.Duration
}

// Verifier runs the loop. One Verifier serves every run.
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
		cfg.MaxSteps = defaultMaxSteps
	}
	if cfg.Budget <= 0 {
		cfg.Budget = defaultBudget
	}
	return &Verifier{mgr: mgr, llm: nim.New(cfg.BaseURL, cfg.APIKey), cfg: cfg, log: log}, nil
}

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
			Name:        "report_verdict",
			Description: "End the run and report the result. Call this exactly once.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"verdict": map[string]any{
						"type":        "string",
						"enum":        []string{"pass", "fail", "inconclusive"},
						"description": "pass if the task succeeded, fail if the thing under test is broken, inconclusive if you could not tell.",
					},
					"summary": str("What happened and what the evidence shows, in a few sentences."),
				},
				"required": []string{"verdict", "summary"},
			},
		},
	}
}

// Run carries out task against the machine and returns what it found. An
// error means the loop itself broke. A task that fails is a Report with
// verdict fail, not an error.
func (v *Verifier) Run(ctx context.Context, runID, task string) (Report, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Budget)
	defer cancel()

	rep := Report{RunID: runID, Verdict: "inconclusive", Model: v.cfg.Model}
	msgs := []nim.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Machine runId: " + runID + "\n\nTask:\n" + task},
	}

	for step := 1; step <= v.cfg.MaxSteps; step++ {
		msg, usage, err := v.llm.Chat(ctx, v.cfg.Model, msgs, v.tools())
		rep.Tokens += usage.PromptTokens + usage.CompletionTokens
		if err != nil {
			rep.Steps = step - 1
			rep.Seconds = since(started)
			return rep, err
		}
		msgs = append(msgs, msg)

		if len(msg.ToolCalls) == 0 {
			// The model answered in prose. Ask it once for a verdict, then
			// take the prose as the summary if it still will not comply.
			if strings.TrimSpace(msg.Content) != "" && step < v.cfg.MaxSteps {
				msgs = append(msgs, nim.Message{Role: "user", Content: "Call report_verdict now with your conclusion."})
				continue
			}
			rep.Summary = strings.TrimSpace(msg.Content)
			rep.Steps = step
			rep.Seconds = since(started)
			return rep, nil
		}

		for _, call := range msg.ToolCalls {
			if call.Name == "report_verdict" {
				verdict, summary := parseVerdict(call.Arguments)
				rep.Verdict, rep.Summary = verdict, summary
				rep.Steps = step
				rep.Seconds = since(started)
				v.log.Info("verifier finished", "runId", runID, "verdict", rep.Verdict, "steps", rep.Steps)
				return rep, nil
			}
			result, artifact := v.runTool(ctx, runID, call)
			if artifact != "" {
				rep.Evidence = append(rep.Evidence, artifact)
			}
			msgs = append(msgs, nim.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
		rep.Steps = step
	}

	rep.Summary = fmt.Sprintf("The verifier used all %d steps without reporting a verdict.", v.cfg.MaxSteps)
	rep.Seconds = since(started)
	return rep, nil
}

// runTool executes one call and returns what the model should see, plus the
// path of any artifact it produced.
func (v *Verifier) runTool(ctx context.Context, runID string, call nim.ToolCall) (result, artifact string) {
	switch call.Name {
	case "machine_exec":
		var in struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &in); err != nil || strings.TrimSpace(in.Command) == "" {
			return "error: machine_exec needs a command", ""
		}
		res, err := v.mgr.Exec(ctx, runID, in.Command, in.Cwd, execTimeout)
		if err != nil {
			return "error: " + err.Error(), ""
		}
		return fmt.Sprintf("exit code %d\nstdout:\n%s\nstderr:\n%s",
			res.ExitCode, clamp(res.Stdout), clamp(res.Stderr)), ""

	case "machine_screenshot":
		png, path, err := v.mgr.Screenshot(ctx, runID)
		if err != nil {
			return "error: " + err.Error(), ""
		}
		desc, err := v.describe(ctx, png)
		if err != nil {
			// A blind verifier is still useful, so say so and continue.
			return fmt.Sprintf("The screenshot was saved to %s but it could not be described: %v", path, err), path
		}
		return "The screen shows:\n" + desc + "\n\nThe image is saved at " + path, path

	default:
		return "error: no tool named " + call.Name, ""
	}
}

const visionPrompt = `This is the screen of a macOS machine under test. Describe what is on it for an engineer who cannot see it.
Name the frontmost application and window. Quote any visible error text or dialog exactly.
If a system dialog is covering the screen, say so first, because that is a fault of the machine and not of the application under test.
Be factual and brief. Do not guess at anything you cannot read.`

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

func parseVerdict(args string) (verdict, summary string) {
	var in struct {
		Verdict string `json:"verdict"`
		Summary string `json:"summary"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	verdict = strings.ToLower(strings.TrimSpace(in.Verdict))
	switch verdict {
	case "pass", "fail", "inconclusive":
	default:
		verdict = "inconclusive"
	}
	return verdict, strings.TrimSpace(in.Summary)
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
