// Command d2-proxy is a local, standard-library simulation of the D2 front
// proxy described in docs/decisions/0026-proxy-fronted-deployment.md.
//
// It is a development/verification tool, not a production proxy: it terminates
// no TLS, has no access log and does not implement real proxy authentication.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "loopback listen address")
	target := flag.String("target", "http://127.0.0.1:8080", "gateway target")
	root := flag.String("root", "frontend/dist", "SPA static root")
	flag.Parse()
	if err := run(*listen, *target, *root); err != nil {
		log.Fatal(err)
	}
}

func run(listen, target, root string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid gateway target %q", target)
	}
	proxy := httputil.NewSingleHostReverseProxy(parsed)
	// R4-3: SSE must not be buffered by this layer.
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		// R4-2: inject the protocol once; an existing value wins so a second
		// proxy/edge in front cannot be silently overwritten here.
		if request.Header.Get("X-Forwarded-Proto") == "" {
			request.Header.Set("X-Forwarded-Proto", "https")
		}
		director(request)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
	}

	mux := http.NewServeMux()
	// R4-4: same paths, no rewrite.
	mux.Handle("/v1/", proxy)
	mux.Handle("/v1/customer", proxy)
	mux.Handle("/admin/v1/", proxy)
	mux.Handle("/healthz", proxy)
	mux.Handle("/setup", proxy)
	mux.Handle("/", spa(root))

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	fmt.Printf("LISTEN %s\n", listener.Addr().String())
	_ = os.Stdout.Sync()
	return http.Serve(listener, mux)
}

// spa serves a built file when it exists and otherwise falls back to index.html.
// The request path is rooted before joining so ".." cannot escape the root.
func spa(root string) http.Handler {
	index := filepath.Join(root, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		clean := path.Clean("/" + request.URL.Path)
		file := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			http.ServeFile(w, request, file)
			return
		}
		if _, err := os.Stat(index); err != nil {
			http.Error(w, "frontend build not found; run npm run build", http.StatusNotFound)
			return
		}
		http.ServeFile(w, request, index)
	})
}
