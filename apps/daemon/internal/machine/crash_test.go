package machine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// runCrashScript runs crashReportScript for real on the host (the same sh, stat and grep as the
// guest) with home as $HOME.
func runCrashScript(t *testing.T, home, app, age string) (CrashReport, bool) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", crashReportScript, "sh", app, age, "1")
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	r, found, err := parseCrashReport(string(out))
	if err != nil {
		t.Fatal(err)
	}
	return r, found
}

func TestTheCrashReportScriptFindsTheNewestReportSinceTheInput(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	header := `{"app_name":"WordCount","bug_type":"309"}` + "\n{\n  \"uptime\" : 100,\n"
	write("WordCount-2026-09-25-100000.ips", header+`  "exception" : {"type":"EXC_BAD_ACCESS","signal":"SIGSEGV"},`+"\n}\n", time.Hour)
	newest := write("WordCount-2026-09-26-013222.ips",
		header+`  "exception" : {"type":"EXC_BREAKPOINT","signal":"SIGTRAP"},`+"\n  \"exception\" : \"second\",\n}\n", time.Second)
	write("Other-2026-09-26-013222.ips", header, 0)
	write("Unit Convert-2026-09-26-013222.ips", header, 0)

	r, found := runCrashScript(t, home, "WordCount", "10")
	if !found || r.Path != newest || r.Exception != `"exception" : {"type":"EXC_BREAKPOINT","signal":"SIGTRAP"}` {
		t.Errorf("report = %+v (found %v), want %s and its first exception line", r, found, newest)
	}
	// Only reports written since the input count.
	if r, found := runCrashScript(t, home, "WordCount", "0"); found {
		t.Errorf("report = %+v, want none older than the input", r)
	}
	if _, found := runCrashScript(t, home, "TipSplit", "10"); found {
		t.Error("found a report for an app that wrote none")
	}
	// A name with a space is an argument, and globs as one.
	if r, found := runCrashScript(t, home, "Unit Convert", "10"); !found || filepath.Base(r.Path) != "Unit Convert-2026-09-26-013222.ips" {
		t.Errorf("report = %+v (found %v), want the Unit Convert report", r, found)
	}
	if _, found := runCrashScript(t, t.TempDir(), "WordCount", "10"); found {
		t.Error("found a report with no report directory")
	}
}
