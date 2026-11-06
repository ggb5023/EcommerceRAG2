package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"
)

func TestIntelObservationFilter(t *testing.T) {
	values := url.Values{"market": {"m"}, "category": {"c"}, "platform": {"p"},
		"since": {"2026-11-01T00:00:00Z"}, "query": {" 保温杯 "}, "limit": {"10"}}
	filter, ok := parseIntelObservationFilter(values)
	if !ok || filter.Market != "m" || filter.Category != "c" || filter.Platform != "p" ||
		filter.Limit != 10 || filter.Since == "" || filter.Query != "保温杯" {
		t.Fatalf("unexpected filter: %+v ok=%v", filter, ok)
	}
	if _, ok = parseIntelObservationFilter(url.Values{"query": {strings.Repeat("q", maxIntelQueryChars+1)}}); ok {
		t.Fatal("oversized query was accepted")
	}
	if _, ok = parseIntelObservationFilter(url.Values{"limit": {"0"}}); ok {
		t.Fatal("limit 0 was accepted")
	}
	if _, ok = parseIntelObservationFilter(url.Values{"limit": {"101"}}); ok {
		t.Fatal("limit 101 was accepted")
	}
	if _, ok = parseIntelObservationFilter(url.Values{"since": {"not-a-date"}}); ok {
		t.Fatal("invalid since was accepted")
	}
	defaults, ok := parseIntelObservationFilter(url.Values{})
	if !ok || defaults.Limit != 50 || defaults.Since != "" {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
}

func TestIntelWatchConfigValidation(t *testing.T) {
	if !validIntelWatchFields("market", "category", "platform") {
		t.Fatal("valid fields rejected")
	}
	for _, fields := range [][3]string{{"", "c", "p"}, {"m", "", "p"}, {"m", "c", ""}} {
		if validIntelWatchFields(fields[0], fields[1], fields[2]) {
			t.Fatalf("empty field accepted: %v", fields)
		}
	}
	if validIntelWatchFields("m", "c", string(make([]byte, 201))) {
		t.Fatal("oversized field accepted")
	}
}

func TestIntelEndpointsRequireRole(t *testing.T) {
	handler := &Handler{}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, request := range []struct {
		method, path string
	}{
		{"GET", "/admin/v1/intel/observations"},
		{"GET", "/admin/v1/intel/watch-configs"},
		{"POST", "/admin/v1/intel/watch-configs"},
		{"PATCH", "/admin/v1/intel/watch-configs/1"},
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, nil))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403", request.method, request.path, recorder.Code)
		}
	}
}

type fakeIntelRead struct {
	response *ragv1.ListObservationsResponse
	err      error
	request  *ragv1.ListObservationsRequest
}

func (f *fakeIntelRead) ListObservations(_ context.Context, in *ragv1.ListObservationsRequest,
	_ ...grpc.CallOption) (*ragv1.ListObservationsResponse, error) {
	f.request = in
	return f.response, f.err
}

func TestIntelObservationsAndWatchCRUD(t *testing.T) {
	api, migration, gateway, alias, handler := integrationHandlerWithIngest(t, false)
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	handler.Intel = &fakeIntelRead{response: &ragv1.ListObservationsResponse{Items: []*ragv1.Observation{
		{Market: "m", Category: "c", Platform: "p", SourceUrl: "https://example.test/a",
			Title: "title", Score: 0.9, PublishedDate: "2026-11-01", ObservedAt: "2026-11-06T10:00:00Z"},
	}}}
	t.Cleanup(func() {
		_, _ = migration.Exec(ctx, `DELETE FROM intel_watch_config WHERE tenant_id=$1 AND market LIKE $2`,
			handler.Session.TenantID, fmt.Sprintf("intel-test-%d%%", stamp))
	})

	observations := apiJSON(t, api, "GET",
		"/admin/v1/intel/observations?market=m&category=c&platform=p&query=保温杯&limit=10", nil, http.StatusOK)
	items, ok := observations["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("observations: %+v", observations)
	}
	if observations["query_degraded"] != false {
		t.Fatalf("query_degraded: %+v", observations)
	}
	fake := handler.Intel.(*fakeIntelRead)
	if fake.request.Market != "m" || fake.request.Limit != 10 || fake.request.Query != "保温杯" {
		t.Fatalf("filters were not passed through: %+v", fake.request)
	}
	item := items[0].(map[string]any)
	if item["similarity"] != float64(0) {
		t.Fatalf("similarity was not mapped: %+v", item)
	}

	market := fmt.Sprintf("intel-test-%d", stamp)
	created := apiJSON(t, api, "POST", "/admin/v1/intel/watch-configs", map[string]any{
		"market": market, "category": "c", "platform": "p", "enabled": true,
	}, http.StatusCreated)
	id := int64(created["id"].(float64))
	if created["enabled"] != true {
		t.Fatalf("created watch: %+v", created)
	}
	list := apiJSON(t, api, "GET", "/admin/v1/intel/watch-configs", nil, http.StatusOK)
	found := false
	for _, raw := range list["items"].([]any) {
		if raw.(map[string]any)["id"].(float64) == float64(id) {
			found = true
		}
	}
	if !found {
		t.Fatalf("created watch not listed: %+v", list)
	}
	patched := apiJSON(t, api, "PATCH", fmt.Sprintf("/admin/v1/intel/watch-configs/%d", id),
		map[string]any{"enabled": false}, http.StatusOK)
	if patched["enabled"] != false {
		t.Fatalf("patched watch: %+v", patched)
	}
	apiJSON(t, api, "POST", "/admin/v1/intel/watch-configs", map[string]any{
		"market": market, "category": "c", "platform": "p",
	}, http.StatusConflict)
	apiJSON(t, api, "PATCH", "/admin/v1/intel/watch-configs/999999999",
		map[string]any{"enabled": true}, http.StatusNotFound)

	var noRoleID int64
	noRoleName := fmt.Sprintf("intel-no-role-%d", stamp)
	if err := migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids)
		VALUES($1,$2,'operator',ARRAY[$3::text]) RETURNING id`,
		handler.Session.TenantID, noRoleName, handler.Session.Shop).Scan(&noRoleID); err != nil {
		t.Fatal(err)
	}
	var noRoleRevision string
	if err := migration.QueryRow(ctx, `SELECT permission_revision FROM app_user WHERE id=$1`,
		noRoleID).Scan(&noRoleRevision); err != nil {
		t.Fatal(err)
	}
	noRoleSession := &auth.Session{TenantID: handler.Session.TenantID, UserID: noRoleID,
		Tenant: alias, User: noRoleName, Role: "operator", Shop: handler.Session.Shop,
		AllowedShops: []string{handler.Session.Shop}, PermissionRevision: noRoleRevision}
	noRole := &Handler{DB: gateway, Session: noRoleSession,
		Authority: &auth.Authority{DB: gateway, Session: noRoleSession,
			SigningKey: handler.Authority.SigningKey, RevisionMu: &sync.RWMutex{}}}
	mux := http.NewServeMux()
	noRole.RegisterRoutes(mux)
	apiJSON(t, mux, "GET", "/admin/v1/intel/watch-configs", nil, http.StatusForbidden)
}
