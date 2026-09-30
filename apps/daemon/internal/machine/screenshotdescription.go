package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ScreenshotDescription is the verifier's accepted description of one saved capture. An error
// means the image still exists, but no description was accepted. It never changes capture time.
type ScreenshotDescription struct {
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

func screenshotDescriptionPath(dir string, seq int) string {
	return filepath.Join(dir, fmt.Sprintf("%03d-screenshot-description.json", seq))
}

// RecordScreenshotDescription binds a write-once description to an existing verifier capture.
// It is internal to the verifier, not a machine tool the model or coder can invoke.
func (m *Manager) RecordScreenshotDescription(runID string, seq int, text string, describeErr error) error {
	steps, err := m.Steps(runID)
	if err != nil {
		return err
	}
	found := false
	for _, s := range steps {
		if s.Seq == seq && s.Tool == "machine_screenshot" && s.By == HolderVerifier && s.Error == "" {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("step %d is not a successful verifier screenshot", seq)
	}
	d := ScreenshotDescription{Text: text}
	if describeErr != nil {
		d.Text = ""
		d.Error = describeErr.Error()
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	// Link a completed temporary file into place: readers never see partial JSON and a second
	// caller cannot replace an accepted description. Both paths are inside the same run directory.
	dir := m.RunDir(runID)
	f, err := os.CreateTemp(dir, ".screenshot-description-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), screenshotDescriptionPath(dir, seq))
}

func loadScreenshotDescriptions(dir string, steps []Step) error {
	for i := range steps {
		s := &steps[i]
		if s.Tool != "machine_screenshot" {
			continue
		}
		b, err := os.ReadFile(screenshotDescriptionPath(dir, s.Seq))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var d ScreenshotDescription
		if err := json.Unmarshal(b, &d); err != nil {
			return fmt.Errorf("screenshot description for step %d: %w", s.Seq, err)
		}
		s.ScreenshotDescription = &d
	}
	return nil
}
