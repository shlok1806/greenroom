package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// A reboot lands in the transcript: that it started and how it ended (daemon ADR 0004).
func TestTheBridgePostsARebootAndHowItEnded(t *testing.T) {
	bin, control := testsupport.FakeTart(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := machine.NewManager(t.TempDir(), log, machine.WithTartBin(bin), machine.WithFrameInterval(0),
		machine.WithReadyTimeout(10*time.Second), machine.WithRebootTimeout(time.Second),
		machine.WithSSHProbe(func(context.Context, string, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	reg := session.NewRegistry(mgr.Root, 2)
	bridgeLifecycle(mgr, reg, true)
	ctx := context.Background()
	mc, err := mgr.Create(ctx, "ghcr.io/example/base:latest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = mgr.Wait(ctx, mc.RunID, 20*time.Second)
		_ = mgr.Destroy(ctx, mc.RunID)
	})
	if got, _ := mgr.Wait(ctx, mc.RunID, 20*time.Second); got.Status != machine.Ready {
		t.Fatalf("machine is %s", got.Status)
	}
	store, err := reg.Get(mc.RunID)
	if err != nil {
		t.Fatal(err)
	}
	events := func() string {
		var out []string
		for _, m := range store.After(0) {
			if m.From == session.System && strings.HasPrefix(m.Text, "machine ") {
				out = append(out, m.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !strings.Contains(events(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("the transcript says\n%s\nwant %q", events(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	if _, _, err := mgr.Reboot(ctx, mc.RunID); err != nil {
		t.Fatal(err)
	}
	waitFor("machine is rebooting")
	waitFor("machine rebooted and is ready")

	testsupport.Flag(t, control, "agent-down")
	if _, _, err := mgr.Reboot(ctx, mc.RunID); err != nil {
		t.Fatal(err)
	}
	waitFor("machine failed to reboot: reboot failed: the machine did not come back within 1s")
	if err := os.Remove(filepath.Join(control, "agent-down")); err != nil {
		t.Fatal(err)
	}
}
