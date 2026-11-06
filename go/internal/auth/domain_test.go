package auth

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func domainScope() *ragv1.RequestContext {
	return &ragv1.RequestContext{
		RequestId: "req-domain", TenantId: "tenant-a", UserId: "user-a", ShopId: "shop-a",
		Role: "operator", AllowedShopIds: []string{"shop-a"}, PermissionRevision: "rev-1",
		AllowedDocumentIds: []string{"doc-a"}, EnforceDocumentScope: true,
		ScopeExpiresAt: time.Now().Add(30 * time.Second).Unix(), TenantDbId: 1, UserDbId: 1,
		ScopeAudience: "ai",
	}
}

func TestScopeDomainDefaultsToAnswer(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	authority := &Authority{SigningKey: key}
	scope := domainScope()
	authority.signScope(scope, "")
	if scope.Domain != ScopeDomainAnswer {
		t.Fatalf("domain=%q want %q", scope.Domain, ScopeDomainAnswer)
	}
	if want := hex.EncodeToString(MAC(key, scope)); scope.ScopeSignature != want {
		t.Fatal("default domain is not covered by the signature")
	}
}

func TestScopeDomainExplicitIntelligenceIsSigned(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	authority := &Authority{SigningKey: key}
	scope := domainScope()
	authority.signScope(scope, ScopeDomainIntelligence)
	if scope.Domain != ScopeDomainIntelligence {
		t.Fatalf("domain=%q want %q", scope.Domain, ScopeDomainIntelligence)
	}
	if want := hex.EncodeToString(MAC(key, scope)); scope.ScopeSignature != want {
		t.Fatal("intelligence domain is not covered by the signature")
	}
	first := MAC(key, scope)
	scope.Domain = ScopeDomainAnswer
	if hex.EncodeToString(first) == hex.EncodeToString(MAC(key, scope)) {
		t.Fatal("changing the domain must change the scope MAC")
	}
}

func TestScopeDomainTamperIsRejected(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	authority := &Authority{SigningKey: key}
	scope := domainScope()
	authority.signScope(scope, ScopeDomainAnswer)
	scope.Domain = ScopeDomainIntelligence
	err := authority.ValidateScope(context.Background(), scope)
	if err == nil {
		t.Fatal("a tampered domain must be rejected")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code=%v want PermissionDenied", status.Code(err))
	}
}

func TestScopeDomainUnknownIsRejected(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	authority := &Authority{SigningKey: key}
	scope := domainScope()
	scope.Domain = "search"
	scope.ScopeSignature = hex.EncodeToString(MAC(key, scope))
	err := authority.ValidateScope(context.Background(), scope)
	if err == nil {
		t.Fatal("an unknown domain must be rejected")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code=%v want PermissionDenied", status.Code(err))
	}
}
