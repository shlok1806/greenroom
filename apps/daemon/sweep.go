package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

type sweepOpts struct {
	root, tartBin string
	olderThan     time.Duration
	delete        bool
}

func sweepFlags() (*flag.FlagSet, *sweepOpts) {
	o := &sweepOpts{}
	fs := flag.NewFlagSet("sweep-orphans", flag.ContinueOnError)
	fs.StringVar(&o.root, "root", defaultRoot(), "the daemon's state directory, whose runs/ (and <root>/*/runs/, the bench's) own run clones")
	fs.DurationVar(&o.olderThan, "older-than", machine.DefaultOrphanAge, "only clones older than this")
	fs.BoolVar(&o.delete, "delete", false, "delete them; without it, only list what serve's start-up sweep would delete")
	fs.StringVar(&o.tartBin, "tart", "", tartUsage)
	return fs, o
}

// sweepOrphans lists, or with -delete deletes, the orphaned run clones serve sweeps at start
// (issue #103). It never loads the daemon's state: a live run's clone is running, or has its
// run directory, so it is never an orphan.
func sweepOrphans(args []string) error {
	fs, o := sweepFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	roots := []string{o.root}
	if o.root != defaultRoot() {
		roots = append(roots, defaultRoot())
	}
	swept, err := machine.SweepOrphans(ctx, tart.NewAt(o.tartBin), roots, func(string) bool { return false },
		o.olderThan, !o.delete, log)
	if err != nil {
		return err
	}
	verb := "would delete"
	if o.delete {
		verb = "deleted"
	}
	for _, s := range swept {
		if s.Error != "" {
			fmt.Printf("%s: could not delete: %s\n", s.Name, s.Error)
			continue
		}
		fmt.Printf("%s: %s (stopped, no run directory, %s old)\n", s.Name, verb, s.Age)
	}
	if len(swept) == 0 {
		fmt.Printf("no orphaned run clones older than %s\n", o.olderThan)
	}
	return nil
}
