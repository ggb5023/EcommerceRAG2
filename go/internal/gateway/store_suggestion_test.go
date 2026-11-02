package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedRetentionDependents(t *testing.T, ctx context.Context, db *pgxpool.Pool, tenantID, turnID, executionID, conversationID int64, requestID string) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO evidence_snapshot(tenant_id,execution_id,evidence_id,content,disclosure_class,customer_eligible,citation_index)
		VALUES($1,$2,'retention-evidence','body','external_allowed',true,1)`, tenantID, executionID)
	exec(`INSERT INTO citation(tenant_id,execution_id,evidence_id,citation_index) VALUES($1,$2,'retention-evidence',1)`, tenantID, executionID)
	exec(`INSERT INTO customer_reply(tenant_id,execution_id,text_plain,is_mock,can_copy,blocked_reason)
		VALUES($1,$2,'copyable',false,true,'')`, tenantID, executionID)
	exec(`INSERT INTO sse_event(tenant_id,execution_id,sequence,event_type,data_json) VALUES($1,$2,1,'completed','{}')`, tenantID, executionID)
	exec(`INSERT INTO turn_decision(tenant_id,conversation_id,turn_id,execution_id,state)
		VALUES($1,$2,$3,$4,'DONE')`, tenantID, fmt.Sprintf("%d", conversationID), fmt.Sprintf("%d", turnID), executionID)
	exec(`INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'assistant','answer')`, tenantID, turnID)
	exec(`INSERT INTO retrieval_trace(tenant_id,request_id,stage) VALUES($1,$2,'retrieve')`, tenantID, requestID)
	exec(`INSERT INTO routing_trace(tenant_id,request_id) VALUES($1,$2)`, tenantID, requestID)
	exec(`INSERT INTO tool_call_trace(tenant_id,request_id,tool_name) VALUES($1,$2,'search')`, tenantID, requestID)
	exec(`INSERT INTO feedback(tenant_id,turn_id,type) VALUES($1,$2,'helpful')`, tenantID, turnID)
}

func TestSuggestionRetentionKeepsFiftyLatest(t *testing.T) {
	ctx := context.Background()
	migration, gatewayDB := lifecycleDBs(t)
	f := newLifecycleFixture(t, migration)
	tx, err := gatewayDB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conversationID, err := ensureSuggestionConversation(ctx, tx, f.tenantID, f.userID, "shop-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	firstRequestID := fmt.Sprintf("retention-%s-00", f.stamp)
	firstTurnID, firstExecutionID, err := insertSuggestionTurn(ctx, tx, f.tenantID, conversationID,
		"query 0", firstRequestID, fmt.Sprintf("retention-key-%s-0", f.stamp),
		"payload-hash", "permission-revision", time.Now().Add(time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Seed every dependent FK after the first turn commits; other roles own some
	// of these tables, so the fixture uses the migration role.
	seedRetentionDependents(t, ctx, migration, f.tenantID, firstTurnID, firstExecutionID, conversationID, firstRequestID)

	tx, err = gatewayDB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 51; i++ {
		requestID := fmt.Sprintf("retention-%s-%02d", f.stamp, i)
		if _, _, err = insertSuggestionTurn(ctx, tx, f.tenantID, conversationID,
			fmt.Sprintf("query %d", i), requestID, fmt.Sprintf("retention-key-%s-%d", f.stamp, i),
			"payload-hash", "permission-revision", time.Now().Add(time.Hour), true); err != nil {
			t.Fatalf("insert suggestion %d: %v", i, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	tx, err = gatewayDB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = insertSuggestionTurn(ctx, tx, f.tenantID, conversationID,
		"query 51", fmt.Sprintf("retention-%s-51", f.stamp), fmt.Sprintf("retention-key-%s-51", f.stamp),
		"payload-hash", "permission-revision", time.Now().Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var turns, executions, messages, idempotency, evidence, citations, replies, events, decisions, traces, feedbackRows int
	if err = gatewayDB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM turn WHERE tenant_id=$1 AND conversation_id=$2 AND kind='suggestion'),
		(SELECT count(*) FROM conversation_execution e JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
			WHERE t.tenant_id=$1 AND t.conversation_id=$2 AND t.kind='suggestion'),
		(SELECT count(*) FROM message m JOIN turn t ON t.tenant_id=m.tenant_id AND t.id=m.turn_id
			WHERE t.tenant_id=$1 AND t.conversation_id=$2 AND t.kind='suggestion'),
		(SELECT count(*) FROM turn_idempotency i JOIN turn t ON t.tenant_id=i.tenant_id AND t.id=i.turn_id
			WHERE t.tenant_id=$1 AND t.conversation_id=$2 AND t.kind='suggestion'),
		(SELECT count(*) FROM evidence_snapshot WHERE tenant_id=$1 AND execution_id=$4),
		(SELECT count(*) FROM citation WHERE tenant_id=$1 AND execution_id=$4),
		(SELECT count(*) FROM customer_reply WHERE tenant_id=$1 AND execution_id=$4),
		(SELECT count(*) FROM sse_event WHERE tenant_id=$1 AND execution_id=$4),
		(SELECT count(*) FROM turn_decision WHERE tenant_id=$1 AND execution_id=$4),
		(SELECT count(*) FROM retrieval_trace WHERE tenant_id=$1 AND request_id LIKE 'retention-'||$3||'-%')
			+ (SELECT count(*) FROM routing_trace WHERE tenant_id=$1 AND request_id LIKE 'retention-'||$3||'-%')
			+ (SELECT count(*) FROM tool_call_trace WHERE tenant_id=$1 AND request_id LIKE 'retention-'||$3||'-%'),
		(SELECT count(*) FROM feedback WHERE tenant_id=$1 AND turn_id=$5)`,
		f.tenantID, conversationID, f.stamp, firstExecutionID, firstTurnID).Scan(
		&turns, &executions, &messages, &idempotency, &evidence, &citations, &replies, &events, &decisions, &traces, &feedbackRows); err != nil {
		t.Fatal(err)
	}
	if turns != suggestionTurnRetention || executions != suggestionTurnRetention ||
		messages != suggestionTurnRetention || idempotency != suggestionTurnRetention {
		t.Fatalf("retention counts turn/execution/message/idempotency = %d/%d/%d/%d, want %d each",
			turns, executions, messages, idempotency, suggestionTurnRetention)
	}
	if evidence != 0 || citations != 0 || replies != 0 || events != 0 || decisions != 0 || traces != 0 || feedbackRows != 0 {
		t.Fatalf("old turn left dependents: evidence=%d citations=%d replies=%d events=%d decisions=%d traces=%d feedback=%d",
			evidence, citations, replies, events, decisions, traces, feedbackRows)
	}
	var exists bool
	if err = gatewayDB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM turn WHERE tenant_id=$1 AND id=$2)`,
		f.tenantID, firstTurnID).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("oldest suggestion turn was not deleted")
	}
	var oldest, newest int
	if err = gatewayDB.QueryRow(ctx, `SELECT min(turn_no),max(turn_no) FROM turn
		WHERE tenant_id=$1 AND conversation_id=$2 AND kind='suggestion'`, f.tenantID, conversationID).Scan(&oldest, &newest); err != nil {
		t.Fatal(err)
	}
	if oldest != 3 || newest != 52 {
		t.Fatalf("retained turn numbers = %d..%d, want 3..52", oldest, newest)
	}
}
