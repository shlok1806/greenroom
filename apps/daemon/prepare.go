package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

type prepareOpts struct {
	vm, root, tartBin string
	lean              bool
}

func prepareFlags() (*flag.FlagSet, *prepareOpts) {
	o := &prepareOpts{}
	fs := flag.NewFlagSet("prepare-image", flag.ContinueOnError)
	fs.StringVar(&o.vm, "vm", "", "name of the running VM to prepare (required)")
	fs.StringVar(&o.root, "root", defaultRoot(), "state directory holding the daemon's ssh key")
	fs.StringVar(&o.tartBin, "tart", "", tartUsage)
	fs.BoolVar(&o.lean, "lean", false, "also apply the lean profile: core apps only in the Dock, other apps' agents, widgets, banners, Siri, indexing, update and setup prompts off (docs/image-experiment)")
	return fs, o
}

// prepareImage makes a running VM an image (scripts/build-image.sh, issue #12, ADR 0018): the input helper,
// the daemon's ssh key, the base profile and toolchain manifest, the lean profile with -lean, and Software
// Update off last. The VM belongs to no run, so no Manager is involved.
func prepareImage(args []string) error {
	fs, o := prepareFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.vm == "" {
		return errors.New("-vm is required")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	_, pubKey, err := machine.EnsureSSHKey(o.root)
	if err != nil {
		return fmt.Errorf("load the daemon's ssh key from %s: %w", o.root, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	tartBin := tart.Resolve(o.tartBin).Bin
	if err := machine.PrepareGuest(ctx, tartBin, o.vm, pubKey, log); err != nil {
		return err
	}
	if o.lean {
		if err := machine.ApplyLeanProfile(ctx, tartBin, o.vm, log); err != nil {
			return err
		}
	}
	// Last, after lean.sh, which still talks to softwareupdated (ADR 0018).
	if err := machine.DisableSoftwareUpdate(ctx, tartBin, o.vm, log); err != nil {
		return err
	}
	profile := ""
	if o.lean {
		profile = " and the lean profile"
	}
	fmt.Printf("greenroom: %s is prepared with input helper version %d%s\n", o.vm, machine.InputHelperVersion(), profile)
	return nil
}
