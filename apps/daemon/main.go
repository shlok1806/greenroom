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
	case "version":
		fmt.Println("greenroom", mcpserver.Version)
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
	fmt.Fprintln(os.Stderr, "\n       greenroom version")
	os.Exit(2)
}

type serveOpts struct {
	addr, root, image, envFile, tartBin, verifierKind string
	maxDisputes, maxMachines, verifierMaxSteps        int
	frameInterval, verifierBudget                     time.Duration
}

func serveFlags() (*flag.FlagSet, *serveOpts) {
	o := &serveOpts{}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:7777", "listen address")
	fs.StringVar(&o.root, "root", defaultRoot(), "state directory")
	fs.StringVar(&o.image, "image", defaultImage, "default image for machine_create")
	fs.IntVar(&o.maxDisputes, "max-disputes", session.DefaultMaxDisputes, "how many times the coding agent may dispute a verdict before it is contested and only a human can close it")
	fs.IntVar(&o.maxMachines, "max-machines", 2, "how many VMs the host may run at once; Apple allows two macOS guests, and 0 removes the check")
	fs.StringVar(&o.envFile, "env-file", ".env", "file of KEY=VALUE lines holding the model credentials")
	fs.StringVar(&o.tartBin, "tart", "", tartUsage)
	fs.DurationVar(&o.frameInterval, "frame-interval", 2*time.Second, "screen frame capture interval for the run recording; 0 disables")
	fs.StringVar(&o.verifierKind, "verifier", "", "verifier brain: nim (model-driven) or manual (a person types instructions in the conversation); default nim, overridden by GREENROOM_VERIFIER when this flag is not set")
	fs.IntVar(&o.verifierMaxSteps, "verifier-max-steps", verifier.DefaultMaxSteps, "tool calls a verifier turn may make before it stops and asks to be continued with another message")
	fs.DurationVar(&o.verifierBudget, "verifier-budget", verifier.DefaultBudget, "wall-clock budget for a single verifier turn before it stops and asks to be continued")
	return fs, o
}

func serve(args []string) error {
	fs, o := serveFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if !api.LoopbackAddr(o.addr) {
		// The Host check stops browsers, not other machines: a remote client can send any Host it likes.
		log.Warn("-addr is not loopback and the daemon has no authentication: anything that can reach it can drive every machine", "addr", o.addr)
	}
	if err := loadEnvFile(o.envFile); err != nil {
		return err
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

	reg := session.NewRegistry(o.root, o.maxDisputes, session.WithOnVerdict(func(runID string, v session.VerdictState) { _ = mgr.RecordVerdict(runID, v) }))
	mgr.SetMessageActivity(reg.LastMessageAt) // machine_list and the capacity error report idle time

	kind := strings.ToLower(strings.TrimSpace(o.verifierKind))
	if kind == "" {
		kind = strings.ToLower(strings.TrimSpace(os.Getenv("GREENROOM_VERIFIER")))
	}
	switch kind {
	case "manual":
		bridgeLifecycle(mgr, reg, true)
		_ = verifier.NewActors(verifier.NewManual(mgr, log), mgr, reg, verifier.WithLogger(log))
		log.Info("verifier enabled", "brain", "manual")
	case "", "nim":
		// Without a key the daemon still serves every machine tool; nobody answers the conversation.
		key := os.Getenv("NVIDIA_API_KEY")
		bridgeLifecycle(mgr, reg, key != "")
		if key == "" {
			log.Info("verifier disabled", "reason", "no NVIDIA_API_KEY in environment or "+o.envFile)
			break
		}
		model, vision := os.Getenv("GREENROOM_VERIFIER_MODEL"), os.Getenv("GREENROOM_VISION_MODEL")
		v, err := verifier.New(mgr, verifier.Config{
			BaseURL:     os.Getenv("NVIDIA_BASE_URL"),
			APIKey:      key,
			Model:       model,
			VisionModel: vision,
			MaxSteps:    o.verifierMaxSteps,
			Budget:      o.verifierBudget,
		}, log)
		if err != nil {
			return err
		}
		_ = verifier.NewActors(v, mgr, reg, verifier.WithLogger(log))
		log.Info("verifier enabled", "brain", "nim", "model", model, "vision", vision)
	default:
		return fmt.Errorf("unknown -verifier %q: want nim or manual", kind)
	}

	httpServer := &http.Server{Addr: o.addr, Handler: routes(mgr, reg, o.image, log), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()
	addr := ln.Addr().String()
	log.Info("greenroom listening", "mcp", "http://"+addr+"/mcp", "api", "http://"+addr+"/api/", "root", o.root, "image", o.image,
		"maxMachines", o.maxMachines, "machines", len(mgr.List()))

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down; machines keep running")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// routes is the daemon's whole HTTP surface. It is unauthenticated, so nothing a web page can reach gets through.
func routes(mgr *machine.Manager, reg *session.Registry, image string, log *slog.Logger) http.Handler {
	server := mcpserver.New(mgr, image, reg)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "ok %d machines\n", len(mgr.List()))
	})
	mux.Handle("/api/", api.New(mgr, reg, log))
	return api.LocalOnly(mux)
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
