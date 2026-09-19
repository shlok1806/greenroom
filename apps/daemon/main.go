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

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/mcpserver"
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
	fmt.Fprintln(os.Stderr, "usage: greenroom serve [-addr 127.0.0.1:7777] [-root ~/.greenroom] [-image <oci image>] [-max-machines 2]")
	fmt.Fprintln(os.Stderr, "       greenroom version")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "listen address")
	root := fs.String("root", defaultRoot(), "state directory")
	image := fs.String("image", defaultImage, "default image for machine_create")
	maxMachines := fs.Int("max-machines", 2, "how many VMs the host may run at once; Apple allows two macOS guests, and 0 removes the check")
	envFile := fs.String("env-file", ".env", "file of KEY=VALUE lines holding the model credentials")
	openViewer := fs.Bool("open-viewer", true, "when a machine is created with watch, open its screen on this Mac")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := loadEnvFile(*envFile); err != nil {
		return err
	}

	opts := []machine.Option{machine.WithMaxMachines(*maxMachines)}
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

	// greenroom's own agent is optional. Without a key the daemon still
	// serves every machine tool, it just does not offer machine_verify.
	var v *verifier.Verifier
	if key := os.Getenv("NVIDIA_API_KEY"); key != "" {
		v, err = verifier.New(mgr, verifier.Config{
			BaseURL:     os.Getenv("NVIDIA_BASE_URL"),
			APIKey:      key,
			Model:       os.Getenv("GREENROOM_VERIFIER_MODEL"),
			VisionModel: os.Getenv("GREENROOM_VISION_MODEL"),
		}, log)
		if err != nil {
			return err
		}
		log.Info("verifier enabled", "model", os.Getenv("GREENROOM_VERIFIER_MODEL"), "vision", os.Getenv("GREENROOM_VISION_MODEL"))
	} else {
		log.Info("verifier disabled", "reason", "no NVIDIA_API_KEY in environment or "+*envFile)
	}
	server := mcpserver.New(mgr, *image, v)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "ok %d machines\n", len(mgr.List()))
	})
	httpServer := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	log.Info("greenroom listening", "mcp", "http://"+*addr+"/mcp", "root", *root, "image", *image,
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
