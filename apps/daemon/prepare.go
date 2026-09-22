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
)

// prepareImage bakes the input helper and the daemon's ssh key into a running VM (scripts/build-image.sh, issue #12),
// so its clones skip that work on first control. The VM belongs to no run, so no Manager is involved.
func prepareImage(args []string) error {
	fs := flag.NewFlagSet("prepare-image", flag.ContinueOnError)
	vm := fs.String("vm", "", "name of the running VM to prepare (required)")
	root := fs.String("root", defaultRoot(), "state directory holding the daemon's ssh key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *vm == "" {
		return errors.New("-vm is required")
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
