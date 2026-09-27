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

	// Finish is how the coding agent ended the run (ADR 0034), mirrored from the conversation's
	// finish event. Absent on a run that has not finished, and on every run from before it.
	Finish *session.Finish `json:"finish,omitempty"`
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

// readJSONL decodes one T per line of dir/name. A missing file is empty. A
// final line with no newline is a write in progress or torn by a crash, so it
// is skipped; a bad line anywhere else is an error.
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
	err = eachLine(f, func(n int, line []byte, terminated bool) error {
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			if !terminated {
				return nil
			}
			return fmt.Errorf("parse %s line %d: %w", name, n, err)
		}
		out = append(out, v)
		return nil
	})
	return out, err
}

// maxLine bounds one JSONL line; a step can carry a lot of output.
const maxLine = 16 * 1024 * 1024

// eachLine calls fn for every non-blank line of r, numbered from 1, saying
// whether it ended in a newline. Only the last line can lack one.
func eachLine(r io.Reader, fn func(n int, line []byte, terminated bool) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for n := 1; ; n++ {
		line, err := br.ReadSlice('\n')
		var long []byte
		for errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, line...)
			if len(long) > maxLine {
				return fmt.Errorf("line %d is longer than %d bytes", n, maxLine)
			}
			line, err = br.ReadSlice('\n')
		}
		if long != nil {
			line = append(long, line...)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		terminated := len(line) > 0 && line[len(line)-1] == '\n'
		if len(bytes.TrimSpace(line)) > 0 {
			if ferr := fn(n, bytes.TrimRight(line, "\r\n"), terminated); ferr != nil {
				return ferr
			}
		}
		if err != nil {
			return nil
		}
	}
}

// cutTornTail truncates path after its last newline. Only the one writer may
// call it, before it appends: a reader would race a line being written.
func cutTornTail(path string) (cut int64, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return 0, err
	}
	size := info.Size()
	buf := make([]byte, 64*1024)
	for end := size; end > 0; {
		start := max(end-int64(len(buf)), 0)
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			keep := start + int64(i) + 1
			if keep == size {
				return 0, nil
			}
			return size - keep, f.Truncate(keep)
		}
		end = start
	}
	return size, f.Truncate(0)
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
	err = eachLine(f, func(n int, line []byte, terminated bool) error {
		var s struct {
			Seq        int       `json:"seq"`
			At         time.Time `json:"at"`
			DurationMS int64     `json:"durationMs"`
		}
		if err := json.Unmarshal(line, &s); err != nil {
			if !terminated {
				return nil // torn or still being written, as in readJSONL
			}
			return fmt.Errorf("parse steps.jsonl line %d: %w", n, err)
		}
		out.Count++
		out.Highest = max(out.Highest, s.Seq)
		if end := s.At.Add(time.Duration(s.DurationMS) * time.Millisecond); end.After(out.Last) {
			out.Last = end
		}
		return nil
	})
	return out, err
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
	// By is the seat that made the step, when the caller named one: HolderVerifier,
	// HolderCoder, or a human seat. Steps from before ADR 0024 have none.
	By string `json:"by,omitempty"`
	// Effect is set on the verifier's UI read after one of its inputs (ADR 0024): what that
	// input changed. Of names the input step.
	Effect *StepEffect `json:"effect,omitempty"`
}

// StepEffect is what an input changed on the screen, found by the UI read it is recorded on.
type StepEffect struct {
	Of      int    `json:"of"`                // the input step
	Kind    string `json:"kind"`              // EffectChanged, EffectNone, EffectUnknown or EffectQuit
	Summary string `json:"summary,omitempty"` // what changed, as the model was told
}

// StepEffect kinds.
const (
	EffectChanged = "changed"
	EffectNone    = "none"
	EffectUnknown = "unknown"
	// EffectQuit: the app that was frontmost before the input is no longer running (ADR 0028).
	EffectQuit = "quit"
)

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
	// A daemon killed mid-append leaves a torn last line; appending after it would glue the next
	// line onto the fragment (issue #67). This runs before the first append, with no other writer.
	for _, name := range []string{"steps.jsonl", "frames.jsonl"} {
		if cut, err := cutTornTail(filepath.Join(dir, name)); err != nil {
			log.Warn("cannot repair a torn line in the run record", "dir", dir, "file", name, "err", err)
		} else if cut > 0 {
			log.Warn("cut a torn final line off the run record", "dir", dir, "file", name, "bytes", cut)
		}
	}
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
	r.completeAs(seq, "", nil, tool, input, output, err, started)
}

// completeAs is complete for a step made by seat by, carrying effect when it is an input's
// effect read (ADR 0024).
func (r *recorder) completeAs(seq int, by string, effect *StepEffect, tool string, input, output any, err error, started time.Time) {
	s := Step{
		Seq:        seq,
		At:         started.UTC(),
		Tool:       tool,
		Input:      input,
		Output:     output,
		DurationMS: time.Since(started).Milliseconds(),
		By:         by,
		Effect:     effect,
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
	return r.stepAs("", tool, input, output, err, started)
}

// stepAs is step for a step made by seat by.
func (r *recorder) stepAs(by, tool string, input, output any, err error, started time.Time) int {
	seq := r.begin()
	r.completeAs(seq, by, nil, tool, input, output, err, started)
	return seq
}

// artifactPath names a file for step seq, e.g. 003-screenshot.png.
func (r *recorder) artifactPath(seq int, kind, ext string) string {
	return filepath.Join(r.dir, fmt.Sprintf("%03d-%s.%s", seq, kind, ext))
}

// artifactDir names a directory for step seq, e.g. 004-pull.
func (r *recorder) artifactDir(seq int, kind string) string {
	return filepath.Join(r.dir, fmt.Sprintf("%03d-%s", seq, kind))
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
