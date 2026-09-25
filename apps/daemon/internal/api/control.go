package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// humanSeat holds every lease taken through this API: one seat per participant kind, not per window (ADR 0009).
const humanSeat = "human"

// controlOut is every control route's answer: the lease, and the screen its fractional coordinates refer to.
type controlOut struct {
	Control *machine.Control `json:"control"`
	Screen  *machine.Screen  `json:"screen,omitempty"`
}

func (a *api) screenshot(w http.ResponseWriter, r *http.Request, id string) {
	_, shot, err := a.mgr.Screenshot(r.Context(), id)
	if err != nil {
		a.failMachine(w, id, err)
		return
	}
	a.event(id, fmt.Sprintf("human took a screenshot (step %d)", shot.Step))
	writeJSON(w, http.StatusOK, shot)
}

func (a *api) takeControl(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		TTLSeconds int  `json:"ttlSeconds"`
		Renew      bool `json:"renew"` // extend the lease this seat holds; never take a new one (issue #100)
	}
	_ = json.NewDecoder(r.Body).Decode(&in) // an empty body takes the default lease
	if in.Renew {
		c, err := a.mgr.RenewControl(id, humanSeat)
		if err != nil {
			a.failControl(w, id, err)
			return
		}
		writeJSON(w, http.StatusOK, controlOut{Control: &c})
		return
	}
	// A lease this replaces because it lapsed is announced by the manager, and posted by New's
	// listener, before TakeControl returns.
	c, fresh, err := a.mgr.TakeControl(id, humanSeat, time.Duration(in.TTLSeconds)*time.Second)
	if err != nil {
		a.failControl(w, id, err)
		return
	}
	// Warm the guest input helper now (a compile of seconds) so the first click after this answer is not slow.
	screen, err := a.mgr.ScreenOf(r.Context(), id)
	if err != nil {
		if fresh { // a lease the human already held stays theirs
			_, _, _ = a.mgr.ReleaseControl(id, humanSeat)
		}
		a.fail(w, http.StatusConflict, err)
		return
	}
	if fresh {
		a.controlEvent(id, "human took control of the screen", session.ControlTaken)
	}
	writeJSON(w, http.StatusOK, controlOut{Control: &c, Screen: &screen})
}

func (a *api) releaseControl(w http.ResponseWriter, _ *http.Request, id string) {
	c, held, err := a.mgr.ReleaseControl(id, humanSeat)
	if err != nil {
		a.failControl(w, id, err)
		return
	}
	// A lapsed lease is not held: the manager announced its lapse instead.
	if held && c.Holder == humanSeat {
		a.controlEvent(id, "human gave the screen back after "+actionCount(c.Actions), session.ControlReturned)
	}
	writeJSON(w, http.StatusOK, controlOut{})
}

// lapsedText records a human lease that expired, so the transcript never shows two takes in a row.
func lapsedText(c machine.Control) string {
	return fmt.Sprintf("human lost control of the screen after %s: the lease lapsed with no input or renewal for %d s",
		actionCount(c.Actions), int(c.TTL().Seconds()))
}

func actionCount(n int) string {
	if n == 1 {
		return "1 action"
	}
	return fmt.Sprintf("%d actions", n)
}

// input posts one batch of actions; coordinates are screen fractions because the app sees a scaled frame (ADR 0009).
func (a *api) input(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Actions []machine.InputAction `json:"actions"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if len(in.Actions) == 0 {
		a.fail(w, http.StatusBadRequest, errors.New("actions is empty"))
		return
	}
	res, err := a.mgr.Input(r.Context(), id, humanSeat, in.Actions)
	if err != nil {
		a.failControl(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// failControl maps lease errors: 423 someone else is driving, 409 take control first or the machine is gone.
func (a *api) failControl(w http.ResponseWriter, runID string, err error) {
	switch {
	case errors.Is(err, machine.ErrControlHeld):
		a.fail(w, http.StatusLocked, err)
	case errors.Is(err, machine.ErrNoControl):
		a.fail(w, http.StatusConflict, err)
	default:
		a.failMachine(w, runID, err)
	}
}

func (a *api) destroy(w http.ResponseWriter, r *http.Request, id string) {
	if err := a.mgr.Destroy(r.Context(), id); err != nil {
		a.failMachine(w, id, err)
		return
	}
	// Posted after the destroy succeeds; the lifecycle bridge in main.go posts its own "machine destroyed".
	a.event(id, "human destroyed the machine")
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{true})
}
