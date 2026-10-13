package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testIdentityAdapter struct{}

func (testIdentityAdapter) Resolve(r *http.Request) (mockSession, func(context.Context) error, error) {
	user := r.Header.Get("Authorization")
	if user == "" {
		return mockSession{}, nil, status.Error(codes.Unauthenticated, "missing_identity")
	}
	return mockSession{Tenant: "tenant-test", User: user, Shop: user + "-shop", Role: "viewer"}, func(context.Context) error { return nil }, nil
}

func TestRequestIdentityIsolation(t *testing.T) {
	g := &gateway{session: mockSession{User: "fixed-development"}, identity: testIdentityAdapter{}, cancels: make(map[string]context.CancelFunc)}
	handler := g.Routes()
	var wait sync.WaitGroup
	for _, user := range []string{"alice", "bob", "charlie"} {
		for range 20 {
			wait.Add(1)
			go func(user string) {
				defer wait.Done()
				req := httptest.NewRequest("GET", "/v1/session", nil)
				req.Header.Set("Authorization", user)
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, req)
				var result map[string]any
				if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || result["userId"] != user || result["shopId"] != user+"-shop" {
					t.Error("request identity mixed")
				}
			}(user)
		}
	}
	wait.Wait()
	if g.session.User != "fixed-development" {
		t.Fatal("request replaced process identity")
	}
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest("GET", "/v1/session", nil))
	if out.Code != 401 {
		t.Fatal("missing OIDC identity fell back to development")
	}
	child := g.scoped(mockSession{User: "alice"}, nil)
	if child.executionRegistryOwner() != g {
		t.Fatal("cancellation map no longer shared across requests")
	}
}

func TestScopeIdentityRequiresBoundLiveCredential(t *testing.T) {
	registry := &scopeIdentityRegistry{}
	scope := &ragv1.RequestContext{ScopeSignature: "signed-scope", ScopeExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := registry.validate(context.Background(), scope); status.Code(err) != codes.PermissionDenied {
		t.Fatal("unbound scope accepted")
	}
	revoked := false
	if err := registry.bind(scope, func(context.Context) error {
		if revoked {
			return status.Error(codes.PermissionDenied, "identity_revoked")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.validate(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	revoked = true
	if err := registry.validate(context.Background(), scope); status.Code(err) != codes.PermissionDenied {
		t.Fatal("revoked token scope accepted")
	}
	scope.ScopeExpiresAt = time.Now().Add(-time.Minute).Unix()
	if err := registry.bind(scope, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := registry.validate(context.Background(), scope); status.Code(err) != codes.PermissionDenied {
		t.Fatal("expired scope accepted")
	}
}
