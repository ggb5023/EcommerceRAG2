package facts

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const fakeObservedAt = "2026-10-12T00:00:00Z"

// NewFakeHandler returns a deterministic, versioned merchant API for local
// acceptance. It is deliberately separate from the production adapter and
// supports explicit fault injection through FACTS_FAKE_FAULT.
func NewFakeHandler(version, fault string) http.Handler {
	if strings.TrimSpace(version) == "" {
		version = "v1"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/facts/") || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		tool := strings.TrimPrefix(r.URL.Path, "/v1/facts/")
		if fault == "partial" && tool == "get_price" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		switch strings.TrimSpace(fault) {
		case "unauthenticated":
			w.WriteHeader(http.StatusUnauthorized)
			return
		case "unauthorized":
			w.WriteHeader(http.StatusForbidden)
			return
		case "rate_limited":
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case "server_error":
			w.WriteHeader(http.StatusBadGateway)
			return
		case "timeout":
			timer := time.NewTimer(4 * time.Second)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
			return
		case "malformed":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"wrong"}`))
			return

		}
		tenant, shop := r.Header.Get("X-Tenant-ID"), r.Header.Get("X-Shop-ID")
		resourceID := r.Header.Get("X-Fact-Resource-ID")
		if tenant == "" || shop == "" || tenant != "v2-demo-tenant" || shop != "shop-demo" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if tool == "get_order_status" || tool == "get_logistics" {
			if r.Header.Get("X-User-ID") != "dev-user" || resourceID != "V2-ORDER-001" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		if tool == "get_stock" || tool == "get_price" {
			if resourceID != "V2-CUP-480" && resourceID != "V2-CUP-750" && resourceID != "SKU-EAR-200" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		stock, price := 12, 8900
		if resourceID == "V2-CUP-750" {
			stock, price = 0, 10900
		}
		if resourceID == "SKU-EAR-200" {
			// Independently versioned fake fact, deliberately different from
			// the historical document snapshot. It is not merchant truth.
			stock, price = 7, 15900
		}
		values := map[string]any{
			"get_stock":        map[string]any{"sku": resourceID, "available": stock, "as_of": "2026-10-12"},
			"get_price":        map[string]any{"sku": resourceID, "amount_cents": price, "currency": "CNY"},
			"get_order_status": map[string]any{"order_id": resourceID, "status": "in_transit"},
			"get_logistics":    map[string]any{"order_id": resourceID, "carrier": "fixture-express", "status": "in_transit"},
			"compare_products": map[string]any{"left": "V2-CUP-480", "right": "V2-CUP-750", "price_delta_cents": 2000},
		}
		value, ok := values[tool]
		if !ok {
			http.NotFound(w, r)
			return
		}
		payload := map[string]any{"version": version, "tenant_id": tenant, "shop_id": shop, "tool_name": tool, "value_json": value, "observed_at": fakeObservedAt, "is_mock": true, "resource_id": resourceID}
		if fault == "wrong_owner" {
			payload["shop_id"] = "other-shop"
		}
		if fault == "wrong_order" || fault == "wrong_order_body" {
			if fault == "wrong_order" {
				payload["resource_id"] = "OTHER-ORDER"
			}
			if object, ok := payload["value_json"].(map[string]any); ok {
				object["order_id"] = "OTHER-ORDER"
			}
		}
		if fault == "wrong_product" {
			payload["resource_id"] = "OTHER-SKU"
		}
		if fault == "wrong_product" || fault == "wrong_product_body" {
			if object, ok := value.(map[string]any); ok {
				object["sku"] = "OTHER-SKU"
			}
		}
		if fault == "missing_fields" {
			delete(payload, "value_json")
		}
		if fault == "missing_version" {
			delete(payload, "version")
		}
		if fault == "empty" {
			payload["value_json"] = nil
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
}
