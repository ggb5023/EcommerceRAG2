package gateway

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func updateExecutionTerminal(ctx context.Context, tx pgx.Tx, id executionIdentity, state, code, answer, answerHash, behavior string, factsJSON []byte) error {
	_, err := tx.Exec(ctx, `UPDATE conversation_execution SET status=$3,finish_reason=$3,error_code=NULLIF($4,''),answer=$5,answer_hash=$6,behavior=NULLIF($8,''),facts_json=COALESCE($7,facts_json),finished_at=now()
		WHERE tenant_id=$1 AND id=$2`, id.tenantID, id.executionID, state, code, answer, answerHash, factsJSON, behavior)
	return err
}

func updateTurnTerminal(ctx context.Context, tx pgx.Tx, id executionIdentity, state, answer string) error {
	_, err := tx.Exec(ctx, `UPDATE turn SET status=$3,answer=$4 WHERE tenant_id=$1 AND id=$2
		AND $5=(SELECT e.id FROM conversation_execution e WHERE e.tenant_id=$1 AND e.turn_id=$2 ORDER BY execution_no DESC LIMIT 1)`,
		id.tenantID, id.turnID, state, answer, id.executionID)
	return err
}

func insertAssistantMessage(ctx context.Context, tx pgx.Tx, tenantID, turnID int64, answer string) error {
	_, err := tx.Exec(ctx, `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'assistant',$3)`, tenantID, turnID, answer)
	return err
}

// countExecutionFacts reports stored fact snapshots. Suggestion turns never
// authorise copying from facts; they still require citations.
func countExecutionFacts(ctx context.Context, tx pgx.Tx, tenantID, executionID int64) (int, error) {
	var facts int
	err := tx.QueryRow(ctx, `SELECT CASE WHEN t.kind='user' AND jsonb_typeof(e.facts_json)='array' THEN
		(SELECT count(*) FROM jsonb_array_elements(e.facts_json) f
		 WHERE COALESCE(f->>'tool_name','') <> '' AND COALESCE(f->>'is_mock','false') <> 'true')
		ELSE 0 END
		FROM conversation_execution e JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		WHERE e.tenant_id=$1 AND e.id=$2`, tenantID, executionID).Scan(&facts)
	return facts, err
}

func countExecutionCitations(ctx context.Context, tx pgx.Tx, tenantID, executionID int64) (int, error) {
	var citations int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM citation WHERE tenant_id=$1 AND execution_id=$2`,
		tenantID, executionID).Scan(&citations)
	return citations, err
}

func upsertCustomerReply(ctx context.Context, tx pgx.Tx, tenantID, executionID int64, answer string, mock, canCopy bool, blocked string) error {
	_, err := tx.Exec(ctx, `INSERT INTO customer_reply(tenant_id,execution_id,text_plain,is_mock,can_copy,blocked_reason)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,execution_id) DO UPDATE SET text_plain=EXCLUDED.text_plain,is_mock=EXCLUDED.is_mock,can_copy=EXCLUDED.can_copy,blocked_reason=EXCLUDED.blocked_reason`,
		tenantID, executionID, answer, mock, canCopy, blocked)
	return err
}

func clearCustomerReply(ctx context.Context, tx pgx.Tx, tenantID, executionID int64) error {
	_, err := tx.Exec(ctx, `UPDATE customer_reply SET text_plain='',can_copy=false,blocked_reason='incomplete' WHERE tenant_id=$1 AND execution_id=$2`, tenantID, executionID)
	return err
}

func nextSSESequence(ctx context.Context, tx pgx.Tx, tenantID, executionID int64) (int64, error) {
	var seq int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM sse_event WHERE tenant_id=$1 AND execution_id=$2`, tenantID, executionID).Scan(&seq)
	return seq, err
}

type recoveryInterrupted struct {
	id     executionIdentity
	state  string
	cancel bool
}

func lockInterruptedExecutions(ctx context.Context, tx pgx.Tx) ([]recoveryInterrupted, error) {
	rows, err := tx.Query(ctx, `SELECT e.tenant_id,e.id,e.turn_id,t.conversation_id,e.execution_no,e.request_id,e.is_mock,e.status,e.cancel_requested
		FROM conversation_execution e JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		WHERE e.status IN ('PENDING','EXECUTING','CANCEL_REQUESTED') ORDER BY e.id FOR UPDATE OF e`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := []recoveryInterrupted{}
	for rows.Next() {
		var item recoveryInterrupted
		if err = rows.Scan(&item.id.tenantID, &item.id.executionID, &item.id.turnID, &item.id.conversationID, &item.id.executionNo, &item.id.requestID, &item.id.mock, &item.state, &item.cancel); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return pending, nil
}
