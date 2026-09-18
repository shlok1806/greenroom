package machine

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// runSh runs a command with sh -c and returns trimmed stdout. Tests use it to
// prove that a quoted string survives a real shell.
func runSh(t *testing.T, command string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		t.Fatalf("sh -c %q: %v", command, err)
	}
	return string(out)
}

func newTestRecorder(t *testing.T) (*recorder, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	man := Manifest{RunID: "20260917-000000-abcdef", Image: "img", MachineName: "greenroom-x", CreatedAt: time.Now().UTC()}
	r, err := newRecorder(dir, man)
	if err != nil {
		t.Fatalf("newRecorder: %v", err)
	}
	return r, dir
}

func readManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

func readSteps(t *testing.T, dir string) []Step {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "steps.jsonl"))
	if err != nil {
		t.Fatalf("read steps: %v", err)
	}
	var steps []Step
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var s Step
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatalf("decode step %q: %v", line, err)
		}
		steps = append(steps, s)
	}
	return steps
}

func TestNewRecorderWritesManifest(t *testing.T) {
	r, dir := newTestRecorder(t)
	got := readManifest(t, dir)
	if got.RunID != "20260917-000000-abcdef" || got.MachineName != "greenroom-x" {
		t.Errorf("manifest not written correctly: %+v", got)
	}
	if got.Steps != 0 {
		t.Errorf("new manifest has %d steps, want 0", got.Steps)
	}
	if got.DestroyedAt != nil {
		t.Error("new manifest has destroyedAt set")
	}
	if r.dir != dir {
		t.Errorf("recorder dir = %q, want %q", r.dir, dir)
	}
}

func TestNewRecorderFailsOnAnUnusableDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A path under a regular file cannot become a directory.
	if _, err := newRecorder(filepath.Join(file, "run"), Manifest{}); err == nil {
		t.Fatal("newRecorder accepted a path under a regular file")
	}
}

func TestRecorderStepAppendsAndNumbers(t *testing.T) {
	r, dir := newTestRecorder(t)
	started := time.Now()

	if seq := r.step("machine_create", map[string]any{"image": "img"}, map[string]any{"ok": true}, nil, started); seq != 1 {
		t.Errorf("first step seq = %d, want 1", seq)
	}
	if seq := r.step("machine_exec", nil, nil, errors.New("boom"), started); seq != 2 {
		t.Errorf("second step seq = %d, want 2", seq)
	}

	steps := readSteps(t, dir)
	if len(steps) != 2 {
		t.Fatalf("steps.jsonl holds %d steps, want 2", len(steps))
	}
	if steps[0].Tool != "machine_create" || steps[0].Error != "" {
		t.Errorf("step 1 wrong: %+v", steps[0])
	}
	if steps[1].Error != "boom" {
		t.Errorf("step 2 did not record the error: %+v", steps[1])
	}
	if steps[0].At.IsZero() {
		t.Error("step 1 has no timestamp")
	}
	if steps[0].DurationMS < 0 {
		t.Errorf("step 1 has a negative duration: %d", steps[0].DurationMS)
	}
	if got := readManifest(t, dir).Steps; got != 2 {
		t.Errorf("manifest says %d steps, want 2", got)
	}
}

func TestRecorderStepIsSafeFromManyGoroutines(t *testing.T) {
	r, dir := newTestRecorder(t)
	const n = 50
	var wg sync.WaitGroup
	seqs := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seqs[i] = r.step("machine_exec", nil, nil, nil, time.Now())
		}(i)
	}
	wg.Wait()

	seen := map[int]bool{}
	for _, s := range seqs {
		if seen[s] {
			t.Fatalf("sequence number %d was handed out twice", s)
		}
		seen[s] = true
	}
	for i := 1; i <= n; i++ {
		if !seen[i] {
			t.Errorf("sequence number %d was never handed out", i)
		}
	}
	if steps := readSteps(t, dir); len(steps) != n {
		t.Errorf("steps.jsonl holds %d lines, want %d", len(steps), n)
	}
	if got := readManifest(t, dir).Steps; got != n {
		t.Errorf("manifest says %d steps, want %d", got, n)
	}
}

func TestRecorderUpdate(t *testing.T) {
	r, dir := newTestRecorder(t)
	now := time.Now().UTC()
	if err := r.update(func(m *Manifest) { m.DestroyedAt = &now; m.IP = "192.168.64.2" }); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := readManifest(t, dir)
	if got.IP != "192.168.64.2" {
		t.Errorf("ip = %q, want 192.168.64.2", got.IP)
	}
	if got.DestroyedAt == nil {
		t.Error("destroyedAt was not written")
	}
}

func TestRecorderArtifactPath(t *testing.T) {
	r, dir := newTestRecorder(t)
	tests := []struct {
		seq  int
		kind string
		ext  string
		want string
	}{
		{1, "screenshot", "png", "001-screenshot.png"},
		{12, "screenshot", "png", "012-screenshot.png"},
		{999, "recording", "mov", "999-recording.mov"},
		{1000, "screenshot", "png", "1000-screenshot.png"},
	}
	for _, tt := range tests {
		if got := r.artifactPath(tt.seq, tt.kind, tt.ext); got != filepath.Join(dir, tt.want) {
			t.Errorf("artifactPath(%d) = %q, want %q", tt.seq, got, filepath.Join(dir, tt.want))
		}
	}
}

// The recorder must keep one JSON object per line, because that is what every
// reader of steps.jsonl assumes.
func TestStepsFileIsOneObjectPerLine(t *testing.T) {
	r, dir := newTestRecorder(t)
	r.step("machine_exec", map[string]any{"command": "echo 'a\nb'"}, ExecResult{Stdout: "a\nb\n"}, nil, time.Now())
	data, err := os.ReadFile(filepath.Join(dir, "steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("a step with newlines in its payload wrote %d lines", len(lines))
	}
	var s Step
	if err := json.Unmarshal([]byte(lines[0]), &s); err != nil {
		t.Fatalf("the line is not valid JSON: %v", err)
	}
}
