package customer

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDatabaseAfterSalesScopeAllowsAnEmptyDocumentGrant(t *testing.T) {
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("protected integration configuration required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			values[k] = strings.Trim(v, "\"'")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connect := func(key string) *pgxpool.Pool {
		pool, err := pgxpool.New(ctx, values[key])
		if err != nil {
			t.Fatal("database unavailable")
		}
		t.Cleanup(pool.Close)
		var name string
		var super, bypass bool
		if pool.QueryRow(ctx, `SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&name, &super, &bypass) != nil || !strings.HasPrefix(name, "ecr_v2_test_") || super || bypass {
			t.Fatal("isolated restricted database required")
		}
		return pool
	}
	migration, db := connect("V2_MIGRATION_DSN"), connect("V2_GATEWAY_DSN")
	stamp := ID()
	var tenant int64
	if migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$1) RETURNING id`, "scope-"+stamp).Scan(&tenant) != nil {
		t.Fatal("tenant fixture failed")
	}
	if _, err = migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,status,customer_chat_enabled)
		VALUES($1,'shop-test','Scope shop','active',true)`, tenant); err != nil {
		t.Fatal("shop fixture failed")
	}
	customer, session := "customer-"+stamp, "session-"+stamp
	if _, err = migration.Exec(ctx, `INSERT INTO customer_identity(id,tenant_id,shop_id) VALUES($1,$2,'shop-test')`, customer, tenant); err != nil {
		t.Fatal("customer fixture failed")
	}
	if _, err = migration.Exec(ctx, `INSERT INTO customer_session(id,tenant_id,shop_id,customer_id,token_sha256,csrf_token,expires_at,idle_until)
		VALUES($1,$2,'shop-test',$3,$4,'csrf',now()+interval '1 day',now()+interval '1 day')`,
		session, tenant, customer, Hash(ID())); err != nil {
		t.Fatal("session fixture failed")
	}
	createConversation := func(serviceType, orderID string) (conversationID, executionID string) {
		conversationID, executionID = "conv-"+ID(), "exec-"+ID()
		if _, err = migration.Exec(ctx, `INSERT INTO customer_conversation(id,tenant_id,shop_id,customer_id,state,service_type,order_id,epoch,active_execution)
			VALUES($1,$2,'shop-test',$3,'AI',$4,NULLIF($5,''),1,$6)`,
			conversationID, tenant, customer, serviceType, orderID, executionID); err != nil {
			t.Fatal("conversation fixture failed")
		}
		if _, err = migration.Exec(ctx, `INSERT INTO customer_execution(id,tenant_id,conversation_id,epoch,session_id,state)
			VALUES($1,$2,$3,1,$4,'PENDING')`, executionID, tenant, conversationID, session); err != nil {
			t.Fatal("execution fixture failed")
		}
		return conversationID, executionID
	}
	tx, err := migration.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO customer_conversation(id,tenant_id,shop_id,customer_id,state,service_type,order_id)
		VALUES($1,$2,'shop-test',$3,'AI','after_sales',NULL)`, "bad-"+ID(), tenant, customer); err == nil {
		tx.Rollback(ctx)
		t.Fatal("after_sales with a NULL order passed the lane CHECK")
	}
	tx.Rollback(ctx)

	key := bytes.Repeat([]byte{7}, 32)
	afterConversation, afterExecution := createConversation("after_sales", "ORD-2609-0005")
	scope, err := auth.IssueCustomerScope(ctx, db, key, session, afterConversation, afterExecution)
	if err != nil {
		t.Fatalf("after-sales scope with no documents: %v", err)
	}
	if len(scope.AllowedDocumentIds) != 0 || scope.ServiceType != "after_sales" || scope.CustomerOrderId != "ORD-2609-0005" {
		t.Fatalf("unexpected after-sales scope %+v", scope)
	}
	authority := &auth.Authority{DB: db, SigningKey: key}
	if err = authority.ValidateScope(ctx, scope); err != nil {
		t.Fatalf("signed empty-document scope rejected: %v", err)
	}
	preConversation, preExecution := createConversation("pre_sales", "")
	if _, err = auth.IssueCustomerScope(ctx, db, key, session, preConversation, preExecution); err == nil || err.Error() != "knowledge_missing" {
		t.Fatalf("pre-sales scope with no documents: %v", err)
	}
}
