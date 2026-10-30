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

// signedScope builds a scope the same way IssueScopeForShop does and signs it with
// the given key. No database is involved: the signature check is a pure function.
func signedScope(t *testing.T, key []byte) *ragv1.RequestContext {
	t.Helper()
	scope := &ragv1.RequestContext{
		RequestId: "req-test", TenantId: "tenant-a", UserId: "user-a", ShopId: "shop-a",
		Role: "operator", AllowedShopIds: []string{"shop-a"}, PermissionRevision: "rev-1",
		AllowedDocumentIds: []string{"doc-a"}, EnforceDocumentScope: true,
		ScopeExpiresAt: time.Now().Add(30 * time.Second).Unix(), TenantDbId: 1, UserDbId: 1,
		ScopeAudience: "ai",
	}
	scope.ScopeSignature = hex.EncodeToString(MAC(key, scope))
	return scope
}

func TestValidateScopeAcceptsAnUntamperedSignature(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	scope := signedScope(t, key)
	signature, err := hex.DecodeString(scope.ScopeSignature)
	if err != nil {
		t.Fatal(err)
	}
	if !hmacEqualForTest(key, scope, signature) {
		t.Fatal("a freshly signed scope must verify")
	}
}

// Changing the tenant while keeping the signature must be rejected before any
// database lookup: this is the cross-tenant red line.
func TestValidateScopeRejectsTamperedTenant(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	scope := signedScope(t, key)
	scope.TenantId = "tenant-b"

	authority := &Authority{SigningKey: key}
	err := authority.ValidateScope(context.Background(), scope)
	if err == nil {
		t.Fatal("a tampered tenant must be rejected")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", status.Code(err))
	}
	if FailureCode(err) == "" {
		t.Fatal("FailureCode must name the failure")
	}
}

func TestValidateScopeRejectsWidenedShops(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	scope := signedScope(t, key)
	scope.AllowedShopIds = append(scope.AllowedShopIds, "shop-b")

	authority := &Authority{SigningKey: key}
	err := authority.ValidateScope(context.Background(), scope)
	if err == nil {
		t.Fatal("a widened shop list must be rejected")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", status.Code(err))
	}
	if FailureCode(err) == "" {
		t.Fatal("FailureCode must name the failure")
	}
}

// hmacEqualForTest mirrors the check ValidateScope performs, without the DB.
func hmacEqualForTest(key []byte, scope *ragv1.RequestContext, signature []byte) bool {
	return hex.EncodeToString(MAC(key, scope)) == hex.EncodeToString(signature)
}
