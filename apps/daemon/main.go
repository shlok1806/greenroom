// Command greenroom is the daemon that gives AI agents macOS machines.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/api"
	"github.com/shlok1806/greenroom/apps/daemon/internal/buildinfo"
	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/mcpserver"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/tart"
	"github.com/shlok1806/greenroom/apps/daemon/internal/verifier"
)

const tartUsage = "path to the tart binary; defaults to " + tart.EnvVar + ", then the pinned tart " + tart.PinnedVersion + " install, then tart on PATH"

const defaultImage = "ghcr.io/cirruslabs/macos-tahoe-base:latest" // scripts/build-image.sh and install.sh repeat this

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "prepare-image":
		err = prepareImage(os.Args[2:])
	case "check-image":
		err = checkImage(os.Args[2:])
	case "sweep-orphans":
		err = sweepOrphans(os.Args[2:])
	case "connect":
		err = connect(os.Args[2:])
	case "bench":
		err = benchCmd(os.Args[2:])
	case "version":
		fmt.Println("greenroom", mcpserver.Version, buildinfo.Get())
	default:
		usage()
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "greenroom:", err)
		os.Exit(1)
	}
}

// usage prints every subcommand's flags from the flag sets themselves, so it cannot drift from them.
func usage() {
	fmt.Fprintln(os.Stderr, "usage: greenroom serve [flags]")
	serve, _ := serveFlags()
	serve.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\n       greenroom prepare-image -vm <name> [flags]")
	prepare, _ := prepareFlags()
	prepare.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\n       greenroom check-image -image <name> [flags]")
	check, _ := checkFlags()
	check.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\n       greenroom sweep-orphans [flags]   (run clones with no run record, issue #103; a dry run unless -delete)")
	sweep, _ := sweepFlags()
	sweep.PrintDefaults()
	connectUsage()
	benchUsage()
	fmt.Fprintln(os.Stderr, "\n       greenroom version")
	os.Exit(2)
}

type serveOpts struct {
	addr, root, image, envFile, tartBin, verifierKind string
	publicHost, dist                                  string
	maxDisputes, maxMachines, verifierMaxSteps        int
	frameInterval, verifierBudget, sweepAfter         time.Duration
}

func serveFlags() (*flag.FlagSet, *serveOpts) {
	o := &serveOpts{}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:7777", "listen address")
	fs.StringVar(&o.root, "root", defaultRoot(), "state directory")
	fs.StringVar(&o.image, "image", "", "default image for machine_create; default GREENROOM_IMAGE, then local greenroom-lean-a, then greenroom-base, then "+defaultImage)
	fs.IntVar(&o.maxDisputes, "max-disputes", session.DefaultMaxDisputes, "how many times the coding agent may dispute a verdict before it is contested and only a human can close it")
	fs.IntVar(&o.maxMachines, "max-machines", 2, "how many VMs the host may run at once; Apple allows two macOS guests, and 0 removes the check")
	fs.StringVar(&o.envFile, "env-file", ".env", "file of KEY=VALUE lines holding the model credentials")
	fs.StringVar(&o.tartBin, "tart", "", tartUsage)
	fs.DurationVar(&o.frameInterval, "frame-interval", 2*time.Second, "screen frame capture interval for the run recording; 0 disables")
	fs.StringVar(&o.verifierKind, "verifier", "", "verifier brain: nim (model-driven) or manual (a person types instructions in the conversation); default nim, overridden by GREENROOM_VERIFIER when this flag is not set")
	fs.IntVar(&o.verifierMaxSteps, "verifier-max-steps", verifier.DefaultMaxSteps, "tool calls a verifier turn may make before it stops and asks to be continued with another message")
	fs.DurationVar(&o.verifierBudget, "verifier-budget", verifier.DefaultBudget, "wall-clock budget for a single verifier turn before it stops and asks to be continued")
	fs.StringVar(&o.publicHost, "public-host", "", "hostname a tunnel forwards to this daemon; requests for it need GREENROOM_TOKEN (ADR 0021); default GREENROOM_PUBLIC_HOST, empty for local only")
	fs.StringVar(&o.dist, "dist", "", "directory holding install.sh and the files under /dl/; default <root>/dist")
	fs.DurationVar(&o.sweepAfter, "sweep-orphans-after", machine.DefaultOrphanAge, "at start, delete greenroom-<runId> clones that are stopped, have no run directory and are older than this (issue #103); 0 keeps them")
	return fs, o
}

func serve(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveUntil(ctx, args)
}

// serveUntil serves until ctx ends, then stops HTTP; machines keep running.
func serveUntil(ctx context.Context, args []string) error {
	fs, o := serveFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if !api.LoopbackAddr(o.addr) {
		// The Host check stops browsers, not other machines: a remote client can send a loopback Host and skip the token.
		log.Warn("-addr is not loopback and a loopback Host needs no token: anything that can reach it can drive every machine; use a tunnel and -public-host instead", "addr", o.addr)
	}
	if err := loadEnvFile(o.envFile); err != nil {
		return err
	}
	if o.publicHost == "" {
		o.publicHost = strings.TrimSpace(os.Getenv("GREENROOM_PUBLIC_HOST"))
	}
	if o.dist == "" {
		o.dist = filepath.Join(o.root, "dist")
	}
	token := strings.TrimSpace(os.Getenv("GREENROOM_TOKEN"))
	if err := checkPublicAccess(o.publicHost, token, o.envFile); err != nil {
		return err
	}
	if o.publicHost != "" {
		log.Info("public access enabled", "host", o.publicHost, "dist", o.dist) // never the token
	}

	// Claim the root and the address before anything reads state or starts a verifier: a daemon
	// that is about to fail must not reattach machines or answer runs on the way (issue #63).
	release, err := lockRoot(o.root)
	if err != nil {
		return err
	}
	defer release()
	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	opts := []machine.Option{
		machine.WithMaxMachines(o.maxMachines),
		machine.WithFrameInterval(o.frameInterval),
		machine.WithTartBin(o.tartBin), // empty keeps internal/tart's own resolution
	}
	mgr, err := machine.NewManager(o.root, log, opts...)
	if err != nil {
		return err
	}
	mgr.CheckTart(context.Background()) // logs a version mismatch, never fatal
	// After loadState, so every machine this daemon still runs is its own. In the background: tart
	// deletes a clone in a second or so, and serving need not wait for it.
	go func() {
		swept, err := mgr.SweepOrphans(ctx, o.sweepAfter, false, defaultRoot())
		if err != nil {
			log.Warn("cannot sweep orphaned run clones", "err", err)
		} else if len(swept) > 0 {
			log.Info("swept orphaned run clones", "count", len(swept))
		}
	}()
	// The same choice scripts/install.sh makes, so a bare serve behaves like the installed daemon.
	if o.image == "" {
		o.image = strings.TrimSpace(os.Getenv("GREENROOM_IMAGE"))
	}
	if o.image == "" {
		o.image = mgr.PreferredImage(context.Background(), defaultImage)
	}

	reg := session.NewRegistry(o.root, o.maxDisputes, session.WithOnVerdict(func(runID string, v session.VerdictState) { _ = mgr.RecordVerdict(runID, v) }))
	mgr.SetMessageActivity(reg.LastMessageAt) // machine_list and the capacity error report idle time

	// What GET /api/version reports (root ADR 0033); the verifier's part is filled in below.
	ver := buildVersion()

	kind := strings.ToLower(strings.TrimSpace(o.verifierKind))
	if kind == "" {
		kind = strings.ToLower(strings.TrimSpace(os.Getenv("GREENROOM_VERIFIER")))
	}
	switch kind {
	case "manual":
		bridgeLifecycle(mgr, reg, true)
		_ = verifier.NewActors(verifier.NewManual(mgr, log), mgr, reg, verifier.WithLogger(log))
		ver.Verifier = "manual"
		log.Info("verifier enabled", "brain", "manual")
	case "", "nim":
		// Without a key the daemon still serves every machine tool; nobody answers the conversation.
		key := os.Getenv("NVIDIA_API_KEY")
		bridgeLifecycle(mgr, reg, key != "")
		if key == "" {
			log.Info("verifier disabled", "reason", "no NVIDIA_API_KEY in environment or "+o.envFile)
			break
		}
		v, err := nimVerifier(mgr, o.verifierMaxSteps, o.verifierBudget, log)
		if err != nil {
			return err
		}
		_ = verifier.NewActors(v, mgr, reg, verifier.WithLogger(log))
		ver.Verifier, ver.VerifierModel, ver.VisionModel = "nim", v.Model(), visionModel(os.Getenv("GREENROOM_VISION_MODEL"))
		log.Info("verifier enabled", "brain", "nim", "model", v.Model(), "vision", ver.VisionModel)
	default:
		return fmt.Errorf("unknown -verifier %q: want nim or manual", kind)
	}

	// Every request's context ends when shutdown starts, so long-lived handlers (the companion's
	// event stream, agent_wait) return at once instead of holding Shutdown to its timeout and
	// the root lock with it (issue #98).
	baseCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	httpServer := &http.Server{Addr: o.addr, Handler: routes(mgr, reg, o.image, o.publicHost, token, o.dist, ver, log), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return baseCtx }}
	httpServer.RegisterOnShutdown(cancelRequests)

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()
	addr := ln.Addr().String()
	log.Info("greenroom listening", "build", ver.String(), "mcp", "http://"+addr+"/mcp", "api", "http://"+addr+"/api/", "root", o.root, "image", o.image,
		"maxMachines", o.maxMachines, "machines", len(mgr.List()))

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down; machines keep running")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Still the stop the person asked for: close what is left rather than fail.
			log.Warn("closing the connections that did not finish in time", "err", err)
			_ = httpServer.Close()
		}
		return nil
	}
}

