package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/diskimage"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

type imageStatusOpts struct {
	images, tartBin string
	rebuildArgs     bool
}

func imageStatusFlags() (*flag.FlagSet, *imageStatusOpts) {
	o := &imageStatusOpts{}
	fs := flag.NewFlagSet("image-status", flag.ContinueOnError)
	fs.StringVar(&o.images, "image", strings.Join(machine.PreferredImages, ","), "comma-separated local images to check")
	fs.StringVar(&o.tartBin, "tart", "", tartUsage)
	fs.BoolVar(&o.rebuildArgs, "rebuild-args", false, "print only scripts/build-image.sh's arguments for each stale image, one line each (install.sh -rebuild reads them)")
	return fs, o
}

// mountImage reads a stopped image's disk; tests replace it.
var mountImage = func(ctx context.Context, disk string) (root string, done func(), err error) {
	m, err := diskimage.MountReadOnly(ctx, disk)
	if err != nil {
		return "", nil, err
	}
	return m.Root, m.Close, nil
}

// imageStatus checks each local default image's input helper and image recipe against this
// daemon's (issue #159), reading its disk on the host without booting it, and prints what is
// stale with the command that rebuilds it. It exits 0 either way: it reports, install.sh acts.
func imageStatus(args []string) error {
	fs, o := imageStatusFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var names []string
	for _, n := range strings.Split(o.images, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	vms, err := tart.NewAt(o.tartBin).List(ctx)
	if err != nil {
		return fmt.Errorf("list tart's VMs: %w", err)
	}
	statuses := checkImages(ctx, names, vms, tartHome())
	if o.rebuildArgs {
		for _, st := range statuses {
			if st.State == machine.ImageStale {
				fmt.Println(st.Rebuild)
			}
		}
		return nil
	}
	printImageStatuses(os.Stdout, statuses)
	return nil
}

// tartHome is where tart keeps its VMs: $TART_HOME, else ~/.tart.
func tartHome() string {
	if h := strings.TrimSpace(os.Getenv("TART_HOME")); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tart")
}

func checkImages(ctx context.Context, names []string, vms []tart.VM, home string) []machine.ImageStatus {
	var out []machine.ImageStatus
	for _, name := range names {
		st := machine.ImageStatus{Image: name, State: machine.ImageAbsent}
		for _, vm := range vms {
			if vm.Source != "local" || vm.Name != name {
				continue
			}
			if vm.State == "running" {
				st.State = machine.ImageRunning
				break
			}
			st = readImage(ctx, name, filepath.Join(home, "vms", name, "disk.img"))
		}
		out = append(out, st)
	}
	return out
}

func readImage(ctx context.Context, name, disk string) machine.ImageStatus {
	unknown := func(err error) machine.ImageStatus {
		return machine.ImageStatus{Image: name, State: machine.ImageUnknown, Reasons: []string{err.Error()}}
	}
	if _, err := os.Stat(disk); err != nil {
		return unknown(err)
	}
	root, done, err := mountImage(ctx, disk)
	if err != nil {
		return unknown(err)
	}
	defer done()
	helpers, manifest, err := machine.ReadImageFacts(root)
	if err != nil {
		return unknown(err)
	}
	return machine.JudgeImage(name, helpers, manifest)
}

func printImageStatuses(w io.Writer, statuses []machine.ImageStatus) {
	for _, st := range statuses {
		switch st.State {
		case machine.ImageCurrent:
			_, _ = fmt.Fprintf(w, "%s: current (input helper %d, image recipe %d)\n", st.Image, machine.InputHelperVersion(), st.Recipe)
		case machine.ImageStale:
			_, _ = fmt.Fprintf(w, "%s: stale: %s.\n  Rebuild it (needs about 20 GB free): scripts/build-image.sh %s\n  or run scripts/install.sh -rebuild\n",
				st.Image, strings.Join(st.Reasons, "; "), st.Rebuild)
		case machine.ImageRunning:
			_, _ = fmt.Fprintf(w, "%s: running, so not checked; the daemon warns when a machine from it boots stale\n", st.Image)
		case machine.ImageAbsent:
			_, _ = fmt.Fprintf(w, "%s: not on this host\n", st.Image)
		default:
			_, _ = fmt.Fprintf(w, "%s: could not read its disk (%s); the daemon warns when a machine from it boots stale\n",
				st.Image, strings.Join(st.Reasons, "; "))
		}
	}
}
