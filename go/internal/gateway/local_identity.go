package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// localSessionResolver is the one localauth store operation the identity
// adapter needs. Keeping it narrow lets the scope-identity check be tested
// without a database.
type localSessionResolver interface {
	ResolveSession(context.Context, string) (localauth.ResolvedSession, error)
}

// localIdentityAdapter resolves the ecr_v2_session cookie into the same
// auth.Session the rest of the gateway already uses. It never decides
// permissions beyond the account state.
type localIdentityAdapter struct {
	store localSessionResolver
}

func (a localIdentityAdapter) Resolve(r *http.Request) (mockSession, func(context.Context) error, error) {
	var session mockSession
	cookie, err := r.Cookie(localauth.SessionCookie)
	if err != nil || localauth.ValidateTokenShape(cookie.Value) != nil {
		return session, nil, status.Error(codes.Unauthenticated, "identity_invalid")
	}
	idHash := localauth.HashToken(cookie.Value)
	resolved, err := a.store.ResolveSession(r.Context(), idHash)
	if errors.Is(err, localauth.ErrAccountDisabled) {
		// Disabled accounts are a 403 account_revoked on every channel.
		return session, nil, status.Error(codes.PermissionDenied, "account_revoked")
	}
	if err != nil {
		return session, nil, status.Error(codes.Unauthenticated, "identity_invalid")
	}
	session = mockSession{
		TenantID:           resolved.TenantID,
		UserID:             resolved.UserID,
		Tenant:             resolved.TenantExternalID,
		User:               resolved.Account.ExternalID,
		Role:               resolved.Account.Role,
		AllowedShops:       resolved.Account.Shops,
		PermissionRevision: resolved.Account.Revision,
		RevisionPinned:     true,
		MustChangePassword: resolved.MustChangePassword,
	}
	if shop := os.Getenv("V2_IDENTITY_SHOP"); shop != "" {
		session.Shop = shop
	} else if len(session.AllowedShops) > 0 {
		session.Shop = session.AllowedShops[0]
	}
	return session, localIdentityCheck(a.store, idHash, session.TenantID, session.UserID), nil
}

// localIdentityCheck is the local-profile counterpart of the OIDC principal
// check. It re-reads the same session on every scope validation so a revoked,
// expired or disabled login cannot keep an ingest scope alive.
func localIdentityCheck(resolver localSessionResolver, idHash string, tenantID, userID int64) func(context.Context) error {
	return func(ctx context.Context) error {
		current, err := resolver.ResolveSession(ctx, idHash)
		switch {
		case errors.Is(err, localauth.ErrAccountDisabled):
			return status.Error(codes.PermissionDenied, "account_revoked")
		case errors.Is(err, localauth.ErrSessionInvalid):
			return status.Error(codes.PermissionDenied, "identity_revoked")
		case err != nil:
			return status.Error(codes.Unavailable, "authorization_unavailable")
		}
		if current.TenantID != tenantID || current.UserID != userID {
			return status.Error(codes.PermissionDenied, "identity_changed")
		}
		return nil
	}
}

// randomToken returns the one-time setup token printed at startup.
func randomToken() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		log.Fatal("cannot generate the setup token")
	}
	return hex.EncodeToString(buffer)
}
