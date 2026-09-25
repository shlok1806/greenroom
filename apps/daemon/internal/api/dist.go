package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// distTypes maps an install file's extension to its type; anything else is a download.
var distTypes = map[string]string{
	".sh":     "text/x-shellscript; charset=utf-8",
	".zip":    "application/zip",
	".gz":     "application/gzip",
	".json":   "application/json",
	".txt":    "text/plain; charset=utf-8",
	".sha256": "text/plain; charset=utf-8",
}

// Dist serves the client installer from dir (ADR 0021): GET /install.sh is dir/install.sh and
// GET /dl/{file} is dir/{file} for a bare file name. Guard lets both through the public host
// without a token, so nothing but these files may be reachable here. Mount it on both patterns.
func Dist(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "install.sh"
		if r.URL.Path != "/install.sh" {
			name = r.PathValue("file")
		}
		if !bareName(name) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		ct := distTypes[strings.ToLower(filepath.Ext(name))]
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		// A rebuilt binary or app keeps its name, so a cached copy would fail its checksum.
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, name, st.ModTime(), f)
	})
}
