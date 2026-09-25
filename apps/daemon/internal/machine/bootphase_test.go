package machine

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/testsupport"
)

// bootEvents records every boot phase event and the lifecycle kinds between them.
type bootEvents struct {
	mu     sync.Mutex
	phases []BootPhase
	kinds  []string // "boot:<phase>:<running|done>" and the other kinds, in order
}

func listenBoot(mgr *Manager) *bootEvents {
	b := &bootEvents{}
	mgr.Listen(func(ev LifecycleEvent) {
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case ev.Kind == "boot" && ev.Boot != nil:
			b.phases = append(b.phases, *ev.Boot)
			state := "done"
			if ev.Boot.Seconds == nil {
				state = "running"
			}
			b.kinds = append(b.kinds, "boot:"+ev.Boot.Phase+":"+state)
		case ev.Kind != "step" && ev.Kind != "frame":
			b.kinds = append(b.kinds, ev.Kind)
		}
	})
	return b
}

func (b *bootEvents) snapshot() ([]BootPhase, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.phases), slices.Clone(b.kinds)
}

// waitFor polls until kind was heard, since ready and failed are emitted after Wait returns.
func (b *bootEvents) waitFor(t *testing.T, kind string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, kinds := b.snapshot(); slices.Contains(kinds, kind) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never heard %q", kind)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func names(phases []BootPhase) []string {
	out := make([]string, len(phases))
	for i, p := range phases {
		out[i] = p.Phase
	}
	return out
}

var wholeBoot = []string{PhaseClone, PhaseStart, PhaseAgent, PhaseIP, PhaseKey, PhaseSettings, PhaseHelper, PhaseChecks, PhaseSSH}

func TestBootPhasesArriveInOrderAsTheyHappen(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	events := listenBoot(mgr)
	mc := readyMachine(t, mgr)

	// The machine carries every phase, ended, oldest first.
	got := mc.BootPhases()
	if !slices.Equal(names(got), wholeBoot) {
		t.Fatalf("phases = %v, want %v", names(got), wholeBoot)
	}
	for i, p := range got {
		if p.Seconds == nil || p.Error != "" || p.At.IsZero() {
			t.Errorf("phase %s = %+v, want ended with no error and a start time", p.Phase, p)
		}
		if i > 0 && p.At.Before(got[i-1].At) {
			t.Errorf("phase %s started at %v, before %s at %v", p.Phase, p.At, got[i-1].Phase, got[i-1].At)
		}
	}
	if got[0].Detail != testImage || got[1].Detail != mc.Name || got[3].Detail != "192.168.64.9" {
		t.Errorf("details = clone %q, start %q, ip %q; want the image, the VM and the address",
			got[0].Detail, got[1].Detail, got[3].Detail)
	}

	// Watchers heard each phase as it happened: the created machine first, clone and start
	// (done before anyone knew the run), then each later phase starting and ending, then ready.
	events.waitFor(t, "ready")
	_, kinds := events.snapshot()
	want := []string{"created", "boot:clone:done", "boot:start:done"}
	for _, p := range wholeBoot[2:] {
		want = append(want, "boot:"+p+":running", "boot:"+p+":done")
	}
	want = append(want, "ready")
	if !slices.Equal(kinds, want) {
		t.Errorf("events =\n%v\nwant\n%v", kinds, want)
	}
}

func TestABootingMachineShowsItsPhasesSoFar(t *testing.T) {
	// ssh never answers within the budget, so the machine sits in its last phase.
	block := make(chan struct{})
	mgr, _, _ := newTestManager(t, WithSSHProbe(func(ctx context.Context, _, _ string) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return ctx.Err()
	}))
	t.Cleanup(func() { close(block) }) // before the manager's cleanup waits for the boot
	mc, err := mgr.Create(context.Background(), testImage)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, live := range mgr.List() {
			if live.RunID != mc.RunID {
				continue
			}
			phases := live.BootPhases()
			if n := len(phases); n > 0 && phases[n-1].Phase == PhaseSSH {
				if phases[n-1].Seconds != nil {
					t.Fatalf("ssh = %+v, want it still running", phases[n-1])
				}
				if live.Status != Booting {
					t.Fatalf("status = %s, want booting", live.Status)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the machine never showed its ssh phase running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAFailedBootShowsThePhaseItFailedAt(t *testing.T) {
	mgr, _, control := newTestManager(t, WithReadyTimeout(2*time.Second))
	testsupport.Flag(t, control, "fail-ip")
	events := listenBoot(mgr)

	mc, err := mgr.Create(context.Background(), testImage)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := mgr.Wait(context.Background(), mc.RunID, 30*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status != Failed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	phases := got.BootPhases()
	if want := []string{PhaseClone, PhaseStart, PhaseAgent, PhaseIP}; !slices.Equal(names(phases), want) {
		t.Fatalf("phases = %v, want %v: nothing after the one that failed", names(phases), want)
	}
	last := phases[len(phases)-1]
	if last.Error == "" || last.Seconds == nil {
		t.Errorf("ip = %+v, want it ended with the error", last)
	}
	if phases[2].Error != "" {
		t.Errorf("agent = %+v, want no error", phases[2])
	}

	// The failed phase is published, with its error, before the machine is reported failed.
	events.waitFor(t, "failed")
	sent, kinds := events.snapshot()
	if i, j := slices.Index(kinds, "boot:ip:done"), slices.Index(kinds, "failed"); i < 0 || j < i {
		t.Errorf("events = %v, want the ended ip phase before failed", kinds)
	}
	if sent[len(sent)-1].Error == "" {
		t.Errorf("last published phase = %+v, want the ip error", sent[len(sent)-1])
	}
}

func TestADestroyedBootPublishesNoMorePhases(t *testing.T) {
	block := make(chan struct{})
	mgr, _, _ := newTestManager(t, WithSSHProbe(func(ctx context.Context, _, _ string) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return ctx.Err()
	}))
	t.Cleanup(func() { close(block) }) // before the manager's cleanup waits for the boot
	events := listenBoot(mgr)
	mc, err := mgr.Create(context.Background(), testImage)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	events.waitFor(t, "boot:ssh:running")
	if err := mgr.Destroy(context.Background(), mc.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	_, kinds := events.snapshot()
	if slices.Contains(kinds, "boot:ssh:done") {
		t.Errorf("events = %v, want no ssh end after the destroy", kinds)
	}
}