// nimVerifier is the model-driven verifier as the environment configures it (NVIDIA_API_KEY,
// NVIDIA_BASE_URL, GREENROOM_VERIFIER_MODEL, GREENROOM_VISION_MODEL). serve and bench share it,
// so the bench measures the verifier the daemon runs.
func nimVerifier(mgr *machine.Manager, maxSteps int, budget time.Duration, log *slog.Logger) (*verifier.Verifier, error) {
	return verifier.New(mgr, verifier.Config{
		BaseURL:     os.Getenv("NVIDIA_BASE_URL"),
		APIKey:      os.Getenv("NVIDIA_API_KEY"),
		Model:       os.Getenv("GREENROOM_VERIFIER_MODEL"),
		VisionModel: visionModel(os.Getenv("GREENROOM_VISION_MODEL")),
		MaxSteps:    maxSteps,
		Budget:      budget,
	}, log)
}

// routes is the daemon's whole HTTP surface. Loopback needs no token, so nothing a web page can
// reach gets through; publicHost (tunnel traffic) needs token on everything but the install files.
func routes(mgr *machine.Manager, reg *session.Registry, image, publicHost, token, dist string, ver api.Version, log *slog.Logger) http.Handler {
	// A call through the public host gets its own server, whose tools never write where the
	// caller names on this host (machine_pull's dest, ADR 0022). Guard marks those requests.
	local, public := mcpserver.New(mgr, image, reg), mcpserver.New(mgr, image, reg, mcpserver.ForPublicHost())
	server := func(r *http.Request) *mcp.Server {
		if api.FromPublicHost(r.Context()) {
			return public
		}
		return local
	}
	mux := http.NewServeMux()
	// The SDK refuses a non-localhost Host on a request from 127.0.0.1 (DNS rebinding), which is
	// what every tunnel request looks like. api.Guard below does that check with the public host
	// allowed, so with a public host it is the only one.
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(server,
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: publicHost != ""}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "ok %d machines\n", len(mgr.List()))
	})
	mux.Handle("/api/", api.New(mgr, reg, log))
	// Read only, and the only build route: nothing here can update the daemon (ADR 0033).
	mux.Handle("GET /api/version", api.VersionHandler(ver))
	install := api.Dist(dist)
	mux.Handle("GET /install.sh", install)
	mux.Handle("GET /dl/{file}", install)
	return api.Guard(mux, publicHost, token)
}

