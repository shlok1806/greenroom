package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

type transcriptOut struct {
	Messages []session.Message    `json:"messages"`
	Last     int                  `json:"last"`
	Verdict  session.VerdictState `json:"verdict"`
}

func (a *api) readMessages(w http.ResponseWriter, r *http.Request, id string) {
	after := 0
	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			a.fail(w, http.StatusBadRequest, fmt.Errorf("after must be a number, not %q", s))
			return
		}
		after = n
	}
	store, err := a.reg.Get(id)
	if err != nil {
		a.fail(w, http.StatusNotFound, err)
		return
	}
	msgs := store.After(after)
	last := after
	if n := len(msgs); n > 0 {
		last = msgs[n-1].Seq
	} else if after == 0 {
		last = store.Len()
	}
	writeJSON(w, http.StatusOK, transcriptOut{Messages: msgs, Last: last, Verdict: store.Verdict()})
}

func (a *api) postMessage(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Kind    string `json:"kind"`
		Text    string `json:"text"`
		ReplyTo int    `json:"replyTo"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	store, err := a.reg.Get(id)
	if err != nil {
		a.fail(w, http.StatusNotFound, err)
		return
	}
	m, err := store.Append(session.Message{From: session.Human, Kind: session.Kind(in.Kind), Text: in.Text, ReplyTo: in.ReplyTo})
	switch {
	case errors.Is(err, session.ErrContested):
		a.fail(w, http.StatusConflict, err)
		return
	case err != nil:
		a.fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Seq int       `json:"seq"`
		At  time.Time `json:"at"`
	}{m.Seq, m.At})
}
