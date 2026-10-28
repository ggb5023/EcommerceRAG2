package gateway

import (
	"fmt"
	"net"
	"strings"
)

// Deployment forms D1–D4 (decision 0026). Only the first three are supported.
//
//	D1  built-in edge: WEB_ROOT set, the gateway terminates TLS and serves the SPA
//	D2  front proxy terminates TLS, the gateway listens on plain HTTP loopback
//	D3  local development: plain HTTP on loopback without WEB_ROOT
//	D4  front proxy plus built-in edge — unsupported, because edge.Proxy rewrites
//	    X-Forwarded-Proto unconditionally and would corrupt the forwarded chain
const (
	formBuiltInEdge  = "D1"
	formFrontProxy   = "D2"
	formLocalDev     = "D3"
	formUnsupported  = "D4"
	deploymentAdvice = "use WEB_ROOT (D1) or a front proxy with an empty WEB_ROOT (D2), never both"
)

// validateDeploymentForm rejects the unsupported combinations at startup so the
// forwarded chain and the protocol decision can never be mixed.
func validateDeploymentForm(webRoot, listenAddr, trustedProxyHost string) error {
	webRoot = strings.TrimSpace(webRoot)
	trustedProxyHost = strings.TrimSpace(trustedProxyHost)
	if webRoot != "" && trustedProxyHost != "" {
		return fmt.Errorf("%s: WEB_ROOT is set and V2_CUSTOMER_PROXY_HOST is configured; %s", formUnsupported, deploymentAdvice)
	}
	if webRoot == "" {
		if err := requireLoopback(listenAddr); err != nil {
			return fmt.Errorf("%s: %w", formFrontProxy, err)
		}
	}
	return nil
}

// requireLoopback accepts an empty address (defaults to loopback) or a loopback host.
func requireLoopback(listenAddr string) error {
	if strings.TrimSpace(listenAddr) == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return fmt.Errorf("listen address %q is not host:port", listenAddr)
	}
	if host == "" {
		return fmt.Errorf("listen address %q binds every interface; plain HTTP must stay on loopback", listenAddr)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q is not loopback; plain HTTP must stay on loopback", listenAddr)
	}
	return nil
}
