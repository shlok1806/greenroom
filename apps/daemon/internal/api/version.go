package api

import (
	"net/http"

	"github.com/shlok1806/greenroom/apps/daemon/internal/buildinfo"
)

// Version is what GET /api/version answers (root ADR 0033): which build is running and what it
// runs with, so the Companion can say whether it is behind main and whether it matches the app.
// Read only: there is no route that updates the daemon, since nothing reachable over the
// network, the tunnel included, may make the host build or run code.
type Version struct {
	// Version is the MCP server's own version string (mcpserver.Version).
	Version string `json:"version"`
	// Commit, Dirty and BuiltAt as install.sh stamped them; empty for an unstamped build.
	buildinfo.Info
	// InputHelper and ImageRecipe are the versions this build expects of an image
	// (machine.InputHelperVersion, machine.ImageRecipeVersion).
	InputHelper int `json:"inputHelper"`
	ImageRecipe int `json:"imageRecipe"`
	// Verifier is the brain that answers runs: "nim", "manual", or "none" (nim with no key).
	Verifier string `json:"verifier"`
	// VerifierModel and VisionModel are the models the nim verifier uses; empty otherwise, and
	// VisionModel is empty when the verifier works without seeing the screen.
	VerifierModel string `json:"verifierModel"`
	VisionModel   string `json:"visionModel"`
	// Checkout is the repository checkout the daemon was installed from (GREENROOM_CHECKOUT,
	// which install.sh writes into the launchd job); empty when it was not installed that way.
	Checkout string `json:"checkout"`
}

// VersionHandler answers GET /api/version with v. The daemon mounts it behind Guard like every
// route, so the public host needs the token for it.
func VersionHandler(v Version) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, v)
	})
}
