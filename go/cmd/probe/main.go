// Probe reports readiness without printing internal responses or configuration.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	port := addr
	if _, p, err := net.SplitHostPort(addr); err == nil {
		port = p
	}
	target := "http://127.0.0.1:" + port + "/healthz"
	client := http.Client{Timeout: 2 * time.Second}
	if os.Getenv("WEB_ROOT") != "" {
		cert := os.Getenv("WEB_TLS_CERT")
		if cert == "" {
			cert = "/run/web/tls.crt"
		}
		authority := os.Getenv("ECR_HOSTNAME")
		pool := x509.NewCertPool()
		pem, err := os.ReadFile(cert)
		if err != nil || !pool.AppendCertsFromPEM(pem) {
			os.Exit(1)
		}
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: authority, MinVersion: tls.VersionTLS13}}
		target = "https://127.0.0.1:" + port + "/healthz"
	}
	response, err := client.Get(target)
	if err != nil {
		os.Exit(1)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		os.Exit(1)
	}
}
