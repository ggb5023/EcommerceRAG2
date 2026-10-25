// Package edge holds the single HTTPS/static front door: host check, CSP,
// frontend fallback and the reverse proxies. Both cmd/web and the gateway use
// this package so the security headers are defined once.
package edge

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// Proxy forwards to an internal service and strips untrusted forwarding input.
func Proxy(target string) http.Handler {
	u, e := url.Parse(target)
	if e != nil || u.Host == "" {
		log.Fatal("internal web target invalid")
	}
	p := httputil.NewSingleHostReverseProxy(u)
	p.FlushInterval = -1
	original := p.Director
	p.Director = func(r *http.Request) {
		host := r.Host
		original(r)
		r.Header.Del("Forwarded")
		r.Header.Del("X-Forwarded-For")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", host)
		hostIP, _, _ := net.SplitHostPort(r.RemoteAddr)
		r.Header.Set("X-Real-IP", hostIP)
		r.Header.Set("X-Forwarded-Port", os.Getenv("ECR_HTTPS_PORT"))
	}
	p.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "service unavailable", 503) }
	return p
}

// SPA serves files under root and falls back to index.html for client routes.
func SPA(root, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix)
		path = filepath.Join(root, filepath.Clean("/"+path))
		if info, e := os.Stat(path); e != nil || info.IsDir() {
			path = filepath.Join(root, "index.html")
		}
		http.ServeFile(w, r, path)
	})
}

// Routes validates the Host, sets the shared security headers, sends API paths
// to the gateway (business routes always win over the static fallback) and
// everything else to the frontend.
func Routes(gateway, identity, frontend http.Handler, authority string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != authority {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", csp)
		switch {
		case identity != nil && strings.HasPrefix(r.URL.Path, "/identity/"):
			identity.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/v1/"), strings.HasPrefix(r.URL.Path, "/admin/v1/"),
			r.URL.Path == "/healthz", r.URL.Path == "/setup":
			gateway.ServeHTTP(w, r)
		default:
			frontend.ServeHTTP(w, r)
		}
	})
}
