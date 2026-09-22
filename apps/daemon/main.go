// Command greenroom is the daemon that gives AI agents macOS machines.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
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
	case "version":
		fmt.Println("greenroom", mcpserver.Version)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "greenroom:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: greenroom serve [-addr 127.0.0.1:7777] [-root ~/.greenroom] [-image <oci image>] [-max-machines 2] [-max-disputes 2] [-frame-interval 2s] [-verifier nim|manual] [-verifier-max-steps 40] [-verifier-budget 10m]")
	fmt.Fprintln(os.Stderr, "       greenroom prepare-image -vm <name> [-root ~/.greenroom]")
	fmt.Fprintln(os.Stderr, "       greenroom version")
	os.Exit(2)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "listen address")
	root := fs.String("root", defaultRoot(), "state directory")
	image := fs.String("image", defaultImage, "default image for machine_create")
	maxDisputes := fs.Int("max-disputes", session.DefaultMaxDisputes, "how many times the coding agent may dispute a verdict before it is contested and only a human can close it")
	maxMachines := fs.Int("max-machines", 2, "how many VMs the host may run at once; Apple allows two macOS guests, and 0 removes the check")
	envFile := fs.String("env-file", ".env", "file of KEY=VALUE lines holding the model credentials")
	tartBin := fs.String("tart", "", "path to the tart binary; defaults to "+tart.EnvVar+", then the pinned tart "+tart.PinnedVersion+" install, then tart on PATH")
	openViewer := fs.Bool("open-viewer", true, "when a machine is created with watch, open its screen on this Mac")
	frameInterval := fs.Duration("frame-interval", 2*time.Second, "screen frame capture interval for the run recording; 0 disables")
	verifierKind := fs.String("verifier", "", "verifier brain: nim (model-driven) or manual (a person types instructions in the conversation); default nim, overridden by GREENROOM_VERIFIER when this flag is not set")
	verifierMaxSteps := fs.Int("verifier-max-steps", verifier.DefaultMaxSteps, "tool calls a verifier turn may make before it stops and asks to be continued with another message")
	verifierBudget := fs.Duration("verifier-budget", verifier.DefaultBudget, "wall-clock budget for a single verifier turn before it stops and asks to be continued")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := loadEnvFile(*envFile); err != nil {
		return err
	}

	opts := []machine.Option{
		machine.WithMaxMachines(*maxMachines),
		machine.WithFrameInterval(*frameInterval),
		machine.WithTartBin(*tartBin), // empty keeps internal/tart's own resolution
	}
	if *openViewer {
		opts = append(opts, machine.WithWatchHandler(func(vncURL string) {
			log.Info("opening the machine's screen", "url", redactVNC(vncURL))
			if err := exec.Command("open", vncURL).Start(); err != nil {
				log.Warn("could not open the viewer", "err", err)
			}
		}))
	}
	mgr, err := machine.NewManager(*root, log, opts...)
	if err != nil {
		return err
	}
	mgr.CheckTart(context.Background()) // logs a version mismatch, never fatal

	reg := session.NewRegistry(*root, *maxDisputes)
	reg.OnVerdict = func(runID string, v session.VerdictState) { _ = mgr.RecordVerdict(runID, v) }

	kind := strings.ToLower(strings.TrimSpace(*verifierKind))
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
			log.Info("verifier disabled", "reason", "no NVIDIA_API_KEY in environment or "+*envFile)
			break
		}
		model, vision := os.Getenv("GREENROOM_VERIFIER_MODEL"), os.Getenv("GREENROOM_VISION_MODEL")
		v, err := verifier.New(mgr, verifier.Config{
			BaseURL:     os.Getenv("NVIDIA_BASE_URL"),
			APIKey:      key,
			Model:       model,
			VisionModel: vision,
			MaxSteps:    *verifierMaxSteps,
			Budget:      *verifierBudget,
		}, log)
		if err != nil {
			return err
		}
		_ = verifier.NewActors(v, mgr, reg, verifier.WithLogger(log))
		log.Info("verifier enabled", "brain", "nim", "model", model, "vision", vision)
	default:
		return fmt.Errorf("unknown -verifier %q: want nim or manual", kind)
	}

	server := mcpserver.New(mgr, *image, reg)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "ok %d machines\n", len(mgr.List()))
	})
	mux.Handle("/api/", api.New(mgr, reg, log))
	httpServer := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	log.Info("greenroom listening", "mcp", "http://"+*addr+"/mcp", "api", "http://"+*addr+"/api/", "root", *root, "image", *image,
		"maxMachines", *maxMachines, "machines", len(mgr.List()))

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

// bridgeLifecycle posts the manager's lifecycle into each run's conversation. It is the only poster of
// ready/failed/stopped/destroyed. Without a verifier it also tells the coder nobody will answer a task.
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
			text = "machine destroyed"
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
		if m.Kind != session.Task || m.From == session.Verifier {
			return
		}
		// Listeners run under the store's lock, so append elsewhere.
		go post(runID, "no verifier is configured on this daemon (set NVIDIA_API_KEY in .env); nobody will answer this task")
	})
}

// redactVNC keeps the one-time screen password out of the log.
func redactVNC(url string) string {
	if i := strings.Index(url, "@"); i >= 0 {
		return "vnc://***" + url[i:]
	}
	return url
}

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".greenroom"
	}
	return filepath.Join(home, ".greenroom")
}
