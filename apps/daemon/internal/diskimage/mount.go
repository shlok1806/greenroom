// Package diskimage reads a stopped VM's raw disk on the host, read-only, with macOS's hdiutil
// and diskutil (issue #159). It imports nothing of the daemon's: callers say which files they
// want from the mounted data volume.
package diskimage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// wholeDiskRE is a whole disk's device name, not a partition's (disk10, not disk10s2).
var wholeDiskRE = regexp.MustCompile(`^disk[0-9]+$`)

// Mounted is a disk's data volume mounted read-only at Root. Close unmounts it, detaches the
// disk and removes the clone.
type Mounted struct {
	Root  string
	whole string // the attached disk, /dev/diskN
	tmp   string
}

// MountReadOnly attaches disk (a raw disk image, as tart keeps disk.img) read-only without
// mounting anything, finds the APFS volume with the Data role (or named Data), and mounts only
// that, read-only and hidden from Finder. It attaches a clonefile copy when the volume allows
// one, so tart starting the VM meanwhile never shares the file; it never makes a full copy.
func MountReadOnly(ctx context.Context, disk string) (*Mounted, error) {
	tmp, err := os.MkdirTemp("", "greenroom-disk-")
	if err != nil {
		return nil, err
	}
	m := &Mounted{tmp: tmp}
	img := filepath.Join(tmp, "disk.img")
	if err := unix.Clonefile(disk, img, 0); err != nil {
		img = disk // another volume, or no clonefile: attach the original, still read-only
	}
	out, err := run(ctx, "hdiutil", "attach", "-readonly", "-nomount", "-noverify", "-noautoopen", "-nobrowse",
		"-imagekey", "diskimage-class=CRawDiskImage", "-plist", img)
	if err != nil {
		m.Close()
		return nil, err
	}
	var attached struct {
		Entities []struct {
			Dev string `json:"dev-entry"`
		} `json:"system-entities"`
	}
	if err := plistJSON(ctx, out, &attached); err != nil {
		m.Close()
		return nil, fmt.Errorf("hdiutil attach: %w", err)
	}
	var parts []string
	for _, e := range attached.Entities {
		dev := strings.TrimPrefix(e.Dev, "/dev/")
		if m.whole == "" && wholeDiskRE.MatchString(dev) {
			m.whole = e.Dev // the first entity is the disk itself; its partitions follow
		}
		parts = append(parts, dev)
	}
	if m.whole == "" {
		m.Close()
		return nil, errors.New("hdiutil attach listed no device")
	}
	vol, err := dataVolume(ctx, parts)
	if err != nil {
		m.Close()
		return nil, err
	}
	m.Root = filepath.Join(tmp, "data")
	if err := os.Mkdir(m.Root, 0o755); err != nil {
		m.Close()
		return nil, err
	}
	if _, err := run(ctx, "diskutil", "mount", "readOnly", "-mountOptions", "nobrowse", "-mountPoint", m.Root, vol); err != nil {
		m.Root = ""
		m.Close()
		return nil, err
	}
	return m, nil
}

// dataVolume is the device of the APFS volume whose container sits on one of parts: the one
// with the Data role, else the one named Data.
func dataVolume(ctx context.Context, parts []string) (string, error) {
	out, err := run(ctx, "diskutil", "apfs", "list", "-plist")
	if err != nil {
		return "", err
	}
	var list struct {
		Containers []struct {
			Reference string `json:"ContainerReference"`
			Stores    []struct {
				Dev string `json:"DeviceIdentifier"`
			} `json:"PhysicalStores"`
			Volumes []struct {
				Dev   string   `json:"DeviceIdentifier"`
				Name  string   `json:"Name"`
				Roles []string `json:"Roles"`
			} `json:"Volumes"`
		} `json:"Containers"`
	}
	if err := plistJSON(ctx, out, &list); err != nil {
		return "", fmt.Errorf("diskutil apfs list: %w", err)
	}
	byName := ""
	for _, c := range list.Containers {
		ours := slices.ContainsFunc(c.Stores, func(s struct {
			Dev string `json:"DeviceIdentifier"`
		}) bool {
			return slices.Contains(parts, s.Dev)
		})
		if !ours {
			continue
		}
		for _, v := range c.Volumes {
			if slices.Contains(v.Roles, "Data") {
				return v.Dev, nil
			}
			if v.Name == "Data" && byName == "" {
				byName = v.Dev
			}
		}
	}
	if byName != "" {
		return byName, nil
	}
	return "", errors.New("no APFS data volume on the disk")
}

// Close unmounts, detaches and removes what MountReadOnly made. It never fails loudly: a
// leftover is retried with force, and the temp directory goes last.
func (m *Mounted) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if m.Root != "" {
		if _, err := run(ctx, "diskutil", "unmount", m.Root); err != nil {
			_, _ = run(ctx, "diskutil", "unmount", "force", m.Root)
		}
	}
	if m.whole != "" {
		if _, err := run(ctx, "hdiutil", "detach", m.whole); err != nil {
			_, _ = run(ctx, "hdiutil", "detach", "-force", m.whole)
		}
	}
	_ = os.RemoveAll(m.tmp)
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// plistJSON decodes a property list through plutil, which every Mac has.
func plistJSON(ctx context.Context, plist []byte, v any) error {
	cmd := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	cmd.Stdin = bytes.NewReader(plist)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("plutil: %w", err)
	}
	return json.Unmarshal(out, v)
}
