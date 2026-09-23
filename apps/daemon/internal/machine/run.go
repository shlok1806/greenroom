package machine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

	// Steps is the highest step number handed out, not a count: a number
	// claimed by begin may never reach steps.jsonl. Count via ReadStepLog.
	Steps int `json:"steps"`

	// Verdict is the conversation's latest verdict (ADR 0006).
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

// saveManifest replaces dir/manifest.json atomically, because the API reads
// it while the recorder writes it.
func saveManifest(dir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "manifest.json"), data)
}

// writeFileAtomic replaces path with data through a synced temp file in the
// same directory, so a crash leaves the old file or the new one, never a torn one.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// ReadSteps loads a run's step log. A run with no steps yet has none.
func ReadSteps(dir string) ([]Step, error) {
	return readJSONL[Step](dir, "steps.jsonl")
}

// readJSONL decodes one T per line of dir/name. A missing file is empty.
func readJSONL[T any](dir, name string) ([]T, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return []T{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := []T{}
	sc := lineScanner(f)
	for sc.Scan() {
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", name, len(out)+1, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// lineScanner allows lines up to 16 MiB; a step can carry a lot of output.
func lineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return sc
}

// appendLine appends one JSON line to path.
func appendLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// StepLog summarises a run's step log.
type StepLog struct {
	Count   int
	Highest int
	// Last is when the newest step ended (start plus duration), so a long
	// final build does not date the run's last activity too early.
	Last time.Time
}

// ReadStepLog summarises steps.jsonl, decoding only the fields it needs.
func ReadStepLog(dir string) (StepLog, error) {
	var out StepLog
	f, err := os.Open(filepath.Join(dir, "steps.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	sc := lineScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var s struct {
			Seq        int       `json:"seq"`
			At         time.Time `json:"at"`
			DurationMS int64     `json:"durationMs"`
		}
		if err := json.Unmarshal(line, &s); err != nil {
			return out, fmt.Errorf("parse steps.jsonl line %d: %w", out.Count+1, err)
		}
		out.Count++
		out.Highest = max(out.Highest, s.Seq)
		if end := s.At.Add(time.Duration(s.DurationMS) * time.Millisecond); end.After(out.Last) {
			out.Last = end
		}
	}
	return out, sc.Err()
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

// recorder writes a run's manifest, step log and frame log under dir. It is
// the only thing that hands out step numbers.
type recorder struct {
	mu             sync.Mutex
	dir            string
	log            *slog.Logger
	manifest       Manifest
	frameErrLogged bool

	// The current capture outage, if any, and whether a recovery was logged.
	frameFails          int
	frameFailSince      time.Time
	frameRecoveryLogged bool
}

func newRecorder(dir string, m Manifest, log *slog.Logger) (*recorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	r := &recorder{dir: dir, log: log, manifest: m}
	return r, r.writeManifest()
}

func (r *recorder) writeManifest() error { return saveManifest(r.dir, r.manifest) }

func (r *recorder) update(fn func(*Manifest)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.manifest)
	return r.writeManifest()
}

// markEnded stamps destroyedAt with the current time.
func (r *recorder) markEnded() {
	now := time.Now().UTC()
	if err := r.update(func(man *Manifest) { man.DestroyedAt = &now }); err != nil {
		r.log.Warn("cannot record the end of the run", "dir", r.dir, "err", err)
	}
}

// begin claims the next step number. A tool that names an artifact claims
// its number first so concurrent callers never pick the same file. This is
// the step's only manifest write: complete leaves Steps unchanged.
func (r *recorder) begin() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifest.Steps++
	if err := r.writeManifest(); err != nil {
		r.log.Warn("cannot write the run manifest", "dir", r.dir, "step", r.manifest.Steps, "err", err)
	}
	return r.manifest.Steps
}

// complete records a step under a number begin already claimed.
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
	line, err := json.Marshal(s)
	if err != nil {
		r.log.Warn("cannot encode a step; it is not recorded", "dir", r.dir, "step", seq, "tool", tool, "err", err)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := appendLine(filepath.Join(r.dir, "steps.jsonl"), line); err != nil {
		r.log.Warn("cannot record a step", "dir", r.dir, "step", seq, "tool", tool, "err", err)
	}
}

// step claims a number and records the step in one call.
func (r *recorder) step(tool string, input, output any, err error, started time.Time) int {
	seq := r.begin()
	r.complete(seq, tool, input, output, err, started)
	return seq
}

// artifactPath names a file for step seq, e.g. 003-screenshot.png.
func (r *recorder) artifactPath(seq int, kind, ext string) string {
	return filepath.Join(r.dir, fmt.Sprintf("%03d-%s.%s", seq, kind, ext))
}

// currentStep is the latest claimed step, which a frame cites.
func (r *recorder) currentStep() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifest.Steps
}

// appendFrame adds one line to frames.jsonl under the step log's lock.
func (r *recorder) appendFrame(fr Frame) error {
	line, err := json.Marshal(fr)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return appendLine(filepath.Join(r.dir, "frames.jsonl"), line)
}

// frameFailed counts a failed capture and reports whether it is the run's
// first, which is the only one logged.
func (r *recorder) frameFailed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frameFails == 0 {
		r.frameFailSince = time.Now()
	}
	r.frameFails++
	first := !r.frameErrLogged
	r.frameErrLogged = true
	return first
}

// frameCaptured ends an outage. It returns the outage the first time captures
// come back after the logged failure, so a transient failure (a host waking
// from sleep) does not read like a recorder that stopped for good.
func (r *recorder) frameCaptured() (fails int, since time.Time, report bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fails, since = r.frameFails, r.frameFailSince
	r.frameFails = 0
	if fails == 0 || r.frameRecoveryLogged {
		return fails, since, false
	}
	r.frameRecoveryLogged = true
	return fails, since, true
}
