// Web hosts the two static apps and the single HTTPS business/auth origin.
package main

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func proxy(target string) http.Handler {
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
func spa(root, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix)
		path = filepath.Join(root, filepath.Clean("/"+path))
		if info, e := os.Stat(path); e != nil || info.IsDir() {
			path = filepath.Join(root, "index.html")
		}
		http.ServeFile(w, r, path)
	})
}
func routes(gateway, identity, web, admin http.Handler, authority string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != authority {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		switch {
		case strings.HasPrefix(r.URL.Path, "/identity/"):
			identity.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/admin/v1/") || r.URL.Path == "/healthz":
			gateway.ServeHTTP(w, r)
		case r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/"):
			admin.ServeHTTP(w, r)
		default:
			web.ServeHTTP(w, r)
		}
	})
}
func main() {
	port := os.Getenv("ECR_HTTPS_PORT")
	if port == "" {
		port = "443"
	}
	gateway := proxy("http://gateway:8080")
	identity := proxy("http://keycloak:8080")
	web := spa("/srv/web", "")
	admin := spa("/srv/admin", "/admin")
	host := os.Getenv("ECR_HOSTNAME")
	if host == "" {
		log.Fatal("HTTPS hostname required")
	}
	authority := host
	if port != "443" {
		authority = net.JoinHostPort(host, port)
	}
	handler := routes(gateway, identity, web, admin, authority)
	server := http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}}
	log.Fatal(server.ListenAndServeTLS("/run/web/tls.crt", "/run/web/tls.key"))
}
