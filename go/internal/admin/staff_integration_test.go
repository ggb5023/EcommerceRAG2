//go:build integration

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func staffIntegrationDSNs(t *testing.T) (migrator, gateway string) {
	t.Helper()
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV is required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatal("owned regular 0600 integration configuration required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("protected configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.Trim(value, "\"'")
		}
	}
	if values["V2_MIGRATION_DSN"] == "" || values["V2_GATEWAY_DSN"] == "" {
		t.Fatal("integration env needs V2_MIGRATION_DSN and V2_GATEWAY_DSN")
	}
	return values["V2_MIGRATION_DSN"], values["V2_GATEWAY_DSN"]
}

func TestStaffCreationRespectsGrantBoundary(t *testing.T) {
	migratorDSN, gatewayDSN := staffIntegrationDSNs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migrator, err := pgxpool.New(ctx, migratorDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()
	gateway, err := pgxpool.New(ctx, gatewayDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()

	suffix := strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	alias := "staff-it-" + suffix
	var tenant, ownerID, operatorID int64
	if err = migrator.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES('Staff IT',$1) RETURNING id`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,public_slug,customer_chat_enabled) VALUES($1,'shop-a','Shop A',$2,false)`, tenant, "staff-"+suffix); err != nil {
		t.Fatal(err)
	}
	if err = migrator.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'owner','owner',ARRAY['shop-a']) RETURNING id`, tenant).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err = migrator.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'operator','operator',ARRAY['shop-a']) RETURNING id`, tenant).Scan(&operatorID); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"merchant_admin", "knowledge_reviewer", "access_admin"} {
		if _, err = migrator.Exec(ctx, `INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,shop_ids,status,granted_by)
			VALUES($1,$2,$3,'shops',ARRAY['shop-a'],'active',$2)`, tenant, ownerID, role); err != nil {
			t.Fatal(err)
		}
	}

	api := func(userID int64, name, role string) http.Handler {
		session := &auth.Session{TenantID: tenant, UserID: userID, Tenant: alias, User: name,
			Role: role, Shop: "shop-a", AllowedShops: []string{"shop-a"}}
		authority := &auth.Authority{DB: gateway, Session: session, RevisionMu: &sync.RWMutex{}}
		handler := &Handler{DB: gateway, Session: session, Authority: authority, Profile: "v2_langchain_mock"}
		mux := http.NewServeMux()
		handler.RegisterRoutes(mux)
		return mux
	}
	post := func(router http.Handler, body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest("POST", "/admin/v1/staff", bytes.NewReader(raw))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	ownerAPI, operatorAPI := api(ownerID, "owner", "owner"), api(operatorID, "operator", "operator")

	if response := post(operatorAPI, map[string]any{"username": "denied", "role": "merchant_admin", "reason": "operator must not create staff"}); response.Code != http.StatusForbidden {
		t.Fatalf("operator got %d, want 403", response.Code)
	}
	if response := post(ownerAPI, map[string]any{"username": "badrole", "role": "access_admin", "reason": "access_admin is out of band"}); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "role_not_grantable") {
		t.Fatalf("access_admin got %d %s", response.Code, response.Body.String())
	}
	response := post(ownerAPI, map[string]any{"username": "staff-a", "role": "merchant_admin", "reason": "acceptance staff account"})
	if response.Code != http.StatusCreated {
		t.Fatalf("create staff got %d %s", response.Code, response.Body.String())
	}
	var created struct {
		Password   string `json:"initial_password"`
		MustChange bool   `json:"must_change_password"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Password) < 12 || !created.MustChange {
		t.Fatalf("unexpected creation payload: password length %d must_change %v", len(created.Password), created.MustChange)
	}
	var mustChange bool
	if err := migrator.QueryRow(ctx, `SELECT c.must_change_password FROM local_credential c
		JOIN app_user u ON u.tenant_id=c.tenant_id AND u.id=c.user_id
		WHERE u.tenant_id=$1 AND u.external_id='staff-a'`, tenant).Scan(&mustChange); err != nil || !mustChange {
		t.Fatalf("credential must_change_password=%v err=%v", mustChange, err)
	}
}
