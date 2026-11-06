package commercesim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func loadTestDataset(t *testing.T) *Dataset {
	t.Helper()
	dataset, err := Load(filepath.Join("..", "..", "..", "data", "synthetic", "commerce-lab-v1"))
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	return dataset
}

func testScope(dataset *Dataset) *ragv1.RequestContext {
	return &ragv1.RequestContext{
		TenantId: dataset.Manifest.TenantID, UserId: "customer:CUST-0001",
		ShopId: dataset.Manifest.ShopID, AllowedShopIds: []string{dataset.Manifest.ShopID},
	}
}

func testServer(t *testing.T, fault string) (*Server, *Dataset, *httptest.Server) {
	t.Helper()
	dataset := loadTestDataset(t)
	server := New(dataset)
	server.Fault = fault
	server.Now = func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return server, dataset, httpServer
}

func lookup(t *testing.T, httpServer *httptest.Server, dataset *Dataset, tool, resource string) (*ragv1.Fact, error) {
	t.Helper()
	provider := facts.HTTPProvider{BaseURL: httpServer.URL, Version: dataset.Manifest.FactsVersion}
	return provider.Lookup(context.Background(), tool, testScope(dataset), resource)
}

func TestFactsContractServesEveryToolFromTheDataset(t *testing.T) {
	_, dataset, httpServer := testServer(t, "")
	shipped := ""
	for _, id := range dataset.OrderOrder {
		if dataset.Orders[id].Status == "shipped" {
			shipped = id
			break
		}
	}
	if shipped == "" {
		t.Fatal("dataset has no shipped order")
	}
	stock, err := lookup(t, httpServer, dataset, "get_stock", "CLB-DRK-001")
	if err != nil {
		t.Fatalf("get_stock: %v", err)
	}
	var stockValue map[string]any
	if json.Unmarshal([]byte(stock.ValueJson), &stockValue) != nil || stockValue["sku"] != "CLB-DRK-001" {
		t.Fatalf("unexpected stock value %s", stock.ValueJson)
	}
	if stock.ResourceId != "CLB-DRK-001" || !stock.IsMock || stock.ObservedAt != "2026-09-29T12:00:00Z" {
		t.Fatalf("unexpected stock fact %+v", stock)
	}
	price, err := lookup(t, httpServer, dataset, "get_price", "CLB-DRK-002")
	if err != nil {
		t.Fatalf("get_price: %v", err)
	}
	var priceValue map[string]any
	if json.Unmarshal([]byte(price.ValueJson), &priceValue) != nil || priceValue["amount_cents"] != float64(8900) {
		t.Fatalf("unexpected price value %s", price.ValueJson)
	}
	status, err := lookup(t, httpServer, dataset, "get_order_status", shipped)
	if err != nil {
		t.Fatalf("get_order_status: %v", err)
	}
	var statusValue map[string]any
	if json.Unmarshal([]byte(status.ValueJson), &statusValue) != nil || statusValue["order_id"] != shipped {
		t.Fatalf("unexpected order value %s", status.ValueJson)
	}
	logistics, err := lookup(t, httpServer, dataset, "get_logistics", shipped)
	if err != nil {
		t.Fatalf("get_logistics: %v", err)
	}
	var logisticsValue map[string]any
	if json.Unmarshal([]byte(logistics.ValueJson), &logisticsValue) != nil ||
		logisticsValue["tracking_no"] == "" || logisticsValue["carrier"] == "" {
		t.Fatalf("unexpected logistics value %s", logistics.ValueJson)
	}
	comparison, err := lookup(t, httpServer, dataset, "compare_products", "")
	if err != nil {
		t.Fatalf("compare_products: %v", err)
	}
	if comparison.ResourceId != "" {
		t.Fatalf("compare_products must not bind a resource: %+v", comparison)
	}
}

func TestFactsRejectUnknownOrdersAndForeignShops(t *testing.T) {
	_, dataset, httpServer := testServer(t, "")
	if _, err := lookup(t, httpServer, dataset, "get_order_status", "ORD-UNKNOWN"); !errors.Is(err, facts.ErrInvalidResponse) {
		t.Fatalf("unknown order should be invalid response, got %v", err)
	}
	foreign := testScope(dataset)
	foreign.ShopId = "other-shop"
	foreign.AllowedShopIds = []string{"other-shop"}
	provider := facts.HTTPProvider{BaseURL: httpServer.URL, Version: dataset.Manifest.FactsVersion}
	if _, err := provider.Lookup(context.Background(), "get_order_status", foreign, dataset.OrderOrder[0]); !errors.Is(err, facts.ErrUnauthorized) {
		t.Fatalf("foreign shop should be unauthorized, got %v", err)
	}
}

func TestFactsFaultMatrixMapsToProviderErrors(t *testing.T) {
	cases := map[string]error{
		"unauthorized": facts.ErrUnauthorized,
		"rate_limited": facts.ErrRateLimited,
		"server_error": facts.ErrUnavailable,
		"malformed":    facts.ErrInvalidResponse,
		"wrong_shop":   facts.ErrInvalidResponse,
		"wrong_order":  facts.ErrInvalidResponse,
	}
	for fault, want := range cases {
		t.Run(fault, func(t *testing.T) {
			_, dataset, httpServer := testServer(t, fault)
			_, err := lookup(t, httpServer, dataset, "get_stock", "CLB-DRK-001")
			if !errors.Is(err, want) {
				t.Fatalf("fault %s: want %v, got %v", fault, want, err)
			}
		})
	}
}

func TestDevRestReadsOrdersInventoryShipmentsAndTickets(t *testing.T) {
	server, dataset, httpServer := testServer(t, "")
	response, err := http.Get(httpServer.URL + "/dev/v1/orders")
	if err != nil {
		t.Fatal(err)
	}
	var orders struct {
		Orders []map[string]any `json:"orders"`
	}
	if err = json.NewDecoder(response.Body).Decode(&orders); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(orders.Orders) != len(dataset.OrderOrder) {
		t.Fatalf("orders %d != %d", len(orders.Orders), len(dataset.OrderOrder))
	}
	missing, err := http.Get(httpServer.URL + "/dev/v1/orders/ORD-UNKNOWN")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown order status %d", missing.StatusCode)
	}
	stock, err := http.Get(httpServer.URL + "/dev/v1/inventory/CLB-DRK-001")
	if err != nil {
		t.Fatal(err)
	}
	var stockBody map[string]any
	if err = json.NewDecoder(stock.Body).Decode(&stockBody); err != nil {
		t.Fatal(err)
	}
	stock.Body.Close()
	if stockBody["title"] == "" {
		t.Fatalf("inventory response has no title: %v", stockBody)
	}
	body, _ := json.Marshal(map[string]string{"order_id": dataset.OrderOrder[0], "reason_code": "damaged"})
	created, err := http.Post(httpServer.URL+"/dev/v1/tickets", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("ticket create status %d", created.StatusCode)
	}
	server.mu.Lock()
	count := len(server.tickets)
	server.mu.Unlock()
	if count != len(dataset.Tickets)+1 {
		t.Fatalf("ticket count %d", count)
	}
}
