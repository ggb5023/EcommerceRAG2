package gateway

import (
	"errors"
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
)

func (g *gateway) requireWriteRole(w http.ResponseWriter) bool {
	if !slices.Contains([]string{"owner", "admin", "operator"}, g.session.Role) {
		writeError(w, http.StatusForbidden, "role_forbidden", "current role cannot modify conversations")
		return false
	}
	return true
}

func authorizationHTTPStatus(err error) int {
	if status.Code(err) == codes.PermissionDenied {
		return http.StatusForbidden
	}
	return http.StatusServiceUnavailable
}

// Registered resources inherit shop scope unless an ACL gate narrows it.
// Every present tenant/source/document gate must grant this principal.
func (g *gateway) validateScopeHTTP(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("V2_RPC_TLS") == "mtls" {
		http.NotFound(w, r)
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		writeError(w, 403, "forbidden", "internal endpoint")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	scope := &ragv1.RequestContext{}
	if err != nil || proto.Unmarshal(body, scope) != nil {
		writeError(w, 400, "invalid_scope", "invalid scope")
		return
	}
	if err = g.authority().ValidateScope(r.Context(), scope); err != nil {
		code := 403
		if status.Code(err) == codes.Unavailable {
			code = 503
		}
		writeError(w, code, auth.FailureCode(err), "authorization failed")
		return
	}
	writeJSON(w, 200, map[string]bool{"allowed": true})
}

func evidenceEqual(a, b *ragv1.Evidence) bool { return a != nil && b != nil && proto.Equal(a, b) }

func (g *gateway) authorizeConversationHistory(w http.ResponseWriter, r *http.Request, id int64) bool {
	var stale bool
	err := g.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM turn t JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
        WHERE t.tenant_id=$1 AND t.conversation_id=$2 AND e.permission_revision IS DISTINCT FROM $3)`, g.session.TenantID, id, g.authority().CurrentRevision()).Scan(&stale)
	if err != nil {
		writeError(w, 503, "authorization_unavailable", "authorization failed")
		return false
	}
	if stale {
		writeError(w, 403, "execution_permission_changed", "historical execution scope is no longer valid")
		return false
	}
	return true
}

func (g *gateway) authorizeExecution(w http.ResponseWriter, r *http.Request, requestID string) bool {
	var revision *string
	err := g.db.QueryRow(r.Context(), `SELECT e.permission_revision FROM conversation_execution e JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
        JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
        WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4`, g.session.TenantID, requestID, g.session.UserID, g.session.Shop).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "turn not found")
		return false
	}
	if err != nil {
		writeError(w, 503, "authorization_unavailable", "authorization failed")
		return false
	}
	if revision == nil || *revision != g.authority().CurrentRevision() {
		writeError(w, 403, "execution_permission_changed", "historical execution scope is no longer valid")
		return false
	}
	return true
}

func (g *gateway) authority() *auth.Authority {
	a := &auth.Authority{DB: g.db, Session: &g.session, SigningKey: g.signingKey, RevisionMu: &g.revisionMu, IdentityCheck: g.identityCheck}
	if g.scopeIdentities != nil {
		a.ValidateScopeIdentity = g.scopeIdentities.validate
		if g.identityCheck != nil {
			a.RegisterScopeIdentity = func(scope *ragv1.RequestContext) error { return g.scopeIdentities.bind(scope, g.identityCheck) }
		}
	}
	return a
}
