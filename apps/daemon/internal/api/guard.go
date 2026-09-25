package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Guard admits two kinds of request (ADR 0021). A loopback Host is a local native client: any
// Origin must be loopback too (browsers send Origin on every cross-site POST), and no token is
// asked for. A Host equal to publicHost came through the tunnel: it must carry
// "Authorization: Bearer <token>" and no Origin at all, since no browser is a client; only the
// install files are served without the token. Every other Host is refused (DNS rebinding).
// cloudflared connects from loopback, so the Host header is the only thing that tells tunnel
// traffic from local traffic; that is safe because the daemon listens on loopback alone.
// An empty publicHost turns the public branch off.
func Guard(h http.Handler, publicHost, token string) http.Handler {
	public := hostName(publicHost)
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case loopbackHost(r.Host):
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !loopbackHost(u.Host) {
					http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
					return
				}
			}
		case public != "" && hostName(r.Host) == public:
			if _, ok := r.Header["Origin"]; ok {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
			if installPath(r) {
				break
			}
			got, ok := bearer(r.Header.Get("Authorization"))
			// Hashing first makes the comparison constant-time in the length as well.
			if sum := sha256.Sum256([]byte(got)); !ok || token == "" || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized: missing or wrong token", http.StatusUnauthorized)
				return
			}
		default:
			http.Error(w, "forbidden: Host must be a loopback address or the configured public host", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// installPath reports whether r fetches the client installer, the only public route without a token.
func installPath(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == "/install.sh" {
		return true
	}
	file, ok := strings.CutPrefix(r.URL.Path, "/dl/")
	return ok && bareName(file)
}

// bearer returns the token of an "Authorization: Bearer <token>" value; the scheme is case-insensitive.
func bearer(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// hostName is hostport's host, lower case, without a port or IPv6 brackets.
func hostName(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	} else if len(host) > 1 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	return strings.ToLower(host)
}

// LoopbackAddr reports whether a listen address like 127.0.0.1:7777 only accepts local connections.
func LoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && host != "" && loopbackHost(host)
}

// loopbackHost reports whether hostport names this machine: localhost or a loopback IP literal, any port.
func loopbackHost(hostport string) bool {
	host := hostName(hostport)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// jsonBodies refuses a write whose body is not JSON, so an HTML form cannot post one.
func (a *api) jsonBodies(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			ct := r.Header.Get("Content-Type")
			if r.ContentLength != 0 || ct != "" {
				if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
					a.fail(w, http.StatusUnsupportedMediaType, errors.New("the body must be JSON (Content-Type: application/json)"))
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}
