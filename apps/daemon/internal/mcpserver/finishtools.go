package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/report"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// errTurnOpen is run_finish's answer while the verifier is in a turn (ADR 0031): a turn is
// never cut short, so the coding agent waits for its reply.
var errTurnOpen = errors.New("the verifier is in a turn on this run; call agent_wait until it replies, then finish")

// addFinishTools adds run_finish and run_report (ADR 0031): how a coding agent ends its job,
// and the run's proof.
func addFinishTools(s *mcp.Server, mgr *machine.Manager, reg *session.Registry, o options) {
	links := func(embed bool) report.Links { return report.Links{BaseURL: o.artifactBase, Embed: embed} }

	type refIn struct {
		Branch string `json:"branch,omitempty" jsonschema:"The branch the work is on."`
		Commit string `json:"commit,omitempty" jsonschema:"The commit sha the work became."`
		PR     string `json:"pr,omitempty" jsonschema:"The pull request, as a URL or a number."`
	}
	type finishIn struct {
		RunID   string `json:"runId" jsonschema:"runId from machine_create"`
		Outcome string `json:"outcome" jsonschema:"verified (the run's current verdict is a pass that was accepted; the daemon checks), unverified (finished without an accepted pass), or abandoned (the work was given up)."`
		Summary string `json:"summary" jsonschema:"One or two sentences of what changed. Required."`
		Ref     *refIn `json:"ref,omitempty" jsonschema:"What the work became, each field free text. Optional."`
		Destroy *bool  `json:"destroy,omitempty" jsonschema:"Destroy the machine after recording the finish, as machine_destroy does. Default true."`
	}
	type finishOut struct {
		Finish       session.Finish `json:"finish"`
		Destroyed    bool           `json:"destroyed" jsonschema:"Whether this call destroyed the machine."`
		DestroyError string         `json:"destroyError,omitempty" jsonschema:"Why destroying the machine failed; the finish is recorded anyway. Call machine_destroy."`
		Report       report.Report  `json:"report"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "run_finish",
		Description: "End your job on this run and get its proof. Call it once, when the work is done or given up. " +
			"outcome is verified, unverified or abandoned. verified is accepted only when the run's current verdict " +
			"is a pass that was accepted (you accept it with agent_send kind accept); otherwise the call is refused " +
			"with the reason, and you may finish as unverified, which says so. You cannot mark your own work " +
			"verified. summary (one or two sentences of what changed) is required; ref (branch, commit, pr) is " +
			"optional. A run finishes once; a second call is refused. While the verifier is in a turn or owes an " +
			"answer, finishing is refused: call agent_wait until it replies. The finish is recorded in the " +
			"conversation and the run manifest, then the machine is destroyed unless destroy is false. Returns the " +
			"run's report, the same as run_report: Markdown to paste into a PR body, and the report as data.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in finishIn) (*mcp.CallToolResult, finishOut, error) {
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, finishOut{}, err
		}
		if mgr.VerifierTurnOpen(in.RunID) {
			return nil, finishOut{}, errTurnOpen
		}
		f := session.Finish{Outcome: strings.ToLower(strings.TrimSpace(in.Outcome)), Summary: strings.TrimSpace(in.Summary)}
		if in.Ref != nil {
			ref := session.Ref{Branch: strings.TrimSpace(in.Ref.Branch), Commit: strings.TrimSpace(in.Ref.Commit), PR: strings.TrimSpace(in.Ref.PR)}
			if !ref.IsZero() {
				f.Ref = &ref
			}
		}
		// Append checks the rules that need the transcript: once, an accepted pass for verified,
		// no turn owed. Its refusal is what the agent needs to read.
		m, err := store.Append(session.Message{From: session.System, Kind: session.Event, Text: session.FinishText(f), Finish: &f})
		if err != nil {
			return nil, finishOut{}, err
		}
		out := finishOut{Finish: *m.Finish}
		if err := mgr.RecordFinish(in.RunID, *m.Finish); err != nil {
			slog.Warn("cannot record the finish in the run manifest", "runId", in.RunID, "err", err)
		}
		if (in.Destroy == nil || *in.Destroy) && mgr.Live(in.RunID) {
			// main.go's lifecycle bridge announces the destroy, as for machine_destroy.
			if err := mgr.Destroy(ctx, in.RunID); err != nil {
				out.DestroyError = err.Error()
			} else {
				out.Destroyed = true
			}
		}
		// Destroying evicts the run's store; Get reopens it from disk.
		if store, err = reg.Get(in.RunID); err != nil {
			return nil, finishOut{}, fmt.Errorf("the finish is recorded, but the report cannot be read: %w", err)
		}
		rep, err := report.FromStore(mgr.RunDir(in.RunID), store, o.models, links(false))
		if err != nil {
			return nil, finishOut{}, fmt.Errorf("the finish is recorded, but the report cannot be made: %w", err)
		}
		out.Report = rep
		text := rep.Markdown()
		if out.DestroyError != "" {
			text = "The finish is recorded, but destroying the machine failed: " + out.DestroyError + ". Call machine_destroy.\n\n" + text
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	})

	type reportIn struct {
		RunID  string `json:"runId" jsonschema:"runId from machine_create"`
		Format string `json:"format,omitempty" jsonschema:"md (default): the text is Markdown to paste into a PR body or comment. json: the text is the report as JSON. The structured result is the JSON report either way."`
		Embed  bool   `json:"embed,omitempty" jsonschema:"Put each evidence screenshot in the report as a data URI instead of a link, for a place that cannot reach this daemon. Large: about 0.7 MB per screenshot."`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "run_report",
		Description: "Read a run's proof, finished or not, live or destroyed: the task, how the run finished (outcome, " +
			"summary, ref), the models that verified it, times, and the verdict with every check: its kinds, status, " +
			"what was observed and its evidence steps, with a link to each evidence screenshot. Checks not checked " +
			"are listed as such. A pass certifies that every listed check was observed on this build, not that the " +
			"change works. Changes nothing.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in reportIn) (*mcp.CallToolResult, report.Report, error) {
		format := strings.ToLower(strings.TrimSpace(in.Format))
		if format != "" && format != "md" && format != "json" {
			return nil, report.Report{}, fmt.Errorf("format must be md or json, not %q", in.Format)
		}
		store, err := reg.Get(in.RunID)
		if err != nil {
			return nil, report.Report{}, err
		}
		rep, err := report.FromStore(mgr.RunDir(in.RunID), store, o.models, links(in.Embed))
		if err != nil {
			return nil, report.Report{}, err
		}
		text := rep.Markdown()
		if format == "json" {
			data, err := json.MarshalIndent(rep, "", "  ")
			if err != nil {
				return nil, report.Report{}, err
			}
			text = string(data)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, rep, nil
	})
}
