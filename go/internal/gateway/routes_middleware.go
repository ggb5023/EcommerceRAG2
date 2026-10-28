package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/customer"
	"github.com/ggb5023/EcommerceRAG/go/internal/identity"
	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Routes is the outer chain: readiness, local setup, identity/CSRF gates and the
// customer channel dispatch all run before the table in businessRoutes.

func (g *gateway) Routes() http.Handler {
	mux := g.businessRoutes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.db != nil && r.URL.Path != "/internal/authorization/validate" && !g.ready.Load() {
			writeError(w, http.StatusServiceUnavailable, "service_starting", "service is starting")
			return
		}
		if r.URL.Path == "/setup" && g.localSetup != nil {
			g.localSetup.Handle(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/auth/") {
			if g.localAuth != nil {
				g.localAuth.Handle(w, r)
				return
			}
			if g.browser != nil {
				if r.Method == "POST" && r.URL.Path == "/v1/auth/logout" && g.customer != nil && g.browser.ValidateWrite(r) == nil {
					_ = g.customer.ReleaseSession(r.Context(), customer.StaffSessionHash(r))
				}
				g.browser.Handle(w, r)
				return
			}
			if r.Method == "GET" && r.URL.Path == "/v1/auth/session" {
				w.Header().Set("Cache-Control", "no-store")
				writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "authenticated": g.identity == nil})
				return
			}
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/customer/") {
			if g.customer == nil {
				http.NotFound(w, r)
				return
			}
			g.customer.Routes().ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/healthz" && r.URL.Path != "/internal/authorization/validate" {
			if g.browser != nil {
				if err := g.browser.ValidateWrite(r); err != nil {
					code := http.StatusForbidden
					if errors.Is(err, identity.ErrInvalid) {
						code = http.StatusUnauthorized
					}
					writeError(w, code, "browser_request_rejected", "browser request rejected")
					return
				}
			}
			if g.localAuth != nil {
				if err := g.localAuth.ValidateWrite(r); err != nil {
					code := http.StatusForbidden
					if errors.Is(err, localauth.ErrSessionInvalid) {
						code = http.StatusUnauthorized
					}
					writeError(w, code, "local_request_rejected", "request rejected")
					return
				}
			}
			if g.identity != nil {
				session, check, err := g.identity.Resolve(r)
				if err != nil {
					code := http.StatusUnauthorized
					if status.Code(err) == codes.PermissionDenied {
						code = http.StatusForbidden
					} else if status.Code(err) == codes.Unavailable {
						code = http.StatusServiceUnavailable
					}
					writeError(w, code, "identity_rejected", "identity rejected")
					return
				}
				if session.MustChangePassword {
					writeError(w, http.StatusForbidden, "password_change_required", "password change required")
					return
				}
				requestGateway := g.scoped(session, check)
				if !requestGateway.authorizeHTTP(w, r) {
					return
				}
				requestGateway.businessRoutes().ServeHTTP(w, r)
				return
			}
			if !g.authorizeHTTP(w, r) {
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
