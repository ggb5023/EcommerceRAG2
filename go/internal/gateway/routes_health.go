package gateway

import "net/http"

// registerHealthRoutes wires the infrastructure endpoints: the readiness probe
// and the internal authorization check used by the Python side.
func (g *gateway) registerHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", g.health)
	mux.HandleFunc("POST /internal/authorization/validate", g.validateScopeHTTP)
}
