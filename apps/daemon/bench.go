package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/bench"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/verifier"
)

// benchCmd is `greenroom bench run|score` (ADR 0025).
func benchCmd(args []string) error {
	if len(args) == 0 {
		benchUsage()
		return errors.New("bench needs run or score")
	}
	switch args[0] {
	case "run":
		return benchRun(args[1:])
	case "score":
		return benchScore(args[1:])
	}
	benchUsage()
	return fmt.Errorf("unknown bench command %q: want run or score", args[0])
}

func benchUsage() {
	fmt.Fprintln(os.Stderr, "\n       greenroom bench run [flags]      (ADR 0025; needs NVIDIA_API_KEY)")
	run, _ := benchRunFlags()
	run.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\n       greenroom bench score [flags] <results.jsonl>")
	score, _ := benchScoreFlags()
	score.PrintDefaults()
}

type benchRunOpts struct {
	benchDir, cases, split, kind, out, root, image, envFile, tartBin string
	trials, parallel, verifierMaxSteps                               int
	verifierBudget, turnTimeout                                      time.Duration
}

func benchRunFlags() (*flag.FlagSet, *benchRunOpts) {
	o := &benchRunOpts{}
	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	fs.StringVar(&o.benchDir, "bench", "", "the bench directory (cases/, apps/); default the nearest bench/ above the working directory")
	fs.StringVar(&o.cases, "case", "", "comma-separated case ids; default every case")
	fs.StringVar(&o.split, "split", "", "only this split: dev or holdout (holdout is never used to tune prompts)")
	fs.StringVar(&o.kind, "kind", "", "only this kind: correct, mutant, lying, infra or ambiguous")
	fs.IntVar(&o.trials, "trials", 3, "trials per case")
	fs.StringVar(&o.out, "out", "", "results file (JSON lines), appended to; case and trial pairs already in it are skipped, so pass the same file to resume; default <root>/results/<time>.jsonl")
	fs.StringVar(&o.root, "root", filepath.Join(defaultRoot(), "bench"), "state directory for the bench's machines and run directories; never the daemon's own root")
	fs.StringVar(&o.image, "image", "", "image to clone; default GREENROOM_IMAGE, then local greenroom-lean-a, then greenroom-base, as serve chooses")
	fs.IntVar(&o.parallel, "parallel", 2, "machines at once; the host allows 2 macOS guests in all, the daemon's included")
	fs.StringVar(&o.envFile, "env-file", ".env", "file of KEY=VALUE lines holding the model credentials")
	fs.StringVar(&o.tartBin, "tart", "", tartUsage)
	fs.IntVar(&o.verifierMaxSteps, "verifier-max-steps", verifier.DefaultMaxSteps, "tool calls a verifier turn may make")
	fs.DurationVar(&o.verifierBudget, "verifier-budget", verifier.DefaultBudget, "wall-clock budget for one verifier turn")
	fs.DurationVar(&o.turnTimeout, "turn-timeout", 30*time.Minute, "how long to wait for the verifier's turn to end, from the task, before recording a timeout")
	return fs, o
}

