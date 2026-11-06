package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeLocalSessions struct {
	resolved localauth.ResolvedSession
	err      error
}

func (f *fakeLocalSessions) ResolveSession(context.Context, string) (localauth.ResolvedSession, error) {
	return f.resolved, f.err
}

func TestLocalIdentitySessionCheck(t *testing.T) {
	valid := localauth.ResolvedSession{Session: localauth.Session{TenantID: 7, UserID: 9}}
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "valid", err: nil, code: codes.OK},
		{name: "revoked or expired", err: localauth.ErrSessionInvalid, code: codes.PermissionDenied},
		{name: "account disabled", err: localauth.ErrAccountDisabled, code: codes.PermissionDenied},
		{name: "database unavailable", err: errors.New("database down"), code: codes.Unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &fakeLocalSessions{resolved: valid, err: tc.err}
			err := localIdentityCheck(resolver, "hash", 7, 9)(context.Background())
			if status.Code(err) != tc.code {
				t.Fatalf("code=%v want %v (err=%v)", status.Code(err), tc.code, err)
			}
		})
	}

	mismatch := &fakeLocalSessions{resolved: localauth.ResolvedSession{Session: localauth.Session{TenantID: 8, UserID: 9}}}
	if err := localIdentityCheck(mismatch, "hash", 7, 9)(context.Background()); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("identity change accepted: %v", err)
	}
}

func TestLocalIdentityScopeRegistration(t *testing.T) {
	resolver := &fakeLocalSessions{resolved: localauth.ResolvedSession{
		Session:          localauth.Session{TenantID: 7, UserID: 9},
		Account:          localauth.Account{ExternalID: "admin", Shops: []string{"main-shop"}, Revision: "rev-1"},
		TenantExternalID: "merchant",
	}}
	adapter := localIdentityAdapter{store: resolver}
	request := httptest.NewRequest("GET", "/admin/v1/session", nil)
	request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: strings.Repeat("a", 32)})
	session, check, err := adapter.Resolve(request)
	if err != nil || check == nil || session.Shop != "main-shop" {
		t.Fatalf("local identity resolve: session=%+v check_nil=%v err=%v", session, check == nil, err)
	}

	root := &gateway{scopeIdentities: &scopeIdentityRegistry{}}
	requestGateway := root.scoped(session, check)
	authority := requestGateway.authority()
	if authority.RegisterScopeIdentity == nil || authority.ValidateScopeIdentity == nil {
		t.Fatal("local profile did not wire the scope identity registry")
	}
	scope := &ragv1.RequestContext{ScopeSignature: "signed-scope", ScopeExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := authority.RegisterScopeIdentity(scope); err != nil {
		t.Fatal(err)
	}
	if err := authority.ValidateScopeIdentity(context.Background(), scope); err != nil {
		t.Fatalf("live local session rejected: %v", err)
	}

	resolver.err = localauth.ErrSessionInvalid
	if err := authority.ValidateScopeIdentity(context.Background(), scope); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("revoked local session accepted: %v", err)
	}
	resolver.err = errors.New("database down")
	if err := authority.ValidateScopeIdentity(context.Background(), scope); status.Code(err) != codes.Unavailable {
		t.Fatalf("unavailable local session code=%v", status.Code(err))
	}
}
