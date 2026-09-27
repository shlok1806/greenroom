// Package report builds a run's proof (ADR 0034): the task, how the run finished, the models
// that verified it, and the verdict with every check and its evidence steps, as JSON and as
// Markdown shaped for a PR body. It reads only the run directory and the conversation, so it
// answers for a live run and for one whose machine is long gone. run_report, run_finish and
// GET /api/runs/{id}/report all build it here, so there is one shape.
package report

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Models names the models that verified a run: the brain (the verifier's reasoning model, or
// "manual" or "none") and the vision model that describes screenshots.
type Models struct {
	Brain  string `json:"brain,omitempty"`
	Vision string `json:"vision,omitempty"`
	// Source says where the names came from. SourceRun: the run's manifest, which records who
	// verified it when it was created (issue #154). SourceDaemon: the daemon's verifier
	// configuration when the report was made, for a run from before that record; it may
	// differ from what verified the run.
	Source string `json:"source,omitempty"`
}

// Where a report's Models came from.
const (
	SourceRun    = "recorded with the run"
	SourceDaemon = "daemon configuration at report time"
)

// FromMachine is m as a report names it, marked with source: a model verifier by its reasoning
// model and describer, any other brain (manual, none) by its name.
func FromMachine(m machine.Models, source string) Models {
	r := Models{Brain: m.Brain, Source: source}
	if m.Brain == machine.BrainNIM {
		r.Brain, r.Vision = m.Model, m.Vision
	}
	return r
}

// Links says how the report points at a screenshot.
type Links struct {
	// BaseURL, when set, is the public host's origin (https://gr.example.com): links go through
	// the artifact route there, which greenroom connect and the tunnel can reach. Empty: the
	// file's path on this host.
	BaseURL string
	// Embed puts each screenshot in the report as a data URI instead of a link.
	Embed bool
}

// Input is everything a report is made from.
type Input struct {
	Dir      string // the run directory
	Messages []session.Message
	Verdict  session.VerdictState // the conversation's current verdict (Store.Verdict)
	Models   Models               // the daemon's verifier models now, for a run that recorded none (see runModels)
	Links    Links
}

// Report is a run's proof.
type Report struct {
	RunID string `json:"runId"`
	// Task is the task the verdict answers (the newest coder or human task before it), else the
	// run's newest task. TaskSeq is its message.
	Task        string          `json:"task,omitempty"`
	TaskSeq     int             `json:"taskSeq,omitempty"`
	Finish      *session.Finish `json:"finish"`
	Models      Models          `json:"models"`
	CreatedAt   time.Time       `json:"createdAt"`
	DestroyedAt *time.Time      `json:"destroyedAt,omitempty"`
	Steps       int             `json:"steps"`
	Verdict     *Verdict        `json:"verdict"`
	// Scope is what the verdict certifies and what it does not (ADR 0024 point 6).
	Scope string `json:"scope"`
}

// Verdict is the run's current verdict with its checks resolved against the step log.
type Verdict struct {
	Seq        int            `json:"seq"`
	At         time.Time      `json:"at"`
	Verdict    string         `json:"verdict"`
	Status     session.Status `json:"status"`
	AcceptedBy session.From   `json:"acceptedBy,omitempty"`
	Disputes   int            `json:"disputes"`
	Summary    string         `json:"summary"`
	Checks     []Check        `json:"checks"`
	// Artifacts are the verdict's own evidence paths (ADR 0024 keeps them for files).
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Check is one acceptance check as the verdict answered it.
type Check struct {
	ID        string   `json:"id"`
	Criterion string   `json:"criterion,omitempty"`
	Kinds     []string `json:"kinds"`
	Within    float64  `json:"within,omitempty"`
	Status    string   `json:"status"` // pass, fail or unchecked
	Observed  string   `json:"observed,omitempty"`
	Evidence  []Step   `json:"evidence"`
	Actions   []Step   `json:"actions"`
}

// Step is a step a check cites, as the step log recorded it.
type Step struct {
	Step int        `json:"step"`
	Tool string     `json:"tool,omitempty"` // empty when the step log has no such step
	At   *time.Time `json:"at,omitempty"`
	By   string     `json:"by,omitempty"`
	// Screenshot is set for a machine_screenshot step whose PNG is in the run directory.
	Screenshot *Artifact `json:"screenshot,omitempty"`
}

// Artifact is a file in the run directory and how a reader opens it.
type Artifact struct {
	File string `json:"file"`
	// Link is the artifact route URL, the host path, or a data URI (Links.Embed).
	Link string `json:"link,omitempty"`
}

// Build makes the report for the run in in.Dir.
func Build(in Input) (Report, error) {
	man, err := machine.ReadManifest(in.Dir)
	if err != nil {
		return Report{}, fmt.Errorf("read the run manifest: %w", err)
	}
	steps, err := machine.ReadSteps(in.Dir)
	if err != nil {
		return Report{}, fmt.Errorf("read the run's steps: %w", err)
	}
	bySeq := make(map[int]machine.Step, len(steps))
	for _, s := range steps {
		bySeq[s.Seq] = s
	}
	b := builder{dir: in.Dir, runID: man.RunID, links: in.Links, steps: bySeq}
	if b.runID == "" {
		b.runID = filepath.Base(in.Dir)
	}

	r := Report{RunID: b.runID, CreatedAt: man.CreatedAt, DestroyedAt: man.DestroyedAt, Steps: len(steps),
		Models: runModels(man, in.Models), Finish: finishOf(in.Messages, man)}
	if in.Verdict.Status != session.None && in.Verdict.Seq > 0 {
		r.Verdict = b.verdict(in.Verdict, in.Messages)
	}
	r.TaskSeq, r.Task = task(in.Messages, in.Verdict.Seq)
	r.Scope = scope(r.Verdict)
	return r, nil
}

// runModels is the models to report: the ones the run's manifest recorded (issue #154), else,
// for a run from before that record, fallback (the daemon's configuration at report time),
// with Source saying which.
func runModels(man machine.Manifest, fallback Models) Models {
	if man.Models != nil {
		return FromMachine(*man.Models, SourceRun)
	}
	if fallback.Source == "" && (fallback.Brain != "" || fallback.Vision != "") {
		fallback.Source = SourceDaemon
	}
	return fallback
}

// finishOf is the run's finish: from its event in the conversation, which is the record, else
// from the manifest's mirror of it.
func finishOf(msgs []session.Message, man machine.Manifest) *session.Finish {
	for _, m := range msgs {
		if m.Finish != nil {
			f := *m.Finish
			return &f
		}
	}
	return man.Finish
}

// task is the task the verdict at verdictSeq answers: the newest coder or human task before it.
// With no verdict it is the newest task.
func task(msgs []session.Message, verdictSeq int) (int, string) {
	seq, text := 0, ""
	for _, m := range msgs {
		if verdictSeq > 0 && m.Seq >= verdictSeq {
			break
		}
		if m.Kind == session.Task && (m.From == session.Coder || m.From == session.Human) {
			seq, text = m.Seq, m.Text
		}
	}
	return seq, text
}

// scope says what the verdict certifies (ADR 0024 point 6, ADR 0034 point 2).
func scope(v *Verdict) string {
	switch {
	case v == nil:
		return "No verdict: the verifier did not check this run, so nothing here was observed on the build."
	case v.Verdict == "pass":
		return "Every listed check was observed on this build. That is the whole claim: it does not say the change works beyond these checks."
	case len(v.Checks) == 0:
		return "This verdict has no checklist, so it certifies no specific check."
	}
	return "Each check shows what was observed on this build. A check listed as not checked was not observed; nothing beyond the listed checks is claimed."
}

type builder struct {
	dir   string
	runID string
	links Links
	steps map[int]machine.Step
}

func (b builder) verdict(v session.VerdictState, msgs []session.Message) *Verdict {
	out := &Verdict{Seq: v.Seq, Verdict: v.Verdict, Status: v.Status, AcceptedBy: v.AcceptedBy, Disputes: v.Disputes,
		Summary: v.Summary, Checks: []Check{}}
	for _, m := range msgs {
		if m.Seq == v.Seq {
			out.At = m.At
		}
	}
	for _, c := range v.Checks {
		kinds := c.Kinds
		if len(kinds) == 0 {
			kinds = []string{session.CheckValue}
		}
		status := c.Status
		if status == "" {
			status = session.CheckUnchecked
		}
		out.Checks = append(out.Checks, Check{ID: c.ID, Criterion: c.Criterion, Kinds: kinds, Within: c.Within,
			Status: status, Observed: c.Observed, Evidence: b.stepList(c.Evidence), Actions: b.stepList(c.Actions)})
	}
	for _, e := range v.Evidence {
		if a, ok := b.artifact(e); ok {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	return out
}

func (b builder) stepList(seqs []int) []Step {
	out := make([]Step, 0, len(seqs))
	for _, n := range seqs {
		out = append(out, b.step(n))
	}
	return out
}

func (b builder) step(n int) Step {
	s, ok := b.steps[n]
	if !ok {
		return Step{Step: n}
	}
	at := s.At
	out := Step{Step: n, Tool: s.Tool, At: &at, By: s.By}
	if s.Tool == "machine_screenshot" && s.Error == "" {
		if path := outputPath(s.Output); path != "" {
			if a, ok := b.artifact(path); ok {
				out.Screenshot = &a
			}
		}
	}
	return out
}

// outputPath is the "path" a step's output names, if any.
func outputPath(output any) string {
	m, ok := output.(map[string]any)
	if !ok {
		return ""
	}
	p, _ := m["path"].(string)
	return p
}

// artifact resolves a path a step or verdict names to a file in the run directory. Only the
// base name is trusted: the recorded path is from the host that made it, and a report must
// never point outside its run.
func (b builder) artifact(path string) (Artifact, bool) {
	name := filepath.Base(strings.TrimSpace(path))
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		return Artifact{}, false
	}
	full := filepath.Join(b.dir, name)
	if st, err := os.Stat(full); err != nil || !st.Mode().IsRegular() {
		return Artifact{}, false
	}
	a := Artifact{File: name}
	switch {
	case b.links.Embed && imageType(name) != "":
		data, err := os.ReadFile(full)
		if err == nil {
			a.Link = "data:" + imageType(name) + ";base64," + base64.StdEncoding.EncodeToString(data)
			break
		}
		a.Link = b.plainLink(name, full)
	default:
		a.Link = b.plainLink(name, full)
	}
	return a, true
}

func (b builder) plainLink(name, full string) string {
	if b.links.BaseURL != "" {
		return strings.TrimRight(b.links.BaseURL, "/") + "/api/runs/" + url.PathEscape(b.runID) + "/artifacts/" + url.PathEscape(name)
	}
	return full
}

func imageType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	}
	return ""
}

// FromStore builds the report for the run in dir from its conversation store.
func FromStore(dir string, store *session.Store, models Models, links Links) (Report, error) {
	return Build(Input{Dir: dir, Messages: store.After(0), Verdict: store.Verdict(), Models: models, Links: links})
}
