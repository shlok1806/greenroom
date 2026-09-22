package tart

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTartBin writes a script that prints version on --version.
func fakeTartBin(t *testing.T, version string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tart")
	script := "#!/bin/sh\n[ \"$1\" = \"--version\" ] && echo " + version + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// pinAt points the resolver at a file for the duration of one test, so these
// never depend on what is installed on the machine running them.
func pinAt(t *testing.T, path string) {
	t.Helper()
	old := pinnedInstall
	pinnedInstall = path
	t.Cleanup(func() { pinnedInstall = old })
}

func TestResolvePrefersExplicitOverrideThenPinnedThenPath(t *testing.T) {
	pinned := fakeTartBin(t, PinnedVersion)
	pinAt(t, pinned)
	t.Setenv(EnvVar, "")

	if got := Resolve("/explicit/tart"); got.Bin != "/explicit/tart" || got.Source != SourceOverride {
		t.Fatalf("an explicit argument must win: got %+v", got)
	}

	if got := Resolve(""); got.Bin != pinned || got.Source != SourcePinned {
		t.Fatalf("the pinned install must be used when it exists: got %+v", got)
	}

	// The env var sits between the two: above the pinned install, below an
	// explicit argument, which is how -tart beats GREENROOM_TART.
	t.Setenv(EnvVar, "/from/env/tart")
	if got := Resolve(""); got.Bin != "/from/env/tart" || got.Source != SourceOverride {
		t.Fatalf("%s must beat the pinned install: got %+v", EnvVar, got)
	}
	if got := Resolve("/explicit/tart"); got.Bin != "/explicit/tart" {
		t.Fatalf("an explicit argument must beat %s: got %+v", EnvVar, got)
	}
}

func TestResolveFallsBackToPathWhenNothingIsPinned(t *testing.T) {
	t.Setenv(EnvVar, "")
	pinAt(t, filepath.Join(t.TempDir(), "not-installed"))

	got := Resolve("")
	if got.Bin != "tart" || got.Source != SourcePath {
		t.Fatalf("with no pinned install the daemon must still run: got %+v", got)
	}
}

func TestResolveIgnoresADirectoryAtThePinnedPath(t *testing.T) {
	t.Setenv(EnvVar, "")
	// The pinned path points inside an app bundle, so a half-finished
	// install can leave a directory where the binary should be. Treating
	// that as the binary would fail on every command instead of falling
	// through to PATH.
	dir := filepath.Join(t.TempDir(), "tart")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pinAt(t, dir)

	if got := Resolve(""); got.Source != SourcePath {
		t.Fatalf("a directory is not a binary: got %+v", got)
	}
}

func TestVersionReadsTheBinary(t *testing.T) {
	c := &Client{Bin: fakeTartBin(t, "2.37.0")}
	got, err := c.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.37.0" {
		t.Fatalf("version = %q, want 2.37.0", got)
	}
}

func TestCheckVersionIsQuietOnThePinnedVersion(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c := &Client{Bin: fakeTartBin(t, PinnedVersion), source: SourcePinned}
	c.CheckVersion(context.Background(), log)

	if strings.Contains(buf.String(), "WARN") {
		t.Fatalf("the pinned version must not warn: %s", buf.String())
	}
	if !strings.Contains(buf.String(), PinnedVersion) {
		t.Fatalf("the version should still be logged: %s", buf.String())
	}
}

func TestCheckVersionWarnsAndNamesBothVersionsOnAMismatch(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	c := &Client{Bin: fakeTartBin(t, "2.32.1"), source: SourcePath}
	c.CheckVersion(context.Background(), log)

	out := buf.String()
	if !strings.Contains(out, "WARN") {
		t.Fatalf("a mismatch must warn: %s", out)
	}
	// Both versions have to appear or the warning cannot be acted on.
	for _, want := range []string{"2.32.1", PinnedVersion} {
		if !strings.Contains(out, want) {
			t.Fatalf("warning must name %s: %s", want, out)
		}
	}
}

func TestCheckVersionWarnsRatherThanFailingWhenTartIsMissing(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	// A daemon that cannot find tart still has to start: it may be asked for
	// run records, and the user's daemon must not die over this.
	c := &Client{Bin: filepath.Join(t.TempDir(), "no-such-tart")}
	c.CheckVersion(context.Background(), log)

	if !strings.Contains(buf.String(), "WARN") {
		t.Fatalf("a missing tart must warn: %s", buf.String())
	}
}
