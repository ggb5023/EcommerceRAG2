package facts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func testScope() *ragv1.RequestContext {
	return &ragv1.RequestContext{TenantId: "tenant-a", UserId: "user-a", ShopId: "shop-a", AllowedShopIds: []string{"shop-a"}}
}

func TestHTTPProviderVersionAndOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Facts-Version") != "2026-01" || r.Header.Get("X-Tenant-ID") != "tenant-a" || r.Header.Get("X-Shop-ID") != "shop-a" {
			t.Fatal("scope/version headers were not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"2026-01","tenant_id":"tenant-a","shop_id":"shop-a","tool_name":"get_stock","value_json":{"sku":"SKU-1","available":4},"observed_at":"2026-01-01T00:00:00Z","is_mock":true}`))
	}))
	defer server.Close()
	fact, err := (HTTPProvider{BaseURL: server.URL, Version: "2026-01"}).Lookup(context.Background(), "get_stock", testScope())
	if err != nil || fact == nil || fact.ToolName != "get_stock" || fact.IsMock != true {
		t.Fatalf("valid fake response rejected: fact=%#v err=%v", fact, err)
	}
	bad := testScope()
	bad.AllowedShopIds = []string{"other-shop"}
	if _, err := (HTTPProvider{BaseURL: server.URL, Version: "2026-01"}).Lookup(context.Background(), "get_stock", bad); err == nil || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unauthorized shop was accepted: %v", err)
	}
}

func TestHTTPProviderFailureMatrix(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unauthorized", http.StatusForbidden, "", ErrUnauthorized},
		{"rate_limited", http.StatusTooManyRequests, "", ErrRateLimited},
		{"server_failure", http.StatusBadGateway, "", ErrUnavailable},
		{"malformed", http.StatusOK, `{}`, ErrInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := (HTTPProvider{BaseURL: server.URL}).Lookup(context.Background(), "get_price", testScope())
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestLocalFakeHandlerReturnsVersionedOwnedFactsAndFaults(t *testing.T) {
	server := httptest.NewServer(NewFakeHandler("2026-10", ""))
	defer server.Close()
	fact, err := (HTTPProvider{BaseURL: server.URL, Version: "2026-10"}).Lookup(context.Background(), "get_logistics", &ragv1.RequestContext{TenantId: "v2-demo-tenant", UserId: "dev-user", ShopId: "shop-demo", AllowedShopIds: []string{"shop-demo"}})
	if err != nil || fact == nil || fact.ToolName != "get_logistics" {
		t.Fatalf("local logistics fake failed: fact=%#v err=%v", fact, err)
	}
	faultServer := httptest.NewServer(NewFakeHandler("2026-10", "rate_limited"))
	defer faultServer.Close()
	if _, err := (HTTPProvider{BaseURL: faultServer.URL, Version: "2026-10"}).Lookup(context.Background(), "get_stock", &ragv1.RequestContext{TenantId: "v2-demo-tenant", UserId: "dev-user", ShopId: "shop-demo", AllowedShopIds: []string{"shop-demo"}}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fake rate limit did not fail closed: %v", err)
	}
	partialServer := httptest.NewServer(NewFakeHandler("2026-10", "partial"))
	defer partialServer.Close()
	if _, err := (HTTPProvider{BaseURL: partialServer.URL, Version: "2026-10"}).Lookup(context.Background(), "get_price", &ragv1.RequestContext{TenantId: "v2-demo-tenant", UserId: "dev-user", ShopId: "shop-demo", AllowedShopIds: []string{"shop-demo"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("fake partial failure did not fail closed: %v", err)
	}
}
