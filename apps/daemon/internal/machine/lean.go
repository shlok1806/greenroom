package machine

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// leanScript is the lean image profile (variant A of docs/image-experiment): the Dock
// shows only the core apps, the other apps' user-level agents are disabled, and
// widgets, notification banners, Siri, Spotlight indexing, media analysis, iCloud and
// Setup Assistant prompts, Software Update, Time Machine offers and Game Center are
// off. It writes preferences and launchd's disabled list only (Data volume); SIP, the
// authenticated root and the sealed system volume stay as they are. It reads every
// setting back and exits non-zero naming each check that failed.
//
//go:embed guest/lean.sh
var leanScript string

// ApplyLeanProfile bakes leanScript into a running VM that PrepareGuest has already
// prepared (`greenroom prepare-image -lean`). It runs as the logged-in user, because
// the Dock and the gui/<uid> launchd domain are that user's, and ends with sync for
// the same reason PrepareGuest does: `tart stop` does not flush the guest's pages.
func ApplyLeanProfile(ctx context.Context, tartBin, vmName string, log *slog.Logger) error {
	if strings.TrimSpace(tartBin) == "" {
		return errors.New("tartBin is required")
	}
	if strings.TrimSpace(vmName) == "" {
		return errors.New("vmName is required")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c := &tart.Client{Bin: tartBin}
	ctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()

	log.Info("applying the lean profile", "vm", vmName)
	res, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", leanScript)
	if err != nil {
		return fmt.Errorf("apply the lean profile in %s: %w", vmName, err)
	}
	if !strings.Contains(res.Stdout, "lean: ok") {
		return fmt.Errorf("apply the lean profile in %s: the script never confirmed its read-back (stdout %q)", vmName, strings.TrimSpace(res.Stdout))
	}
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", "sync"); err != nil {
		return fmt.Errorf("sync %s's disk before it can be stopped safely: %w", vmName, err)
	}
	log.Info("lean profile applied", "vm", vmName)
	return nil
}
