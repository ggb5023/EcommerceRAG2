package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCopySuggestionDeniedWhenNotCopyable(t *testing.T) {
	ctx := context.Background()
	migration, gatewayDB := lifecycleDBs(t)
	f := newLifecycleFixture(t, migration)
	if _, err := migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,status) VALUES($1,'shop-lifecycle','lifecycle','active')
		ON CONFLICT (tenant_id,id) DO NOTHING`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `UPDATE conversation SET kind='suggestion',shop_id='shop-lifecycle' WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `UPDATE turn SET kind='suggestion' WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.turnID); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `UPDATE conversation_execution SET is_mock=false WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.executionID); err != nil {
		t.Fatal(err)
	}
	tx, err := gatewayDB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = persistExecutionTerminal(ctx, tx, executionIdentity{tenantID: f.tenantID, executionID: f.executionID,
		turnID: f.turnID, conversationID: f.conversationID, executionNo: 1, requestID: "exec-" + f.stamp}, "DONE", "", "not copyable", nil); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var revision string
	if err = migration.QueryRow(ctx, `SELECT permission_revision FROM app_user WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.userID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	g := &gateway{db: gatewayDB, session: mockSession{TenantID: f.tenantID, UserID: f.userID,
		User: "user-" + f.stamp, Shop: "shop-lifecycle", AllowedShops: []string{"shop-lifecycle"},
		Role: "operator", PermissionRevision: revision}}
	request := httptest.NewRequest(http.MethodPost, "/v1/support/suggestions/exec-"+f.stamp+"/copy", nil)
	request.SetPathValue("id", "exec-"+f.stamp)
	recorder := httptest.NewRecorder()
	g.copySuggestion(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("copy status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	var audits int
	if err = migration.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE tenant_id=$1 AND actor_user_id=$2
		AND action='support.suggestion.copy' AND resource_id=$3 AND result='denied'`,
		f.tenantID, f.userID, "exec-"+f.stamp).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("denied copy audit rows=%d, want 1", audits)
	}
}
