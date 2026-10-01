package machine

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// tccGrantScript is machine_approve_control's guest half (ADR 0038, issue #252): the run-time
// twin of base.sh's build-time Apple Events enumeration, for an app whose bundle id does not
// exist until something is built or synced in the guest.
//
//go:embed guest/tccgrant.sh
var tccGrantScript string

// tccGrantTimeout bounds one grant: a handful of sqlite3 writes and a killall, well under a
// second measured live, with room for a slow guest.
const tccGrantTimeout = 30 * time.Second

// TCCGrant is what machine_approve_control reports: the app's resolved bundle id and
// executable, and the TCC services it now has.
type TCCGrant struct {
	BundleID   string   `json:"bundleId"`
	Executable string   `json:"executable"`
	Granted    []string `json:"granted"`
}

// ApproveControl grants an app under test the TCC rows base.sh already grants every
// pre-installed app (Apple Events from tart-guest-agent and sshd-keygen-wrapper, and from the
// app's own executable to System Events and to itself), plus the services a test machine
// commonly needs beyond Apple Events: Accessibility, screen capture (the plain TCC row;
// machine_approve_capture is the separate replayd bypass alert), the Desktop, Documents and
// Downloads folders, the camera and the microphone (ADR 0038, issue #252). Call it once the
// app is built and before the first thing that might control, script or otherwise touch it:
// measured live, a TCC.db write after a prompt is already on screen does not unblock the
// process waiting on it, only the next Apple Event is affected.
func (m *Manager) ApproveControl(ctx context.Context, runID, app string) (TCCGrant, int, error) {
	return m.approveControl(ctx, runID, "", app, scopeAny)
}

// ApproveControlInHome is ApproveControl for a seat that may approve only what its run built or
// synced (ADR 0044, issue #269): the verifier. The guest script refuses an .app bundle, or its
// executable, that resolves outside the guest home, where every sync, checkout and Xcode build
// lands; pre-installed apps already have their rows from the image. The step records the seat.
func (m *Manager) ApproveControlInHome(ctx context.Context, runID, by, app string) (TCCGrant, int, error) {
	return m.approveControl(ctx, runID, by, app, scopeHome)
}

// The scopes tccgrant.sh takes as its second argument.
const (
	scopeAny  = "any"
	scopeHome = "home"
)

func (m *Manager) approveControl(ctx context.Context, runID, by, app, scope string) (TCCGrant, int, error) {
	mc, err := m.get(runID)
	if err != nil {
		return TCCGrant{}, 0, err
	}
	if err := m.awaitReady(ctx, mc); err != nil {
		return TCCGrant{}, 0, err
	}
	started := time.Now()
	grant, err := m.approveControlApp(ctx, mc, app, scope)
	step := mc.rec.stepAs(by, "machine_approve_control", map[string]any{"app": app},
		map[string]any{"bundleId": grant.BundleID, "executable": grant.Executable, "granted": grant.Granted}, err, started)
	m.emitStep(mc.RunID, step)
	return grant, step, err
}

func (m *Manager) approveControlApp(ctx context.Context, mc *Machine, app, scope string) (TCCGrant, error) {
	if rest, tilde := homeRelative(app); tilde {
		app = rest
	}
	if !strings.HasSuffix(strings.TrimRight(app, "/"), ".app") {
		return TCCGrant{}, fmt.Errorf("app %q must be the path of an .app bundle in the guest", app)
	}
	ctx, cancel := context.WithTimeout(ctx, tccGrantTimeout)
	defer cancel()
	args := []string{"/bin/sh", "-c", tccGrantScript, "sh", app}
	if scope != scopeAny {
		args = append(args, scope)
	}
	res, err := execChecked(ctx, m.tart, mc.Name, args...)
	if err != nil {
		return TCCGrant{}, fmt.Errorf("grant TCC for %s: %w", app, err)
	}
	var g TCCGrant
	if uerr := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &g); uerr != nil {
		return TCCGrant{}, fmt.Errorf("grant TCC for %s: could not read the result: %w: %s", app, uerr, strings.TrimSpace(res.Stdout))
	}
	return g, nil
}
