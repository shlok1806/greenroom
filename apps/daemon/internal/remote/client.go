package remote

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrTokenRejected is a 401 from the daemon: the token is wrong or missing.
var ErrTokenRejected = errors.New("token rejected (HTTP 401): check the token in client.json or " + EnvToken)

// Options are what the caller knows that the config does not.
type Options struct {
	Version string       // this binary's version, for the User-Agent and the MCP client info
	Logger  *slog.Logger // stderr only: stdout is the MCP channel
}

// Remote is an MCP client session with the daemon that re-dials when the connection breaks.
type Remote struct {
	cfg    Config
	opts   Options
	http   *http.Client
	auth   *authTransport
	mu     sync.Mutex
	cs     *mcp.ClientSession
	closed bool
}

// Dial connects to the daemon's MCP endpoint at <url>/mcp.
func Dial(ctx context.Context, cfg Config, opts Options) (*Remote, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	auth := &authTransport{base: base, token: cfg.Token, userAgent: "greenroom-connect/" + opts.Version}
	r := &Remote{cfg: cfg, opts: opts, auth: auth, http: &http.Client{Transport: auth}}
	cs, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	r.cs = cs
	return r, nil
}

// HTTPClient carries the token and User-Agent on every request.
func (r *Remote) HTTPClient() *http.Client { return r.http }

// Config is what this Remote was dialled with.
func (r *Remote) Config() Config { return r.cfg }

// InitializeResult is the daemon's answer to initialize: its name, version and instructions.
func (r *Remote) InitializeResult() *mcp.InitializeResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cs == nil {
		return nil
	}
	return r.cs.InitializeResult()
}

// Tools lists every tool the daemon offers, following pagination.
func (r *Remote) Tools(ctx context.Context) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	err := r.withSession(ctx, func(cs *mcp.ClientSession) error {
		tools = nil
		for t, err := range cs.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			tools = append(tools, t)
		}
		return nil
	})
	return tools, err
}

// CallTool forwards one call. A tool error comes back as the daemon's result; an error
// the daemon answered (JSON-RPC) comes back unchanged. A transport failure is retried once
// on a fresh connection only for a tool that just reads: the daemon may already have acted
// on a call whose answer was lost, and a second machine_create or input batch is worse than
// an error. Any other call's failure says so, and the next call dials again.
func (r *Remote) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	var res *mcp.CallToolResult
	call := func(cs *mcp.ClientSession) error {
		var err error
		res, err = cs.CallTool(ctx, params)
		return err
	}
	if readOnlyTools[params.Name] {
		return res, r.withSession(ctx, call)
	}
	cs, err := r.session(ctx, nil)
	if err != nil {
		return nil, err
	}
	err = call(cs)
	if err != nil && retryable(ctx, err) {
		r.drop(cs)
		return nil, fmt.Errorf("%w; it may or may not have run on the daemon, so check (machine_list, agent_transcript) before calling it again", r.explain(err))
	}
	return res, r.explain(err)
}

// readOnlyTools change nothing on the daemon, so running one twice is harmless.
var readOnlyTools = map[string]bool{
	"machine_list": true, "machine_wait": true, "machine_exec_wait": true, "machine_screenshot": true,
	"machine_ui": true, "machine_session_read": true, "agent_wait": true, "agent_transcript": true,
}

// drop forgets broken so the next call dials a new session.
func (r *Remote) drop(broken *mcp.ClientSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cs == broken {
		_ = r.cs.Close()
		r.cs = nil
	}
}

// Close ends the session.
func (r *Remote) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.cs == nil {
		return nil
	}
	err := r.cs.Close()
	r.cs = nil
	return err
}

