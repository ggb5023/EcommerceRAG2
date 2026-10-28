package gateway

import (
	"net/http"

	"github.com/ggb5023/EcommerceRAG/go/internal/customer"
)

// registerCustomerRoutes wires the staff conversation endpoints and the customer
// channel proxy. The handlers themselves stay where they are.
func (g *gateway) registerCustomerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/session", g.getSession)
	mux.HandleFunc("GET /v1/conversations", g.listConversations)
	mux.HandleFunc("POST /v1/conversations", g.createConversation)
	mux.HandleFunc("GET /v1/conversations/{id}", g.getConversation)
	mux.HandleFunc("POST /v1/conversations/{id}/turns", g.createTurn)
	mux.HandleFunc("POST /v1/turns/{id}/resume", g.resumeTurn)
	mux.HandleFunc("GET /v1/turns/{id}", g.getTurn)
	mux.HandleFunc("GET /v1/turns/{id}/events", g.events)
	mux.HandleFunc("POST /v1/turns/{id}/cancel", g.cancelTurn)
	if g.customer != nil {
		mux.HandleFunc("/v1/support/", func(w http.ResponseWriter, r *http.Request) {
			if !g.requireWriteRole(w) {
				return
			}
			g.customer.StaffRoutes(customer.Staff{Tenant: g.session.TenantID, User: g.session.UserID, Shops: g.session.AllowedShops, Role: g.session.Role, SessionHash: customer.StaffSessionHash(r), Check: g.authority().Refresh}).ServeHTTP(w, r)
		})
	}
}
