package main

import (
	"bufio"
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
// (ADR 0018, superseding ADR 0005's describer): it read every labelled screen with no failed call
// and no invented value offline, and gave more verdicts with fewer failed turns in the realistic
// suite than nemotron-3-nano-omni.
const defaultVisionModel = "moonshotai/kimi-k3"

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
