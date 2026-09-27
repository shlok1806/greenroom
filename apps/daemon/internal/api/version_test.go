package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/shlok1806/greenroom/apps/daemon/internal/buildinfo"
)

// The Companion decodes this shape (root ADR 0033); a renamed or dropped key breaks its
// builds section without a compile error on either side.
func TestVersionAnswersItsShape(t *testing.T) {
	v := Version{
		Version:       "0.0.2",
		Info:          buildinfo.Info{Commit: "abc1234", Dirty: true, BuiltAt: "2026-09-27T10:00:00Z"},
		InputHelper:   7,
		ImageRecipe:   2,
		Verifier:      "nim",
		VerifierModel: "brain/model",
		VisionModel:   "vision/model",
		Checkout:      "/Users/me/greenroom",
	}
	res := httptest.NewRecorder()
	VersionHandler(v).ServeHTTP(res, httptest.NewRequest("GET", "/api/version", nil))
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %q", res.Code, res.Header().Get("Content-Type"))
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"version": "0.0.2", "commit": "abc1234", "dirty": true, "builtAt": "2026-09-27T10:00:00Z",
		"inputHelper": float64(7), "imageRecipe": float64(2), "verifier": "nim",
		"verifierModel": "brain/model", "visionModel": "vision/model", "checkout": "/Users/me/greenroom",
	}
	if !reflect.DeepEqual(got, want) {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("got %v (keys %v), want %v", got, keys, want)
	}
}

// An unstamped build still answers every key, empty, so a client never mistakes a missing
// key for an old daemon.
func TestAnUnstampedVersionKeepsEveryKey(t *testing.T) {
	res := httptest.NewRecorder()
	VersionHandler(Version{Verifier: "none"}).ServeHTTP(res, httptest.NewRequest("GET", "/api/version", nil))
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "commit", "dirty", "builtAt", "inputHelper", "imageRecipe", "verifier", "verifierModel", "visionModel", "checkout"} {
		if _, ok := got[key]; !ok {
			t.Errorf("no %q in %v", key, got)
		}
	}
	if got["commit"] != "" || got["dirty"] != false {
		t.Errorf("unstamped: %v", got)
	}
}
