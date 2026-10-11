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
		if tenant == "" || shop == "" || tenant != "v2-demo-tenant" || shop != "shop-demo" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		values := map[string]any{
			"get_stock":        map[string]any{"sku": "V2-CUP-480", "available": 12, "as_of": "2026-10-12"},
			"get_price":        map[string]any{"sku": "V2-CUP-480", "amount_cents": 8900, "currency": "CNY"},
			"get_order_status": map[string]any{"order_id": "V2-ORDER-001", "status": "in_transit"},
			"get_logistics":    map[string]any{"order_id": "V2-ORDER-001", "carrier": "fixture-express", "status": "in_transit"},
			"compare_products": map[string]any{"left": "V2-CUP-480", "right": "V2-CUP-750", "price_delta_cents": 2000},
		}
		value, ok := values[tool]
		if !ok {
			http.NotFound(w, r)
			return
		}
		payload := map[string]any{"version": version, "tenant_id": tenant, "shop_id": shop, "tool_name": tool, "value_json": value, "observed_at": fakeObservedAt, "is_mock": true}
		if fault == "wrong_owner" {
			payload["shop_id"] = "other-shop"
		}
		if fault == "empty" {
			payload["value_json"] = nil
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
}