// buildVersion is this build's identity and the image versions it expects, with no verifier
// (serve fills that in once it knows which one runs). The checkout is the one install.sh
// recorded in the launchd job; a daemon started any other way has none.
func buildVersion() api.Version {
	return api.Version{
		Version:     mcpserver.Version,
		Info:        buildinfo.Get(),
		InputHelper: machine.InputHelperVersion(),
		ImageRecipe: machine.ImageRecipeVersion(),
		Verifier:    "none",
		Checkout:    strings.TrimSpace(os.Getenv("GREENROOM_CHECKOUT")),
	}
}

// minTokenLength is the shortest GREENROOM_TOKEN a daemon with a public host accepts.
const minTokenLength = 32

// checkPublicAccess refuses a public host that is not a bare hostname, or one without a token long enough to guard it.
func checkPublicAccess(publicHost, token, envFile string) error {
	if publicHost == "" {
		return nil
	}
	if strings.ContainsAny(publicHost, "/ ") {
		return fmt.Errorf("public host %q must be a bare hostname like greenroom.example.com, with no scheme or path", publicHost)
	}
	if len(token) < minTokenLength {
		return fmt.Errorf("public host %s needs GREENROOM_TOKEN of at least %d characters (it has %d); make one with: echo \"GREENROOM_TOKEN=$(openssl rand -hex 32)\" >> %s",
			publicHost, minTokenLength, len(token), envFile)
	}
	return nil
}

