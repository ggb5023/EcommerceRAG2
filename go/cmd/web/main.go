// Web hosts the single merged frontend and the HTTPS business/auth origin.
package main

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/edge"
)

func main() {
	port := os.Getenv("ECR_HTTPS_PORT")
	if port == "" {
		port = "443"
	}
	gateway := edge.Proxy("http://gateway:8080")
	identity := edge.Proxy("http://keycloak:8080")
	frontend := edge.SPA("/srv/frontend", "")
	host := os.Getenv("ECR_HOSTNAME")
	if host == "" {
		log.Fatal("HTTPS hostname required")
	}
	authority := host
	if port != "443" {
		authority = net.JoinHostPort(host, port)
	}
	handler := edge.Routes(gateway, identity, frontend, authority)
	server := http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}}
	log.Fatal(server.ListenAndServeTLS("/run/web/tls.crt", "/run/web/tls.key"))
}
