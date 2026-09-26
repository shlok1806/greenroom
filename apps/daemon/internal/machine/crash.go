package machine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// A crash is evidence (ADR 0028): when the app that was frontmost before a verifier input is no
// longer running after it, the effect read names the newest crash report macOS wrote for it
// since the input.

// CrashReport is a crash report found in the guest.
type CrashReport struct {
	Path      string // in the guest
	Exception string // its first "exception" line, trimmed; empty when it has none
}

// crashReportWait is how long the guest looks for a report still being written: ReportCrash
// writes it a moment after the process dies, often after the UI read that saw it gone.
const crashReportWait = 3 * time.Second

// crashReportScript prints the newest $HOME/Library/Logs/DiagnosticReports/<app>*.ips modified
// at most <age> seconds ago by the guest's own clock, then its first exception line, or nothing.
// It looks again every half second for <tries> tries. The app name is an argument, never
// script text, so no name can run as a command.
const crashReportScript = `: greenroom-crash-report
app="$1"; since=$(( $(date +%s) - $2 )); tries="$3"
dir="$HOME/Library/Logs/DiagnosticReports"
i=0
while :; do
  newest=""; best=0
  for f in "$dir/$app"*.ips; do
    [ -f "$f" ] || continue
    m=$(stat -f %m "$f" 2>/dev/null) || continue
    if [ "$m" -ge "$since" ] && [ "$m" -ge "$best" ]; then best=$m; newest=$f; fi
  done
  if [ -n "$newest" ]; then
    printf '%s\n' "$newest"
    grep -m1 '"exception"' "$newest" 2>/dev/null | cut -c1-300
    exit 0
  fi
  i=$((i + 1))
  [ "$i" -ge "$tries" ] && exit 0
  sleep 0.5
done
`

// FindCrashReport returns the newest crash report for app written within the last age, by the
// guest's clock, waiting briefly for one being written. found is false when there is none.
// It records no step: it is part of the effect read that asked (ADR 0028).
func (m *Manager) FindCrashReport(ctx context.Context, runID, app string, age time.Duration) (report CrashReport, found bool, err error) {
	if strings.TrimSpace(app) == "" {
		return CrashReport{}, false, nil
	}
	mc, err := m.get(runID)
	if err != nil {
		return CrashReport{}, false, err
	}
	secs := strconv.Itoa(int(math.Ceil(age.Seconds())) + 1) // mtime has whole seconds
	tries := strconv.Itoa(int(crashReportWait / (500 * time.Millisecond)))
	res, err := m.tart.Exec(ctx, mc.Name, "/bin/sh", "-c", crashReportScript, "sh", app, secs, tries)
	if err != nil {
		return CrashReport{}, false, fmt.Errorf("look for a crash report: %w", err)
	}
	if res.ExitCode != 0 {
		return CrashReport{}, false, fmt.Errorf("look for a crash report: exit %d: %.200s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return parseCrashReport(res.Stdout)
}

// parseCrashReport reads crashReportScript's output: a path line, then an exception line.
func parseCrashReport(out string) (CrashReport, bool, error) {
	lines := strings.SplitN(strings.TrimSpace(out), "\n", 2)
	path := strings.TrimSpace(lines[0])
	if path == "" {
		return CrashReport{}, false, nil
	}
	r := CrashReport{Path: path}
	if len(lines) == 2 {
		r.Exception = strings.TrimSuffix(strings.TrimSpace(lines[1]), ",")
	}
	return r, true, nil
}
