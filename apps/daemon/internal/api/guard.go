package api

import (
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
)

// LocalOnly refuses requests a web page could make: a Host that is not loopback (DNS rebinding) or a
// non-loopback Origin (browsers send Origin on every cross-site POST). Native clients send neither.
func LocalOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden: Host must be a loopback address", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !loopbackHost(u.Host) {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// LoopbackAddr reports whether a listen address like 127.0.0.1:7777 only accepts local connections.
func LoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && host != "" && loopbackHost(host)
}

// loopbackHost reports whether hostport names this machine: localhost or a loopback IP literal, any port.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	} else if len(host) > 1 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
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
