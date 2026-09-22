package verifier

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// TurnRetryDelays is the wait before each retry of a failed turn, so a turn
// is attempted len(TurnRetryDelays)+1 times. Tests zero it.
var TurnRetryDelays = []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second}

// Actors keeps one verifier goroutine per run, taking one turn at a time
// whatever state the machine is in (ADR 0006), until the machine is destroyed.
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

// WithLogger sets the logger; the default is slog.Default().
func WithLogger(log *slog.Logger) ActorOption {
	return func(a *Actors) { a.log = log }
}

// NewActors wires b to the manager's lifecycle: created machines gain an
// actor, destroyed ones lose it, and every undestroyed run gets one at start.
// A failed boot keeps its actor so a human can still ask what happened.
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
	// The manager forgets machines tart no longer lists, so also start one
	// for every run directory that was never destroyed.
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

// loop is the actor. If the last turn-starting message is unanswered, as
// after a daemon restart mid-conversation, that turn runs first.
func (a *Actors) loop(ctx context.Context, runID string, store *session.Store, done chan struct{}) {
	defer close(done)
	seen := store.Len()
	if pending := lastUnanswered(store.After(0)); pending > 0 {
		seen = pending - 1
	}
	for {
		msgs := store.Wait(ctx, seen)
		if ctx.Err() != nil {
			return
		}
		seen = store.Len()
		if slices.ContainsFunc(msgs, session.Message.StartsTurn) {
			a.runTurn(ctx, runID, store, &seen)
		}
	}
}

// runTurn takes a turn, retrying on failure until it succeeds, a newer
// turn-starting message makes it moot, or TurnRetryDelays is spent (which it
// announces, so the conversation is never silently stuck). It advances seen
// past the turn's own posts.
func (a *Actors) runTurn(ctx context.Context, runID string, store *session.Store, seen *int) {
	attempts := len(TurnRetryDelays) + 1
	for attempt := 1; ; attempt++ {
		_, err := a.brain.Turn(ctx, runID, store)
		if err == nil || ctx.Err() != nil {
			*seen = store.Len()
			return
		}
		a.log.Warn("verifier turn failed", "runId", runID, "attempt", attempt, "err", err)
		if newer := firstStarterAfter(store, *seen); newer > 0 {
			// Leave the newer message unseen; its turn runs next.
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

// awaitRetry waits d, returning early if a new turn-starting message lands.
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
