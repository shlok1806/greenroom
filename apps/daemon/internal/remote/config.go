// Package remote is `greenroom connect`: a stdio MCP server on the agent's computer that
// forwards every tool to a greenroom daemon on another host (ADR 0021, decision 3). It
// handles machine_sync and machine_pull itself, because the daemon cannot reach this
// computer's files: it uploads a sync's source to the daemon's sync route and unpacks a
// pull from its pull route here. A screenshot's PNG is fetched here too.
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Environment variables that override the config file.
const (
	EnvURL   = "GREENROOM_URL"
	EnvToken = "GREENROOM_TOKEN"
)

// Config is where the daemon is and the token it wants.
type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// DefaultConfigPath is ~/.greenroom/client.json, the file install.sh writes.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".greenroom", "client.json")
	}
	return filepath.Join(home, ".greenroom", "client.json")
}

// DefaultDir is ~/.greenroom/connect, where connect puts what it brings to this computer:
// runs/<runId>/NNN-pull for machine_pull without a dest, and each machine_screenshot's PNG.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".greenroom", "connect")
	}
	return filepath.Join(home, ".greenroom", "connect")
}

// LoadConfig resolves each field on its own: the flag, then the environment, then the
// file at path. A missing file is fine when the flags or the environment supply both
// fields; one named explicitly (explicitPath) must exist. The URL loses any trailing slash.
func LoadConfig(flagURL, flagToken, path string, explicitPath bool, getenv func(string) string) (Config, error) {
	var file Config
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &file); err != nil {
			return Config{}, fmt.Errorf("read %s: %w", path, err)
		}
	case errors.Is(err, fs.ErrNotExist) && !explicitPath:
	default:
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg := Config{
		URL:   first(flagURL, getenv(EnvURL), file.URL),
		Token: first(flagToken, getenv(EnvToken), file.Token),
	}
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.Token = strings.TrimSpace(cfg.Token)
	if cfg.URL == "" {
		return Config{}, fmt.Errorf("no daemon url: pass -url, set %s, or write it to %s", EnvURL, path)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return Config{}, fmt.Errorf("daemon url %q must be http(s)://host", cfg.URL)
	}
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("no token: pass -token, set %s, or write it to %s", EnvToken, path)
	}
	return cfg, nil
}

func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
