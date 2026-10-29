//go:build integration

package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// An approval changes the request state and issues the grant inside one
// transaction. If the grant is refused, the request must stay pending.
func TestDecidePermissionRequestRollsBackWithGrantFailure(t *testing.T) {
	migratorDSN, _ := staffIntegrationDSNs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migrator, err := pgxpool.New(ctx, migratorDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()

	suffix := time.Now().Format("150405.000000")
	var tenant, requester, actor int64
	if err = migrator.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES('Req IT',$1) RETURNING id`, "req-it-"+suffix).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = migrator.Exec(context.Background(), `DELETE FROM tenant WHERE id=$1`, tenant) }()
	if _, err = migrator.Exec(ctx, `INSERT INTO shop(tenant_id,id,name) VALUES($1,'shop-req','Req')`, tenant); err != nil {
		t.Fatal(err)
	}
	if err = migrator.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'requester','admin',ARRAY['shop-req']) RETURNING id`, tenant).Scan(&requester); err != nil {
		t.Fatal(err)
	}
	if err = migrator.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'actor','admin',ARRAY['shop-req']) RETURNING id`, tenant).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	// The actor deliberately holds no admin_role_grant, so ecr_issue_admin_grant
	// must refuse the approval.

	var store Store
	hash := sha256.Sum256([]byte(suffix))
	createTx, err := migrator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requestID, err := store.CreatePermissionRequest(ctx, createTx, PermissionRequest{
		TenantID: tenant, RequestedBy: requester, Role: "knowledge_reviewer", ScopeKind: "shops",
		ShopIDs: []string{"shop-req"}, Reason: "integration approval request", PayloadHash: hex.EncodeToString(hash[:]),
	})
	if err != nil {
		_ = createTx.Rollback(ctx)
		t.Fatalf("create request: %v", err)
	}
	if err = createTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	tx, err := migrator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = store.DecidePermissionRequest(ctx, tx, PermissionDecision{
		TenantID: tenant, RequestID: requestID, ActorID: actor, Decision: "approved",
		Reason: "integration approval attempt", TargetUser: requester, Role: "knowledge_reviewer",
		ScopeKind: "shops", ShopIDs: []string{"shop-req"},
	})
	if err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("an approval without a grant must fail")
	}
	if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}

	var status string
	if err = migrator.QueryRow(ctx, `SELECT status FROM admin_permission_request WHERE tenant_id=$1 AND id=$2`, tenant, requestID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("request status after rollback = %q, want pending", status)
	}
	var grants int
	if err = migrator.QueryRow(ctx, `SELECT count(*) FROM admin_role_grant WHERE tenant_id=$1 AND user_id=$2`, tenant, requester).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Fatalf("grant rows after rollback = %d, want 0", grants)
	}
}

// A nil transaction is a programming error, not a silent new transaction.
func TestStoreRequestRequiresTransaction(t *testing.T) {
	var store Store
	if _, err := store.CreatePermissionRequest(context.Background(), nil, PermissionRequest{}); !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("create err = %v", err)
	}
	if err := store.DecidePermissionRequest(context.Background(), nil, PermissionDecision{}); !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("decide err = %v", err)
	}
	if err := store.WriteAgentReviewSetting(context.Background(), nil, AgentReviewSetting{}); !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("setting err = %v", err)
	}
}

// The agent-review row must follow the caller's transaction as well.
func TestWriteAgentReviewSettingRollsBackWithCaller(t *testing.T) {
	migratorDSN, _ := staffIntegrationDSNs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migrator, err := pgxpool.New(ctx, migratorDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()

	suffix := time.Now().Format("150405.000000")
	var tenant, actor int64
	if err = migrator.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES('Setting IT',$1) RETURNING id`, "setting-it-"+suffix).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = migrator.Exec(context.Background(), `DELETE FROM tenant WHERE id=$1`, tenant) }()
	if err = migrator.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'setter','admin',ARRAY[]::text[]) RETURNING id`, tenant).Scan(&actor); err != nil {
		t.Fatal(err)
	}

	tx, err := migrator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var store Store
	if err = store.WriteAgentReviewSetting(ctx, tx, AgentReviewSetting{
		TenantID: tenant, Enabled: true, PolicyVersion: "v1", ActorUserID: actor,
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("write setting: %v", err)
	}
	var inside int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM tenant_agent_review WHERE tenant_id=$1`, tenant).Scan(&inside); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if inside != 1 {
		_ = tx.Rollback(ctx)
		t.Fatalf("setting row not visible inside its own transaction: %d", inside)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err = migrator.QueryRow(ctx, `SELECT count(*) FROM tenant_agent_review WHERE tenant_id=$1`, tenant).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Fatalf("setting survived rollback: %d", after)
	}
}
