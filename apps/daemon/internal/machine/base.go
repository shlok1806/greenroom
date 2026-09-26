package machine

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// baseScript is the base image profile (ADR 0018): Apple Events rows, Safari's JavaScript
// from Apple Events, crash dialogs off, and loginwindow's relaunch list cut to Finder. It
// reads every setting back and exits non-zero naming each failed check.
//
//go:embed guest/base.sh
var baseScript string

// toolchainScript measures the image's Swift toolchain and writes ToolchainPath (ADR 0019).
//
//go:embed guest/toolchain.sh
var toolchainScript string

// ToolchainPath is where an image reports its toolchain. The daemon reads whatever is there.
const ToolchainPath = "/usr/local/greenroom/toolchain.json"

// softwareUpdateLabels are the two system jobs that run softwareupdated on macOS 26: the
// classic one and MobileSoftwareUpdate's, which alone keeps the daemon (and its update
// badge) coming back after a reboot.
var softwareUpdateLabels = []string{"com.apple.softwareupdated", "com.apple.mobile.softwareupdated"}

// softwareUpdateScript disables softwareupdated for good: launchd's disabled list lives on
// the Data volume and survives reboots and clones. bootout stops the running one. It reads
// the disable back; CheckImage checks after a reboot that it is not running.
func softwareUpdateScript() string {
	var b strings.Builder
	b.WriteString(": greenroom-softwareupdate-off\nfail=\"\"\n")
	for _, l := range softwareUpdateLabels {
		fmt.Fprintf(&b, "sudo -n launchctl disable system/%[1]s\nsudo -n launchctl bootout system/%[1]s 2>/dev/null || true\n", l)
	}
	for _, l := range softwareUpdateLabels {
		fmt.Fprintf(&b, "launchctl print-disabled system | grep -qF '\"%[1]s\" => disabled' || fail=\"$fail %[1]s\"\n", l)
	}
	b.WriteString(`[ -z "$fail" ] || { echo "softwareupdate: not disabled:$fail" >&2; exit 1; }` + "\necho \"softwareupdate: off\"\n")
	return b.String()
}

// applyBaseProfile runs baseScript and fails unless it confirmed its read-back.
func applyBaseProfile(ctx context.Context, c *tart.Client, vmName string) error {
	res, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", baseScript)
	if err != nil {
		return fmt.Errorf("apply the base profile: %w", err)
	}
	if !strings.Contains(res.Stdout, "base: ok") {
		return fmt.Errorf("apply the base profile: the script never confirmed its read-back (stdout %q)", strings.TrimSpace(res.Stdout))
	}
	return nil
}

// writeToolchainManifest runs toolchainScript and checks the file it wrote reads back, with
// this recipe's version, and that an Xcode it found passes every probe (ADR 0026).
func writeToolchainManifest(ctx context.Context, c *tart.Client, vmName string) (map[string]any, error) {
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", toolchainScript, "sh", strconv.Itoa(imageRecipeVersion)); err != nil {
		return nil, fmt.Errorf("measure the toolchain: %w", err)
	}
	t, err := readToolchain(ctx, c, vmName)
	if err != nil {
		return nil, err
	}
	if known, _ := t["known"].(bool); !known {
		return nil, fmt.Errorf("measure the toolchain: %s does not read back: %v", ToolchainPath, t)
	}
	if r, ok := imageRecipeOf(t); !ok || r != imageRecipeVersion {
		return nil, fmt.Errorf("measure the toolchain: %s records image recipe %v, want %d", ToolchainPath, t["imageRecipe"], imageRecipeVersion)
	}
	if p := toolchainProblems(t); len(p) > 0 {
		return nil, fmt.Errorf("the image has Xcode at %v, but %s", t["xcodePath"], strings.Join(p, "; "))
	}
	return t, nil
}

// xcodeProbes are the manifest's checks that must all be true when it reports Xcode: an
// image never claims a toolchain it does not have (ADR 0026, point 4).
var xcodeProbes = []struct{ key, what string }{
	{"xcodeFirstLaunch", "xcodebuild -checkFirstLaunchStatus says first launch is not done"},
	{"xctest", "the XCTest probe failed"},
	{"swiftTesting", "the swift-testing probe failed"},
	{"xcodebuild", "the xcodebuild probe failed"},
}

// toolchainProblems names each Xcode probe a manifest with xcode true did not pass, with the
// probe's own error where it wrote one. A manifest without Xcode has none.
func toolchainProblems(t map[string]any) []string {
	if x, _ := t["xcode"].(bool); !x {
		return nil
	}
	var out []string
	for _, p := range xcodeProbes {
		if ok, _ := t[p.key].(bool); ok {
			continue
		}
		msg := p.what
		if e, _ := t[p.key+"Error"].(string); e != "" {
			msg += ": " + e
		}
		out = append(out, msg)
	}
	return out
}

// imageRecipeOf is the recipe version a manifest records, false when it records none (an
// image built before recipe versions, or one this daemon did not build).
func imageRecipeOf(t map[string]any) (int, bool) {
	f, ok := t["imageRecipe"].(float64)
	if !ok || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

// staleRecipe reports the recipe an image was built with when it is not this daemon's. Only
// a manifest that is known is judged: an image with none is not one this recipe made.
func staleRecipe(t map[string]any) (found int, stale bool) {
	if known, _ := t["known"].(bool); !known {
		return 0, false
	}
	found, _ = imageRecipeOf(t)
	return found, found != imageRecipeVersion
}

// unknownToolchain is what an image without a manifest reports.
func unknownToolchain() map[string]any { return map[string]any{"known": false} }

// readToolchain returns the image's toolchain manifest as the image wrote it, or
// {"known":false} when there is none. Only a JSON object passes; anything else is an error.
func readToolchain(ctx context.Context, c *tart.Client, vmName string) (map[string]any, error) {
	res, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", ": greenroom-toolchain-read\ncat "+ToolchainPath+" 2>/dev/null || true")
	if err != nil {
		return unknownToolchain(), fmt.Errorf("read %s: %w", ToolchainPath, err)
	}
	raw := strings.TrimSpace(res.Stdout)
	if raw == "" {
		return unknownToolchain(), nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
		return unknownToolchain(), fmt.Errorf("read %s: not a JSON object: %q", ToolchainPath, raw)
	}
	return obj, nil
}

// DisableSoftwareUpdate is the last step of every image build (ADR 0018): lean.sh still
// talks to softwareupdated, so it runs after the lean profile. It ends with sync.
func DisableSoftwareUpdate(ctx context.Context, tartBin, vmName string, log *slog.Logger) error {
	if strings.TrimSpace(tartBin) == "" || strings.TrimSpace(vmName) == "" {
		return errors.New("tartBin and vmName are required")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c := &tart.Client{Bin: tartBin}
	log.Info("disabling Software Update", "vm", vmName, "labels", softwareUpdateLabels)
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", softwareUpdateScript()); err != nil {
		return fmt.Errorf("disable Software Update in %s: %w", vmName, err)
	}
	if _, err := execChecked(ctx, c, vmName, "/bin/sh", "-c", "sync"); err != nil {
		return fmt.Errorf("sync %s's disk before it can be stopped safely: %w", vmName, err)
	}
	return nil
}
