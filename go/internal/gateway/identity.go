package gateway

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/identity"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Request identities must never replace a process-wide development session.
type IdentityAdapter interface {
	Resolve(*http.Request) (mockSession, func(context.Context) error, error)
}

type oidcIdentityAdapter struct {
	provider *identity.Provider
	db       *pgxpool.Pool
}

func identityFailure(err error) error {
	if errors.Is(err, identity.ErrUnavailable) {
		return status.Error(codes.Unavailable, "identity_unavailable")
	}
	if errors.Is(err, identity.ErrRevoked) {
		return status.Error(codes.PermissionDenied, "identity_revoked")
	}
	return status.Error(codes.Unauthenticated, "identity_invalid")
}

func (a oidcIdentityAdapter) Resolve(r *http.Request) (mockSession, func(context.Context) error, error) {
	var session mockSession
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.ContainsAny(strings.TrimPrefix(values[0], "Bearer "), " \t\r\n") {
		return session, nil, identityFailure(identity.ErrInvalid)
	}
	principal, err := a.provider.Resolve(r.Context(), strings.TrimPrefix(values[0], "Bearer "))
	if err != nil {
		return session, nil, identityFailure(err)
	}
	return a.resolvePrincipal(r.Context(), principal)
}

func (a oidcIdentityAdapter) resolvePrincipal(ctx context.Context, principal identity.Principal) (mockSession, func(context.Context) error, error) {
	var session mockSession
	if a.db == nil {
		return session, nil, status.Error(codes.Unavailable, "authorization_unavailable")
	}
	err := a.db.QueryRow(ctx, `SELECT t.id,u.id,t.external_id,u.external_id,u.Role,COALESCE(u.shop_ids,ARRAY[]::text[]),u.permission_revision,u.revoked_at
	 FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.external_id=$1 AND u.external_id=$2 AND t.status='active'`, principal.Binding.Tenant, principal.Binding.User).Scan(&session.TenantID, &session.UserID, &session.Tenant, &session.User, &session.Role, &session.AllowedShops, &session.PermissionRevision, &session.RevokedAt)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return session, nil, status.Error(codes.Unavailable, "authorization_unavailable")
		}
		return session, nil, status.Error(codes.PermissionDenied, "identity_not_found")
	}
	actual, claimed := slices.Clone(session.AllowedShops), slices.Clone(principal.Claims.Shops)
	slices.Sort(actual)
	slices.Sort(claimed)
	if session.RevokedAt != nil {
		return session, nil, status.Error(codes.PermissionDenied, "identity_revoked")
	}
	if session.Role != principal.Claims.Role || session.PermissionRevision != principal.Claims.Revision || !slices.Equal(actual, claimed) {
		return session, nil, status.Error(codes.PermissionDenied, "permission_changed")
	}
	session.Shop, session.RevisionPinned = principal.Binding.Shop, true
	check := func(ctx context.Context) error {
		if err := principal.Check(ctx); err != nil {
			return identityFailure(err)
		}
		return nil
	}
	return session, check, nil
}

type browserIdentityAdapter struct {
	browser *identity.Browser
	adapter oidcIdentityAdapter
}

func (a browserIdentityAdapter) Resolve(r *http.Request) (mockSession, func(context.Context) error, error) {
	principal, err := a.browser.Resolve(r)
	if err != nil {
		return mockSession{}, nil, identityFailure(err)
	}
	return a.adapter.resolvePrincipal(r.Context(), principal)
}

type scopeIdentityEntry struct {
	expires int64
	check   func(context.Context) error
}
type scopeIdentityRegistry struct {
	mu      sync.Mutex
	entries map[string]scopeIdentityEntry
}

func (s *scopeIdentityRegistry) bind(scope *ragv1.RequestContext, check func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	for key, entry := range s.entries {
		if entry.expires <= now {
			delete(s.entries, key)
		}
	}
	if len(s.entries) >= 4096 {
		return status.Error(codes.ResourceExhausted, "identity_scope_capacity")
	}
	if s.entries == nil {
		s.entries = make(map[string]scopeIdentityEntry)
	}
	s.entries[scope.ScopeSignature] = scopeIdentityEntry{scope.ScopeExpiresAt, check}
	return nil
}

func (s *scopeIdentityRegistry) validate(ctx context.Context, scope *ragv1.RequestContext) error {
	s.mu.Lock()
	entry, found := s.entries[scope.ScopeSignature]
	s.mu.Unlock()
	if !found || entry.expires <= time.Now().Unix() {
		return status.Error(codes.PermissionDenied, "identity_scope_missing")
	}
	return entry.check(ctx)
}

func (g *gateway) scoped(session mockSession, check func(context.Context) error) *gateway {
	return &gateway{db: g.db, rag: g.rag, ingest: g.ingest, facts: g.facts, grpcConn: g.grpcConn, session: session, signingKey: g.signingKey, profile: g.profile,
		root: g, identityCheck: check, scopeIdentities: g.scopeIdentities, customer: g.customer}
}

func (g *gateway) executionRegistryOwner() *gateway {
	if g.root != nil {
		return g.root
	}
	return g
}
