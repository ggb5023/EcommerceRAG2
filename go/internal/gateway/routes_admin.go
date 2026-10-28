package gateway

import (
	"net/http"

	"github.com/ggb5023/EcommerceRAG/go/internal/admin"
)

// registerAdminRoutes mounts the management API. The admin package owns every
// handler; this file only passes the dependencies through.
func (g *gateway) registerAdminRoutes(mux *http.ServeMux) {
	customerOrigin := ""
	if g.customer != nil {
		customerOrigin = g.customer.Config.Origin
	}
	(&admin.Handler{DB: g.db, Session: &g.session, Authority: g.authority(), Ingest: g.ingest, AIConnection: g.grpcConn, Profile: g.profile, SigningKey: g.signingKey, CustomerOrigin: customerOrigin}).RegisterRoutes(mux)
}
