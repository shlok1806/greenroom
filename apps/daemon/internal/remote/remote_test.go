package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "0123456789abcdef0123456789abcdef"

// fakeDaemon is the daemon's contract as connect sees it: a real MCP server (stateless
// Streamable HTTP) on /mcp, /healthz and the sync route, all behind the bearer token.
type fakeDaemon struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	calls      []string // tools the MCP server ran
	syncs      []syncRequest
	auths      []string
	userAgents []string
	dropNext   bool // hang up on the next tools/call without answering
	forbid     bool // 403 everything, as a daemon with another public host would
	syncStatus int
	syncBody   string
}

type syncRequest struct {
	runID, dest, name, contentType string
	hasDest                        bool
	entries                        map[string]tarEntry
}

type tarEntry struct {
	typ   byte
	mode  int64
	mtime time.Time
	link  string
	body  string
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{t: t}
	server := mcp.NewServer(&mcp.Implementation{Name: "greenroom", Version: "9.9.9"},
		&mcp.ServerOptions{Instructions: "remote instructions", PageSize: 2})
	type echoIn struct {
		Text string `json:"text"`
	}
	type echoOut struct {
		Echo string `json:"echo"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text."},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, echoOut, error) {
			d.record("echo")
			return nil, echoOut{Echo: in.Text}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "Always a tool error."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			d.record("fail")
			return nil, struct{}{}, errors.New("machine abc is not ready")
		})
	mcp.AddTool(server, &mcp.Tool{Name: "machine_list", Description: "List machines."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			d.record("machine_list")
			return nil, struct{}{}, nil
		})
	type syncIn struct {
		RunID   string   `json:"runId"`
		Source  string   `json:"source"`
		Dest    string   `json:"dest,omitempty"`
		Exclude []string `json:"exclude,omitempty"`
	}
	type syncOut struct {
		Dest    string  `json:"dest"`
		Summary string  `json:"summary"`
		Seconds float64 `json:"seconds"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: SyncTool, Description: "Copy a host directory into the machine with rsync."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ syncIn) (*mcp.CallToolResult, syncOut, error) {
			d.record(SyncTool)
			return nil, syncOut{}, errors.New("forwarded machine_sync: the daemon cannot read this path")
		})

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok 0 machines\n") })
	mux.HandleFunc("PUT /api/runs/{runId}/sync", d.sync)
	d.srv = httptest.NewServer(d.guard(mux))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDaemon) record(tool string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, tool)
}

func (d *fakeDaemon) called() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *fakeDaemon) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.auths = append(d.auths, r.Header.Get("Authorization"))
		d.userAgents = append(d.userAgents, r.Header.Get("User-Agent"))
		forbid := d.forbid
		d.mu.Unlock()
		if forbid {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/mcp" && r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			d.mu.Lock()
			drop := d.dropNext && strings.Contains(string(body), `"tools/call"`)
			if drop {
				d.dropNext = false
			}
			d.mu.Unlock()
			if drop {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (d *fakeDaemon) sync(w http.ResponseWriter, r *http.Request) {
	req := syncRequest{
		runID:       r.PathValue("runId"),
		dest:        r.URL.Query().Get("dest"),
		name:        r.URL.Query().Get("name"),
		contentType: r.Header.Get("Content-Type"),
		entries:     map[string]tarEntry{},
	}
	_, req.hasDest = r.URL.Query()["dest"]
	gz, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, `{"error":"not gzip"}`, http.StatusBadRequest)
		return
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, `{"error":"bad tar"}`, http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(tr)
		req.entries[hdr.Name] = tarEntry{typ: hdr.Typeflag, mode: hdr.Mode, mtime: hdr.ModTime, link: hdr.Linkname, body: string(body)}
	}
	d.mu.Lock()
	d.syncs = append(d.syncs, req)
	status, body := d.syncStatus, d.syncBody
	d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}
	dest := req.dest
	if dest == "" {
		dest = "work/" + req.name
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"dest": dest, "summary": "sent 3 files", "seconds": 1.25})
}

func (d *fakeDaemon) cfg() Config { return Config{URL: d.srv.URL, Token: testToken} }

