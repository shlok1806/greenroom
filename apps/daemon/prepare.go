package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
)

// prepareImage runs the prepare-image CLI: bake the compiled input helper
// and the daemon's ssh key into a VM that a build script (scripts/build-image.sh,
// issue #12) started, so that every clone of it skips the ~27 s the guest
// would otherwise pay on its first control request. It takes no *machine.Manager,
// on purpose: the VM it prepares belongs to no run and no manager, only to the
// build.
func prepareImage(args []string) error {
	fs := flag.NewFlagSet("prepare-image", flag.ContinueOnError)
	vm := fs.String("vm", "", "name of the running VM to prepare (required)")
	root := fs.String("root", defaultRoot(), "state directory holding the daemon's ssh key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *vm == "" {
		return fmt.Errorf("-vm is required")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	_, pubKey, err := machine.EnsureSSHKey(*root)
	if err != nil {
		return fmt.Errorf("load the daemon's ssh key from %s: %w", *root, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := machine.PrepareGuest(ctx, "tart", *vm, pubKey, log); err != nil {
		return err
	}
	fmt.Printf("greenroom: %s is prepared with input helper version %d\n", *vm, machine.InputHelperVersion())
	return nil
}
