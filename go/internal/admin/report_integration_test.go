package admin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
)

func TestReportHandlerIntegration(t *testing.T) {
	api, migration, gateway, _, handler := integrationHandlerWithIngest(t, false)
	path := writeReportFile(t, validReportJSON)
	t.Setenv("ECR_REPORT_PATH", path)

	result := apiJSON(t, api, "GET", "/admin/v1/report", nil, http.StatusOK)
	if len(result) != 5 {
		t.Fatalf("sections=%d want 5: %v", len(result), result)
	}
	if _, ok := result["leak"]; ok {
		t.Fatal("extra top-level key was not dropped")
	}

	ctx := context.Background()
	stamp := time.Now().UnixNano()
	var userID int64
	if err := migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids)
		VALUES($1,$2,'operator',ARRAY[$3::text]) RETURNING id`,
		handler.Session.TenantID, fmt.Sprintf("report-no-role-%d", stamp), handler.Session.Shop).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var revision string
	if err := migration.QueryRow(ctx, `SELECT permission_revision FROM app_user WHERE id=$1`, userID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	session := &auth.Session{TenantID: handler.Session.TenantID, UserID: userID,
		Tenant: handler.Session.Tenant, User: fmt.Sprintf("report-no-role-%d", stamp),
		Role: "operator", Shop: handler.Session.Shop, AllowedShops: []string{handler.Session.Shop},
		PermissionRevision: revision}
	authority := &auth.Authority{DB: gateway, Session: session,
		SigningKey: handler.Authority.SigningKey, RevisionMu: &sync.RWMutex{}}
	noRole := &Handler{DB: gateway, Session: session, Authority: authority}
	mux := http.NewServeMux()
	noRole.RegisterRoutes(mux)
	apiJSON(t, mux, "GET", "/admin/v1/report", nil, http.StatusForbidden)
}
