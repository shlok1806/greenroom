package machine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// Manifest describes one run: one machine, one agent, recorded as it happens.
type Manifest struct {
	RunID       string     `json:"runId"`
	Image       string     `json:"image"`
	MachineName string     `json:"machineName"`
	IP          string     `json:"ip,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	DestroyedAt *time.Time `json:"destroyedAt,omitempty"`
	Steps       int        `json:"steps"`

	// Verdict is the state of the conversation's latest verdict (ADR 0006),
	// kept here so a reviewer sees it without opening the transcript.
	Verdict *session.VerdictState `json:"verdict,omitempty"`
}

// ReadManifest loads a run's manifest from its directory.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}

// ReadSteps loads a run's step log. A run with no steps yet has none.
func ReadSteps(dir string) ([]Step, error) {
	f, err := os.Open(filepath.Join(dir, "steps.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return []Step{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	steps := []Step{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var s Step
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return nil, fmt.Errorf("parse steps.jsonl line %d: %w", len(steps)+1, err)
		}
		steps = append(steps, s)
	}
	return steps, sc.Err()
}

// Step is one tool call against the machine, appended to steps.jsonl.
type Step struct {
	Seq        int       `json:"seq"`
	At         time.Time `json:"at"`
	Tool       string    `json:"tool"`
	Input      any       `json:"input,omitempty"`
	Output     any       `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"durationMs"`
}

// recorder writes a run's manifest and step log under dir.
type recorder struct {
	mu       sync.Mutex
	dir      string
	manifest Manifest
}

func newRecorder(dir string, m Manifest) (*recorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	r := &recorder{dir: dir, manifest: m}
	return r, r.writeManifest()
}

func (r *recorder) writeManifest() error {
	data, err := json.MarshalIndent(r.manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "manifest.json"), data, 0o644)
}

func (r *recorder) update(fn func(*Manifest)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.manifest)
	return r.writeManifest()
}

// begin claims the next step number. A caller that must name an artifact
// before it can record the step claims its number first, so that two callers
// at the same time never choose the same file name.
func (r *recorder) begin() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifest.Steps++
	_ = r.writeManifest()
	return r.manifest.Steps
}

// complete records a step under a number that begin already claimed.
func (r *recorder) complete(seq int, tool string, input, output any, err error, started time.Time) {
	s := Step{
		Seq:        seq,
		At:         started.UTC(),
		Tool:       tool,
		Input:      input,
		Output:     output,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		s.Error = err.Error()
	}
	line, _ := json.Marshal(s)

	r.mu.Lock()
	defer r.mu.Unlock()
	f, ferr := os.OpenFile(filepath.Join(r.dir, "steps.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if ferr == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}
	_ = r.writeManifest()
}

// step claims a number and records the step in one call. Use it for every
// tool that does not name a file.
func (r *recorder) step(tool string, input, output any, err error, started time.Time) int {
	seq := r.begin()
	r.complete(seq, tool, input, output, err, started)
	return seq
}

// artifactPath names a file for step seq, e.g. 003-screenshot.png.
func (r *recorder) artifactPath(seq int, kind, ext string) string {
	return filepath.Join(r.dir, fmt.Sprintf("%03d-%s.%s", seq, kind, ext))
}
