package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the active HTTP handlers and database functions with two identities.
// Only identity/grant fixtures use migration credentials; all requests use the
// restricted Gateway role. No answer, ingestion result, or audit is fabricated.
func TestDatabaseAdministrativeAccessLifecycle(t *testing.T) {
	config := os.Getenv("V2_INTEGRATION_ENV")
	if config == "" {
		t.Skip("V2_INTEGRATION_ENV is required")
	}
	info, err := os.Lstat(config)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("owned regular 0600 integration configuration required")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		t.Fatal("integration configuration owner mismatch")
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal("protected configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.Trim(value, "\"'")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connect := func(key, expectedRole string) *pgxpool.Pool {
		t.Helper()
		pool, err := pgxpool.New(ctx, values[key])
		if err != nil {
			t.Fatal("isolated database pool unavailable")
		}
		t.Cleanup(pool.Close)
		var database, role string
		var superuser, bypass bool
		err = pool.QueryRow(ctx, `SELECT current_database(),current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&database, &role, &superuser, &bypass)
		if err != nil || !strings.HasPrefix(database, "ecr_v2_test_") || (expectedRole != "" && role != expectedRole) || superuser || bypass {
			t.Fatal("restricted isolated database role required")
		}
		return pool
	}
	migration := connect("V2_MIGRATION_DSN", "")
	gateway := connect("V2_GATEWAY_DSN", "rag_v2_gateway")
	alias := fmt.Sprintf("admin-acceptance-%d", time.Now().UnixNano())
	var tenant, manager, member int64
	if migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$1) RETURNING id`, alias).Scan(&tenant) != nil {
		t.Fatal("could not create isolated tenant fixture")
	}
	if _, err = migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name) VALUES($1,'shop-a','A'),($1,'shop-b','B')`, tenant); err != nil {
		t.Fatal("could not create isolated shop fixtures")
	}
	for _, actor := range []struct {
		name, role string
		id         *int64
	}{{"manager", "admin", &manager}, {"member", "operator", &member}} {
		err = migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,$2,$3,ARRAY['shop-a']) RETURNING id`, tenant, actor.name, actor.role).Scan(actor.id)
		if err != nil {
			t.Fatal("could not create isolated identity fixture")
		}
	}
	for _, role := range []string{"access_admin", "merchant_admin", "knowledge_reviewer"} {
		_, err = migration.Exec(ctx, `INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,shop_ids,granted_by) VALUES($1,$2,$3,'shops',ARRAY['shop-a'],$2)`, tenant, manager, role)
		if err != nil {
			t.Fatal("could not create isolated manager grant fixture")
		}
	}
	router := func(user int64, name, role string) http.Handler {
		session := &auth.Session{TenantID: tenant, UserID: user, Tenant: alias, User: name, Role: role, Shop: "shop-a", AllowedShops: []string{"shop-a"}}
		authority := &auth.Authority{DB: gateway, Session: session, RevisionMu: &sync.RWMutex{}}
		handler := &Handler{DB: gateway, Session: session, Authority: authority, Profile: "v2_langchain_mock"}
		mux := http.NewServeMux()
		handler.RegisterRoutes(mux)
		return mux
	}
	managerAPI, memberAPI := router(manager, "manager", "admin"), router(member, "member", "operator")
	request := func(api http.Handler, method, path string, body any, expected int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal("request encoding failed")
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		var result map[string]any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("response is not JSON")
		}
		if w.Code != expected {
			t.Fatalf("%s %s returned %d, expected %d (code=%v)", method, path, w.Code, expected, result["code"])
		}
		return result
	}
	for _, path := range []string{"session", "access/accounts", "access/audit", "merchant/shops", "merchant/sources", "merchant/ingestion", "merchant/reviews", "merchant/versions"} {
		request(managerAPI, "GET", "/admin/v1/"+path, nil, 200)
		request(memberAPI, "GET", "/admin/v1/"+path, nil, 403)
	}
	request(managerAPI, "GET", "/admin/v1/merchant/agent-review", nil, 200)
	request(managerAPI, "PUT", "/admin/v1/merchant/agent-review", map[string]any{"enabled": true, "policy_version": "v1", "confirm": true, "reason": "Enable model assistance for isolated test"}, 200)
	request(managerAPI, "PUT", "/admin/v1/merchant/agent-review", map[string]any{"enabled": false, "policy_version": "v1", "confirm": true, "reason": "Disable model assistance for isolated test"}, 200)
	request(memberAPI, "PUT", "/admin/v1/merchant/agent-review", map[string]any{"enabled": true, "policy_version": "v1", "confirm": true, "reason": "Unauthorized model assistance change"}, 403)
	if _, err = migration.Exec(ctx, `UPDATE tenant_agent_review SET enabled=true WHERE tenant_id=$1`, tenant); err == nil {
		t.Fatal("tenant_agent_review update was not rejected")
	}
	if _, err = migration.Exec(ctx, `DELETE FROM tenant_agent_review WHERE tenant_id=$1`, tenant); err == nil {
		t.Fatal("tenant_agent_review delete was not rejected")
	}
	request(memberAPI, "GET", "/admin/v1/access/requests", nil, 200)
	request(managerAPI, "GET", "/admin/v1/merchant/shops?shop_id=shop-b", nil, 403)
	makeRequest := func(api http.Handler, role, scope string, shops []string, expected int) map[string]any {
		return request(api, "POST", "/admin/v1/access/requests", map[string]any{"role": role, "scope_kind": scope, "shop_ids": shops, "reason": "Request controlled isolated administrative access"}, expected)
	}
	makeRequest(memberAPI, "knowledge_reviewer", "shops", []string{"shop-a"}, 400)
	makeRequest(memberAPI, "merchant_admin", "shops", []string{"shop-b"}, 400)
	self := makeRequest(managerAPI, "merchant_admin", "shops", []string{"shop-a"}, 201)
	decision := map[string]any{"confirm": true, "decision": "approved", "reason": "Approve controlled isolated administrative access"}
	request(managerAPI, "POST", "/admin/v1/access/requests/"+self["id"].(string)+"/decision", decision, 409)
	memberRequests := request(memberAPI, "GET", "/admin/v1/access/requests", nil, 200)
	if len(memberRequests["items"].([]any)) != 0 {
		t.Fatal("member can read another user's permission request")
	}
	tenantRequest := makeRequest(memberAPI, "merchant_admin", "tenant", []string{}, 201)
	request(managerAPI, "POST", "/admin/v1/access/requests/"+tenantRequest["id"].(string)+"/decision", decision, 403)
	created := makeRequest(memberAPI, "merchant_admin", "shops", []string{"shop-a"}, 201)
	path := "/admin/v1/access/requests/" + created["id"].(string) + "/decision"
	request(managerAPI, "POST", path, map[string]any{"confirm": false, "decision": "approved", "reason": decision["reason"]}, 400)
	request(managerAPI, "POST", path, decision, 200)
	request(managerAPI, "POST", path, decision, 409)
	request(memberAPI, "GET", "/admin/v1/session", nil, 200)
	var grant int64
	if gateway.QueryRow(ctx, `SELECT id FROM admin_role_grant WHERE tenant_id=$1 AND user_id=$2 AND role='merchant_admin' AND status='active'`, tenant, member).Scan(&grant) != nil {
		t.Fatal("HTTP approval did not issue a real grant")
	}
	request(managerAPI, "POST", fmt.Sprintf("/admin/v1/access/grants/%d/revoke", grant), map[string]any{"confirm": true, "reason": "Revoke the temporary isolated administrative grant"}, 200)
	request(memberAPI, "GET", "/admin/v1/session", nil, 403)
	var auditCount int
	if gateway.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE tenant_id=$1`, tenant).Scan(&auditCount) != nil || auditCount < 5 {
		t.Fatal("administrative HTTP mutations lack persistent audit records")
	}
	if _, err = gateway.Exec(ctx, `UPDATE admin_audit SET reason=reason WHERE tenant_id=$1`, tenant); err == nil {
		t.Fatal("Gateway can mutate immutable audit records")
	}
	if _, err = gateway.Exec(ctx, `DELETE FROM admin_audit WHERE tenant_id=$1`, tenant); err == nil {
		t.Fatal("Gateway can delete immutable audit records")
	}
}