// withSession runs f on the current session and, when it fails below the protocol, once
// more on a new one.
func (r *Remote) withSession(ctx context.Context, f func(*mcp.ClientSession) error) error {
	cs, err := r.session(ctx, nil)
	if err != nil {
		return err
	}
	err = f(cs)
	if err == nil || !retryable(ctx, err) {
		return r.explain(err)
	}
	r.opts.Logger.Warn("daemon connection failed; dialling again", "err", err)
	if cs, err = r.session(ctx, cs); err != nil {
		return err
	}
	return r.explain(f(cs))
}

// session returns the live session, or dials a new one when broken is the current one.
func (r *Remote) session(ctx context.Context, broken *mcp.ClientSession) (*mcp.ClientSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("connection to the daemon is closed")
	}
	if r.cs != nil && r.cs != broken {
		return r.cs, nil
	}
	if r.cs != nil {
		_ = r.cs.Close()
		r.cs = nil
	}
	cs, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	r.cs = cs
	return cs, nil
}

func (r *Remote) dial(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "greenroom-connect", Version: r.opts.Version},
		&mcp.ClientOptions{Logger: sdkLogger(r.opts.Logger)})
	// The daemon is stateless: it never pushes, so there is no stream to hold open.
	t := &mcp.StreamableClientTransport{Endpoint: r.cfg.URL + "/mcp", HTTPClient: r.http, DisableStandaloneSSE: true}
	cs, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s/mcp: %w", r.cfg.URL, r.explain(err))
	}
	return cs, nil
}

// explain replaces the SDK's bare "Unauthorized" with what to do about it.
func (r *Remote) explain(err error) error {
	if err == nil {
		return nil
	}
	if r.auth.lastStatus.Load() == http.StatusUnauthorized && strings.Contains(err.Error(), http.StatusText(http.StatusUnauthorized)) {
		return fmt.Errorf("%w (%v)", ErrTokenRejected, err)
	}
	return err
}

// retryable is true for failures below the protocol: the daemon never answered.
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if answered(err) {
		return false
	}
	return !strings.Contains(err.Error(), http.StatusText(http.StatusUnauthorized)) &&
		!strings.Contains(err.Error(), http.StatusText(http.StatusForbidden))
}

// codeRejected is the SDK's jsonrpc2.ErrRejected: a transport failure dressed as a
// JSON-RPC error. The daemon never sent it.
const codeRejected = -32005

// answered is true when err is a JSON-RPC error the daemon itself sent.
func answered(err error) bool {
	var werr *jsonrpc.Error
	return errors.As(err, &werr) && werr.Code != codeRejected
}

// authTransport adds the bearer token and User-Agent, and remembers the last status.
type authTransport struct {
	base       http.RoundTripper
	token      string
	userAgent  string
	lastStatus atomic.Int64
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("User-Agent", t.userAgent)
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		t.lastStatus.Store(int64(resp.StatusCode))
	}
	return resp, err
}

// quiet passes on only warnings and errors: the SDK logs every connection at info.
type quiet struct{ slog.Handler }

func sdkLogger(l *slog.Logger) *slog.Logger { return slog.New(quiet{l.Handler()}) }

func (q quiet) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn && q.Handler.Enabled(ctx, level)
}
func (q quiet) WithAttrs(attrs []slog.Attr) slog.Handler { return quiet{q.Handler.WithAttrs(attrs)} }
func (q quiet) WithGroup(name string) slog.Handler       { return quiet{q.Handler.WithGroup(name)} }

// describeTransportError names the usual ways a request never reaches the daemon.
func describeTransportError(target string, err error) error {
	var dnsErr *net.DNSError
	var certErr *x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var opErr *net.OpError
	switch {
	case errors.As(err, &dnsErr):
		return fmt.Errorf("cannot resolve %s (DNS): %w", dnsErr.Name, err)
	case errors.As(err, &certErr), errors.As(err, &hostErr):
		return fmt.Errorf("TLS certificate of %s is not trusted: %w", target, err)
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return fmt.Errorf("cannot connect to %s: %w", target, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("timed out reaching %s: %w", target, err)
	}
	return fmt.Errorf("cannot reach %s: %w", target, err)
}