// connectClient runs connect's local server against d and returns an MCP client of it,
// connected the way Claude Code would be (over a pipe instead of stdio).
func connectClient(t *testing.T, d *fakeDaemon) (*mcp.ClientSession, *Remote) {
	t.Helper()
	ctx := context.Background()
	r, err := Dial(ctx, d.cfg(), Options{Version: "1.2.3"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	srv, err := NewServer(ctx, r)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, r
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestRemoteConnectMirrorsTheDaemonsToolsAndForwardsCalls(t *testing.T) {
	d := newFakeDaemon(t)
	cs, _ := connectClient(t, d)

	init := cs.InitializeResult()
	if init.ServerInfo.Name != "greenroom" || init.ServerInfo.Version != "9.9.9" {
		t.Errorf("server info = %+v, want the daemon's", init.ServerInfo)
	}
	if init.Instructions != "remote instructions" {
		t.Errorf("instructions = %q", init.Instructions)
	}

	// The daemon pages two tools at a time; connect must follow the cursor to get all four.
	byName := map[string]*mcp.Tool{}
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		byName[tool.Name] = tool
	}
	if len(byName) != 4 {
		t.Fatalf("tools = %v, want echo, fail, machine_list, machine_sync", byName)
	}
	echo := byName["echo"]
	if echo.Description != "Echo text." || echo.OutputSchema == nil {
		t.Errorf("echo = %+v, want the daemon's definition", echo)
	}
	props, _ := echo.InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := props["text"]; !ok {
		t.Errorf("echo input schema = %v, want the daemon's", echo.InputSchema)
	}
	if want := "Copy a host directory into the machine with rsync." + SyncNote; byName[SyncTool].Description != want {
		t.Errorf("machine_sync description = %q, want %q", byName[SyncTool].Description, want)
	}

	res := call(t, cs, "echo", map[string]any{"text": "hi"})
	if res.IsError || text(res) != `{"echo":"hi"}` {
		t.Errorf("echo = %+v %q", res, text(res))
	}
	if got, _ := json.Marshal(res.StructuredContent); string(got) != `{"echo":"hi"}` {
		t.Errorf("echo structured = %s", got)
	}
	res = call(t, cs, "fail", map[string]any{})
	if !res.IsError || !strings.Contains(text(res), "machine abc is not ready") {
		t.Errorf("fail = %+v %q, want the daemon's tool error unchanged", res, text(res))
	}
	if got := d.called(); strings.Join(got, ",") != "echo,fail" {
		t.Errorf("daemon ran %v", got)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for i, a := range d.auths {
		if a != "Bearer "+testToken {
			t.Errorf("request %d Authorization = %q", i, a)
		}
		if d.userAgents[i] != "greenroom-connect/1.2.3" {
			t.Errorf("request %d User-Agent = %q", i, d.userAgents[i])
		}
	}
}

func TestRemoteConnectRetriesAReadOnlyCallOnceWhenTheConnectionDrops(t *testing.T) {
	d := newFakeDaemon(t)
	cs, _ := connectClient(t, d)
	d.mu.Lock()
	d.dropNext = true
	d.mu.Unlock()
	res := call(t, cs, "machine_list", map[string]any{})
	if res.IsError {
		t.Fatalf("machine_list after a dropped connection = %q", text(res))
	}
	if got := d.called(); strings.Join(got, ",") != "machine_list" {
		t.Errorf("daemon ran %v, want one machine_list", got)
	}
}

// A call that changes something is never sent twice: the daemon may have acted on it.
func TestRemoteConnectDoesNotRetryACallThatActs(t *testing.T) {
	d := newFakeDaemon(t)
	cs, _ := connectClient(t, d)
	d.mu.Lock()
	d.dropNext = true
	d.mu.Unlock()
	res := call(t, cs, "echo", map[string]any{"text": "once"})
	if !res.IsError || !strings.Contains(text(res), "may or may not have run") {
		t.Fatalf("echo after a dropped connection = %q, want an error saying it may have run", text(res))
	}
	if got := d.called(); len(got) != 0 {
		t.Errorf("daemon ran %v, want nothing (the fake hangs up before running it)", got)
	}
	// The broken session is gone: the next call dials again and works.
	res = call(t, cs, "echo", map[string]any{"text": "next"})
	if res.IsError || text(res) != `{"echo":"next"}` {
		t.Fatalf("echo after the failed one = %q", text(res))
	}
}

func TestRemoteDialSurfacesARejectedToken(t *testing.T) {
	d := newFakeDaemon(t)
	cfg := d.cfg()
	cfg.Token = "wrong"
	_, err := Dial(context.Background(), cfg, Options{})
	if !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("Dial with a wrong token = %v, want ErrTokenRejected", err)
	}
}

// writeProject makes a project tree with something for every exclude rule.
func writeProject(t *testing.T) (string, time.Time) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "myapp")
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	files := map[string]os.FileMode{
		"src/main.go":               0o644,
		"run.sh":                    0o755,
		"node_modules/x/index.js":   0o644,
		"sub/node_modules/y/a.js":   0o644,
		".git/HEAD":                 0o644,
		"build/out.o":               0o644,
		"sub/build":                 0o644, // a file: "build/" is directories only
		"dist/app.js":               0o644, // "/dist" is the root's only
		"sub/dist/keep.js":          0o644,
		"debug.log":                 0o644,
		"sub/deep/trace.log":        0o644,
		"docs/tmp/scratch.md":       0o644,
		"docs/readme.md":            0o644,
		"other/docs/tmp/gone.md":    0o644, // "docs/tmp" matches at any component boundary
		"other/mydocs/tmp/stays.md": 0o644,
	}
	for name, mode := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content of "+name), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("src/main.go", filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src", filepath.Join(dir, "srclink")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, mtime
}

