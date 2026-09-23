package machine

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// helperCompileTimeout bounds the boot-time compile of a stale helper. It has
// its own budget, like ssh, so a slow compile cannot starve the other phases.
const helperCompileTimeout = 3 * time.Minute

// helperCheckScript prints "current" when this daemon's input helper answers
// --version in the guest, else "stale" and the helpers the image does have
// (none at all for an image that was never prepared). It only reads. The
// first line names it for the fake tart.
func helperCheckScript() string {
	return fmt.Sprintf(`: greenroom-helper-check
bin="$HOME/%s"
if [ -x "$bin" ] && "$bin" --version >/dev/null 2>&1; then echo current; exit 0; fi
echo stale $(ls "$HOME/.greenroom/bin" 2>/dev/null | grep '^greenroom-input-')
`, helperName())
}

// bootInputHelper is boot's input helper phase (issue #41). An image baked
// before inputHelperVersion changed makes the first UI call of every machine
// compile the helper with swiftc, 30 to 50 s inside one agent's tool call.
// Boot finds that, says so with the fix, and compiles it here, before ready,
// where waiting is expected. Nothing here is fatal: the first UI call
// compiles again if this failed.
func (m *Manager) bootInputHelper(boot context.Context, mc *Machine, timings map[string]any) {
	at := time.Now()
	defer func() { timings["inputHelperSeconds"] = round1(time.Since(at)) }()
	ctx, cancel := context.WithTimeout(boot, helperCompileTimeout)
	defer cancel()

	res, err := execChecked(ctx, m.tart, mc.Name, "/bin/sh", "-c", helperCheckScript())
	if err != nil {
		timings["inputHelperError"] = fmt.Sprintf("check the input helper: %v", err)
		m.Log.Warn("cannot check this machine's input helper", "runId", mc.RunID, "err", err)
		return
	}
	found, stale := strings.CutPrefix(strings.TrimSpace(res.Stdout), "stale")
	if !stale {
		return
	}
	found = strings.TrimSpace(found)
	if found == "" {
		found = "none"
	}
	timings["inputHelperStale"], timings["inputHelperFound"] = true, found
	m.Log.Warn("the image's input helper is older than this daemon's; compiling it during boot, which every new "+
		"machine from this image pays until the image is rebuilt: run scripts/build-image.sh -force "+
		"(or -lean -name greenroom-lean-a -force for the lean image)",
		"runId", mc.RunID, "image", mc.Image, "found", found, "need", helperName())
	if _, err := execChecked(ctx, m.tart, mc.Name, "/bin/sh", "-c", installHelperScript()); err != nil {
		timings["inputHelperError"] = fmt.Sprintf("install the input helper: %v", err)
		m.Log.Warn("cannot compile the input helper during boot; the first UI call will try again",
			"runId", mc.RunID, "err", err)
	}
}