// bridgeLifecycle posts the manager's lifecycle into each run's conversation. It is the only poster of
// ready/failed/stopped/destroyed. Without a verifier it also says nobody will answer each message that starts a turn.
func bridgeLifecycle(mgr *machine.Manager, reg *session.Registry, verifierEnabled bool) {
	post := func(runID, text string) {
		store, err := reg.Get(runID)
		if err != nil {
			return
		}
		_, _ = store.Append(session.Message{From: session.System, Kind: session.Event, Text: text})
	}
	withError := func(text string, mc *machine.Machine) string {
		if mc != nil && mc.Error != "" {
			return text + ": " + mc.Error
		}
		return text
	}
	mgr.Listen(func(ev machine.LifecycleEvent) {
		var text string
		switch ev.Kind {
		case "created":
			// reg.Listen only fans out from opened stores, so open this one before its first message.
			go func() { _, _ = reg.Get(ev.RunID) }()
			return
		case "ready":
			text = "machine is ready"
		case "failed":
			text = withError("machine failed to boot", ev.Machine)
		case "stopped":
			text = withError("machine stopped", ev.Machine) // a ready machine's VM went away, not a boot failure
		case "destroyed":
			post(ev.RunID, "machine destroyed")
			reg.Evict(ev.RunID)
			return
		default:
			return
		}
		// Synchronous so the event lands before anything a caller does in reaction to it.
		post(ev.RunID, text)
	})
	if verifierEnabled {
		return
	}
	reg.Listen(func(runID string, m session.Message) {
		if !m.StartsTurn() {
			return
		}
		// Listeners run under the store's lock, so append elsewhere.
		go post(runID, noVerifierNotice+string(m.Kind))
	})
}

// noVerifierNotice, followed by the kind of message, is what a daemon without a verifier posts
// after every message that starts a turn, so no client waits for an answer.
const noVerifierNotice = "no verifier is configured on this daemon (set NVIDIA_API_KEY in .env); nobody will answer this "

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".greenroom"
	}
	return filepath.Join(home, ".greenroom")
}
