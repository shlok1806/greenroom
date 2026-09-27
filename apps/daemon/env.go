package main

import (
	"bufio"
	"log/slog"
	"os"
	"strings"
)

// loadEnvFile sets KEY=VALUE lines from path into the environment without overriding variables already set.
// A missing file is fine: the daemon then runs without a model verifier.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = envValue(value)
		if key == "" {
			continue
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return sc.Err()
}

// envValue unquotes a matching quote pair; an unquoted value ends at a # that follows whitespace.
func envValue(raw string) string {
	v := strings.TrimSpace(raw)
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : end+1]
		}
		return v
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			return strings.TrimSpace(raw[:i])
		}
	}
	return v
}

// defaultVisionModel describes screenshots for the verifier when GREENROOM_VISION_MODEL is unset
// (ADR 0032, superseding ADR 0020's kimi-k3): on the 40 labelled screens it gave recall 0.91 with
// no invented value and a p50 of 4.2 s, against 0.72 and 20 to 62 s for kimi-k3 and omni
// (ADR 0030). It needs thinking off, which nim.describeFields sends for it.
const defaultVisionModel = "meta/muse-glimmer-30b"

// describerOverride says which describer GREENROOM_VISION_MODEL (raw) selects instead of the
// default, or "" when it selects the default. "none" is an override too: no describer at all.
func describerOverride(raw string) string {
	switch v := visionModel(raw); v {
	case defaultVisionModel:
		return ""
	case "":
		return "none"
	default:
		return v
	}
}

// warnDescriberOverride logs a warning at start when the environment (or envFile, which fills
// it) picks a describer other than the default. A stale .env line swapped in a weaker describer
// for every run and nobody saw it until a log was read by hand (issue #154).
func warnDescriberOverride(log *slog.Logger, raw, envFile string) {
	if got := describerOverride(raw); got != "" {
		log.Warn("GREENROOM_VISION_MODEL overrides the default screenshot describer", "vision", got,
			"default", defaultVisionModel, "from", "the environment or "+envFile)
	}
}

// visionModel is the describer to use for a GREENROOM_VISION_MODEL value: the default when it is
// unset or blank, nothing (the verifier works without seeing the screen) for "none".
func visionModel(raw string) string {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return defaultVisionModel
	case strings.EqualFold(v, "none"):
		return ""
	}
	return v
}
