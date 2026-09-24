package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
)

// errCheckFailed is check-image's verdict: the image showed something it must not. It is
// not a usage or infrastructure error, but it still exits 1.
var errCheckFailed = errors.New("the image failed the dialog check")

type checkOpts struct {
	image, out, tartBin string
}

func checkFlags() (*flag.FlagSet, *checkOpts) {
	o := &checkOpts{}
	fs := flag.NewFlagSet("check-image", flag.ContinueOnError)
	fs.StringVar(&o.image, "image", "", "local image to check (required); it is cloned, never booted itself")
	fs.StringVar(&o.out, "out", "", "directory for the screenshots and report (default: a new temp directory)")
	fs.StringVar(&o.tartBin, "tart", "", tartUsage)
	return fs, o
}

// checkImage is the dialog gate (ADR 0016) that scripts/build-image.sh runs last. It prints
// a report, keeps screenshots of both passes, and fails on any finding.
func checkImage(args []string) error {
	fs, o := checkFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.image == "" {
		return errors.New("-image is required")
	}
	if o.out == "" {
		dir, err := os.MkdirTemp("", "greenroom-check-"+o.image+"-")
		if err != nil {
			return err
		}
		o.out = dir
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	started := time.Now()
	res, err := machine.CheckImage(ctx, machine.ImageCheck{
		TartBin: tart.Resolve(o.tartBin).Bin,
		Image:   o.image,
		OutDir:  o.out,
		Log:     log,
	})
	if err != nil {
		return fmt.Errorf("check %s: %w", o.image, err)
	}
	report, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.out, "report.json"), report, 0o644); err != nil {
		return err
	}
	for _, p := range res.Passes {
		verdict := "clean"
		if len(p.Findings) > 0 {
			verdict = "FAILED"
		}
		fmt.Printf("%s: %s (screenshot %s)\n", p.Label, verdict, p.Screenshot)
		for _, f := range p.Findings {
			fmt.Printf("  - %s\n", f)
		}
	}
	fmt.Printf("dialog check of %s took %.0f s; report %s\n", o.image, time.Since(started).Seconds(), filepath.Join(o.out, "report.json"))
	if !res.Passed {
		return errCheckFailed
	}
	fmt.Printf("dialog check: %s passed\n", o.image)
	return nil
}
