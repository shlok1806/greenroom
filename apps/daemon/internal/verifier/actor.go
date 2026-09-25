package verifier

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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

	// ended serializes answerEnded, so a message is told nothing will answer it once.
	ended sync.Mutex
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
			a.answerEnded(ev.RunID)
		}
	})
	reg.Listen(a.answerEndedRuns)
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

// destroyedNotice starts the event that answers a turn-starting message on a destroyed run.
const destroyedNotice = "this run's machine was destroyed, so the verifier has stopped and nothing will answer this "

// answerEndedRuns tells whoever starts a turn on a run whose machine is gone that nothing will
// answer, so a coder's agent_wait returns instead of looping forever (issue #33). A run with an
// actor, such as a failed boot, is answered by it.
func (a *Actors) answerEndedRuns(runID string, m session.Message) {
	if !m.StartsTurn() || a.Running(runID) {
		return
	}
	// Listeners run under the store's lock, so append elsewhere.
	go a.answerEnded(runID)
}

// answerEnded posts the destroyed notice for the last unanswered turn-starting message on a
// destroyed run, unless it already has one. It covers messages that land after the actor is gone
// and turns that were queued or cut short when Stop cancelled the actor.
func (a *Actors) answerEnded(runID string) {
	a.ended.Lock()
	defer a.ended.Unlock()
	man, err := machine.ReadManifest(a.mgr.RunDir(runID))
	if err != nil || man.DestroyedAt == nil {
		return
	}
	store, err := a.reg.Get(runID)
	if err != nil {
		return
	}
	msgs := store.After(0)
	pending := lastUnanswered(msgs)
	if pending == 0 {
		return
	}
	var kind session.Kind
	for _, m := range msgs {
		switch {
		case m.Seq == pending:
			kind = m.Kind
		case m.Seq > pending && m.From == session.System && m.Kind == session.Event && strings.HasPrefix(m.Text, destroyedNotice):
			return
		}
	}
	appendMessage(a.log, store, session.Message{From: session.System, Kind: session.Event, Text: destroyedNotice + string(kind)})
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
	go a.loop(ctx, runID, store, done, startSeen(store))
}

// startSeen is where a new actor starts reading: past everything already there, or just before
// the last turn-starting message still unanswered, as after a daemon restart mid-conversation.
// It is read before Start returns, so a message appended after that is never skipped (a Give
// Back right after the actor started was, issue #124).
func startSeen(store *session.Store) int {
	if pending := lastUnanswered(store.After(0)); pending > 0 {
		return pending - 1
	}
	return store.Len()
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

// loop is the actor, reading from seen (startSeen). If the last turn-starting message is
// unanswered, as after a daemon restart mid-conversation, that turn runs first.
func (a *Actors) loop(ctx context.Context, runID string, store *session.Store, done chan struct{}, seen int) {
	defer close(done)
	for {
		msgs := store.Wait(ctx, seen)
		if ctx.Err() != nil {
			return
		}
		if len(msgs) == 0 {
			// The store was evicted on destroy, and Stop follows.
			<-ctx.Done()
			return
		}
		seen = store.Len()
		if slices.ContainsFunc(msgs, session.Message.StartsTurn) || a.resumes(msgs, store) {
			a.runTurn(ctx, runID, store, &seen)
		}
	}
}

// resumer is a Brain that picks up by itself when the screen comes back (issue #124).
type resumer interface {
	resumeOwed(all []session.Message) bool
}

// resumes reports whether msgs give the screen back to a brain that stopped for it. It is asked
// only between turns: a handover during a turn reaches that turn as a late message, and seen
// then moves past it, so giving back and a message sent right after it make one turn.
func (a *Actors) resumes(msgs []session.Message, store *session.Store) bool {
	r, ok := a.brain.(resumer)
	if !ok || !slices.ContainsFunc(msgs, func(m session.Message) bool {
		return m.Kind == session.Event && m.Control == session.ControlReturned
	}) {
		return false
	}
	return r.resumeOwed(store.After(0))
}

// runTurn takes a turn, retrying on failure until it succeeds, a newer
// turn-starting message makes it moot, or TurnRetryDelays is spent (which it
// announces, so the conversation is never silently stuck). It advances seen
// past the turn's own posts.
func (a *Actors) runTurn(ctx context.Context, runID string, store *session.Store, seen *int) {
	attempts := len(TurnRetryDelays) + 1
	for attempt := 1; ; attempt++ {
		a.mgr.SetVerifierTurn(runID, true)
		_, err := a.brain.Turn(ctx, runID, store)
		a.mgr.SetVerifierTurn(runID, false)
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
				Text: fmt.Sprintf("verifier gave up on this turn after %d attempts; send a task, answer or dispute to try again. "+
					"A human's note also starts a turn; a coding agent's note does not start a turn.", attempts)})
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
		if len(store.Wait(wctx, at)) == 0 {
			<-wctx.Done() // timed out, or the store was evicted
			return
		}
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
