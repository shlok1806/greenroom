package verifier

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// TurnRetryDelays is the wait before each retry of a turn that failed. Its
// length is the number of retries, so a turn is attempted
// len(TurnRetryDelays)+1 times. The model is hosted and the transport is the
// internet: a turn that died on a bad minute must be taken again, because
// whoever spoke is otherwise never answered. Tests zero it.
var TurnRetryDelays = []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second}

// Actors keeps one verifier goroutine alive per run. Each one waits on the
// run's conversation, takes a turn when a message needs one, and exits when
// the machine is destroyed. One turn runs at a time per run.
//
// The turn always runs. Whether the machine is booting, ready or dead is the
// verifier's to say, not the actor's: a human asking "how is the boot going"
// or "what happened" is owed an answer either way (ADR 0006).
type Actors struct {
	brain Brain
	mgr   *machine.Manager
	reg   *session.Registry
	log   *slog.Logger

	mu     sync.Mutex
	cancel map[string]context.CancelFunc
	done   map[string]chan struct{}
}

// ActorOption configures Actors beyond its required arguments.
type ActorOption func(*Actors)

// WithLogger gives Actors its own logger. Without it, Actors logs to
// slog.Default(), which is enough for a test but not for the daemon, which
// passes its own logger so a run's actor logs land next to everything else.
func WithLogger(log *slog.Logger) ActorOption {
	return func(a *Actors) { a.log = log }
}

// NewActors wires a Brain, the model-driven Verifier or the human-driven
// Manual, to the manager's lifecycle: a created machine gets an actor, a
// destroyed one loses it, and runs the daemon reattached on start get theirs
// back.
//
// A boot failure does not end the actor. The run is still a conversation a
// human can join to ask what happened, and only the brain can answer.
func NewActors(b Brain, mgr *machine.Manager, reg *session.Registry, opts ...ActorOption) *Actors {
	a := &Actors{brain: b, mgr: mgr, reg: reg, log: slog.Default(),
		cancel: map[string]context.CancelFunc{}, done: map[string]chan struct{}{}}
	for _, opt := range opts {
		opt(a)
	}
	mgr.Listen(func(ev machine.LifecycleEvent) {
		switch ev.Kind {
		case "created":
			a.Start(ev.RunID)
		case "destroyed":
			a.Stop(ev.RunID)
		}
	})
	for _, mc := range mgr.List() {
		a.Start(mc.RunID)
	}
	// After a restart the manager holds only the machines tart still lists,
	// so a run whose boot failed would lose its voice. Every run directory
	// that was never destroyed keeps one; a finished run gets none.
	ids, err := reg.RunIDs()
	if err != nil {
		a.log.Error("verifier cannot list runs", "err", err)
		return a
	}
	for _, id := range ids {
		man, err := machine.ReadManifest(mgr.RunDir(id))
		if err != nil || man.DestroyedAt != nil {
			continue
		}
		a.Start(id)
	}
	return a
}

// Start begins an actor for runID if none is running.
func (a *Actors) Start(runID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.cancel[runID]; ok {
		return
	}
	store, err := a.reg.Get(runID)
	if err != nil {
		a.log.Error("verifier cannot open conversation", "runId", runID, "err", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.cancel[runID], a.done[runID] = cancel, done
	go a.loop(ctx, runID, store, done)
}

// Stop ends the actor for runID and waits for its current turn to unwind.
func (a *Actors) Stop(runID string) {
	a.mu.Lock()
	cancel, ok := a.cancel[runID]
	done := a.done[runID]
	delete(a.cancel, runID)
	delete(a.done, runID)
	a.mu.Unlock()
	if ok {
		cancel()
		<-done
	}
}

// Running reports whether runID has an actor.
func (a *Actors) Running(runID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.cancel[runID]
	return ok
}

// loop is the actor. It resumes from wherever the transcript left off: if
// the last turn-starting message has no reply yet, that turn runs now, which
// is how a daemon restart mid-conversation picks the work back up.
func (a *Actors) loop(ctx context.Context, runID string, store *session.Store, done chan struct{}) {
	defer close(done)
	seen := 0
	if pending := lastUnanswered(store.After(0)); pending > 0 {
		seen = pending - 1
	} else {
		seen = store.Len()
	}
	for {
		msgs := store.Wait(ctx, seen)
		if ctx.Err() != nil {
			return
		}
		start := false
		for _, m := range msgs {
			if m.StartsTurn() {
				start = true
			}
		}
		seen = store.Len()
		if !start {
			continue
		}
		a.runTurn(ctx, runID, store, &seen)
	}
}

// runTurn takes the turn the messages after seen have earned, and takes it
// again if it fails. A failed turn is the whole conversation stuck: the
// actor would otherwise sit waiting for a message nobody knows is needed. It
// gives up when a newer turn-starting message makes the old turn moot (that
// message's own turn runs next), or after TurnRetryDelays is spent, which it
// says in the transcript so a human knows the turn is theirs to restart.
//
// seen is advanced past the turn's own posts, which are not new work.
func (a *Actors) runTurn(ctx context.Context, runID string, store *session.Store, seen *int) {
	attempts := len(TurnRetryDelays) + 1
	for attempt := 1; ; attempt++ {
		_, err := a.brain.Turn(ctx, runID, store)
		if ctx.Err() != nil {
			*seen = store.Len()
			return
		}
		if err == nil {
			*seen = store.Len()
			return
		}
		a.log.Warn("verifier turn failed", "runId", runID, "attempt", attempt, "err", err)
		if newer := firstStarterAfter(store, *seen); newer > 0 {
			// Someone has spoken since. Their message starts the turn that
			// matters now, so leave it unseen for the loop to pick up.
			*seen = newer - 1
			return
		}
		if attempt >= attempts {
			appendMessage(a.log, store, session.Message{From: session.System, Kind: session.Event,
				Text: fmt.Sprintf("verifier gave up on this turn after %d attempts; send another message to try again", attempts)})
			*seen = store.Len()
			return
		}
		appendMessage(a.log, store, session.Message{From: session.System, Kind: session.Event,
			Text: fmt.Sprintf("verifier retrying the turn (attempt %d of %d)", attempt+1, attempts)})
		awaitRetry(ctx, store, *seen, TurnRetryDelays[attempt-1])
		if ctx.Err() != nil {
			*seen = store.Len()
			return
		}
	}
}

// awaitRetry waits d before the next attempt, and returns early when a new
// turn-starting message lands, because that turn is worth more than this
// retry and nobody should wait two minutes to be heard.
func awaitRetry(ctx context.Context, store *session.Store, seen int, d time.Duration) {
	wctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	at := store.Len()
	for wctx.Err() == nil {
		store.Wait(wctx, at)
		at = store.Len()
		if firstStarterAfter(store, seen) > 0 {
			return
		}
	}
}

// firstStarterAfter returns the seq of the first turn-starting message after
// seq, or 0.
func firstStarterAfter(store *session.Store, seq int) int {
	for _, m := range store.After(seq) {
		if m.StartsTurn() {
			return m.Seq
		}
	}
	return 0
}

// lastUnanswered returns the seq of the last turn-starting message that has
// no verifier reply after it, or 0.
func lastUnanswered(msgs []session.Message) int {
	pending := 0
	for _, m := range msgs {
		switch {
		case m.StartsTurn():
			pending = m.Seq
		case m.EndsTurn():
			pending = 0
		}
	}
	return pending
}
