package machine

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// diskScript waits for the guest's APFS container to fill the disk build-image.sh grew,
// resizes it if the Cirrus boot-time resize never happened, and reads it back (ADR 0026).
//
//go:embed guest/disk.sh
var diskScript string

// xcodeScript selects the copied Xcode, accepts its license, runs its first launch, turns
// developer mode on and reads each back, with the copy's signature (ADR 0026).
//
//go:embed guest/xcode.sh
var xcodeScript string

// GuestXcodePath is where every image carries Xcode, whatever the host's copy is called.
const GuestXcodePath = "/Applications/Xcode.app"

// xcodeCopyTimeout bounds the copy: about 90 s for Xcode 27's 10 GB (4 GB compressed) on an
// M-series host, measured 2026-09-25.
const xcodeCopyTimeout = 15 * time.Minute

// XcodeInstall configures InstallXcode.
type XcodeInstall struct {
	TartBin string
	VM      string // the running build VM
	App     string // the host's Xcode.app
	SSHKey  string // private key path; its public half is PubKey
	PubKey  string
	Log     *slog.Logger
}

// InstallXcode puts the host's Xcode in a running build VM (ADR 0026): it waits for the
// grown disk, copies App to GuestXcodePath over ssh, and runs guest/xcode.sh. PrepareGuest
// runs after it, so the toolchain manifest measures the Xcode it installed. It ends with sync.
//
// The copy is `ditto -c` on the host piped over ssh into `ditto -x --hfsCompression` in the
// guest, not rsync as ADR 0026 says: Xcode's files are APFS-compressed, and rsync (and a
// plain ditto) write them expanded, 10.2 GB instead of 4.0 GB for Xcode 27, which the host's
// sparse disk image pays for.
func InstallXcode(ctx context.Context, o XcodeInstall) error {
	if strings.TrimSpace(o.TartBin) == "" || strings.TrimSpace(o.VM) == "" {
		return errors.New("TartBin and VM are required")
	}
	if strings.TrimSpace(o.SSHKey) == "" || strings.TrimSpace(o.PubKey) == "" {
		return errors.New("SSHKey and PubKey are required")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	app, err := HostXcode(o.App)
	if err != nil {
		return err
	}
	c := &tart.Client{Bin: o.TartBin}

	o.Log.Info("waiting for the grown disk", "vm", o.VM)
	res, err := execChecked(ctx, c, o.VM, "/bin/sh", "-c", diskScript)
	if err != nil {
		return fmt.Errorf("grow %s's disk: %w", o.VM, err)
	}
	o.Log.Info(strings.TrimSpace(res.Stdout), "vm", o.VM)

	if _, err := execChecked(ctx, c, o.VM, "sh", "-c", appendAuthorizedKeyScript(o.PubKey)); err != nil {
		return fmt.Errorf("install the ssh key in %s: %w", o.VM, err)
	}
	ip, err := c.IP(ctx, o.VM)
	if err != nil {
		return fmt.Errorf("find %s's IP: %w", o.VM, err)
	}

	o.Log.Info("copying Xcode", "vm", o.VM, "from", app.Path, "version", app.Version, "to", GuestXcodePath)
	at := time.Now()
	if err := copyXcode(ctx, app.Path, o.SSHKey, ip); err != nil {
		return fmt.Errorf("copy %s into %s: %w", app.Path, o.VM, err)
	}
	o.Log.Info("Xcode copied", "vm", o.VM, "seconds", round1(time.Since(at)))

	o.Log.Info("setting up Xcode (xcode-select, license, first launch, developer mode)", "vm", o.VM)
	res, err = execChecked(ctx, c, o.VM, "/bin/sh", "-c", xcodeScript, "sh", GuestXcodePath, app.MinimumOS)
	if err != nil {
		return fmt.Errorf("set up Xcode in %s: %w", o.VM, err)
	}
	if !strings.Contains(res.Stdout, "xcode: ok") {
		return fmt.Errorf("set up Xcode in %s: the script never confirmed its read-back (stdout %q)", o.VM, strings.TrimSpace(res.Stdout))
	}
	o.Log.Info(strings.TrimSpace(res.Stdout), "vm", o.VM)
	if _, err := execChecked(ctx, c, o.VM, "/bin/sh", "-c", "sync"); err != nil {
		return fmt.Errorf("sync %s's disk before it can be stopped safely: %w", o.VM, err)
	}
	return nil
}

// HostApp is the host's Xcode as InstallXcode copies it.
type HostApp struct {
	Path      string // the .app
	Version   string // CFBundleShortVersionString
	MinimumOS string // LSMinimumSystemVersion
}

// HostXcode resolves the Xcode to copy: app when set, else the app that `xcode-select -p`
// points into. It fails by name when there is none, since every image carries Xcode.
func HostXcode(app string) (HostApp, error) {
	if strings.TrimSpace(app) == "" {
		out, err := exec.Command("xcode-select", "-p").Output()
		dev := strings.TrimSpace(string(out))
		if err != nil || !strings.HasSuffix(dev, ".app/Contents/Developer") {
			return HostApp{}, fmt.Errorf("this host has no Xcode selected (xcode-select -p: %q), and every image carries "+
				"the host's Xcode (ADR 0026): install Xcode, select it with sudo xcode-select -s /Applications/Xcode.app, "+
				"or pass -xcode /path/to/Xcode.app", dev)
		}
		app = strings.TrimSuffix(dev, "/Contents/Developer")
	}
	return readHostApp(app)
}

// readHostApp checks app is an Xcode and reads its version and minimum macOS.
func readHostApp(app string) (HostApp, error) {
	app = filepath.Clean(app)
	info := filepath.Join(app, "Contents", "Info.plist")
	if _, err := os.Stat(filepath.Join(app, "Contents", "Developer", "usr", "bin", "xcodebuild")); err != nil {
		return HostApp{}, fmt.Errorf("%s is not an Xcode: it has no Contents/Developer/usr/bin/xcodebuild", app)
	}
	read := func(key string) string {
		out, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", info).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	h := HostApp{Path: app, Version: read("CFBundleShortVersionString"), MinimumOS: read("LSMinimumSystemVersion")}
	if h.Version == "" {
		return HostApp{}, fmt.Errorf("%s is not an Xcode: cannot read CFBundleShortVersionString from %s", app, info)
	}
	return h, nil
}

// copyXcode streams app into the guest's GuestXcodePath: `ditto -c` on the host, over ssh,
// `ditto -x --hfsCompression` in the guest as root, so the files stay compressed and keep
// Xcode's root:wheel ownership. An earlier copy is removed first.
func copyXcode(ctx context.Context, app, key, ip string) error {
	ctx, cancel := context.WithTimeout(ctx, xcodeCopyTimeout)
	defer cancel()
	pack := exec.CommandContext(ctx, "ditto", "-c", app, "-")
	unpack := exec.CommandContext(ctx, "ssh", sshArgs(key, ip,
		"sudo -n rm -rf "+GuestXcodePath+" && sudo -n ditto -x --hfsCompression - "+GuestXcodePath)...)
	pipe, err := pack.StdoutPipe()
	if err != nil {
		return err
	}
	unpack.Stdin = pipe
	var packErr, unpackOut bytes.Buffer
	pack.Stderr = &packErr
	unpack.Stdout, unpack.Stderr = &unpackOut, &unpackOut
	if err := pack.Start(); err != nil {
		return fmt.Errorf("ditto -c: %w", err)
	}
	uerr := unpack.Run()
	perr := pack.Wait()
	if perr != nil {
		return fmt.Errorf("ditto -c %s: %w: %s", app, perr, strings.TrimSpace(packErr.String()))
	}
	if uerr != nil {
		return fmt.Errorf("ditto -x in the guest: %w: %s", uerr, strings.TrimSpace(unpackOut.String()))
	}
	return nil
}

// sshArgs are ssh's arguments to run command in the guest as admin, as rsyncShell's are.
func sshArgs(key, ip, command string) []string {
	return []string{"-i", key, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "BatchMode=yes", guestUser + "@" + ip, command}
}
