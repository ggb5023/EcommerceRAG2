//go:build integration

package admin

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The audit row must follow the caller's transaction: if the surrounding
// business transaction rolls back, no admin_audit row may survive.
func TestAppendAuditRollsBackWithCaller(t *testing.T) {
	migratorDSN, _ := staffIntegrationDSNs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migrator, err := pgxpool.New(ctx, migratorDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()

	var tenant int64
	if err = migrator.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES('Audit IT',$1) RETURNING id`,
		"audit-it-"+time.Now().Format("150405.000000")).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = migrator.Exec(context.Background(), `DELETE FROM tenant WHERE id=$1`, tenant) }()

	var before int64
	if err = migrator.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE tenant_id=$1`, tenant).Scan(&before); err != nil {
		t.Fatal(err)
	}

	tx, err := migrator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var store Store
	action := "audit.it.rollback"
	if err = store.AppendAudit(ctx, tx, AuditEntry{
		TenantID: tenant, UserID: tenant, Action: action, ResourceType: "tenant",
		ResourceID: "it", Result: "success", Metadata: map[string]any{"case": "rollback"},
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("append inside transaction: %v", err)
	}
	var inside int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE tenant_id=$1 AND action=$2`, tenant, action).Scan(&inside); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if inside != 1 {
		_ = tx.Rollback(ctx)
		t.Fatalf("audit row not visible inside its own transaction: %d", inside)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var after int64
	if err = migrator.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE tenant_id=$1 AND action=$2`, tenant, action).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 || before != 0 {
		t.Fatalf("audit survived rollback: before=%d after=%d", before, after)
	}
}
