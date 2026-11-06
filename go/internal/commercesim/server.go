package commercesim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Server exposes the dataset through the facts contract plus a read-only
// development REST surface. Fault injection exists for the facts matrix only.
type Server struct {
	Dataset *Dataset
	Fault   string
	Now     func() time.Time

	mu         sync.Mutex
	tickets    []Ticket
	nextTicket int
}

func New(dataset *Dataset) *Server {
	server := &Server{Dataset: dataset, Now: time.Now}
	server.tickets = append(server.tickets, dataset.Tickets...)
	server.nextTicket = len(server.tickets) + 1
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /v1/facts/{tool}", s.fact)
	mux.HandleFunc("GET /dev/v1/orders", s.listOrders)
	mux.HandleFunc("GET /dev/v1/orders/{id}", s.getOrder)
	mux.HandleFunc("GET /dev/v1/inventory/{sku}", s.getInventory)
	mux.HandleFunc("GET /dev/v1/shipments/{orderID}", s.getShipments)
	mux.HandleFunc("GET /dev/v1/tickets", s.listTickets)
	mux.HandleFunc("POST /dev/v1/tickets", s.createTicket)
	return mux
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "dataset": s.Dataset.Manifest.DatasetID})
}

func (s *Server) fact(w http.ResponseWriter, r *http.Request) {
	switch s.Fault {
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
	case "malformed":
		writeJSON(w, http.StatusOK, map[string]any{"version": "wrong"})
		return
	case "timeout":
		timer := time.NewTimer(4 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
		}
		return
	}
	version := r.Header.Get("X-Facts-Version")
	tenant := r.Header.Get("X-Tenant-ID")
	user := r.Header.Get("X-User-ID")
	shop := r.Header.Get("X-Shop-ID")
	resource := r.Header.Get("X-Fact-Resource-ID")
	if version == "" || version != s.Dataset.Manifest.FactsVersion {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported facts version"})
		return
	}
	if tenant != s.Dataset.Manifest.TenantID || shop != s.Dataset.Manifest.ShopID || strings.TrimSpace(user) == "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	tool := r.PathValue("tool")
	bound := tool == "get_stock" || tool == "get_price" ||
		tool == "get_order_status" || tool == "get_logistics"
	if bound && strings.TrimSpace(resource) == "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	value, ok := s.factValue(tool, resource)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if s.Fault == "wrong_shop" {
		shop = "other-shop"
	}
	resourceID := ""
	if bound {
		resourceID = resource
	}
	if s.Fault == "wrong_order" || s.Fault == "wrong_resource" {
		if object, ok := value.(map[string]any); ok {
			if _, orderBound := object["order_id"]; orderBound {
				object["order_id"] = "OTHER-ORDER"
			}
			if _, skuBound := object["sku"]; skuBound {
				object["sku"] = "OTHER-SKU"
			}
		}
		resourceID = "OTHER-RESOURCE"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version, "tenant_id": tenant, "shop_id": shop, "tool_name": tool,
		"value_json": value, "observed_at": s.now().Format(time.RFC3339),
		"is_mock": true, "resource_id": resourceID,
	})
}

func (s *Server) factValue(tool, resource string) (any, bool) {
	switch tool {
	case "get_stock":
		inventory, ok := s.Dataset.Inventory[resource]
		if !ok {
			return nil, false
		}
		available := inventory.OnHand - inventory.Reserved
		if available < 0 {
			available = 0
		}
		return map[string]any{
			"sku": resource, "available": available, "on_hand": inventory.OnHand,
			"reserved": inventory.Reserved, "warehouse": inventory.Warehouse,
			"as_of": s.Dataset.Manifest.ReferenceDate,
		}, true
	case "get_price":
		product, ok := s.Dataset.Products[resource]
		if !ok {
			return nil, false
		}
		return map[string]any{
			"sku": resource, "amount_cents": product.PriceCents, "currency": product.Currency,
		}, true
	case "get_order_status":
		order, ok := s.Dataset.Orders[resource]
		if !ok {
			return nil, false
		}
		return map[string]any{
			"order_id": resource, "status": order.Status, "placed_at": order.PlacedAt,
			"item_count": len(s.Dataset.ItemsByOrder[resource]),
		}, true
	case "get_logistics":
		order, ok := s.Dataset.Orders[resource]
		if !ok {
			return nil, false
		}
		shipments := s.Dataset.ShipmentsByOrder[resource]
		if len(shipments) == 0 {
			return map[string]any{
				"order_id": resource, "status": "not_shipped", "order_status": order.Status,
				"carrier": "", "tracking_no": "",
			}, true
		}
		shipment := shipments[len(shipments)-1]
		latest := map[string]any{}
		if events := s.Dataset.EventsByShipment[shipment.ID]; len(events) > 0 {
			event := events[len(events)-1]
			latest = map[string]any{"event_at": event.EventAt, "location": event.Location, "status": event.Status}
		}
		return map[string]any{
			"order_id": resource, "status": shipment.Status, "carrier": shipment.Carrier,
			"tracking_no": shipment.TrackingNo, "shipped_at": shipment.ShippedAt,
			"delivered_at": shipment.DeliveredAt, "last_event": latest,
		}, true
	case "compare_products":
		left, right, ok := s.comparison()
		if !ok {
			return nil, false
		}
		return map[string]any{
			"left": left.SKU, "right": right.SKU,
			"left_price_cents": left.PriceCents, "right_price_cents": right.PriceCents,
			"price_delta_cents": right.PriceCents - left.PriceCents,
			"currency":          left.Currency,
		}, true
	default:
		return nil, false
	}
}

