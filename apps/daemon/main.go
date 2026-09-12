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
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/mcpserver"
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
	fmt.Fprintln(os.Stderr, "usage: greenroom serve [-addr 127.0.0.1:7777] [-root ~/.greenroom] [-image <oci image>]")
	fmt.Fprintln(os.Stderr, "       greenroom version")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "listen address")
	root := fs.String("root", defaultRoot(), "state directory")
	image := fs.String("image", defaultImage, "default image for machine_create")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	mgr, err := machine.NewManager(*root, log)
	if err != nil {
		return err
	}
	server := mcpserver.New(mgr, *image)

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
	log.Info("greenroom listening", "mcp", "http://"+*addr+"/mcp", "root", *root, "image", *image, "machines", len(mgr.List()))

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

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".greenroom"
	}
	return filepath.Join(home, ".greenroom")
}
