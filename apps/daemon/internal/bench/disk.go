package bench

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Low disk (issue #155). The bench shares its host with CI's VM suite and the maintainer's own
// machines, and a clone that fills the disk mid-trial fails in ways that read like a wrong
// verdict. So no trial starts under Config.MinFreeDisk free on tart's volume: the runner waits
// up to Config.DiskWait for space, then stops as Ctrl-C does (nothing cut short, resumable),
// and a trial the disk killed is a setup error of cause disk, never a result.

// ErrLowDisk is what Run returns when it stopped for low disk. The trial that could not start
// is recorded as a setup error of cause disk, which a rerun with the same results file retries.
var ErrLowDisk = errors.New("stopped for low disk")

// CauseDisk is Result.Cause for a trial the disk kept from starting or killed.
const CauseDisk = "disk"

// DefaultMinFreeDisk is the free space under which no trial starts: a trial's clone grows by a
// few GB (sync, build, frames), and two run at once.
const DefaultMinFreeDisk = 5 << 30

// noSpaceRE matches what a command says when the disk is full.
var noSpaceRE = regexp.MustCompile(`(?i)no space left on device|ENOSPC|not enough (free )?(disk )?space|disk is full`)

// TartStorage is where tart keeps its VMs: $TART_HOME, else ~/.tart.
func TartStorage() string {
	if h := strings.TrimSpace(os.Getenv("TART_HOME")); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return filepath.Join(home, ".tart")
}

// FreeDiskAt probes the space this user may still write on path's volume, measuring its
// nearest existing ancestor when path does not exist yet.
func FreeDiskAt(path string) func() (uint64, string, error) {
	return func() (uint64, string, error) {
		p := path
		for {
			if _, err := os.Stat(p); err == nil || filepath.Dir(p) == p {
				break
			}
			p = filepath.Dir(p)
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			return 0, path, err
		}
		return st.Bavail * uint64(st.Bsize), path, nil
	}
}

// fmtGB prints bytes as "3.2 GB" (GiB, as df -h does).
func fmtGB(b uint64) string { return fmt.Sprintf("%.1f GB", float64(b)/(1<<30)) }
