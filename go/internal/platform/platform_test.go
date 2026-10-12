package platform

import (
	"context"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

type testFactSource struct{}

func (testFactSource) GetFact(_ context.Context, _ *ragv1.RequestContext, tool, _ string) (*ragv1.Fact, error) {
	return &ragv1.Fact{ToolName: tool, ValueJson: `{ "available": true }`, ObservedAt: "2026-10-09T00:00:00Z"}, nil
}

func TestScopedFactSourceRequiresSignedScopeShape(t *testing.T) {
	source := ScopedFactSource{Source: testFactSource{}}
	if _, err := source.Lookup(context.Background(), "get_stock", &ragv1.RequestContext{TenantId: "t", UserId: "u"}, ""); err == nil {
		t.Fatal("fact source accepted a scope without selected shop")
	}
	fact, err := source.Lookup(context.Background(), "get_stock", &ragv1.RequestContext{
		TenantId: "t", UserId: "u", ShopId: "s", AllowedShopIds: []string{"s"},
	}, "")
	if err != nil || fact.ToolName != "get_stock" {
		t.Fatalf("scoped fact source failed: %#v %v", fact, err)
	}
}

func TestNormalizePlatformKeyRejectsCredentialLikeValues(t *testing.T) {
	if _, err := NormalizeKey("shopify"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "https://example.test", "token value", "a/b"} {
		if _, err := NormalizeKey(value); err == nil {
			t.Fatalf("platform key accepted invalid value %q", value)
		}
	}
}
