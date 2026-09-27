package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shlok1806/greenroom/apps/daemon/internal/report"
)

// Option configures New beyond its required arguments.
type Option func(*api)

// WithModels names the verifier's models now, which a run report names for a run whose
// manifest recorded none (ADR 0034, runs from before issue #154).
func WithModels(m report.Models) Option { return func(a *api) { a.models = m } }

// runReport is GET /api/runs/{id}/report?format=md|json[&embed=true] (ADR 0034): the run's proof,
// the same report run_report and run_finish return. Through the public host its screenshots link
// to the artifact route there; locally, to their paths on this host.
func (a *api) runReport(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	format := strings.ToLower(q.Get("format"))
	if format == "" {
		format = "md"
	}
	if format != "md" && format != "json" {
		a.fail(w, http.StatusBadRequest, fmt.Errorf("format must be md or json, not %q", q.Get("format")))
		return
	}
	embed := false
	if v := q.Get("embed"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			a.fail(w, http.StatusBadRequest, fmt.Errorf("embed must be true or false, not %q", v))
			return
		}
		embed = b
	}
	store, err := a.reg.Get(id)
	if err != nil {
		a.fail(w, http.StatusNotFound, err)
		return
	}
	links := report.Links{Embed: embed}
	if FromPublicHost(r.Context()) {
		links.BaseURL = "https://" + r.Host
	}
	rep, err := report.FromStore(a.mgr.RunDir(id), store, a.models, links)
	if err != nil {
		a.fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(rep.Markdown()))
}