var testExcludes = []string{"node_modules", ".git", "build/", "/dist", "*.log", "docs/tmp"}

func TestRemoteMachineSyncUploadsTheLocalSourceInsteadOfForwarding(t *testing.T) {
	d := newFakeDaemon(t)
	cs, _ := connectClient(t, d)
	dir, mtime := writeProject(t)

	res := call(t, cs, SyncTool, map[string]any{"runId": "r1", "source": dir, "dest": "~/work/x", "exclude": testExcludes})
	if res.IsError {
		t.Fatalf("machine_sync: %s", text(res))
	}
	if got := d.called(); len(got) != 0 {
		t.Fatalf("daemon ran %v over MCP; machine_sync must never be forwarded", got)
	}
	want := map[string]any{"dest": "~/work/x", "summary": "sent 3 files", "seconds": 1.25}
	if got, _ := json.Marshal(res.StructuredContent); string(got) != mustJSON(want) {
		t.Errorf("structured = %s, want %s", got, mustJSON(want))
	}
	if text(res) != mustJSON(want) {
		t.Errorf("text = %q", text(res))
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.syncs) != 1 {
		t.Fatalf("sync requests = %d", len(d.syncs))
	}
	s := d.syncs[0]
	if s.runID != "r1" || s.dest != "~/work/x" || s.name != "myapp" || s.contentType != "application/gzip" {
		t.Errorf("sync request = %+v", s)
	}
	if d.auths[len(d.auths)-1] != "Bearer "+testToken {
		t.Errorf("upload Authorization = %q", d.auths[len(d.auths)-1])
	}

	var names []string
	for name := range s.entries {
		names = append(names, name)
	}
	wantNames := []string{
		"docs/", "docs/readme.md", "link.go", "other/", "other/docs/", "other/mydocs/", "other/mydocs/tmp/",
		"other/mydocs/tmp/stays.md", "run.sh", "src/", "src/main.go", "srclink", "sub/", "sub/build",
		"sub/deep/", "sub/dist/", "sub/dist/keep.js",
	}
	if got, want := strings.Join(sorted(names), " "), strings.Join(sorted(wantNames), " "); got != want {
		t.Errorf("entries:\n got %s\nwant %s", got, want)
	}
	if e := s.entries["link.go"]; e.typ != tar.TypeSymlink || e.link != "src/main.go" {
		t.Errorf("link.go = %+v, want a symlink to src/main.go", e)
	}
	if e := s.entries["srclink"]; e.typ != tar.TypeSymlink || e.link != "src" {
		t.Errorf("srclink = %+v, want a symlink, not descended", e)
	}
	if e := s.entries["run.sh"]; e.mode&0o777 != 0o755 || !e.mtime.Equal(mtime) || e.body != "content of run.sh" {
		t.Errorf("run.sh = %+v, want mode 755, mtime %v and its content", e, mtime)
	}
	if e := s.entries["src/main.go"]; e.mode&0o777 != 0o644 || !e.mtime.Equal(mtime) {
		t.Errorf("src/main.go = %+v", e)
	}
	if e := s.entries["src/"]; e.typ != tar.TypeDir {
		t.Errorf("src/ = %+v, want a directory", e)
	}
}

