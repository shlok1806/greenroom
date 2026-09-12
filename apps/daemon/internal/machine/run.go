package machine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
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

// step appends one step and returns its sequence number.
func (r *recorder) step(tool string, input, output any, err error, started time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifest.Steps++
	s := Step{
		Seq:        r.manifest.Steps,
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
	f, ferr := os.OpenFile(filepath.Join(r.dir, "steps.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if ferr == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}
	_ = r.writeManifest()
	return s.Seq
}

// artifactPath names a file for step seq, e.g. 003-screenshot.png.
func (r *recorder) artifactPath(seq int, kind, ext string) string {
	return filepath.Join(r.dir, fmt.Sprintf("%03d-%s.%s", seq, kind, ext))
}
