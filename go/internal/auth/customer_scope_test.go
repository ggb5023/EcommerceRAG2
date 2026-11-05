package auth

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func customerScopePair() (*ragv1.RequestContext, *ragv1.RequestContext) {
	scope := &ragv1.RequestContext{
		TenantId: "tenant-a", TenantDbId: 11, ShopId: "shop-a", CustomerId: "customer-a",
		PermissionRevision: "rev-1", ConversationEpoch: 7, AllowedDocumentIds: []string{"doc-a"},
	}
	current := &ragv1.RequestContext{
		TenantId: "tenant-a", TenantDbId: 11, ShopId: "shop-a", CustomerId: "customer-a",
		PermissionRevision: "rev-1", ConversationEpoch: 7, AllowedDocumentIds: []string{"doc-a", "doc-b"},
	}
	return scope, current
}

func TestCustomerScopeMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ragv1.RequestContext, *ragv1.RequestContext)
		want   string
	}{
		{name: "matching scope", mutate: func(*ragv1.RequestContext, *ragv1.RequestContext) {}, want: ""},
		{name: "customer changed", mutate: func(_ *ragv1.RequestContext, current *ragv1.RequestContext) { current.CustomerId = "customer-b" }, want: "customer_scope_stale"},
		{name: "tenant db changed", mutate: func(_ *ragv1.RequestContext, current *ragv1.RequestContext) { current.TenantDbId++ }, want: "customer_scope_stale"},
		{name: "tenant changed", mutate: func(_ *ragv1.RequestContext, current *ragv1.RequestContext) { current.TenantId = "tenant-b" }, want: "customer_scope_stale"},
		{name: "shop changed", mutate: func(_ *ragv1.RequestContext, current *ragv1.RequestContext) { current.ShopId = "shop-b" }, want: "customer_scope_stale"},
		{name: "revision changed", mutate: func(_ *ragv1.RequestContext, current *ragv1.RequestContext) { current.PermissionRevision = "rev-2" }, want: "customer_scope_stale"},
		{name: "epoch advanced", mutate: func(_ *ragv1.RequestContext, current *ragv1.RequestContext) { current.ConversationEpoch++ }, want: "customer_scope_stale"},
		{name: "epoch regressed", mutate: func(_ *ragv1.RequestContext, current *ragv1.RequestContext) { current.ConversationEpoch-- }, want: "customer_scope_stale"},
		{name: "scope subset allowed", mutate: func(scope *ragv1.RequestContext, _ *ragv1.RequestContext) { scope.AllowedDocumentIds = []string{"doc-b"} }, want: ""},
		{name: "document escalation rejected", mutate: func(scope *ragv1.RequestContext, _ *ragv1.RequestContext) { scope.AllowedDocumentIds = []string{"doc-a", "doc-c"} }, want: "customer_document_forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, current := customerScopePair()
			tc.mutate(scope, current)
			if got := customerScopeMismatch(scope, current); got != tc.want {
				t.Fatalf("customerScopeMismatch() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScopeExpiryInvalid(t *testing.T) {
	const now = int64(1_800_000_000)
	for _, tc := range []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{name: "already expired", expiresAt: now - 1, want: true},
		{name: "expires at now", expiresAt: now, want: true},
		{name: "valid one second", expiresAt: now + 1, want: false},
		{name: "valid upper bound", expiresAt: now + 70, want: false},
		{name: "too far ahead", expiresAt: now + 71, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeExpiryInvalid(tc.expiresAt, now); got != tc.want {
				t.Fatalf("scopeExpiryInvalid() = %v, want %v", got, tc.want)
			}
		})
	}
}