func TestRemoteMachineSyncOmitsAnUnsetDest(t *testing.T) {
	d := newFakeDaemon(t)
	cs, _ := connectClient(t, d)
	dir, _ := writeProject(t)
	res := call(t, cs, SyncTool, map[string]any{"runId": "r2", "source": dir})
	if res.IsError {
		t.Fatalf("machine_sync: %s", text(res))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.syncs[0]; s.hasDest || s.name != "myapp" {
		t.Errorf("sync request = %+v, want no dest and name myapp", s)
	}
	if _, ok := d.syncs[0].entries["node_modules/"]; !ok {
		t.Errorf("with no excludes node_modules should be sent")
	}
}

func TestRemoteMachineSyncReportsTheDaemonsRefusal(t *testing.T) {
	d := newFakeDaemon(t)
	cs, _ := connectClient(t, d)
	dir, _ := writeProject(t)
	d.mu.Lock()
	d.syncStatus, d.syncBody = http.StatusConflict, `{"error":"machine r3 is booting: call machine_wait and try again"}`
	d.mu.Unlock()
	res := call(t, cs, SyncTool, map[string]any{"runId": "r3", "source": dir})
	if !res.IsError || !strings.Contains(text(res), "HTTP 409") || !strings.Contains(text(res), "call machine_wait and try again") {
		t.Errorf("result = %q, want a tool error with the status and the daemon's message", text(res))
	}
}

func TestRemoteMachineSyncRefusesARelativeOrMissingSource(t *testing.T) {
	d := newFakeDaemon(t)
	cs, _ := connectClient(t, d)
	for _, source := range []string{"myapp", filepath.Join(t.TempDir(), "absent")} {
		res := call(t, cs, SyncTool, map[string]any{"runId": "r4", "source": source})
		if !res.IsError || !strings.Contains(text(res), "source") {
			t.Errorf("source %q: result = %q, want a tool error", source, text(res))
		}
	}
	if len(d.called()) != 0 {
		t.Errorf("a refused sync reached the daemon")
	}
}

func TestRemoteExcludes(t *testing.T) {
	x := ParseExcludes(testExcludes)
	for _, c := range []struct {
		rel   string
		dir   bool
		match bool
	}{
		{"node_modules", true, true},
		{"a/b/node_modules", true, true},
		{".git", true, true},
		{"build", true, true},
		{"build", false, false},
		{"dist", true, true},
		{"a/dist", true, false},
		{"x.log", false, true},
		{"a/x.log", false, true},
		{"x.logs", false, false},
		{"docs/tmp", true, true},
		{"a/docs/tmp", true, true},
		{"a/mydocs/tmp", true, false},
		{"src", true, false},
	} {
		if got := x.Match(c.rel, c.dir); got != c.match {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", c.rel, c.dir, got, c.match)
		}
	}
}

func TestRemoteConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "client.json")
	if err := os.WriteFile(file, []byte(`{"url":"https://file.example/","token":"file-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	cfg, err := LoadConfig("", "", file, false, getenv)
	if err != nil || cfg.URL != "https://file.example" || cfg.Token != "file-token" {
		t.Errorf("file only = %+v, %v", cfg, err)
	}
	env[EnvURL], env[EnvToken] = "https://env.example", "env-token"
	cfg, err = LoadConfig("", "", file, false, getenv)
	if err != nil || cfg.URL != "https://env.example" || cfg.Token != "env-token" {
		t.Errorf("env over file = %+v, %v", cfg, err)
	}
	cfg, err = LoadConfig("https://flag.example//", "flag-token", file, false, getenv)
	if err != nil || cfg.URL != "https://flag.example" || cfg.Token != "flag-token" {
		t.Errorf("flags over env = %+v, %v", cfg, err)
	}
	// Each field on its own: a flag url and the env token.
	cfg, err = LoadConfig("https://flag.example", "", file, false, getenv)
	if err != nil || cfg.URL != "https://flag.example" || cfg.Token != "env-token" {
		t.Errorf("mixed = %+v, %v", cfg, err)
	}

	missing := filepath.Join(dir, "absent.json")
	if _, err := LoadConfig("", "", missing, false, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "no daemon url") {
		t.Errorf("nothing configured: %v", err)
	}
	if _, err := LoadConfig("https://x.example", "", missing, false, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("no token: %v", err)
	}
	if _, err := LoadConfig("https://x.example", "t", missing, true, func(string) string { return "" }); err == nil {
		t.Errorf("an explicit -config that does not exist must fail")
	}
	if _, err := LoadConfig("x.example", "t", missing, false, func(string) string { return "" }); err == nil {
		t.Errorf("a url with no scheme must fail")
	}
}

func TestRemoteCheck(t *testing.T) {
	d := newFakeDaemon(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := Check(ctx, d.cfg(), Options{}, &out); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := "ok: " + d.srv.URL + " (4 tools)\n"; out.String() != want {
		t.Errorf("Check printed %q, want %q", out.String(), want)
	}

	bad := d.cfg()
	bad.Token = "wrong"
	if err := Check(ctx, bad, Options{}, io.Discard); !errors.Is(err, ErrTokenRejected) {
		t.Errorf("wrong token: %v, want ErrTokenRejected", err)
	}

	d.mu.Lock()
	d.forbid = true
	d.mu.Unlock()
	if err := Check(ctx, d.cfg(), Options{}, io.Discard); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("forbidden: %v, want a 403", err)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if err := Check(ctx, Config{URL: closed.URL, Token: testToken}, Options{}, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot connect") {
		t.Errorf("nothing listening: %v, want cannot connect", err)
	}

	if err := Check(ctx, Config{URL: "https://greenroom.invalid", Token: testToken}, Options{}, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot resolve") {
		t.Errorf("unknown host: %v, want cannot resolve", err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
