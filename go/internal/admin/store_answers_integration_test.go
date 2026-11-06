package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func answersIntegrationPools(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("configuration unavailable")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.Trim(value, "\"'")
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
	return connect("V2_MIGRATION_DSN"), connect("V2_GATEWAY_DSN")
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestMerchantAnswersListsAndReviewsBothSources(t *testing.T) {
	migration, db := answersIntegrationPools(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stamp := fmt.Sprint(time.Now().UnixNano())
	var tenant, user int64
	if migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$1) RETURNING id`, "answers-"+stamp).Scan(&tenant) != nil {
		t.Fatal("tenant fixture failed")
	}
	if migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,$2,'owner',ARRAY['shop-test']) RETURNING id`, tenant, "answers-"+stamp).Scan(&user) != nil {
		t.Fatal("user fixture failed")
	}
	if _, err := migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,status) VALUES($1,'shop-test','Answers shop','active')`, tenant); err != nil {
		t.Fatal("shop fixture failed")
	}
	var conversation int64
	if migration.QueryRow(ctx, `INSERT INTO conversation(tenant_id,user_id,shop_id,title) VALUES($1,$2,'shop-test','answers') RETURNING id`, tenant, user).Scan(&conversation) != nil {
		t.Fatal("conversation fixture failed")
	}
	var turn int64
	if migration.QueryRow(ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,kind)
		VALUES($1,$2,1,$3,'员工问题','DONE','user') RETURNING id`, tenant, conversation, "req-"+stamp).Scan(&turn) != nil {
		t.Fatal("turn fixture failed")
	}
	facts := `[{"tool_name":"get_order_status","resource_id":"ORD-2609-0005","observed_at":"2026-09-29T12:00:00Z","is_mock":true,"value_json":"{\"order_id\":\"ORD-2609-0005\",\"status\":\"shipped\"}"}]`
	var execution int64
	if migration.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,answer,is_mock,facts_json)
		VALUES($1,$2,1,$3,'DONE','员工回答',$4,$5) RETURNING id`, tenant, turn, "exec-"+stamp, false, facts).Scan(&execution) != nil {
		t.Fatal("execution fixture failed")
	}
	if _, err := migration.Exec(ctx, `INSERT INTO evidence_snapshot(tenant_id,execution_id,evidence_id,content,disclosure_class,customer_eligible)
		VALUES($1,$2,'db-chunk-1','员工引用正文','external_allowed',true)`, tenant, execution); err != nil {
		t.Fatal("evidence fixture failed")
	}
	if _, err := migration.Exec(ctx, `INSERT INTO citation(tenant_id,execution_id,evidence_id,citation_index) VALUES($1,$2,'db-chunk-1',1)`, tenant, execution); err != nil {
		t.Fatal("citation fixture failed")
	}

	customerID, sessionID := "customer-"+stamp, "session-"+stamp
	if _, err := migration.Exec(ctx, `INSERT INTO customer_identity(id,tenant_id,shop_id) VALUES($1,$2,'shop-test')`, customerID, tenant); err != nil {
		t.Fatal("customer fixture failed")
	}
	if _, err := migration.Exec(ctx, `INSERT INTO customer_session(id,tenant_id,shop_id,customer_id,token_sha256,csrf_token,expires_at,idle_until)
		VALUES($1,$2,'shop-test',$3,$4,'csrf',now()+interval '1 day',now()+interval '1 day')`,
		sessionID, tenant, customerID, tokenHash(stamp)); err != nil {
		t.Fatal("session fixture failed")
	}
	customerConversation := "cc-" + stamp
	if _, err := migration.Exec(ctx, `INSERT INTO customer_conversation(id,tenant_id,shop_id,customer_id,state) VALUES($1,$2,'shop-test',$3,'AI')`,
		customerConversation, tenant, customerID); err != nil {
		t.Fatal("customer conversation fixture failed")
	}
	if _, err := migration.Exec(ctx, `INSERT INTO customer_message(id,tenant_id,conversation_id,source,content) VALUES('q-'||$1,$2,$3,'customer','顾客问题')`,
		stamp, tenant, customerConversation); err != nil {
		t.Fatal("customer question fixture failed")
	}
	citationsJSON, _ := json.Marshal([]map[string]any{{
		"id": "db-chunk-2", "content": "顾客引用正文", "document_id": "doc-others",
		"version_id": "1", "rank": 1, "disclosure_class": "external_allowed", "customer_eligible": true,
	}})
	if _, err := migration.Exec(ctx, `INSERT INTO customer_message(id,tenant_id,conversation_id,source,content,citations_json)
		VALUES('a-'||$1,$2,$3,'assistant','顾客回答',$4)`, stamp, tenant, customerConversation, citationsJSON); err != nil {
		t.Fatal("customer answer fixture failed")
	}
	customerExecution := "ce-" + stamp
	if _, err := migration.Exec(ctx, `INSERT INTO customer_execution(id,tenant_id,conversation_id,epoch,session_id,state,behavior,answer,is_mock,facts_json)
		VALUES($1,$2,$3,1,$4,'DONE','ANSWER','顾客回答',false,$5)`, customerExecution, tenant, customerConversation, sessionID, facts); err != nil {
		t.Fatal("customer execution fixture failed")
	}
	if _, err := migration.Exec(ctx, `UPDATE customer_message SET execution_id=$1 WHERE id='a-'||$2`, customerExecution, stamp); err != nil {
		t.Fatal("customer execution binding failed")
	}

	store := Store{}
	rows, _, err := store.MerchantAnswers(ctx, db, tenant, []string{"shop-test"}, "all", nil, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two answers, got %d", len(rows))
	}
	kinds := map[string]bool{}
	for _, row := range rows {
		kinds[row.Kind] = true
		if row.ShopID != "shop-test" || row.Question == "" || row.Answer == "" {
			t.Fatalf("unexpected row %+v", row)
		}
	}
	if !kinds["staff"] || !kinds["customer"] {
		t.Fatalf("missing source kind: %v", kinds)
	}
	// Customer identity is never part of the row shape.
	if strings.Contains(fmt.Sprint(rows), customerID) || strings.Contains(fmt.Sprint(rows), sessionID) {
		t.Fatal("customer identity leaked into the answer list")
	}
	filtered, _, err := store.MerchantAnswers(ctx, db, tenant, []string{"shop-test"}, "customer", nil, nil, 10)
	if err != nil || len(filtered) != 1 || filtered[0].Kind != "customer" {
		t.Fatalf("customer filter failed: %v %v", filtered, err)
	}
	mock := true
	mockRows, _, err := store.MerchantAnswers(ctx, db, tenant, []string{"shop-test"}, "all", &mock, nil, 10)
	if err != nil || len(mockRows) != 0 {
		t.Fatalf("mock filter failed: %v %v", mockRows, err)
	}

	staff, err := store.MerchantAnswer(ctx, db, tenant, []string{"shop-test"}, "staff", strconv.FormatInt(turn, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(staff.Citations) != 1 || staff.Citations[0].EvidenceID != "db-chunk-1" || staff.Citations[0].Valid {
		t.Fatalf("staff citations: %+v", staff.Citations)
	}
	if len(staff.Facts) != 1 || staff.Facts[0].ToolName != "get_order_status" {
		t.Fatalf("staff facts: %+v", staff.Facts)
	}
	review, err := store.MerchantAnswer(ctx, db, tenant, []string{"shop-test"}, "customer", "a-"+stamp)
	if err != nil {
		t.Fatal(err)
	}
	if review.Question != "顾客问题" || len(review.Citations) != 1 || review.Citations[0].EvidenceID != "db-chunk-2" {
		t.Fatalf("customer detail: %+v", review)
	}
	if len(review.Facts) != 1 || review.Facts[0].ResourceID != "ORD-2609-0005" {
		t.Fatalf("customer facts: %+v", review.Facts)
	}
	if _, err = store.MerchantAnswer(ctx, db, tenant, []string{"other-shop"}, "customer", "a-"+stamp); err == nil {
		t.Fatal("foreign shop read was accepted")
	}
	if err = store.RecordAnswerViewAudit(ctx, db, tenant, user, "customer", "a-"+stamp); err != nil {
		t.Fatal(err)
	}
	var audited int
	if err = migration.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE tenant_id=$1 AND action='merchant.answer.view' AND resource_id=$2`,
		tenant, "a-"+stamp).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit row missing: %d %v", audited, err)
	}
}