func benchRun(args []string) error {
	fs, o := benchRunFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("bench run takes no arguments, got %q", fs.Args())
	}
	dir, err := findBenchDir(o.benchDir)
	if err != nil {
		return err
	}
	all, err := bench.LoadCases(dir)
	if err != nil {
		return err
	}
	var ids []string
	for _, id := range strings.Split(o.cases, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	cases := bench.Filter(all, ids, o.split, o.kind)
	for _, id := range ids {
		if len(bench.Filter(all, []string{id}, "", "")) == 0 {
			return fmt.Errorf("no case %q in %s", id, filepath.Join(dir, "cases"))
		}
	}
	if len(cases) == 0 {
		return errors.New("no case matches -case, -split and -kind")
	}

	if err := loadEnvFile(o.envFile); err != nil {
		return err
	}
	if os.Getenv("NVIDIA_API_KEY") == "" {
		return fmt.Errorf("bench run needs the verifier's model: set NVIDIA_API_KEY and GREENROOM_VERIFIER_MODEL in the environment or %s", o.envFile)
	}
	if err := os.MkdirAll(o.root, 0o755); err != nil {
		return err
	}
	release, err := lockRoot(o.root)
	if err != nil {
		return err
	}
	defer release()
	if o.out == "" {
		o.out = filepath.Join(o.root, "results", time.Now().Format("20060102-150405")+".jsonl")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mgr, err := machine.NewManager(o.root, log, machine.WithMaxMachines(2), machine.WithTartBin(o.tartBin))
	if err != nil {
		return err
	}
	mgr.CheckTart(context.Background())
	if o.image == "" {
		o.image = strings.TrimSpace(os.Getenv("GREENROOM_IMAGE"))
	}
	if o.image == "" {
		o.image = mgr.PreferredImage(context.Background(), defaultImage)
	}
	v, err := nimVerifier(mgr, o.verifierMaxSteps, o.verifierBudget, log)
	if err != nil {
		return err
	}
	reg := session.NewRegistry(o.root, session.DefaultMaxDisputes, session.WithOnVerdict(func(runID string, st session.VerdictState) { _ = mgr.RecordVerdict(runID, st) }))
	mgr.SetMessageActivity(reg.LastMessageAt)
	bridgeLifecycle(mgr, reg, true) // as serve does: ready, failed, stopped and destroyed reach the transcript

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner := bench.New(bench.Config{
		BenchDir: dir, Cases: cases, Trials: o.trials, Out: o.out, Image: o.image, Model: v.Model(),
		Parallel: o.parallel, TurnTimeout: o.turnTimeout, Log: log, Progress: os.Stderr,
	}, mgr, reg, v)
	fmt.Fprintf(os.Stderr, "bench: %d cases x %d trials, image %s, model %s, results %s\n", len(cases), o.trials, o.image, v.Model(), o.out)
	sum, err := runner.Run(ctx)
	fmt.Fprintf(os.Stderr, "bench: ran %d (%d right, %d setup errors), skipped %d already in %s\n", sum.Ran, sum.Right, sum.SetupErrors, sum.Skipped, o.out)
	fmt.Fprintf(os.Stderr, "bench: score with: greenroom bench score %s\n", o.out)
	if errors.Is(err, context.Canceled) {
		return errors.New("interrupted; run again with the same -out to resume")
	}
	return err
}

type benchScoreOpts struct{ report string }

func benchScoreFlags() (*flag.FlagSet, *benchScoreOpts) {
	o := &benchScoreOpts{}
	fs := flag.NewFlagSet("bench score", flag.ContinueOnError)
	fs.StringVar(&o.report, "report", "", "Markdown report to write; default the results file with .md, - for stdout")
	return fs, o
}

func benchScore(args []string) error {
	fs, o := benchScoreFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("bench score takes one results file")
	}
	path := fs.Arg(0)
	results, err := bench.ReadResults(path)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("%s has no results", path)
	}
	report := bench.Report(path, results, time.Now())
	if o.report == "-" {
		_, err := fmt.Print(report)
		return err
	}
	if o.report == "" {
		o.report = strings.TrimSuffix(path, filepath.Ext(path)) + ".md"
	}
	if err := os.WriteFile(o.report, []byte(report), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "bench: wrote", o.report)
	return nil
}

// findBenchDir returns dir, or the nearest bench/ with a cases/ directory at or above the
// working directory, so the command works from apps/daemon as well as the repo root.
func findBenchDir(dir string) (string, error) {
	if dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "cases")); err != nil {
			return "", fmt.Errorf("-bench %s: %w", dir, err)
		}
		return filepath.Abs(dir)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, "bench", "cases")); err == nil && st.IsDir() {
			return filepath.Join(d, "bench"), nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no bench/cases at or above %s; pass -bench", wd)
		}
	}
}
