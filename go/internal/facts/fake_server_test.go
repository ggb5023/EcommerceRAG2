package facts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeRequest(t *testing.T, handler http.Handler, tool, tenant, shop, user, resource string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/facts/"+tool, nil)
	request.Header.Set("X-Tenant-ID", tenant)
	request.Header.Set("X-Shop-ID", shop)
	request.Header.Set("X-User-ID", user)
	request.Header.Set("X-Fact-Resource-ID", resource)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestFakeSupportsDocumentOrderAndBagResources(t *testing.T) {
	handler := NewFakeHandler("v1", "")
	for _, test := range []struct {
		tool, resource string
	}{
		{"get_order_status", "DEMO-1001"},
		{"get_logistics", "DEMO-1001"},
		{"get_stock", "SKU-BAG-RED-20"},
		{"get_price", "SKU-BAG-RED-20"},
		{"get_order_status", "V2-ORDER-001"},
		{"get_stock", "SKU-EAR-200"},
	} {
		if response := fakeRequest(t, handler, test.tool, "v2-demo-tenant", "shop-demo", "dev-user", test.resource); response.Code != http.StatusOK {
			t.Fatalf("%s %s: got %d, want 200", test.tool, test.resource, response.Code)
		}
	}
}

func TestFakePriceMatchesDocumentSnapshot(t *testing.T) {
	response := fakeRequest(t, NewFakeHandler("v1", ""), "get_price", "v2-demo-tenant", "shop-demo", "dev-user", "SKU-BAG-RED-20")
	var payload struct {
		ValueJSON struct {
			AmountCents int64 `json:"amount_cents"`
		} `json:"value_json"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ValueJSON.AmountCents != 12900 {
		t.Fatalf("amount_cents=%d, want 12900", payload.ValueJSON.AmountCents)
	}
}

func TestFakeRejectsUnknownResourcesAndCrossTenant(t *testing.T) {
	handler := NewFakeHandler("v1", "")
	if response := fakeRequest(t, handler, "get_stock", "v2-demo-tenant", "shop-demo", "dev-user", "SKU-UNKNOWN"); response.Code != http.StatusForbidden {
		t.Fatalf("unknown stock: got %d, want 403", response.Code)
	}
	if response := fakeRequest(t, handler, "get_order_status", "v2-demo-tenant", "shop-demo", "dev-user", "ORDER-UNKNOWN"); response.Code != http.StatusForbidden {
		t.Fatalf("unknown order: got %d, want 403", response.Code)
	}
	if response := fakeRequest(t, handler, "get_stock", "other-tenant", "shop-demo", "dev-user", "SKU-BAG-RED-20"); response.Code != http.StatusForbidden {
		t.Fatalf("cross tenant: got %d, want 403", response.Code)
	}
	if response := fakeRequest(t, handler, "get_price", "v2-demo-tenant", "other-shop", "dev-user", "SKU-BAG-RED-20"); response.Code != http.StatusForbidden {
		t.Fatalf("cross shop: got %d, want 403", response.Code)
	}
}
