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
	"github.com/shlok1806/greenroom/apps/daemon/internal/verifier"
)

const defaultImage = "ghcr.io/cirruslabs/macos-tahoe-base:latest"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "greenroom:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("greenroom", mcpserver.Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: greenroom serve [-addr 127.0.0.1:7777] [-root ~/.greenroom] [-image <oci image>] [-max-machines 2] [-max-disputes 2] [-frame-interval 2s] [-verifier nim|manual] [-verifier-max-steps 40] [-verifier-budget 10m]")
	fmt.Fprintln(os.Stderr, "       greenroom version")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "listen address")
	root := fs.String("root", defaultRoot(), "state directory")
	image := fs.String("image", defaultImage, "default image for machine_create")
	maxDisputes := fs.Int("max-disputes", session.DefaultMaxDisputes, "how many times the coding agent may dispute a verdict before it is contested and only a human can close it")
	maxMachines := fs.Int("max-machines", 2, "how many VMs the host may run at once; Apple allows two macOS guests, and 0 removes the check")
	envFile := fs.String("env-file", ".env", "file of KEY=VALUE lines holding the model credentials")
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

	opts := []machine.Option{machine.WithMaxMachines(*maxMachines), machine.WithFrameInterval(*frameInterval)}
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

	// Every run owns one conversation (ADR 0006). It is the only way to
	// reach the verifier, and the manifest follows whatever it decides.
	reg := session.NewRegistry(*root, *maxDisputes)
	reg.OnVerdict = func(runID string, v session.VerdictState) { _ = mgr.RecordVerdict(runID, v) }

	// greenroom's own agent is optional, and comes in two brains. "manual" is
	// the model brain with a person for a model: it needs no key, and it
	// answers the conversation itself, one instruction per line. "nim" is
	// the model-driven brain and needs NVIDIA_API_KEY. Without either, the
	// daemon still serves every machine tool; the conversation simply has
	// nobody answering on the verifier's side.
	verifierKindVal := strings.ToLower(strings.TrimSpace(*verifierKind))
	if verifierKindVal == "" {
		verifierKindVal = strings.ToLower(strings.TrimSpace(os.Getenv("GREENROOM_VERIFIER")))
	}
	if verifierKindVal == "" {
		verifierKindVal = "nim"
	}
	switch verifierKindVal {
	case "manual":
		bridgeLifecycle(mgr, reg, true)
		_ = verifier.NewActors(verifier.NewManual(mgr, log), mgr, reg, verifier.WithLogger(log))
		log.Info("verifier enabled", "brain", "manual")
	case "nim":
		key := os.Getenv("NVIDIA_API_KEY")
		verifierEnabled := key != ""
		bridgeLifecycle(mgr, reg, verifierEnabled)
		if verifierEnabled {
			v, err := verifier.New(mgr, verifier.Config{
				BaseURL:     os.Getenv("NVIDIA_BASE_URL"),
				APIKey:      key,
				Model:       os.Getenv("GREENROOM_VERIFIER_MODEL"),
				VisionModel: os.Getenv("GREENROOM_VISION_MODEL"),
				MaxSteps:    *verifierMaxSteps,
				Budget:      *verifierBudget,
			}, log)
			if err != nil {
				return err
			}
			_ = verifier.NewActors(v, mgr, reg, verifier.WithLogger(log))
			log.Info("verifier enabled", "brain", "nim", "model", os.Getenv("GREENROOM_VERIFIER_MODEL"), "vision", os.Getenv("GREENROOM_VISION_MODEL"))
		} else {
			log.Info("verifier disabled", "reason", "no NVIDIA_API_KEY in environment or "+*envFile)
		}
	default:
		return fmt.Errorf("unknown -verifier %q: want nim or manual", verifierKindVal)
	}
	server := mcpserver.New(mgr, *image, reg)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "ok %d machines\n", len(mgr.List()))
	})
	// The companion app's surface (ADR 0007), over the same manager and the
	// same conversation store the MCP tools use.
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

// bridgeLifecycle turns the manager's own lifecycle into conversation
// events, so that a machine becoming ready, failing to boot or going away is
// announced once, from the one place that knows it really happened. Nothing
// else posts those three.
//
// Without a verifier there is nobody to answer a task, so the daemon says so
// rather than leaving the coder to poll agent_wait forever.
func bridgeLifecycle(mgr *machine.Manager, reg *session.Registry, verifierEnabled bool) {
	post := func(runID, text string) {
		store, err := reg.Get(runID)
		if err != nil {
			return
		}
		_, _ = store.Append(session.Message{From: session.System, Kind: session.Event, Text: text})
	}
	mgr.Listen(func(ev machine.LifecycleEvent) {
		var text string
		switch ev.Kind {
		case "created":
			// Open the store now. reg.Listen only fans out from stores the
			// registry has opened, so a run must be known from its first
			// second or the event stream misses its earliest messages.
			go func() { _, _ = reg.Get(ev.RunID) }()
			return
		case "ready":
			text = "machine is ready"
		case "failed":
			text = "machine failed to boot"
			if ev.Machine != nil && ev.Machine.Error != "" {
				text += ": " + ev.Machine.Error
			}
		case "destroyed":
			text = "machine destroyed"
		default:
			return
		}
		// Posted synchronously on purpose: an append is one local write, and
		// a lifecycle event must land in the transcript before anything a
		// caller does in reaction to it (a task sent after machine_wait
		// reported failed, for instance) or the record reads out of order.
		post(ev.RunID, text)
	})
	if verifierEnabled {
		return
	}
	reg.Listen(func(runID string, m session.Message) {
		if m.Kind != session.Task || m.From == session.Verifier {
			return
		}
		// This runs under the store's lock, so the append has to happen
		// somewhere else.
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
