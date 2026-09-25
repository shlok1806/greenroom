package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/mcpserver"
	"github.com/shlok1806/greenroom/apps/daemon/internal/remote"
)

type connectOpts struct {
	url, token, config, dir string
	check                   bool
}

func connectFlags() (*flag.FlagSet, *connectOpts) {
	o := &connectOpts{}
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.StringVar(&o.url, "url", "", "daemon URL, e.g. https://greenroom.example.com; default "+remote.EnvURL+", then the config file")
	fs.StringVar(&o.token, "token", "", "bearer token; default "+remote.EnvToken+", then the config file")
	fs.StringVar(&o.config, "config", remote.DefaultConfigPath(), "client config file holding {\"url\", \"token\"}")
	fs.StringVar(&o.dir, "dir", remote.DefaultDir(), "directory on this computer for machine_pull without a dest and for screenshots")
	fs.BoolVar(&o.check, "check", false, "check the url and token, print ok: <url> (<n> tools), and exit")
	return fs, o
}

// connectUsage is connect's part of usage().
func connectUsage() {
	fmt.Fprintln(os.Stderr, "\n       greenroom connect [-check] [flags]   # stdio MCP server for a daemon on another host (ADR 0021)")
	fs, _ := connectFlags()
	fs.PrintDefaults()
}

// connect runs the stdio MCP server that forwards to a remote daemon. Stdout is the MCP
// channel: everything else goes to stderr.
func connect(args []string) error {
	fs, o := connectFlags()
	if err := fs.Parse(args); err != nil {
		return err
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "config" })
	cfg, err := remote.LoadConfig(o.url, o.token, o.config, explicit, os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	opts := remote.Options{Version: mcpserver.Version, Logger: logger, Dir: o.dir}

	if o.check {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// The installer reads the reason, so it is the whole message.
		if err := remote.Check(ctx, cfg, remote.Options{Version: mcpserver.Version}, os.Stdout); err != nil {
			return fmt.Errorf("check %s: %w", cfg.URL, err)
		}
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return remote.Serve(ctx, cfg, opts, &mcp.StdioTransport{})
}