func (s *Server) comparison() (Product, Product, bool) {
	featured := s.Dataset.Manifest.FeaturedComparison
	if len(featured) >= 2 {
		left, leftOK := s.Dataset.Products[featured[0]]
		right, rightOK := s.Dataset.Products[featured[1]]
		return left, right, leftOK && rightOK
	}
	if len(s.Dataset.ProductOrder) < 2 {
		return Product{}, Product{}, false
	}
	return s.Dataset.Products[s.Dataset.ProductOrder[0]],
		s.Dataset.Products[s.Dataset.ProductOrder[1]], true
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	orders := make([]map[string]any, 0, len(s.Dataset.OrderOrder))
	for _, id := range s.Dataset.OrderOrder {
		order := s.Dataset.Orders[id]
		orders = append(orders, map[string]any{
			"order_id": order.ID, "status": order.Status, "placed_at": order.PlacedAt,
			"item_count": len(s.Dataset.ItemsByOrder[id]),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, ok := s.Dataset.Orders[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "order not found"})
		return
	}
	items := make([]map[string]any, 0)
	for _, item := range s.Dataset.ItemsByOrder[id] {
		title := ""
		if product, found := s.Dataset.Products[item.SKU]; found {
			title = product.Title
		}
		items = append(items, map[string]any{"sku": item.SKU, "title": title, "quantity": item.Quantity})
	}
	shipments := make([]map[string]any, 0)
	for _, shipment := range s.Dataset.ShipmentsByOrder[id] {
		shipments = append(shipments, map[string]any{
			"shipment_id": shipment.ID, "carrier": shipment.Carrier,
			"tracking_no": shipment.TrackingNo, "status": shipment.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"order_id": order.ID, "status": order.Status, "placed_at": order.PlacedAt,
		"updated_at": order.UpdatedAt, "items": items, "shipments": shipments,
	})
}

func (s *Server) getInventory(w http.ResponseWriter, r *http.Request) {
	sku := r.PathValue("sku")
	inventory, ok := s.Dataset.Inventory[sku]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "sku not found"})
		return
	}
	product := s.Dataset.Products[sku]
	available := inventory.OnHand - inventory.Reserved
	if available < 0 {
		available = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sku": sku, "title": product.Title, "on_hand": inventory.OnHand,
		"reserved": inventory.Reserved, "available": available,
		"warehouse": inventory.Warehouse, "updated_at": inventory.UpdatedAt,
	})
}

func (s *Server) getShipments(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("orderID")
	if _, ok := s.Dataset.Orders[orderID]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "order not found"})
		return
	}
	shipments := make([]map[string]any, 0)
	for _, shipment := range s.Dataset.ShipmentsByOrder[orderID] {
		events := make([]map[string]any, 0)
		for _, event := range s.Dataset.EventsByShipment[shipment.ID] {
			events = append(events, map[string]any{
				"seq": event.Seq, "event_at": event.EventAt, "location": event.Location,
				"status": event.Status,
			})
		}
		sort.Slice(events, func(i, j int) bool {
			return events[i]["seq"].(int) < events[j]["seq"].(int)
		})
		shipments = append(shipments, map[string]any{
			"shipment_id": shipment.ID, "carrier": shipment.Carrier,
			"tracking_no": shipment.TrackingNo, "status": shipment.Status,
			"shipped_at": shipment.ShippedAt, "delivered_at": shipment.DeliveredAt,
			"events": events,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"order_id": orderID, "shipments": shipments})
}

func (s *Server) listTickets(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tickets := append([]Ticket(nil), s.tickets...)
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (s *Server) createTicket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderID    string `json:"order_id"`
		ReasonCode string `json:"reason_code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid ticket body"})
		return
	}
	if _, ok := s.Dataset.Orders[body.OrderID]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "order not found"})
		return
	}
	if strings.TrimSpace(body.ReasonCode) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "reason_code is required"})
		return
	}
	s.mu.Lock()
	ticket := Ticket{
		ID:         fmt.Sprintf("SIM-TKT-%04d", s.nextTicket),
		OrderID:    body.OrderID,
		ReasonCode: body.ReasonCode,
		Status:     "open",
		CreatedAt:  s.now().Format(time.RFC3339),
	}
	s.nextTicket++
	s.tickets = append(s.tickets, ticket)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, ticket)
}
