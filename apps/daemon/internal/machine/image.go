// image.go builds the greenroom base image (issue #12, the open M1 task in
// docs/01-plan.md): a Tart VM prepared so that a clone of it never pays the
// costs docs/02-spike.md and ADR 0009 measured on a fresh Cirrus clone -- a
// swiftc compile on the first control request, and an ssh key install on
// first boot.
package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// prepareTimeout bounds one PrepareGuest call. It has to fit a swiftc
// compile (installInputHelper gives that 3 minutes on its own) plus the ssh
// key install and the verification round trips, all against a VM that a
// build script, not a person, is waiting on.
const prepareTimeout = 4 * time.Minute

// InputHelperVersion is inputHelperVersion, the version PrepareGuest bakes
// into a VM and the contract scripts/build-image.sh and the daemon's own
// image documentation name. It is exported only so a CLI (prepare.go) or a
// script can report which version an image build asked for without
// duplicating the constant.
func InputHelperVersion() int { return inputHelperVersion }

// PrepareGuest turns a RUNNING VM into a greenroom base image candidate: it
// bakes in the compiled input helper (ADR 0009, issue #12) at the exact
// path and version installInputHelper would install at first use, and it
// appends pubKey to the guest's authorized_keys so a clone needs no key
// install at boot either. Both steps use the same idempotent scripts a
// machine's own boot and first control request use, so running it against a
// guest that already has either is a no-op for that step.
//
// It takes no *Manager on purpose: scripts/build-image.sh calls it, through
// the prepare-image CLI (prepare.go), for a VM that no manager owns and
// that will never be a run's machine.
func PrepareGuest(ctx context.Context, tartBin, vmName, pubKey string, log *slog.Logger) error {
	if strings.TrimSpace(tartBin) == "" {
		return errors.New("tartBin is required")
	}
	if strings.TrimSpace(vmName) == "" {
		return errors.New("vmName is required")
	}
	if strings.TrimSpace(pubKey) == "" {
		return errors.New("pubKey is required")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c := &tart.Client{Bin: tartBin}
	ctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()

	log.Info("installing the input helper", "vm", vmName, "path", helperName(), "version", inputHelperVersion)
	res, err := c.Exec(ctx, vmName, "/bin/sh", "-c", installHelperScript())
	if err != nil {
		return fmt.Errorf("install the input helper in %s: %w", vmName, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("install the input helper in %s: exit %d: %s", vmName, res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	log.Info("installing the ssh key", "vm", vmName)
	res, err = c.Exec(ctx, vmName, "sh", "-c", appendAuthorizedKeyScript(pubKey))
	if err != nil {
		return fmt.Errorf("install the ssh key in %s: %w", vmName, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("install the ssh key in %s: exit %d: %s", vmName, res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	log.Info("verifying the input helper answers", "vm", vmName)
	screen, err := verifyHelper(ctx, c, vmName)
	if err != nil {
		return fmt.Errorf("verify the input helper in %s: %w", vmName, err)
	}

	// A caller that stops the VM right after PrepareGuest returns (as
	// scripts/build-image.sh does) is trusting that what was just written
	// survives the stop. It does not by default: tart stop's graceful path
	// tears down the guest without giving its own filesystem a chance to
	// flush dirty pages, so a compiled helper that was never fsynced is gone
	// on the next boot even though verifyHelper just proved it worked while
	// running. `sync` in the guest is what makes PrepareGuest's promise (a
	// clone of this VM already has the helper) actually true; without it the
	// whole point of issue #12 silently does not hold, and the first sign is
	// a base image that still pays the compile cost it was built to avoid.
	if res, err := c.Exec(ctx, vmName, "/bin/sh", "-c", "sync"); err != nil {
		return fmt.Errorf("sync %s's disk before it can be stopped safely: %w", vmName, err)
	} else if res.ExitCode != 0 {
		return fmt.Errorf("sync %s's disk before it can be stopped safely: exit %d: %s", vmName, res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	log.Info("guest prepared", "vm", vmName, "helperVersion", inputHelperVersion, "screen", fmt.Sprintf("%dx%d", screen.Width, screen.Height))
	return nil
}

// appendAuthorizedKeyScript is the same idempotent shell installSSHKey runs
// on every boot: create ~/.ssh if it is missing, and append pubKey only if
// it is not already the last line of authorized_keys. It is spelled out
// again here, rather than shared, because installSSHKey is a *Manager
// method and PrepareGuest must not need one; the two are kept in exact sync
// by both delegating to shellQuote and doing nothing else to the key.
func appendAuthorizedKeyScript(pubKey string) string {
	return fmt.Sprintf(
		"mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && (grep -qF %s ~/.ssh/authorized_keys || echo %s >> ~/.ssh/authorized_keys)",
		shellQuote(pubKey), shellQuote(pubKey))
}

// verifyHelper asks the freshly-installed helper for its version and then
// for a screen reading, the same request ScreenOf makes on a machine's
// first control call. A base image that cannot answer both is not done:
// the whole point of baking the helper in is that the first real control
// request never has to find this out.
func verifyHelper(ctx context.Context, c *tart.Client, vmName string) (Screen, error) {
	res, err := c.Exec(ctx, vmName, "/bin/sh", "-c", fmt.Sprintf(`exec "$HOME/%s" --version`, helperName()))
	if err != nil {
		return Screen{}, fmt.Errorf("--version: %w", err)
	}
	if res.ExitCode != 0 {
		return Screen{}, fmt.Errorf("--version: exit %d: %s", res.ExitCode, helperError(res.Stderr))
	}

	res, err = c.Exec(ctx, vmName, "/bin/sh", "-c",
		fmt.Sprintf(`exec "$HOME/%s" --json-base64 %s`, helperName(),
			base64.StdEncoding.EncodeToString([]byte(`{"actions":[]}`))))
	if err != nil {
		return Screen{}, fmt.Errorf("read the screen size: %w", err)
	}
	if res.ExitCode != 0 {
		return Screen{}, fmt.Errorf("read the screen size: exit %d: %s", res.ExitCode, helperError(res.Stderr))
	}
	var out struct {
		Screen Screen `json:"screen"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &out); err != nil {
		return Screen{}, fmt.Errorf("read the screen size: %w: %s", err, strings.TrimSpace(res.Stdout))
	}
	if out.Screen.Width <= 0 || out.Screen.Height <= 0 {
		return Screen{}, fmt.Errorf("the machine reports a %dx%d screen", out.Screen.Width, out.Screen.Height)
	}
	return out.Screen, nil
}
