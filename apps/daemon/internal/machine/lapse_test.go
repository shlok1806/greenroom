package machine

import (
	"context"
	"sync"
	"testing"
	"time"
)

// lapses records every "control" event that carries a lapsed lease, in order.
type lapses struct {
	mu     sync.Mutex
	events []LifecycleEvent
}

func (l *lapses) listen(mgr *Manager) {
	mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind == "control" && ev.Lapsed != nil {
			l.mu.Lock()
			l.events = append(l.events, ev)
			l.mu.Unlock()
		}
	})
}

func (l *lapses) all() []LifecycleEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]LifecycleEvent(nil), l.events...)
}

// expireLease makes holder's lease on mc one that ran out before its lapse timer fired: the
// window between a lease's expiry and the timer is what a take or release can land in.
func expireLease(t *testing.T, mgr *Manager, mc *Machine, holder string) {
	t.Helper()
	if _, _, err := mgr.TakeControl(mc.RunID, holder, 0); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	mgr.mu.Lock()
	mc.lapse.Stop()
	c := *mc.Control
	c.Actions = 2
	c.Expires = time.Now().UTC().Add(-time.Second)
	mc.Control = &c
	mgr.mu.Unlock()
}

// The coder acting right after the human's lease ran out used to replace it without a trace: InputAs
// takes the lease through TakeControl, which dropped the lapsed lease, and the timer then found
// the coder's lease instead. The manager announces the lapse whichever take replaces it.
func TestALapseReplacedByTheCodersInputIsStillAnnounced(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	live, _ := mgr.get(readyMachine(t, mgr).RunID)
	var seen lapses
	seen.listen(mgr)
	expireLease(t, mgr, live, "human")

	if _, err := mgr.InputAs(context.Background(), live.RunID, HolderCoder,
		[]InputAction{{Type: "click", X: frac(0.5), Y: frac(0.5)}}); err != nil {
		t.Fatalf("InputAs: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // a timer that was still due would fire now
	got := seen.all()
	if len(got) != 1 || got[0].Lapsed.Holder != "human" || got[0].Lapsed.Actions != 2 {
		t.Fatalf("lapse events %+v, want one for the human's lease of 2 actions", got)
	}
}

// Letting go of a lease that already ran out announces its lapse once, from the manager.
func TestReleasingALapsedLeaseAnnouncesTheLapse(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	live, _ := mgr.get(readyMachine(t, mgr).RunID)
	var seen lapses
	seen.listen(mgr)
	expireLease(t, mgr, live, "human")

	if _, held, err := mgr.ReleaseControl(live.RunID, "human"); err != nil || held {
		t.Fatalf("ReleaseControl of a lapsed lease: held=%v err=%v, want not held", held, err)
	}
	if got := seen.all(); len(got) != 1 || got[0].Lapsed.Holder != "human" {
		t.Fatalf("lapse events %+v, want one for the human's lease", got)
	}
}

// A take that follows a lapse returns only after the lapse was announced, so whatever the taker
// posts next lands after it: the transcript reads "lost control", then "took control".
func TestATakeWaitsForTheLapseBeforeIt(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	live, _ := mgr.get(readyMachine(t, mgr).RunID)
	announcing, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	mgr.Listen(func(ev LifecycleEvent) {
		if ev.Kind == "control" && ev.Lapsed != nil {
			once.Do(func() { close(announcing) })
			<-finish // a slow listener, like one appending to the conversation
		}
	})
	if _, _, err := mgr.TakeControl(live.RunID, "human", 200*time.Millisecond); err != nil {
		t.Fatalf("TakeControl: %v", err)
	}
	select {
	case <-announcing:
	case <-time.After(5 * time.Second):
		close(finish)
		t.Fatal("the lapse was never announced")
	}
	took := make(chan error, 1)
	go func() {
		_, _, err := mgr.TakeControl(live.RunID, "human", 0)
		took <- err
	}()
	select {
	case err := <-took:
		close(finish)
		t.Fatalf("the take returned (err %v) before the lapse it follows was announced", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(finish)
	if err := <-took; err != nil {
		t.Fatalf("TakeControl after the lapse: %v", err)
	}
}

// A lapse timer that fires once the machine is being destroyed announces nothing: an announcement
// after "destroyed" would reopen the conversation the daemon just evicted.
func TestALapseAfterDestroyIsNotAnnounced(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	live, _ := mgr.get(readyMachine(t, mgr).RunID)
	var seen lapses
	seen.listen(mgr)
	expireLease(t, mgr, live, "human")
	if err := mgr.Destroy(context.Background(), live.RunID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	mgr.checkLapse(live) // the timer, had it been due just as Destroy stopped it
	if got := seen.all(); len(got) != 0 {
		t.Fatalf("lapse events %+v after destroy, want none", got)
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if live.Control != nil {
		t.Errorf("a destroyed machine still carries a lease: %+v", live.Control)
	}
}
