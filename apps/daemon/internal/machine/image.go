package machine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// prepareTimeout fits a swiftc compile, the base profile, two small `swift test` runs for
// the toolchain manifest, and the key install and verification.
const prepareTimeout = 12 * time.Minute

// InputHelperVersion is the helper version PrepareGuest bakes into an image.
func InputHelperVersion() int { return inputHelperVersion }

// PrepareGuest turns a running VM into a base image candidate (issue #12, ADR 0018): it
// bakes in the input helper and the ssh key with the same idempotent scripts
// a machine's own boot and first control request run, the base profile
// (guest/base.sh) and the toolchain manifest (guest/toolchain.sh). The caller ends
// the build with DisableSoftwareUpdate, after any lean profile. It takes no Manager
// because the VM it prepares is never a run's machine.
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
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", installHelperScript()); err != nil {
		return fmt.Errorf("install the input helper in %s: %w", vmName, err)
	}

	log.Info("installing the ssh key", "vm", vmName)
	if _, err := execChecked(ctx, c, vmName, "sh", "-c", appendAuthorizedKeyScript(pubKey)); err != nil {
		return fmt.Errorf("install the ssh key in %s: %w", vmName, err)
	}

	log.Info("pre-approving screen capture", "vm", vmName)
	if err := approveScreenCapture(ctx, c, vmName); err != nil {
		return fmt.Errorf("in %s: %w", vmName, err)
	}

	log.Info("setting the desktop preferences", "vm", vmName)
	if err := applyDesktopPrefs(ctx, c, vmName); err != nil {
		return fmt.Errorf("in %s: %w", vmName, err)
	}

	log.Info("applying the base profile (Apple Events, Safari JavaScript, crash dialogs, apps at login)", "vm", vmName)
	if err := applyBaseProfile(ctx, c, vmName); err != nil {
		return fmt.Errorf("in %s: %w", vmName, err)
	}

	log.Info("measuring the toolchain", "vm", vmName, "path", ToolchainPath)
	toolchain, err := writeToolchainManifest(ctx, c, vmName)
	if err != nil {
		return fmt.Errorf("in %s: %w", vmName, err)
	}
	log.Info("toolchain", "vm", vmName, "manifest", toolchain)

	log.Info("verifying the input helper answers", "vm", vmName)
	screen, err := verifyHelper(ctx, c, vmName)
	if err != nil {
		return fmt.Errorf("verify the input helper in %s: %w", vmName, err)
	}

	// Load-bearing: `tart stop` does not flush the guest's dirty pages, so
	// without sync the baked helper is gone on the next boot.
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", "sync"); err != nil {
		return fmt.Errorf("sync %s's disk before it can be stopped safely: %w", vmName, err)
	}

	log.Info("guest prepared", "vm", vmName, "helperVersion", inputHelperVersion, "screen", fmt.Sprintf("%dx%d", screen.Width, screen.Height))
	return nil
}

// verifyHelper checks the installed helper answers --version and a screen read.
func verifyHelper(ctx context.Context, c *tart.Client, vmName string) (Screen, error) {
	if _, err := runHelper(ctx, c, vmName, "--version"); err != nil {
		return Screen{}, fmt.Errorf("--version: %w", err)
	}
	return readScreen(ctx, c, vmName)
}

// PreferredImages are the local images a bare `greenroom serve` uses, first found first, the same
// order scripts/install.sh picks for the launchd daemon: the lean image, then the prepared base.
var PreferredImages = []string{"greenroom-lean-a", "greenroom-base"}

// PreferredImage is the first of PreferredImages that tart has locally, else fallback. A tart that
// cannot list its VMs gets the fallback too.
func (m *Manager) PreferredImage(ctx context.Context, fallback string) string {
	vms, err := m.tart.List(ctx)
	if err != nil {
		return fallback
	}
	for _, want := range PreferredImages {
		for _, vm := range vms {
			if vm.Source == "local" && vm.Name == want {
				return want
			}
		}
	}
	return fallback
}
