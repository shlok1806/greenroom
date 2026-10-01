package machine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScreenshotDescriptionsAreWriteOnceAndReloadWithCaptureTime(t *testing.T) {
	m := &Manager{Root: t.TempDir()}
	dir := m.RunDir("run")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)
	step := Step{Seq: 1, Tool: "machine_screenshot", By: HolderVerifier, At: at, DurationMS: 125}
	b, _ := json.Marshal(step)
	if err := appendLine(filepath.Join(dir, "steps.jsonl"), b); err != nil {
		t.Fatal(err)
	}
	historical, err := m.Steps("run")
	if err != nil || historical[0].ScreenshotDescription != nil {
		t.Fatalf("historical capture: %+v, %v", historical, err)
	}
	if err := m.RecordScreenshotDescription("run", 1, "Run B is selected.", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordScreenshotDescription("run", 1, "Run A is selected.", nil); !os.IsExist(err) {
		t.Fatalf("description overwritten: %v", err)
	}
	got, err := ReadSteps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ScreenshotDescription.Text != "Run B is selected." || !got[0].At.Equal(at) || got[0].DurationMS != 125 {
		t.Fatalf("reloaded capture %+v", got[0])
	}
	if err := m.RecordScreenshotDescription("../run", 1, "escape", nil); err == nil {
		t.Fatal("invalid run accepted")
	}
	if err := m.RecordScreenshotDescription("run", 2, "another step", nil); err == nil {
		t.Fatal("unknown capture accepted")
	}
	if err := os.WriteFile(screenshotDescriptionPath(dir, 1), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSteps(dir); err == nil {
		t.Fatal("corrupt description accepted")
	}
}

func TestDescriptionsCannotBeBoundToCoderOrFailedOrNonScreenshotSteps(t *testing.T) {
	for _, s := range []Step{
		{Seq: 1, Tool: "machine_screenshot", By: HolderCoder},
		{Seq: 1, Tool: "machine_screenshot", By: HolderVerifier, Error: "capture failed"},
		{Seq: 1, Tool: "machine_ui", By: HolderVerifier},
	} {
		m := &Manager{Root: t.TempDir()}
		dir := m.RunDir("run")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(s)
		if err := appendLine(filepath.Join(dir, "steps.jsonl"), b); err != nil {
			t.Fatal(err)
		}
		if err := m.RecordScreenshotDescription("run", 1, "invented", nil); err == nil {
			t.Fatalf("bound to %+v", s)
		}
	}
}
